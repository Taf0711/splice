package tui

// narration_test.go (P3 GAP-L): the class-normalization table over
// representative rows and the visibility rules per verbosity level. These
// pin the contract the render path will consume; the property they protect
// is "verbosity changes what is SHOWN, never what is RECORDED".

import (
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/tools"
)

// Every transcript row kind maps to exactly one of the 11 contract classes,
// derived from fields that persist across resume (kind/tool/status), so the
// classification is stable after /resume.
func TestNarrationClassTable(t *testing.T) {
	cases := []struct {
		row  transcriptRow
		want NarrationClass
	}{
		{transcriptRow{kind: rowUser, text: "fix the login bug"}, NarrationUser},
		{transcriptRow{kind: rowAssistant, text: "Here is what changed"}, NarrationAgentNarration},
		{transcriptRow{kind: rowReasoning, id: "r1", runID: 1, text: "I should check the tests first"}, NarrationAgentDecision},
		{transcriptRow{kind: rowToolCall, id: "t1", runID: 1, tool: "read_file"}, NarrationAgentAction},
		{transcriptRow{kind: rowToolResult, id: "t1", runID: 1, tool: "read_file", status: tools.StatusOK}, NarrationToolActivity},
		{transcriptRow{kind: rowSpecialist}, NarrationAgentObservation},
		{transcriptRow{kind: rowPermission, permission: &agent.PermissionEvent{ToolCallID: "p1", Action: agent.PermissionActionPrompt}}, NarrationGate},
		{transcriptRow{kind: rowAskUser, id: "a1", runID: 1}, NarrationGate},
		{transcriptRow{kind: rowError, text: "boom"}, NarrationReceipt},
		{transcriptRow{kind: rowSystem, text: "Run cancelled."}, NarrationSystemNotice},
		{transcriptRow{kind: rowRecap, text: "recap: fixed login"}, NarrationSystemNotice},
		{transcriptRow{kind: rowWelcome, text: "Welcome"}, NarrationSystemNotice},
	}
	for _, tc := range cases {
		if got := classifyNarration(tc.row); got != tc.want {
			t.Errorf("classify(%s) = %d, want %d", rowKindName(tc.row.kind), got, tc.want)
		}
	}
}

// Detailed verbosity (the default) shows everything.
func TestNarrationDetailedShowsAll(t *testing.T) {
	rows := allNarrationFixtureRows()
	for i, row := range rows {
		if !narrationVisible(row, verbosityDetailed) {
			t.Errorf("row %d (%s) hidden at detailed", i, rowKindName(row.kind))
		}
	}
}

// Quiet collapses the agent's action call rows and transient system chatter;
// gates, receipts, user, narration, and decisions survive.
func TestNarrationQuietCollapsesActions(t *testing.T) {
	cases := []struct {
		row  transcriptRow
		want bool
	}{
		{transcriptRow{kind: rowUser, text: "prompt"}, true},
		{transcriptRow{kind: rowAssistant, text: "answer"}, true},
		{transcriptRow{kind: rowReasoning, id: "r", runID: 1, text: "decision"}, true},
		{transcriptRow{kind: rowToolCall, id: "t", runID: 1, tool: "bash"}, false},
		{transcriptRow{kind: rowToolResult, id: "t", runID: 1, tool: "bash", status: tools.StatusOK}, false},
		{transcriptRow{kind: rowPermission, permission: &agent.PermissionEvent{ToolCallID: "p", Action: agent.PermissionActionPrompt}}, true},
		{transcriptRow{kind: rowError, text: "failed"}, true},
		{transcriptRow{kind: rowSystem, text: "Transcript cleared."}, false},
	}
	for _, tc := range cases {
		if got := narrationVisible(tc.row, verbosityQuiet); got != tc.want {
			t.Errorf("quiet(%s) = %v, want %v", rowKindName(tc.row.kind), got, tc.want)
		}
	}
}

// Normal keeps tool results (collapsed cards) and system rows, drops only
// the standalone call rows.
func TestNarrationNormalKeepsResults(t *testing.T) {
	if !narrationVisible(transcriptRow{kind: rowToolResult, id: "t", runID: 1, tool: "bash", status: tools.StatusOK}, verbosityNormal) {
		t.Error("normal must keep tool results (their bodies are the evidence)")
	}
	if narrationVisible(transcriptRow{kind: rowToolCall, id: "t", runID: 1, tool: "bash"}, verbosityNormal) {
		t.Error("normal must still drop standalone call rows")
	}
	if !narrationVisible(transcriptRow{kind: rowSystem, text: "note"}, verbosityNormal) {
		t.Error("normal must keep system notices")
	}
}

// Verbosity only changes what is SHOWN: the transcript rows are untouched by
// the classifier (no mutation, no filtering at rest) — the property that
// makes switching live-safe and resume-safe (DoD 41/47/48). Rows are compared
// by identity key + kind + text, since transcriptRow carries non-comparable
// slices.
func TestNarrationVisibilityIsPureProjection(t *testing.T) {
	rows := allNarrationFixtureRows()
	before := make([]string, len(rows))
	for i, row := range rows {
		before[i] = transcriptRowKey(row) + "|" + rowKindName(row.kind) + "|" + row.text
	}
	for _, verbosity := range []narrationVerbosity{verbosityQuiet, verbosityNormal, verbosityDetailed} {
		for _, row := range rows {
			narrationVisible(row, verbosity)
		}
	}
	for i, row := range rows {
		now := transcriptRowKey(row) + "|" + rowKindName(row.kind) + "|" + row.text
		if now != before[i] {
			t.Fatalf("row %d mutated by visibility checks: %q -> %q", i, before[i], now)
		}
	}
}

// Stable identity: the tool/reasoning rows' dedup keys already survive
// rehydration (transcriptRowKey is run-scoped), which is what makes the
// classification stable across resume without a second ID scheme (DoD 43/44).
func TestNarrationRowsKeepStableIdentity(t *testing.T) {
	call := transcriptRow{kind: rowToolCall, id: "call-1", runID: 7, tool: "bash"}
	if key := transcriptRowKey(call); key == "" {
		t.Fatal("tool call row lost its identity key")
	}
	resumed := transcriptRow{kind: rowToolCall, id: "call-1", runID: 7, tool: "bash"}
	if transcriptRowKey(call) != transcriptRowKey(resumed) {
		t.Fatal("identity key differs between live and rehydrated rows")
	}
}

func allNarrationFixtureRows() []transcriptRow {
	return []transcriptRow{
		{kind: rowUser, text: "prompt"},
		{kind: rowAssistant, text: "answer"},
		{kind: rowReasoning, id: "r", runID: 1, text: "decision"},
		{kind: rowToolCall, id: "t", runID: 1, tool: "bash"},
		{kind: rowToolResult, id: "t", runID: 1, tool: "bash", status: tools.StatusOK},
		{kind: rowPermission, permission: &agent.PermissionEvent{ToolCallID: "p", Action: agent.PermissionActionPrompt}},
		{kind: rowAskUser, id: "a", runID: 1},
		{kind: rowError, text: "failed"},
		{kind: rowSpecialist},
		{kind: rowSystem, text: "note"},
		{kind: rowRecap, text: "recap"},
		{kind: rowWelcome, text: "welcome"},
	}
}

func rowKindName(kind rowKind) string {
	switch kind {
	case rowWelcome:
		return "welcome"
	case rowUser:
		return "user"
	case rowAssistant:
		return "assistant"
	case rowReasoning:
		return "reasoning"
	case rowToolCall:
		return "toolCall"
	case rowToolResult:
		return "toolResult"
	case rowPermission:
		return "permission"
	case rowAskUser:
		return "askUser"
	case rowSystem:
		return "system"
	case rowError:
		return "error"
	case rowSpecialist:
		return "specialist"
	case rowRecap:
		return "recap"
	default:
		return "unknown"
	}
}
