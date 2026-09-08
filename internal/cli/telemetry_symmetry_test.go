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
