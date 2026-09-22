// Package pgvector provides a Postgres-backed vector store implementation.
package pgvector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pgvector/pgvector-go"

	"github.com/aldy505/faux-seer/internal/vectorstore"
)

// Store implements vectorstore.Store using Postgres with pgvector.
type Store struct {
	db         *sql.DB
	dimensions int
}

// New creates and migrates a pgvector-backed store.
func New(ctx context.Context, dsn string, dimensions int) (*Store, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pgvector DSN: %w", err)
	}
	if dimensions <= 0 {
		return nil, fmt.Errorf("pgvector dimensions must be greater than zero")
	}
	db := stdlib.OpenDB(*config)
	store := &Store{db: db, dimensions: dimensions}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close closes the underlying Postgres connection pool.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE EXTENSION IF NOT EXISTS vector`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS grouping_records (
			project_id BIGINT NOT NULL,
			hash TEXT NOT NULL,
			exception_type TEXT NULL,
			vector vector(%d) NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (project_id, hash)
		)`, s.dimensions),
		`CREATE INDEX IF NOT EXISTS idx_grouping_records_project ON grouping_records(project_id)`,
		`CREATE TABLE IF NOT EXISTS supergroups (
id BIGSERIAL PRIMARY KEY,
organization_id BIGINT NOT NULL,
group_id BIGINT NOT NULL,
project_id BIGINT NOT NULL,
artifact_json JSONB NOT NULL,
created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`,
		`CREATE INDEX IF NOT EXISTS idx_supergroups_org_project ON supergroups(organization_id, project_id)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS code_chunks (
			organization_id BIGINT NOT NULL,
			provider TEXT NOT NULL,
			owner TEXT NOT NULL,
			name TEXT NOT NULL,
			ref TEXT NOT NULL,
			path TEXT NOT NULL,
			chunk_index INTEGER NOT NULL,
			start_line INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			text TEXT NOT NULL,
			vector vector(%d) NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (organization_id, provider, owner, name, ref, path, chunk_index)
		)`, s.dimensions),
		`CREATE INDEX IF NOT EXISTS idx_code_chunks_org ON code_chunks(organization_id)`,
		`CREATE INDEX IF NOT EXISTS idx_code_chunks_repo ON code_chunks(provider, owner, name)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("run pgvector migration %q: %w", compact(statement), err)
		}
	}
	return nil
}

func (s *Store) UpsertGroupingRecords(ctx context.Context, records []vectorstore.GroupingRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pgvector upsert tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO grouping_records (project_id, hash, exception_type, vector, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (project_id, hash)
DO UPDATE SET exception_type = EXCLUDED.exception_type, vector = EXCLUDED.vector
`)
	if err != nil {
		return fmt.Errorf("prepare pgvector upsert: %w", err)
	}
	defer stmt.Close()
	for _, record := range records {
		if _, err := stmt.ExecContext(ctx, record.ProjectID, record.Hash, record.ExceptionType, pgvector.NewVector(record.Vector), time.Now().UTC()); err != nil {
			return fmt.Errorf("exec pgvector upsert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pgvector upsert tx: %w", err)
	}
	return nil
}

func (s *Store) SearchSimilar(ctx context.Context, projectID int64, hash string, vector []float32, k int, threshold float64) ([]vectorstore.SimilarIssue, error) {
	if k <= 0 {
		k = 1
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT hash, vector <=> $1 AS distance
FROM grouping_records
WHERE project_id = $2 AND hash <> $3 AND vector <=> $1 <= $4
ORDER BY distance ASC
LIMIT $5
`, pgvector.NewVector(vector), projectID, hash, threshold, k)
	if err != nil {
		return nil, fmt.Errorf("query pgvector nearest neighbors: %w", err)
	}
	defer rows.Close()
	results := make([]vectorstore.SimilarIssue, 0, k)
	for rows.Next() {
		var item vectorstore.SimilarIssue
		if err := rows.Scan(&item.ParentHash, &item.StacktraceDistance); err != nil {
			return nil, fmt.Errorf("scan pgvector neighbor: %w", err)
		}
		item.ShouldGroup = true
		results = append(results, item)
	}
	return results, rows.Err()
}

func (s *Store) DeleteProject(ctx context.Context, projectID int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM grouping_records WHERE project_id = $1`, projectID)
	if err != nil {
		return false, fmt.Errorf("delete pgvector records for project: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("pgvector project delete rows affected: %w", err)
	}
	return deleted >= 0, nil
}

func (s *Store) DeleteHashes(ctx context.Context, projectID int64, hashes []string) (bool, error) {
	if len(hashes) == 0 {
		return true, nil
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM grouping_records WHERE project_id = $1 AND hash = ANY($2)`, projectID, hashes)
	if err != nil {
		return false, fmt.Errorf("delete pgvector hashes: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("pgvector hash delete rows affected: %w", err)
	}
	return deleted >= 0, nil
}

func (s *Store) InsertSupergroup(ctx context.Context, record vectorstore.SupergroupRecord) error {
	payload, err := json.Marshal(record.Artifact)
	if err != nil {
		return fmt.Errorf("marshal supergroup artifact for pgvector: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO supergroups (organization_id, group_id, project_id, artifact_json, created_at) VALUES ($1, $2, $3, $4::jsonb, $5)`, record.OrganizationID, record.GroupID, record.ProjectID, string(payload), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("insert pgvector supergroup: %w", err)
	}
	return nil
}

func (s *Store) ListSupergroups(ctx context.Context, organizationID int64, projectIDs []int64, offset, limit int) ([]map[string]any, error) {
	query := `SELECT artifact_json FROM supergroups WHERE organization_id = $1`
	args := []any{organizationID}
	if len(projectIDs) > 0 {
		query += ` AND project_id = ANY($2)`
		args = append(args, projectIDs)
	}
	query += ` ORDER BY id DESC`
	if limit <= 0 {
		limit = 50
	}
	placeholder := len(args) + 1
	query += fmt.Sprintf(` OFFSET $%d LIMIT $%d`, placeholder, placeholder+1)
	args = append(args, offset, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list pgvector supergroups: %w", err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan pgvector supergroup: %w", err)
		}
		var item map[string]any
		if err := json.Unmarshal(payload, &item); err != nil {
			return nil, fmt.Errorf("decode pgvector supergroup: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) UpsertCodeChunks(ctx context.Context, chunks []vectorstore.CodeChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pgvector code chunk tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO code_chunks (
	organization_id, provider, owner, name, ref, path, chunk_index, start_line, end_line, text, vector, created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (organization_id, provider, owner, name, ref, path, chunk_index)
DO UPDATE SET
	start_line = EXCLUDED.start_line,
	end_line = EXCLUDED.end_line,
	text = EXCLUDED.text,
	vector = EXCLUDED.vector
`)
	if err != nil {
		return fmt.Errorf("prepare pgvector code chunk upsert: %w", err)
	}
	defer stmt.Close()
	for _, chunk := range chunks {
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
			pgvector.NewVector(chunk.Vector),
			time.Now().UTC(),
		); err != nil {
			return fmt.Errorf("exec pgvector code chunk upsert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pgvector code chunk tx: %w", err)
	}
	return nil
}

func (s *Store) SearchCode(ctx context.Context, vector []float32, k int, filters vectorstore.CodeFilters) ([]vectorstore.CodeResult, error) {
	if k <= 0 {
		k = 10
	}
	conditions := []string{"organization_id = $2"}
	args := []any{pgvector.NewVector(vector), filters.OrganizationID}
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
		args = append(args, filter.value)
		conditions = append(conditions, fmt.Sprintf("%s = $%d", filter.column, len(args)))
	}
	args = append(args, k)
	query := fmt.Sprintf(`
SELECT provider, owner, name, ref, path, chunk_index, start_line, end_line, text, vector <=> $1 AS distance
FROM code_chunks
WHERE %s
ORDER BY distance ASC
LIMIT $%d
`, strings.Join(conditions, " AND "), len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query pgvector code chunks: %w", err)
	}
	defer rows.Close()
	results := make([]vectorstore.CodeResult, 0, k)
	for rows.Next() {
		var item vectorstore.CodeResult
		if err := rows.Scan(&item.Provider, &item.Owner, &item.Name, &item.Ref, &item.Path, &item.ChunkIndex, &item.StartLine, &item.EndLine, &item.Text, &item.Distance); err != nil {
			return nil, fmt.Errorf("scan pgvector code chunk: %w", err)
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func (s *Store) DeleteCodeRepo(ctx context.Context, organizationID int64, provider, owner, name string) (bool, error) {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM code_chunks WHERE organization_id = $1 AND provider = $2 AND owner = $3 AND name = $4`, organizationID, provider, owner, name); err != nil {
		return false, fmt.Errorf("delete pgvector code chunks: %w", err)
	}
	return true, nil
}

func (s *Store) HasCodeChunks(ctx context.Context, organizationID int64) (bool, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM code_chunks WHERE organization_id = $1)`, organizationID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check pgvector code chunks: %w", err)
	}
	return exists, nil
}

func compact(sql string) string {
	parts := strings.Fields(sql)
	if len(parts) <= 8 {
		return strings.Join(parts, " ")
	}
	return strings.Join(slices.Concat(parts[:8], []string{"..."}), " ")
}
