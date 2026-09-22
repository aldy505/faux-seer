package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateUpgradesLegacyDatabase covers a database created by an older
// binary: the status columns are missing and none of the newer tables exist.
// Opening it must add the columns in place and create the missing tables without
// disturbing existing rows.
func TestMigrateUpgradesLegacyDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")

	legacy, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
CREATE TABLE autofix_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  group_id INTEGER NULL,
  provider TEXT NULL,
  pr_id INTEGER NULL,
  state_json TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE explorer_runs (
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
INSERT INTO autofix_runs (group_id, state_json) VALUES (7, '{"coding_agents":{}}');
INSERT INTO explorer_runs (organization_id, title, state_json, created_at, last_triggered_at, updated_at)
VALUES (1, 'legacy run', '{"status":"completed"}', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z');
`); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	store, err := New(ctx, path)
	if err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	defer func() { _ = store.Close() }()

	for _, table := range []string{"runs", "run_commands", "replay_breadcrumb_summaries", "project_preferences"} {
		var exists bool
		if err := store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("expected migration to create table %s", table)
		}
	}

	// Existing rows keep their data and gain the status default.
	record, err := store.GetAutofixRun(ctx, 1)
	if err != nil {
		t.Fatalf("read migrated autofix run: %v", err)
	}
	if record == nil || record.Status != RunStatusCompleted {
		t.Fatalf("expected the legacy run to default to %q, got %#v", RunStatusCompleted, record)
	}
	if len(record.StateJSON) == 0 {
		t.Fatalf("expected the legacy state blob to survive migration, got %#v", record)
	}

	// A run created after the migration starts as processing.
	newID, err := store.CreateAutofixRun(ctx, nil, []byte(`{}`))
	if err != nil {
		t.Fatalf("create autofix run: %v", err)
	}
	created, err := store.GetAutofixRun(ctx, newID)
	if err != nil {
		t.Fatalf("read new autofix run: %v", err)
	}
	if created.Status != RunStatusProcessing {
		t.Fatalf("expected a new run to start as %q, got %q", RunStatusProcessing, created.Status)
	}

	// Reopening an already-migrated database must be a no-op.
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated database: %v", err)
	}
	reopened, err := New(ctx, path)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat database: %v", err)
	}
}
