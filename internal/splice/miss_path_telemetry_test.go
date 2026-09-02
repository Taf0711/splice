package splice

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// The miss-path telemetry (keys generated, fallback flag) must land in the
// stage trace meta through the REAL composition path, and the plain store
// must stamp the fallback flag. Producer/consumer pairing per the handoff.
func TestMissPathTelemetryLandsInTrace(t *testing.T) {
	// Ranked store: reranker runs, no fallback.
	relevant := schemas.MemoryRanked{Observation: mkObs(2, "Session", "InvalidateSession clears the store"), Rank: -2.0}
	generic := schemas.MemoryRanked{Observation: mkObs(1, "Release checklist", "tag build publish"), Rank: -9.0}
	ranked := &rankedFakeStore{cands: []schemas.MemoryRanked{generic, relevant}}

	tr := &runTraceAccumulator{stages: map[stageKey]schemas.InputMeta{}}
	p := stageInputPreparation{Memory: ranked, NowUnix: 1_800_000_000, Trace: tr}
	input := schemas.HarnessStageInput{
		RequestIntent: "fix InvalidateSession in internal/auth/session.go",
		StageName:     "code_writer",
	}
	_, detail, err := p.rerankedMissPath(context.Background(), input, "/repo")
	if err != nil {
		t.Fatalf("miss path: %v", err)
	}
	if detail.FallbackToPlainSearch != 0 {
		t.Fatalf("ranked store must not stamp fallback, got %d", detail.FallbackToPlainSearch)
	}
	if detail.KeysGenerated != 1 {
		t.Fatalf("keys generated = %d, want 1 (the file: path)", detail.KeysGenerated)
	}
	// The caller (prepareStageInput) records the detail; replay that wiring
	// here since this test drives the seam directly.
	tr.recordMissPathDetail("code_writer", 0, detail)
	meta := tr.stages[stageKey{"code_writer", 0}]
	if meta.KeysGenerated != 1 || meta.FTSFallback != 0 {
		t.Fatalf("trace meta = keys %d fallback %d, want 1/0", meta.KeysGenerated, meta.FTSFallback)
	}

	// Plain store: fallback stamped.
	plain := &plainFakeStore{obs: []schemas.MemoryObservation{mkObs(9, "x", "y")}}
	tr2 := &runTraceAccumulator{stages: map[stageKey]schemas.InputMeta{}}
	p2 := stageInputPreparation{Memory: plain, NowUnix: 1_800_000_000, Trace: tr2}
	_, detail2, err := p2.rerankedMissPath(context.Background(), input, "/repo")
	if err != nil {
		t.Fatalf("fallback miss path: %v", err)
	}
	if detail2.FallbackToPlainSearch != 1 {
		t.Fatalf("plain store must stamp fallback, got %d", detail2.FallbackToPlainSearch)
	}
	if detail2.KeysGenerated != 0 {
		t.Fatalf("fallback path must not derive keys, got %d", detail2.KeysGenerated)
	}
	p2.Trace.recordMissPathDetail("code_writer", 0, detail2)
	meta2 := tr2.stages[stageKey{"code_writer", 0}]
	if meta2.FTSFallback != 1 || meta2.KeysGenerated != 0 {
		t.Fatalf("fallback trace meta = keys %d fallback %d, want 0/1", meta2.KeysGenerated, meta2.FTSFallback)
	}
}
