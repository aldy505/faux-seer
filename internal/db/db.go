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

// Persisted run statuses. These match the literals Sentry's SeerRunState model
// accepts, so they are safe to return verbatim over the wire.
const (
	RunStatusProcessing = "processing"
	RunStatusCompleted  = "completed"
	RunStatusError      = "error"
	RunStatusAwaiting   = "awaiting_user_input"
)

// AutofixRunRecord persists an autofix run state blob.
type AutofixRunRecord struct {
	ID        int64
	GroupID   *int64
	Provider  *string
	PRID      *int64
	Status    string
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
	Status          string
	StateJSON       []byte
	CreatedAt       string
	LastTriggeredAt string
}

// RunRecord persists a generic background run (investigations, assisted query,
// agent feature runs, index jobs).
type RunRecord struct {
	ID              int64
	Kind            string
	OrganizationID  *int64
	Status          string
	RequestJSON     []byte
	ResultJSON      []byte
	Error           *string
	WorkflowVersion int64
	IdempotencyKey  *string
	CreatedAt       string
	UpdatedAt       string
}

// ReplayBreadcrumbSummaryRecord persists a replay breadcrumb summarization.
type ReplayBreadcrumbSummaryRecord struct {
	ReplayID       string
	OrganizationID *int64
	ProjectID      *int64
	Status         string
	CreatedAt      string
	CompletedAt    *string
	SummaryJSON    []byte
}

// ProjectPreferenceRecord persists per-organization repository preferences.
type ProjectPreferenceRecord struct {
	OrganizationID int64
	ReposJSON      []byte
	IntegrationID  *int64
	UpdatedAt      string
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
	// SQLite allows a single writer and ":memory:" databases are per-connection,
	// so the pool is pinned to one connection. Background runs write to the same
	// handle the HTTP handlers read from, which would otherwise either see an
	// empty in-memory database or fail with SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	// A second process (or an older binary) holding the same file must not turn
	// into an immediate SQLITE_BUSY failure.
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set sqlite busy timeout: %w", err)
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

CREATE TABLE IF NOT EXISTS runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL,
  organization_id INTEGER NULL,
  status TEXT NOT NULL,
  idempotency_key TEXT NULL,
  request_json TEXT NULL,
  result_json TEXT NULL,
  error TEXT NULL,
  workflow_version INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_runs_kind_status ON runs(kind, status);
CREATE INDEX IF NOT EXISTS idx_runs_idempotency ON runs(kind, idempotency_key);

CREATE TABLE IF NOT EXISTS run_commands (
  run_id INTEGER NOT NULL,
  request_id TEXT NOT NULL,
  workflow_version INTEGER NOT NULL,
  command_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (run_id, request_id)
);

CREATE TABLE IF NOT EXISTS replay_breadcrumb_summaries (
  replay_id TEXT PRIMARY KEY,
  organization_id INTEGER NULL,
  project_id INTEGER NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  completed_at TEXT NULL,
  summary_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS project_preferences (
  organization_id INTEGER PRIMARY KEY,
  repos_json TEXT NOT NULL DEFAULT '[]',
  integration_id INTEGER NULL,
  updated_at TEXT NOT NULL
);
`
	_, err := s.DB.ExecContext(ctx, schema)
	if err != nil {
		return fmt.Errorf("run sqlite migrations: %w", err)
	}
	// Run status is duplicated onto the run tables so in-flight work can be
	// enumerated without decoding state blobs. Older databases predate the
	// column, so add it when the table already exists without it.
	for _, column := range []struct{ table, name, definition string }{
		{"autofix_runs", "status", "TEXT NOT NULL DEFAULT 'completed'"},
		{"explorer_runs", "status", "TEXT NOT NULL DEFAULT 'completed'"},
	} {
		if err := s.addColumnIfMissing(ctx, column.table, column.name, column.definition); err != nil {
			return err
		}
	}
	return nil
}

// addColumnIfMissing adds a column to an existing table when it is absent.
func (s *Store) addColumnIfMissing(ctx context.Context, table, column, definition string) error {
	rows, err := s.DB.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("inspect %s schema: %w", table, err)
	}
	found := false
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("scan %s schema: %w", table, err)
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close %s schema rows: %w", table, err)
	}
	if found {
		return nil
	}
	if _, err := s.DB.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition)); err != nil {
		return fmt.Errorf("add %s.%s column: %w", table, column, err)
	}
	return nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error { return s.DB.Close() }

// CreateAutofixRun inserts a new autofix run.
func (s *Store) CreateAutofixRun(ctx context.Context, groupID *int64, state []byte) (int64, error) {
	result, err := s.DB.ExecContext(ctx, `INSERT INTO autofix_runs (group_id, state_json, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, groupID, string(state), RunStatusProcessing, nowString(), nowString())
	if err != nil {
		return 0, fmt.Errorf("insert autofix run: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

// CreateAutofixRunWithID inserts an autofix run using a caller-supplied id.
//
// Sentry reports coding-agent state for run ids Seer created, so the id must be
// preserved. Inserting an existing id is a no-op, which keeps repeated state
// snapshots for the same run idempotent.
func (s *Store) CreateAutofixRunWithID(ctx context.Context, runID int64, groupID *int64, state []byte) error {
	_, err := s.DB.ExecContext(
		ctx,
		`INSERT INTO autofix_runs (id, group_id, state_json, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		runID, groupID, string(state), RunStatusProcessing, nowString(), nowString(),
	)
	if err != nil {
		return fmt.Errorf("insert autofix run with id: %w", err)
	}
	return nil
}

// UpdateAutofixRun updates the persisted state for a run.
func (s *Store) UpdateAutofixRun(ctx context.Context, runID int64, provider *string, prID *int64, status string, state []byte) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE autofix_runs SET provider = COALESCE(?, provider), pr_id = COALESCE(?, pr_id), status = ?, state_json = ?, updated_at = ? WHERE id = ?`, provider, prID, status, string(state), nowString(), runID)
	if err != nil {
		return fmt.Errorf("update autofix run: %w", err)
	}
	return nil
}

// GetAutofixRun fetches a run by ID.
func (s *Store) GetAutofixRun(ctx context.Context, runID int64) (*AutofixRunRecord, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, group_id, provider, pr_id, status, state_json FROM autofix_runs WHERE id = ?`, runID)
	var rec AutofixRunRecord
	var state string
	if err := row.Scan(&rec.ID, &rec.GroupID, &rec.Provider, &rec.PRID, &rec.Status, &state); err != nil {
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
	row := s.DB.QueryRowContext(ctx, `SELECT id, group_id, provider, pr_id, status, state_json FROM autofix_runs WHERE provider = ? AND pr_id = ? ORDER BY id DESC LIMIT 1`, provider, prID)
	var rec AutofixRunRecord
	var state string
	if err := row.Scan(&rec.ID, &rec.GroupID, &rec.Provider, &rec.PRID, &rec.Status, &state); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get autofix run by pr: %w", err)
	}
	rec.StateJSON = []byte(state)
	return &rec, nil
}

// FindAutofixRunByPRURL finds a run whose state blob references a pull request URL.
func (s *Store) FindAutofixRunByPRURL(ctx context.Context, url string) (*AutofixRunRecord, error) {
	if url == "" {
		return nil, nil
	}
	row := s.DB.QueryRowContext(ctx, `SELECT id, group_id, provider, pr_id, status, state_json FROM autofix_runs WHERE state_json LIKE ? ORDER BY id DESC LIMIT 1`, "%"+url+"%")
	var rec AutofixRunRecord
	var state string
	if err := row.Scan(&rec.ID, &rec.GroupID, &rec.Provider, &rec.PRID, &rec.Status, &state); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("find autofix run by pr url: %w", err)
	}
	rec.StateJSON = []byte(state)
	return &rec, nil
}

// ListAutofixRuns returns every persisted autofix run in id order.
func (s *Store) ListAutofixRuns(ctx context.Context) ([]AutofixRunRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, group_id, provider, pr_id, status, state_json FROM autofix_runs ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list autofix runs: %w", err)
	}
	defer rows.Close()
	var out []AutofixRunRecord
	for rows.Next() {
		var rec AutofixRunRecord
		var state string
		if err := rows.Scan(&rec.ID, &rec.GroupID, &rec.Provider, &rec.PRID, &rec.Status, &state); err != nil {
			return nil, fmt.Errorf("scan autofix run: %w", err)
		}
		rec.StateJSON = []byte(state)
		out = append(out, rec)
	}
	return out, rows.Err()
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
	if record.Status == "" {
		record.Status = RunStatusProcessing
	}
	result, err := s.DB.ExecContext(
		ctx,
		`INSERT INTO explorer_runs (
			organization_id, user_id, title, category_key, category_value, provider, pr_id, status, state_json, created_at, last_triggered_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.OrganizationID,
		record.UserID,
		record.Title,
		record.CategoryKey,
		record.CategoryValue,
		record.Provider,
		record.PRID,
		record.Status,
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
		SET title = ?, category_key = ?, category_value = ?, provider = ?, pr_id = ?, status = ?, state_json = ?, last_triggered_at = ?, updated_at = ?
		WHERE id = ?`,
		record.Title,
		record.CategoryKey,
		record.CategoryValue,
		record.Provider,
		record.PRID,
		record.Status,
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
			id, organization_id, user_id, title, category_key, category_value, provider, pr_id, status, state_json, created_at, last_triggered_at
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
		&rec.Status,
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
			id, organization_id, user_id, title, category_key, category_value, provider, pr_id, status, state_json, created_at, last_triggered_at
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
		&rec.Status,
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

// CreateRun inserts a generic background run.
func (s *Store) CreateRun(ctx context.Context, record RunRecord) (int64, error) {
	timestamp := nowString()
	if record.CreatedAt == "" {
		record.CreatedAt = timestamp
	}
	if record.Status == "" {
		record.Status = RunStatusProcessing
	}
	if record.WorkflowVersion == 0 {
		record.WorkflowVersion = 1
	}
	result, err := s.DB.ExecContext(
		ctx,
		`INSERT INTO runs (kind, organization_id, status, idempotency_key, request_json, result_json, error, workflow_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.Kind,
		record.OrganizationID,
		record.Status,
		record.IdempotencyKey,
		nullString(record.RequestJSON),
		nullString(record.ResultJSON),
		record.Error,
		record.WorkflowVersion,
		record.CreatedAt,
		timestamp,
	)
	if err != nil {
		return 0, fmt.Errorf("insert run: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("run last insert id: %w", err)
	}
	return id, nil
}

// EnsureRun inserts a run with a caller-supplied id when that id is unused.
//
// Sentry addresses runs by the id it was given, so endpoints that can be called
// before their run exists create it on demand instead of failing.
func (s *Store) EnsureRun(ctx context.Context, id int64, record RunRecord) error {
	timestamp := nowString()
	if record.Status == "" {
		record.Status = RunStatusProcessing
	}
	if record.WorkflowVersion == 0 {
		record.WorkflowVersion = 1
	}
	_, err := s.DB.ExecContext(
		ctx,
		`INSERT INTO runs (id, kind, organization_id, status, idempotency_key, request_json, result_json, error, workflow_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		id,
		record.Kind,
		record.OrganizationID,
		record.Status,
		record.IdempotencyKey,
		nullString(record.RequestJSON),
		nullString(record.ResultJSON),
		record.Error,
		record.WorkflowVersion,
		record.CreatedAt,
		timestamp,
	)
	if err != nil {
		return fmt.Errorf("ensure run: %w", err)
	}
	return nil
}

// FindRunByIdempotencyKey returns the run previously created for a provider
// idempotency key, so a retried request reuses its run instead of starting a
// second one.
func (s *Store) FindRunByIdempotencyKey(ctx context.Context, kind, key string) (*RunRecord, error) {
	if key == "" {
		return nil, nil
	}
	row := s.DB.QueryRowContext(
		ctx,
		`SELECT id, kind, organization_id, status, request_json, result_json, error, workflow_version, idempotency_key, created_at, updated_at
		FROM runs WHERE kind = ? AND idempotency_key = ? ORDER BY id ASC LIMIT 1`,
		kind,
		key,
	)
	var rec RunRecord
	var request, result sql.NullString
	var runErr sql.NullString
	if err := row.Scan(&rec.ID, &rec.Kind, &rec.OrganizationID, &rec.Status, &request, &result, &runErr, &rec.WorkflowVersion, &rec.IdempotencyKey, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("find run by idempotency key: %w", err)
	}
	if request.Valid {
		rec.RequestJSON = []byte(request.String)
	}
	if result.Valid {
		rec.ResultJSON = []byte(result.String)
	}
	if runErr.Valid {
		value := runErr.String
		rec.Error = &value
	}
	return &rec, nil
}

// GetRun fetches a generic run by ID.
func (s *Store) GetRun(ctx context.Context, runID int64) (*RunRecord, error) {
	row := s.DB.QueryRowContext(
		ctx,
		`SELECT id, kind, organization_id, status, request_json, result_json, error, workflow_version, idempotency_key, created_at, updated_at FROM runs WHERE id = ?`,
		runID,
	)
	var rec RunRecord
	var request, result sql.NullString
	var runErr sql.NullString
	if err := row.Scan(&rec.ID, &rec.Kind, &rec.OrganizationID, &rec.Status, &request, &result, &runErr, &rec.WorkflowVersion, &rec.IdempotencyKey, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get run: %w", err)
	}
	if request.Valid {
		rec.RequestJSON = []byte(request.String)
	}
	if result.Valid {
		rec.ResultJSON = []byte(result.String)
	}
	if runErr.Valid {
		value := runErr.String
		rec.Error = &value
	}
	return &rec, nil
}

// UpdateRun persists the status, result, and workflow version of a run.
func (s *Store) UpdateRun(ctx context.Context, record RunRecord) error {
	_, err := s.DB.ExecContext(
		ctx,
		`UPDATE runs SET status = ?, result_json = ?, error = ?, workflow_version = ?, updated_at = ? WHERE id = ?`,
		record.Status,
		nullString(record.ResultJSON),
		record.Error,
		record.WorkflowVersion,
		nowString(),
		record.ID,
	)
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	return nil
}

// AppendRunCommand records a command for a run and reports whether it was new.
// A repeated request id is a duplicate and leaves the stored command untouched.
func (s *Store) AppendRunCommand(ctx context.Context, runID int64, requestID string, workflowVersion int64, command []byte) (bool, error) {
	result, err := s.DB.ExecContext(
		ctx,
		`INSERT INTO run_commands (run_id, request_id, workflow_version, command_json, created_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(run_id, request_id) DO NOTHING`,
		runID, requestID, workflowVersion, string(command), nowString(),
	)
	if err != nil {
		return false, fmt.Errorf("append run command: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("append run command rows affected: %w", err)
	}
	return inserted > 0, nil
}

// GetRunCommandVersion returns the stored workflow version for a command, and
// whether that command was already recorded.
func (s *Store) GetRunCommandVersion(ctx context.Context, runID int64, requestID string) (int64, bool, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT workflow_version FROM run_commands WHERE run_id = ? AND request_id = ?`, runID, requestID)
	var version int64
	if err := row.Scan(&version); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("get run command: %w", err)
	}
	return version, true, nil
}

// UpsertReplayBreadcrumbSummary stores a replay breadcrumb summarization.
func (s *Store) UpsertReplayBreadcrumbSummary(ctx context.Context, record ReplayBreadcrumbSummaryRecord) error {
	_, err := s.DB.ExecContext(
		ctx,
		`INSERT INTO replay_breadcrumb_summaries (replay_id, organization_id, project_id, status, created_at, completed_at, summary_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(replay_id) DO UPDATE SET
			organization_id = excluded.organization_id,
			project_id = excluded.project_id,
			status = excluded.status,
			completed_at = excluded.completed_at,
			summary_json = excluded.summary_json`,
		record.ReplayID,
		record.OrganizationID,
		record.ProjectID,
		record.Status,
		record.CreatedAt,
		record.CompletedAt,
		string(record.SummaryJSON),
	)
	if err != nil {
		return fmt.Errorf("upsert replay breadcrumb summary: %w", err)
	}
	return nil
}

// GetReplayBreadcrumbSummary fetches a replay breadcrumb summarization.
func (s *Store) GetReplayBreadcrumbSummary(ctx context.Context, replayID string) (*ReplayBreadcrumbSummaryRecord, error) {
	row := s.DB.QueryRowContext(
		ctx,
		`SELECT replay_id, organization_id, project_id, status, created_at, completed_at, summary_json FROM replay_breadcrumb_summaries WHERE replay_id = ?`,
		replayID,
	)
	var rec ReplayBreadcrumbSummaryRecord
	var summary string
	if err := row.Scan(&rec.ReplayID, &rec.OrganizationID, &rec.ProjectID, &rec.Status, &rec.CreatedAt, &rec.CompletedAt, &summary); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get replay breadcrumb summary: %w", err)
	}
	rec.SummaryJSON = []byte(summary)
	return &rec, nil
}

// DeleteReplayBreadcrumbSummaries removes summarizations for the given replays.
func (s *Store) DeleteReplayBreadcrumbSummaries(ctx context.Context, replayIDs []string) error {
	for _, replayID := range replayIDs {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM replay_breadcrumb_summaries WHERE replay_id = ?`, replayID); err != nil {
			return fmt.Errorf("delete replay breadcrumb summary: %w", err)
		}
	}
	return nil
}

// GetProjectPreference fetches the stored repository preference for an org.
func (s *Store) GetProjectPreference(ctx context.Context, organizationID int64) (*ProjectPreferenceRecord, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT organization_id, repos_json, integration_id, updated_at FROM project_preferences WHERE organization_id = ?`, organizationID)
	var rec ProjectPreferenceRecord
	var repos string
	if err := row.Scan(&rec.OrganizationID, &repos, &rec.IntegrationID, &rec.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get project preference: %w", err)
	}
	rec.ReposJSON = []byte(repos)
	return &rec, nil
}

// SaveProjectPreference upserts the repository preference for an org.
func (s *Store) SaveProjectPreference(ctx context.Context, record ProjectPreferenceRecord) error {
	_, err := s.DB.ExecContext(
		ctx,
		`INSERT INTO project_preferences (organization_id, repos_json, integration_id, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(organization_id) DO UPDATE SET
			repos_json = excluded.repos_json,
			integration_id = excluded.integration_id,
			updated_at = excluded.updated_at`,
		record.OrganizationID,
		string(record.ReposJSON),
		record.IntegrationID,
		nowString(),
	)
	if err != nil {
		return fmt.Errorf("save project preference: %w", err)
	}
	return nil
}

// nullString renders an optional byte payload as a NULL-able column value.
func nullString(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
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
