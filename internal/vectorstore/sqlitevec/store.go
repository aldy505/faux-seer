// Package sqlitevec provides a SQLite-backed vector store.
package sqlitevec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/vectorstore"
	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
)

// Store implements vectorstore.Store using sqlite-vec-backed SQLite queries.
type Store struct {
	db         *db.Store
	dimensions int
}

// New creates a sqlite-vec-backed store and verifies the extension is available.
func New(ctx context.Context, store *db.Store, dimensions int) (*Store, error) {
	if store == nil || store.DB == nil {
		return nil, fmt.Errorf("sqlitevec store requires an initialized sqlite database")
	}
	vectorStore := &Store{db: store, dimensions: dimensions}
	if err := vectorStore.migrate(ctx); err != nil {
		return nil, err
	}
	return vectorStore, nil
}

func (s *Store) UpsertGroupingRecords(ctx context.Context, records []vectorstore.GroupingRecord) error {
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite-vec upsert tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO sqlitevec_grouping_records (project_id, hash, exception_type, vector, created_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(project_id, hash) DO UPDATE
SET exception_type = excluded.exception_type,
    vector = excluded.vector
`)
	if err != nil {
		return fmt.Errorf("prepare sqlite-vec upsert: %w", err)
	}
	defer stmt.Close()

	for _, record := range records {
		serialized, err := sqlite_vec.SerializeFloat32(record.Vector)
		if err != nil {
			return fmt.Errorf("serialize sqlite-vec vector: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, record.ProjectID, record.Hash, record.ExceptionType, serialized); err != nil {
			return fmt.Errorf("exec sqlite-vec upsert: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite-vec upsert tx: %w", err)
	}
	return nil
}

func (s *Store) SearchSimilar(ctx context.Context, projectID int64, hash string, vector []float32, k int, threshold float64) ([]vectorstore.SimilarIssue, error) {
	if k <= 0 {
		k = 1
	}
	serialized, err := sqlite_vec.SerializeFloat32(vector)
	if err != nil {
		return nil, fmt.Errorf("serialize sqlite-vec query vector: %w", err)
	}

	rows, err := s.db.DB.QueryContext(ctx, `
SELECT hash, distance
FROM (
	SELECT hash, vec_distance_cosine(vector, ?) AS distance
	FROM sqlitevec_grouping_records
	WHERE project_id = ? AND hash <> ?
)
WHERE distance <= ?
ORDER BY distance ASC
LIMIT ?
`, serialized, projectID, hash, threshold, k)
	if err != nil {
		return nil, fmt.Errorf("query sqlite-vec nearest neighbors: %w", err)
	}
	defer rows.Close()

	results := make([]vectorstore.SimilarIssue, 0, k)
	for rows.Next() {
		var item vectorstore.SimilarIssue
		if err := rows.Scan(&item.ParentHash, &item.StacktraceDistance); err != nil {
			return nil, fmt.Errorf("scan sqlite-vec neighbor: %w", err)
		}
		item.ShouldGroup = true
		results = append(results, item)
	}
	return results, rows.Err()
}

func (s *Store) DeleteProject(ctx context.Context, projectID int64) (bool, error) {
	_, err := s.db.DB.ExecContext(ctx, `DELETE FROM sqlitevec_grouping_records WHERE project_id = ?`, projectID)
	if err != nil {
		return false, fmt.Errorf("delete sqlite-vec project records: %w", err)
	}
	return true, nil
}

func (s *Store) DeleteHashes(ctx context.Context, projectID int64, hashes []string) (bool, error) {
	if len(hashes) == 0 {
		return true, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(hashes)), ",")
	args := make([]any, 0, len(hashes)+1)
	args = append(args, projectID)
	for _, hash := range hashes {
		args = append(args, hash)
	}
	query := `DELETE FROM sqlitevec_grouping_records WHERE project_id = ? AND hash IN (` + placeholders + `)`
	if _, err := s.db.DB.ExecContext(ctx, query, args...); err != nil {
		return false, fmt.Errorf("delete sqlite-vec hashes: %w", err)
	}
	return true, nil
}

func (s *Store) InsertSupergroup(ctx context.Context, record vectorstore.SupergroupRecord) error {
	return s.db.InsertSupergroup(ctx, record.OrganizationID, record.GroupID, record.ProjectID, record.Artifact)
}

func (s *Store) ListSupergroups(ctx context.Context, organizationID int64, projectIDs []int64, offset, limit int) ([]map[string]any, error) {
	rows, err := s.db.ListSupergroups(ctx, organizationID, projectIDs, offset, limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var item map[string]any
		if err := json.Unmarshal(row, &item); err != nil {
			return nil, fmt.Errorf("decode supergroup row: %w", err)
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Store) migrate(ctx context.Context) error {
	var vecVersion string
	if err := s.db.DB.QueryRowContext(ctx, `SELECT vec_version()`).Scan(&vecVersion); err != nil {
		return fmt.Errorf("verify sqlite-vec extension: %w", err)
	}

	checkConstraint := `typeof(vector) = 'blob'`
	if s.dimensions > 0 {
		checkConstraint += fmt.Sprintf(" AND vec_length(vector) = %d", s.dimensions)
	}
	statement := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS sqlitevec_grouping_records (
	project_id INTEGER NOT NULL,
	hash TEXT NOT NULL,
	exception_type TEXT NULL,
	vector BLOB NOT NULL CHECK(%s),
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(project_id, hash)
);
CREATE INDEX IF NOT EXISTS idx_sqlitevec_grouping_records_project ON sqlitevec_grouping_records(project_id);

CREATE TABLE IF NOT EXISTS sqlitevec_code_chunks (
	organization_id INTEGER NOT NULL,
	provider TEXT NOT NULL,
	owner TEXT NOT NULL,
	name TEXT NOT NULL,
	ref TEXT NOT NULL,
	path TEXT NOT NULL,
	chunk_index INTEGER NOT NULL,
	start_line INTEGER NOT NULL,
	end_line INTEGER NOT NULL,
	text TEXT NOT NULL,
	vector BLOB NOT NULL CHECK(%s),
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(organization_id, provider, owner, name, ref, path, chunk_index)
);
CREATE INDEX IF NOT EXISTS idx_sqlitevec_code_chunks_org ON sqlitevec_code_chunks(organization_id);
CREATE INDEX IF NOT EXISTS idx_sqlitevec_code_chunks_repo ON sqlitevec_code_chunks(provider, owner, name);
`, checkConstraint, checkConstraint)
	if _, err := s.db.DB.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("run sqlite-vec migration: %w", err)
	}
	return nil
}

func (s *Store) UpsertCodeChunks(ctx context.Context, chunks []vectorstore.CodeChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite-vec code chunk tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO sqlitevec_code_chunks (
	organization_id, provider, owner, name, ref, path, chunk_index, start_line, end_line, text, vector, created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(organization_id, provider, owner, name, ref, path, chunk_index) DO UPDATE
SET start_line = excluded.start_line,
    end_line = excluded.end_line,
    text = excluded.text,
    vector = excluded.vector
`)
	if err != nil {
		return fmt.Errorf("prepare sqlite-vec code chunk upsert: %w", err)
	}
	defer stmt.Close()

	for _, chunk := range chunks {
		serialized, err := sqlite_vec.SerializeFloat32(chunk.Vector)
		if err != nil {
			return fmt.Errorf("serialize sqlite-vec code chunk vector: %w", err)
		}
		if _, err := stmt.ExecContext(
			ctx,
			chunk.OrganizationID,
			chunk.Provider,
			chunk.Owner,
			chunk.Name,
			chunk.Ref,
			chunk.Path,
			chunk.ChunkIndex,
			chunk.StartLine,
			chunk.EndLine,
			chunk.Text,
			serialized,
		); err != nil {
			return fmt.Errorf("exec sqlite-vec code chunk upsert: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite-vec code chunk tx: %w", err)
	}
	return nil
}

func (s *Store) SearchCode(ctx context.Context, vector []float32, k int, filters vectorstore.CodeFilters) ([]vectorstore.CodeResult, error) {
	if k <= 0 {
		k = 10
	}
	serialized, err := sqlite_vec.SerializeFloat32(vector)
	if err != nil {
		return nil, fmt.Errorf("serialize sqlite-vec code query vector: %w", err)
	}

	conditions := []string{"organization_id = ?"}
	args := []any{serialized, filters.OrganizationID}
	for _, filter := range []struct {
		value  string
		column string
	}{
		{filters.Provider, "provider"},
		{filters.Owner, "owner"},
		{filters.Name, "name"},
		{filters.Ref, "ref"},
	} {
		if filter.value == "" {
			continue
		}
		conditions = append(conditions, filter.column+" = ?")
		args = append(args, filter.value)
	}

	query := `
SELECT provider, owner, name, ref, path, chunk_index, start_line, end_line, text, distance
FROM (
	SELECT provider, owner, name, ref, path, chunk_index, start_line, end_line, text,
		vec_distance_cosine(vector, ?) AS distance
	FROM sqlitevec_code_chunks
	WHERE ` + strings.Join(conditions, " AND ") + `
)
ORDER BY distance ASC
LIMIT ?`
	args = append(args, k)

	rows, err := s.db.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query sqlite-vec code chunks: %w", err)
	}
	defer rows.Close()

	results := make([]vectorstore.CodeResult, 0, k)
	for rows.Next() {
		var item vectorstore.CodeResult
		if err := rows.Scan(&item.Provider, &item.Owner, &item.Name, &item.Ref, &item.Path, &item.ChunkIndex, &item.StartLine, &item.EndLine, &item.Text, &item.Distance); err != nil {
			return nil, fmt.Errorf("scan sqlite-vec code chunk: %w", err)
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func (s *Store) DeleteCodeRepo(ctx context.Context, organizationID int64, provider, owner, name string) (bool, error) {
	_, err := s.db.DB.ExecContext(ctx, `DELETE FROM sqlitevec_code_chunks WHERE organization_id = ? AND provider = ? AND owner = ? AND name = ?`, organizationID, provider, owner, name)
	if err != nil {
		return false, fmt.Errorf("delete sqlite-vec code chunks: %w", err)
	}
	return true, nil
}

func (s *Store) HasCodeChunks(ctx context.Context, organizationID int64) (bool, error) {
	var exists bool
	if err := s.db.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlitevec_code_chunks WHERE organization_id = ?)`, organizationID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check sqlite-vec code chunks: %w", err)
	}
	return exists, nil
}
