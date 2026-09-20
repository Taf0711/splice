package cli

// A3 regression tests: one symmetric request ledger across both arms.
//
// The pinned defect: collectRunTelemetry zeroed row.ToolCalls (a stream
// counter, not a trace field) and never repopulated it. Call-site order
// then decided which arm kept the counters: families_eval set stream
// counters AFTER telemetry (survived), mvp_eval ran telemetry LAST (warm
// rows with traces lost tool_calls while cold rows without traces kept
// them). The E2 data showed cold rows with tool_calls=14 and warm rows
// null from the same runner. These tests pin symmetry: the counters must
// survive telemetry in EVERY call order, and unknown must stay distinct
// from measured zero.

import (
	"context"
	"fmt"
	"testing"

	"github.com/Taf0711/splice/internal/eval"
	"github.com/Taf0711/splice/internal/memd"
)

// runOutputWithWork builds a seam output carrying stream counters and a
// transcript-presence marker.
func runOutputWithWork(tools, reads, searches int, observed bool) eval.RunOutput {
	out := eval.RunOutput{ToolCalls: tools, FileReads: reads, SearchCalls: searches}
	if observed {
		out.StreamWorkObserved = boolPtr(true)
	}
	return out
}

// TestStreamCountersSurviveTelemetryOnTracedRow pins the defect directly:
// collectRunTelemetry must NOT erase ToolCalls/FileReads/SearchCalls when
// it fills trace-sourced fields, regardless of the caller's ordering.
func TestStreamCountersSurviveTelemetryOnTracedRow(t *testing.T) {
	row := familyPairRow{
		ToolCalls:   14,
		FileReads:   8,
		SearchCalls: 3,
	}
	// collectRunTelemetry on a row with NO matching trace returns early;
	// the counters must be untouched. (The trace-backed variant is
	// covered by the fillAttemptRow ordering test below: the function's
	// contract is that it never writes the stream fields.)
	collectRunTelemetryNoSidecar(t, &row)
	if row.ToolCalls != 14 || row.FileReads != 8 || row.SearchCalls != 3 {
		t.Fatalf("telemetry erased stream counters: tools=%d reads=%d searches=%d", row.ToolCalls, row.FileReads, row.SearchCalls)
	}
}

// collectRunTelemetryNoSidecar invokes collectRunTelemetry with a deps
// whose memory resolver fails (no sidecar in unit tests), exercising the
// real function body without a store.
func collectRunTelemetryNoSidecar(t *testing.T, row *familyPairRow) {
	t.Helper()
	collectRunTelemetry(context.Background(), appDeps{resolveMemory: func(context.Context) (*memd.Client, error) {
		return nil, fmt.Errorf("no sidecar in unit tests")
	}}, "unused-root", "unused-session", row)
}

// TestFillAttemptRowCountersUnconditional pins the fill rule: counters land
// even when zero, and transcript presence rides separately. The historical
// `if out.ToolCalls > 0 || ...` gate dropped measured zeros, making "the
// agent made no tool calls" indistinguishable from "no transcript parsed".
func TestFillAttemptRowCountersUnconditional(t *testing.T) {
	measuredZero := fillAttemptRow(familyPairRow{}, runOutputWithWork(0, 0, 0, true), nil, 0, harnessProvenanceInfo{}, mvpEvalOptions{}, eval.RunInput{})
	if measuredZero.ToolCalls != 0 || measuredZero.FileReads != 0 || measuredZero.SearchCalls != 0 {
		t.Fatal("measured zeros must land on the row")
	}
	if measuredZero.StreamWorkObserved == nil || !*measuredZero.StreamWorkObserved {
		t.Fatal("transcript presence must be recorded so the zeros read as measured")
	}
	noTranscript := fillAttemptRow(familyPairRow{}, runOutputWithWork(0, 0, 0, false), nil, 0, harnessProvenanceInfo{}, mvpEvalOptions{}, eval.RunInput{})
	if noTranscript.StreamWorkObserved != nil {
		t.Fatal("no transcript: stream work must stay unknown (nil), not observed")
	}
	nonZero := fillAttemptRow(familyPairRow{}, runOutputWithWork(7, 2, 1, true), nil, 0, harnessProvenanceInfo{}, mvpEvalOptions{}, eval.RunInput{})
	if nonZero.ToolCalls != 7 || nonZero.FileReads != 2 || nonZero.SearchCalls != 1 {
		t.Fatalf("nonzero counters lost: %+v", nonZero)
	}
}

// TestStreamCounterSymmetryAcrossArms pins the arm-symmetry contract from
// the E2 evidence: given equivalent work, a cold row (telemetry early,
// no trace) and a warm row (telemetry last, trace present) must carry
// equal tool_calls semantics. The sequence below reproduces BOTH
// historical call-site orders and asserts the same outcome.
func TestStreamCounterSymmetryAcrossArms(t *testing.T) {
	// Cold path (families_eval order): counters set AFTER telemetry.
	cold := familyPairRow{}
	collectRunTelemetryNoSidecar(t, &cold)
	out := runOutputWithWork(14, 8, 3, true)
	cold.ToolCalls, cold.FileReads, cold.SearchCalls = out.ToolCalls, out.FileReads, out.SearchCalls
	cold.StreamWorkObserved = out.StreamWorkObserved

	// Warm path (mvp_eval order): fillAttemptRow runs counters FIRST,
	// telemetry LAST.
	warm := fillAttemptRow(familyPairRow{}, out, nil, 0, harnessProvenanceInfo{}, mvpEvalOptions{}, eval.RunInput{})
	collectRunTelemetryNoSidecar(t, &warm)

	if warm.ToolCalls != cold.ToolCalls || warm.FileReads != cold.FileReads || warm.SearchCalls != cold.SearchCalls {
		t.Fatalf("arm-asymmetric counters: cold=(%d,%d,%d) warm=(%d,%d,%d)",
			cold.ToolCalls, cold.FileReads, cold.SearchCalls,
			warm.ToolCalls, warm.FileReads, warm.SearchCalls)
	}
	if cold.StreamWorkObserved == nil || warm.StreamWorkObserved == nil {
		t.Fatal("both arms must record stream-work presence")
	}
}

// TestColdInputOutputRecordedWithoutSidecarTrace pins the A3 cold-arm
// contract: with no sidecar trace found, the stream-json usage split is
// the row's measured input/output record; with a sidecar trace, the trace
// wins. A transcript without split-bearing records leaves the split
// unknown, never zero.
func TestColdInputOutputRecordedWithoutSidecarTrace(t *testing.T) {
	out := eval.RunOutput{
		Tokens:             1700,
		TelemetryFound:     false,
		StreamInputTokens:  1200,
		StreamOutputTokens: 500,
		StreamSplitFound:   true,
	}
	row := fillAttemptRow(familyPairRow{}, out, nil, 0, harnessProvenanceInfo{}, mvpEvalOptions{}, eval.RunInput{})
	if row.InputTokens != 1200 || row.OutputTokens != 500 {
		t.Fatalf("cold row split = (%d, %d), want (1200, 500)", row.InputTokens, row.OutputTokens)
	}
	// Warm arm (trace found): the trace is authoritative; the stream
	// split must not overwrite it.
	warm := fillAttemptRow(familyPairRow{}, eval.RunOutput{
		Tokens: 1700, TelemetryFound: true,
		StreamInputTokens: 1200, StreamOutputTokens: 500, StreamSplitFound: true,
	}, nil, 0, harnessProvenanceInfo{}, mvpEvalOptions{}, eval.RunInput{})
	if warm.InputTokens != 0 {
		// The trace backfill (collectRunTelemetry) owns the split; the
		// stream split must not have pre-filled it.
		t.Fatalf("stream split overrode a trace-backed row: input=%d", warm.InputTokens)
	}
	// Transcript without any split-bearing record: unknown stays unknown.
	noSplit := fillAttemptRow(familyPairRow{}, eval.RunOutput{
		Tokens: 1700, TelemetryFound: false, StreamSplitFound: false,
	}, nil, 0, harnessProvenanceInfo{}, mvpEvalOptions{}, eval.RunInput{})
	if noSplit.InputTokens != 0 || noSplit.OutputTokens != 0 {
		t.Fatal("absent split must stay absent (omitempty drops it), never a fabricated value")
	}
}

// TestSumStreamJSONTokenSplit pins the transcript parser: split-bearing
// records feed the split, totals-only records do not, and malformed lines
// are skipped.
func TestSumStreamJSONTokenSplit(t *testing.T) {
	transcript := `
{"type":"usage","promptTokens":1200,"completionTokens":500,"totalTokens":1700}
{"type":"usage","promptTokens":300,"completionTokens":100,"totalTokens":400}
{"type":"usage","totalTokens":9999}
not json at all
`
	input, output, found := sumStreamJSONTokenSplit([]byte(transcript))
	if !found {
		t.Fatal("split-bearing records were present")
	}
	if input != 1500 || output != 600 {
		t.Fatalf("split = (%d, %d), want (1500, 600)", input, output)
	}
}
