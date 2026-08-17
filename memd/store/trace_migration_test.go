package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestTraceIntentColumnMigration pins the additive migration: a run_traces
// table created before the intent column existed gains the column on open, its
// existing rows keep NULL intent (never match the FTS), and new rows write
// intent normally.
func TestTraceIntentColumnMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")

	// Create the pre-PC3 schema manually: run_traces without the intent column.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE run_traces (
			run_id TEXT PRIMARY KEY, session_id TEXT, repo_root TEXT NOT NULL,
			tier TEXT NOT NULL, status TEXT NOT NULL, created_at INTEGER NOT NULL,
			payload TEXT NOT NULL
		);
		INSERT INTO run_traces (run_id, repo_root, tier, status, created_at, payload)
		VALUES ('legacy', '/repo', 'light', 'completed', 1, '{}');
	`); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	raw.Close()

	s, err := New(path)
	if err != nil {
		t.Fatalf("New (migrate): %v", err)
	}
	defer s.Close()

	// A legacy row with NULL intent must not match any FTS query.
	rows, err := s.QueryTraces(t.Context(), TraceFilter{RepoRoot: "/repo", Verdict: "kept", Query: "anything"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("legacy NULL-intent row matched FTS: %d rows", len(rows))
	}

	// New writes carry intent and are FTS-matchable.
	if _, err := s.UpsertTrace(t.Context(), &TraceRow{
		RunID: "new-1", RepoRoot: "/repo", Tier: "light", Status: "completed",
		Intent: "add a Hello function", CreatedAt: 2, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("upsert new trace: %v", err)
	}
	if err := s.UpsertVerdict(t.Context(), &VerdictRow{RunID: "new-1", DecidedAt: 2, Verdict: "kept", Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	matched, err := s.QueryTraces(t.Context(), TraceFilter{RepoRoot: "/repo", Verdict: "kept", Query: "add a Hello function"})
	if err != nil {
		t.Fatalf("match query: %v", err)
	}
	if len(matched) != 1 || matched[0].Trace.RunID != "new-1" {
		t.Fatalf("match query = %d rows, want new-1", len(matched))
	}
}
