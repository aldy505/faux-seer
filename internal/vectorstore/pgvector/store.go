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

func compact(sql string) string {
	parts := strings.Fields(sql)
	if len(parts) <= 8 {
		return strings.Join(parts, " ")
	}
	return strings.Join(slices.Concat(parts[:8], []string{"..."}), " ")
}
