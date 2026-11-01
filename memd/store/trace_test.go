package store

import (
	"context"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir() + "/memory.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func tracePayload(runID, repoRoot string) []byte {
	return []byte(`{"schema_version":"1","run_id":"` + runID + `","repo_root":"` + repoRoot + `","tier":"light","outcome":{"status":"completed"},"plan":{"tier":"light","request_intent":"x","stages":[{"name":"code_writer","budget":{"input_max":1,"output_max":1}}],"token_budget":{"total_input_budget":1,"total_output_budget":1}}}`)
}

// TestUpsertTraceUpdatesAndGuardsSettledRows pins the true-upsert contract: a
// later write replaces the row, except a "running" partial write never clobbers
// a settled (completed/aborted/failed) row.
func TestUpsertTraceUpdatesAndGuardsSettledRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// A partial write lands first with status running.
	inserted, err := s.UpsertTrace(ctx, &TraceRow{
		RunID: "run-1", SessionID: "sess-1", RepoRoot: "/repo", Tier: "light",
		Status: "running", CreatedAt: 1000, Payload: []byte(`{"partial":true}`),
	})
	if err != nil || !inserted {
		t.Fatalf("partial upsert inserted=%v err=%v", inserted, err)
	}

	// The final write replaces the row (running -> completed).
	inserted, err = s.UpsertTrace(ctx, &TraceRow{
		RunID: "run-1", SessionID: "sess-1", RepoRoot: "/repo", Tier: "light",
		Status: "completed", CreatedAt: 2000, Payload: []byte(`{"final":true}`),
	})
	if err != nil || !inserted {
		t.Fatalf("final upsert inserted=%v err=%v", inserted, err)
	}

	// A late partial write must not clobber the settled row.
	inserted, err = s.UpsertTrace(ctx, &TraceRow{
		RunID: "run-1", RepoRoot: "/repo", Tier: "light",
		Status: "running", CreatedAt: 3000, Payload: []byte(`{"late":true}`),
	})
	if err != nil || inserted {
		t.Fatalf("late running upsert inserted=%v err=%v, want guarded no-op", inserted, err)
	}

	rows, err := s.QueryTraces(ctx, TraceFilter{RepoRoot: "/repo"})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Trace.Status != "completed" {
		t.Fatalf("status = %q, want completed (settled row preserved)", rows[0].Trace.Status)
	}
	if string(rows[0].Trace.Payload) != `{"final":true}` {
		t.Fatalf("payload = %s, want final payload preserved", rows[0].Trace.Payload)
	}
}

// TestQueryTracesStatusFilterExcludesRunning pins that a partial "running" row
// is invisible to the learning queries, which filter completed and aborted only.
func TestQueryTracesStatusFilterExcludesRunning(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.UpsertTrace(ctx, &TraceRow{RunID: "run-running", RepoRoot: "/repo", Tier: "light", Status: "running", Payload: []byte(`{"run_id":"run-running"}`)}); err != nil {
		t.Fatalf("UpsertTrace running: %v", err)
	}
	if _, err := s.UpsertTrace(ctx, &TraceRow{RunID: "run-done", RepoRoot: "/repo", Tier: "light", Status: "completed", Payload: []byte(`{"run_id":"run-done"}`)}); err != nil {
		t.Fatalf("UpsertTrace completed: %v", err)
	}

	completed, err := s.QueryTraces(ctx, TraceFilter{RepoRoot: "/repo", Status: "completed"})
	if err != nil {
		t.Fatalf("QueryTraces completed: %v", err)
	}
	if len(completed) != 1 || completed[0].Trace.RunID != "run-done" {
		t.Fatalf("completed rows = %#v, want only run-done (running row excluded)", completed)
	}
}

// TestTraceQuerySelfContained pins self-containment: the stored payload
// round-trips byte-identically, so a trace reconstructs its plan and stages
// with zero external references.
func TestTraceQuerySelfContained(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	payload := tracePayload("run-2", "/repo")

	if _, err := s.UpsertTrace(ctx, &TraceRow{
		RunID: "run-2", RepoRoot: "/repo", Tier: "light", Status: "completed",
		CreatedAt: 1000, Payload: payload,
	}); err != nil {
		t.Fatalf("UpsertTrace: %v", err)
	}

	rows, err := s.QueryTraces(ctx, TraceFilter{RepoRoot: "/repo", Tier: "light", Status: "completed", Since: 999})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if string(rows[0].Trace.Payload) != string(payload) {
		t.Fatalf("payload not self-contained:\n got %s\nwant %s", rows[0].Trace.Payload, payload)
	}
	if !contains(rows[0].Trace.Payload, "request_intent") || !contains(rows[0].Trace.Payload, "stages") {
		t.Fatalf("payload must embed the plan; got %s", rows[0].Trace.Payload)
	}
}

// TestUnknownVerdictIsAbsentVerdict pins the unknown-verdict contract: a trace
// with no verdict row returns a nil Verdict, never a fabricated default.
func TestUnknownVerdictIsAbsentVerdict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.UpsertTrace(ctx, &TraceRow{
		RunID: "run-3", RepoRoot: "/repo", Tier: "light", Status: "completed",
		CreatedAt: 1000, Payload: tracePayload("run-3", "/repo"),
	}); err != nil {
		t.Fatalf("UpsertTrace: %v", err)
	}

	rows, err := s.QueryTraces(ctx, TraceFilter{})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Verdict != nil {
		t.Fatalf("unknown verdict must be a nil Verdict, got %#v", rows[0].Verdict)
	}
}

// TestVerdictLatestWins pins the append-only verdict contract: multiple
// verdicts for a run are appended, and the latest decided_at wins at query.
func TestVerdictLatestWins(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.UpsertTrace(ctx, &TraceRow{
		RunID: "run-4", RepoRoot: "/repo", Tier: "light", Status: "completed",
		CreatedAt: 1000, Payload: tracePayload("run-4", "/repo"),
	}); err != nil {
		t.Fatalf("UpsertTrace: %v", err)
	}

	if err := s.UpsertVerdict(ctx, &VerdictRow{
		RunID: "run-4", DecidedAt: 1000, Verdict: "rejected", Reason: "wrong_approach",
		Payload: []byte(`{"run_id":"run-4","verdict":"rejected","reject_reason":"wrong_approach"}`),
	}); err != nil {
		t.Fatalf("UpsertVerdict: %v", err)
	}
	if err := s.UpsertVerdict(ctx, &VerdictRow{
		RunID: "run-4", DecidedAt: 2000, Verdict: "kept", Reason: "",
		Payload: []byte(`{"run_id":"run-4","verdict":"kept"}`),
	}); err != nil {
		t.Fatalf("UpsertVerdict: %v", err)
	}

	rows, err := s.QueryTraces(ctx, TraceFilter{})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(rows) != 1 || rows[0].Verdict == nil {
		t.Fatalf("expected one trace with a verdict, got %d rows verdict=%#v", len(rows), rows[0].Verdict)
	}
	if rows[0].Verdict.Verdict != "kept" {
		t.Fatalf("latest verdict = %q, want kept", rows[0].Verdict.Verdict)
	}
	if rows[0].Verdict.DecidedAt != 2000 {
		t.Fatalf("latest decided_at = %d, want 2000", rows[0].Verdict.DecidedAt)
	}
}

func contains(b []byte, sub string) bool {
	return strings.Contains(string(b), sub)
}
