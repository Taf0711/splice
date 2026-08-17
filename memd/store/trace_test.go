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

// TestUpsertTraceWriteOnce pins the append-only write-once contract: a second
// upsert of the same run_id leaves the first payload byte-identical and never
// updates the row.
func TestUpsertTraceWriteOnce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	payload := tracePayload("run-1", "/repo")

	inserted, err := s.UpsertTrace(ctx, &TraceRow{
		RunID: "run-1", SessionID: "sess-1", RepoRoot: "/repo", Tier: "light",
		Status: "completed", CreatedAt: 1000, Payload: payload,
	})
	if err != nil || !inserted {
		t.Fatalf("first upsert inserted=%v err=%v", inserted, err)
	}

	// Second write with different content must be a no-op, never an update.
	inserted, err = s.UpsertTrace(ctx, &TraceRow{
		RunID: "run-1", SessionID: "sess-1", RepoRoot: "/repo", Tier: "light",
		Status: "failed", CreatedAt: 2000, Payload: []byte(`{"status":"different"}`),
	})
	if err != nil || inserted {
		t.Fatalf("second upsert inserted=%v err=%v, want no-op", inserted, err)
	}

	rows, err := s.QueryTraces(ctx, TraceFilter{RepoRoot: "/repo"})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if string(rows[0].Trace.Payload) != string(payload) {
		t.Fatalf("payload changed after duplicate upsert:\n got %s\nwant %s", rows[0].Trace.Payload, payload)
	}
	if rows[0].Trace.Status != "completed" {
		t.Fatalf("status = %q, want completed (first write wins)", rows[0].Trace.Status)
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
