// Package db manages SQLite persistence for faux-seer.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

// Store wraps the application's persistent database.
type Store struct {
	DB *sql.DB
}

var sqliteVecAutoOnce sync.Once

// AutofixRunRecord persists an autofix run state blob.
type AutofixRunRecord struct {
	ID        int64
	GroupID   *int64
	Provider  *string
	PRID      *int64
	StateJSON []byte
}

// ExplorerRunRecord persists an explorer run state blob and list metadata.
type ExplorerRunRecord struct {
	ID              int64
	OrganizationID  int64
	UserID          *int64
	Title           string
	CategoryKey     *string
	CategoryValue   *string
	Provider        *string
	PRID            *int64
	StateJSON       []byte
	CreatedAt       string
	LastTriggeredAt string
}

// ExplorerRunFilter controls explorer run listing.
type ExplorerRunFilter struct {
	OrganizationID int64
	UserID         *int64
	CategoryKey    *string
	CategoryValue  *string
	Offset         int
	Limit          int
}

// GroupingRecord stores an embedding for similarity lookup.
type GroupingRecord struct {
	ProjectID     int64
	Hash          string
	ExceptionType *string
	Vector        []float32
}

// New opens the configured database and runs migrations.
func New(ctx context.Context, databasePath string) (*Store, error) {
	sqliteVecAutoOnce.Do(sqlite_vec.Auto)
	if databasePath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	store := &Store{DB: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS autofix_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  group_id INTEGER NULL,
  provider TEXT NULL,
  pr_id INTEGER NULL,
  state_json TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_autofix_runs_group_id ON autofix_runs(group_id);
CREATE INDEX IF NOT EXISTS idx_autofix_runs_pr ON autofix_runs(provider, pr_id);

CREATE TABLE IF NOT EXISTS project_preferences (
  project_id INTEGER PRIMARY KEY,
  organization_id INTEGER NOT NULL,
  preference_json TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS grouping_records (
  project_id INTEGER NOT NULL,
  hash TEXT NOT NULL,
  exception_type TEXT NULL,
  vector_json TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY(project_id, hash)
);
CREATE INDEX IF NOT EXISTS idx_grouping_records_project ON grouping_records(project_id);

CREATE TABLE IF NOT EXISTS supergroups (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  organization_id INTEGER NOT NULL,
  group_id INTEGER NOT NULL,
  project_id INTEGER NOT NULL,
  artifact_json TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_supergroups_org_project ON supergroups(organization_id, project_id);

CREATE TABLE IF NOT EXISTS explorer_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  organization_id INTEGER NOT NULL,
  user_id INTEGER NULL,
  title TEXT NOT NULL,
  category_key TEXT NULL,
  category_value TEXT NULL,
  provider TEXT NULL,
  pr_id INTEGER NULL,
  state_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_triggered_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_explorer_runs_org ON explorer_runs(organization_id, last_triggered_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_explorer_runs_org_user ON explorer_runs(organization_id, user_id, last_triggered_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_explorer_runs_org_category ON explorer_runs(organization_id, category_key, category_value, last_triggered_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_explorer_runs_pr ON explorer_runs(organization_id, provider, pr_id);
`
	_, err := s.DB.ExecContext(ctx, schema)
	if err != nil {
		return fmt.Errorf("run sqlite migrations: %w", err)
	}
	return nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error { return s.DB.Close() }

// CreateAutofixRun inserts a new autofix run.
func (s *Store) CreateAutofixRun(ctx context.Context, groupID *int64, state []byte) (int64, error) {
	result, err := s.DB.ExecContext(ctx, `INSERT INTO autofix_runs (group_id, state_json, created_at, updated_at) VALUES (?, ?, ?, ?)`, groupID, string(state), nowString(), nowString())
	if err != nil {
		return 0, fmt.Errorf("insert autofix run: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

// UpdateAutofixRun updates the persisted state for a run.
func (s *Store) UpdateAutofixRun(ctx context.Context, runID int64, provider *string, prID *int64, state []byte) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE autofix_runs SET provider = COALESCE(?, provider), pr_id = COALESCE(?, pr_id), state_json = ?, updated_at = ? WHERE id = ?`, provider, prID, string(state), nowString(), runID)
	if err != nil {
		return fmt.Errorf("update autofix run: %w", err)
	}
	return nil
}

// GetAutofixRun fetches a run by ID.
func (s *Store) GetAutofixRun(ctx context.Context, runID int64) (*AutofixRunRecord, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, group_id, provider, pr_id, state_json FROM autofix_runs WHERE id = ?`, runID)
	var rec AutofixRunRecord
	var state string
	if err := row.Scan(&rec.ID, &rec.GroupID, &rec.Provider, &rec.PRID, &state); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get autofix run: %w", err)
	}
	rec.StateJSON = []byte(state)
	return &rec, nil
}

// GetAutofixRunByPR fetches a run by provider/pr pair.
func (s *Store) GetAutofixRunByPR(ctx context.Context, provider string, prID int64) (*AutofixRunRecord, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, group_id, provider, pr_id, state_json FROM autofix_runs WHERE provider = ? AND pr_id = ? ORDER BY id DESC LIMIT 1`, provider, prID)
	var rec AutofixRunRecord
	var state string
	if err := row.Scan(&rec.ID, &rec.GroupID, &rec.Provider, &rec.PRID, &state); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get autofix run by pr: %w", err)
	}
	rec.StateJSON = []byte(state)
	return &rec, nil
}

// CreateExplorerRun inserts a new explorer run.
func (s *Store) CreateExplorerRun(ctx context.Context, record ExplorerRunRecord) (int64, error) {
	timestamp := nowString()
	if record.CreatedAt == "" {
		record.CreatedAt = timestamp
	}
	if record.LastTriggeredAt == "" {
		record.LastTriggeredAt = record.CreatedAt
	}
	result, err := s.DB.ExecContext(
		ctx,
		`INSERT INTO explorer_runs (
			organization_id, user_id, title, category_key, category_value, provider, pr_id, state_json, created_at, last_triggered_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.OrganizationID,
		record.UserID,
		record.Title,
		record.CategoryKey,
		record.CategoryValue,
		record.Provider,
		record.PRID,
		string(record.StateJSON),
		record.CreatedAt,
		record.LastTriggeredAt,
		timestamp,
	)
	if err != nil {
		return 0, fmt.Errorf("insert explorer run: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("explorer run last insert id: %w", err)
	}
	return id, nil
}

// UpdateExplorerRun updates a persisted explorer run state.
func (s *Store) UpdateExplorerRun(ctx context.Context, record ExplorerRunRecord) error {
	_, err := s.DB.ExecContext(
		ctx,
		`UPDATE explorer_runs
		SET title = ?, category_key = ?, category_value = ?, provider = ?, pr_id = ?, state_json = ?, last_triggered_at = ?, updated_at = ?
		WHERE id = ?`,
		record.Title,
		record.CategoryKey,
		record.CategoryValue,
		record.Provider,
		record.PRID,
		string(record.StateJSON),
		record.LastTriggeredAt,
		nowString(),
		record.ID,
	)
	if err != nil {
		return fmt.Errorf("update explorer run: %w", err)
	}
	return nil
}

// GetExplorerRun fetches an explorer run by ID.
func (s *Store) GetExplorerRun(ctx context.Context, runID int64) (*ExplorerRunRecord, error) {
	row := s.DB.QueryRowContext(
		ctx,
		`SELECT
			id, organization_id, user_id, title, category_key, category_value, provider, pr_id, state_json, created_at, last_triggered_at
		FROM explorer_runs
		WHERE id = ?`,
		runID,
	)
	var rec ExplorerRunRecord
	var state string
	if err := row.Scan(
		&rec.ID,
		&rec.OrganizationID,
		&rec.UserID,
		&rec.Title,
		&rec.CategoryKey,
		&rec.CategoryValue,
		&rec.Provider,
		&rec.PRID,
		&state,
		&rec.CreatedAt,
		&rec.LastTriggeredAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get explorer run: %w", err)
	}
	rec.StateJSON = []byte(state)
	return &rec, nil
}

// GetExplorerRunByPR fetches an explorer run by provider/pr pair.
func (s *Store) GetExplorerRunByPR(ctx context.Context, organizationID int64, provider string, prID int64) (*ExplorerRunRecord, error) {
	row := s.DB.QueryRowContext(
		ctx,
		`SELECT
			id, organization_id, user_id, title, category_key, category_value, provider, pr_id, state_json, created_at, last_triggered_at
		FROM explorer_runs
		WHERE organization_id = ? AND provider = ? AND pr_id = ?
		ORDER BY id DESC
		LIMIT 1`,
		organizationID,
		provider,
		prID,
	)
	var rec ExplorerRunRecord
	var state string
	if err := row.Scan(
		&rec.ID,
		&rec.OrganizationID,
		&rec.UserID,
		&rec.Title,
		&rec.CategoryKey,
		&rec.CategoryValue,
		&rec.Provider,
		&rec.PRID,
		&state,
		&rec.CreatedAt,
		&rec.LastTriggeredAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get explorer run by pr: %w", err)
	}
	rec.StateJSON = []byte(state)
	return &rec, nil
}

// ListExplorerRuns returns explorer runs using compatibility-focused filters.
func (s *Store) ListExplorerRuns(ctx context.Context, filter ExplorerRunFilter) ([]ExplorerRunRecord, error) {
	query := `SELECT
		id, organization_id, user_id, title, category_key, category_value, provider, pr_id, state_json, created_at, last_triggered_at
	FROM explorer_runs
	WHERE organization_id = ?`
	args := []any{filter.OrganizationID}
	if filter.UserID != nil {
		query += ` AND user_id = ?`
		args = append(args, *filter.UserID)
	}
	if filter.CategoryKey != nil {
		query += ` AND category_key = ?`
		args = append(args, *filter.CategoryKey)
	}
	if filter.CategoryValue != nil {
		query += ` AND category_value = ?`
		args = append(args, *filter.CategoryValue)
	}
	query += ` ORDER BY last_triggered_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list explorer runs: %w", err)
	}
	defer rows.Close()
	var out []ExplorerRunRecord
	for rows.Next() {
		var rec ExplorerRunRecord
		var state string
		if err := rows.Scan(
			&rec.ID,
			&rec.OrganizationID,
			&rec.UserID,
			&rec.Title,
			&rec.CategoryKey,
			&rec.CategoryValue,
			&rec.Provider,
			&rec.PRID,
			&state,
			&rec.CreatedAt,
			&rec.LastTriggeredAt,
		); err != nil {
			return nil, fmt.Errorf("scan explorer run: %w", err)
		}
		rec.StateJSON = []byte(state)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// PutProjectPreference stores a project preference blob.
func (s *Store) PutProjectPreference(ctx context.Context, projectID, organizationID int64, preference any) error {
	payload, err := json.Marshal(preference)
	if err != nil {
		return fmt.Errorf("marshal preference: %w", err)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO project_preferences (project_id, organization_id, preference_json, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(project_id) DO UPDATE SET organization_id = excluded.organization_id, preference_json = excluded.preference_json, updated_at = excluded.updated_at`, projectID, organizationID, string(payload), nowString())
	if err != nil {
		return fmt.Errorf("upsert preference: %w", err)
	}
	return nil
}

// GetProjectPreference returns a raw preference blob.
func (s *Store) GetProjectPreference(ctx context.Context, projectID int64) (json.RawMessage, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT preference_json FROM project_preferences WHERE project_id = ?`, projectID)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get preference: %w", err)
	}
	return json.RawMessage(payload), nil
}

// ListProjectPreferences returns raw preference blobs for a set of project IDs.
func (s *Store) ListProjectPreferences(ctx context.Context, projectIDs []int64) (map[int64]json.RawMessage, error) {
	result := make(map[int64]json.RawMessage, len(projectIDs))
	for _, projectID := range projectIDs {
		payload, err := s.GetProjectPreference(ctx, projectID)
		if err != nil {
			return nil, err
		}
		if payload != nil {
			result[projectID] = payload
		}
	}
	return result, nil
}

// UpsertGroupingRecords stores grouping vectors.
func (s *Store) UpsertGroupingRecords(ctx context.Context, records []GroupingRecord) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin grouping tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO grouping_records (project_id, hash, exception_type, vector_json, created_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(project_id, hash) DO UPDATE SET exception_type = excluded.exception_type, vector_json = excluded.vector_json`)
	if err != nil {
		return fmt.Errorf("prepare grouping stmt: %w", err)
	}
	defer stmt.Close()
	for _, record := range records {
		payload, err := json.Marshal(record.Vector)
		if err != nil {
			return fmt.Errorf("marshal vector: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, record.ProjectID, record.Hash, record.ExceptionType, string(payload), nowString()); err != nil {
			return fmt.Errorf("exec grouping upsert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit grouping tx: %w", err)
	}
	return nil
}

// ListGroupingRecords returns all grouping vectors for a project.
func (s *Store) ListGroupingRecords(ctx context.Context, projectID int64) ([]GroupingRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT project_id, hash, exception_type, vector_json FROM grouping_records WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, fmt.Errorf("query grouping records: %w", err)
	}
	defer rows.Close()
	var out []GroupingRecord
	for rows.Next() {
		var rec GroupingRecord
		var payload string
		if err := rows.Scan(&rec.ProjectID, &rec.Hash, &rec.ExceptionType, &payload); err != nil {
			return nil, fmt.Errorf("scan grouping record: %w", err)
		}
		if err := json.Unmarshal([]byte(payload), &rec.Vector); err != nil {
			return nil, fmt.Errorf("unmarshal grouping vector: %w", err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DeleteGroupingRecordsForProject deletes vectors for a project.
func (s *Store) DeleteGroupingRecordsForProject(ctx context.Context, projectID int64) (bool, error) {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM grouping_records WHERE project_id = ?`, projectID)
	if err != nil {
		return false, fmt.Errorf("delete grouping records for project: %w", err)
	}
	return true, nil
}

// DeleteGroupingRecordsByHash deletes vectors matching hashes.
func (s *Store) DeleteGroupingRecordsByHash(ctx context.Context, projectID int64, hashes []string) (bool, error) {
	for _, hash := range hashes {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM grouping_records WHERE project_id = ? AND hash = ?`, projectID, hash); err != nil {
			return false, fmt.Errorf("delete grouping record by hash: %w", err)
		}
	}
	return true, nil
}

// InsertSupergroup stores a supergroup artifact.
func (s *Store) InsertSupergroup(ctx context.Context, organizationID, groupID, projectID int64, artifact any) error {
	payload, err := json.Marshal(artifact)
	if err != nil {
		return fmt.Errorf("marshal supergroup artifact: %w", err)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO supergroups (organization_id, group_id, project_id, artifact_json, created_at) VALUES (?, ?, ?, ?, ?)`, organizationID, groupID, projectID, string(payload), nowString())
	if err != nil {
		return fmt.Errorf("insert supergroup: %w", err)
	}
	return nil
}

// ListSupergroups returns raw supergroup artifacts.
func (s *Store) ListSupergroups(ctx context.Context, organizationID int64, projectIDs []int64, offset, limit int) ([]json.RawMessage, error) {
	query := `SELECT artifact_json FROM supergroups WHERE organization_id = ? ORDER BY id DESC`
	args := []any{organizationID}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list supergroups: %w", err)
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(payload))
	}
	if offset > len(out) {
		return []json.RawMessage{}, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, rows.Err()
}

func nowString() string { return time.Now().UTC().Format(time.RFC3339Nano) }
