package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// TestReceiptCardsRenderActionRow pins the P2b cell-6 contract: all three
// terminal receipts render a styled card with a title, a body, and a
// `[k] action` recovery row — never a bare line.
func TestReceiptCardsRenderActionRow(t *testing.T) {
	cards := []struct {
		name     string
		card     receiptCard
		contains []string
	}{
		{
			name: "verified",
			card: verifiedReceiptCard(
				[]string{"tests 128/128", "lint 0 findings"},
				"3 files · +169 -12",
				"31.2K tok · $0.48",
				"9m24s",
			),
			contains: []string{"VERIFIED", "9m24s", "[+]", "tests 128/128", "[O]", "open diff", "[E]", "export receipt", "[R]", "resume"},
		},
		{
			name: "failed",
			card: failedReceiptCard(
				"acceptance_verifier",
				"tests 126/128 — 2 failing on streamed bodies",
				"test_runner pass 2",
				"1m02s",
			),
			contains: []string{"FAILED", "1m02s", "acceptance_verifier", "2 failing", "restorable", "[R]", "resume", "[I]", "intervene", "[L]", "logs"},
		},
		{
			name:     "cancelled",
			card:     cancelledReceiptCard("code_writer", 3, "0m48s"),
			contains: []string{"CANCELLED", "0m48s", "stopped by you at code_writer", "3 file(s) staged, not applied", "nothing was written", "[A]", "apply staged", "[D]", "discard", "[R]", "resume"},
		},
	}
	for _, tc := range cards {
		t.Run(tc.name, func(t *testing.T) {
			plain := stripANSI(renderReceiptCard(tc.card, 100, presentation.GlyphTierASCII))
			for _, want := range tc.contains {
				if !strings.Contains(plain, want) {
					t.Fatalf("card missing %q:\n%s", want, plain)
				}
			}
		})
	}
}

// TestCancelledReceiptIsNotFailure pins the receipts contract: cancelled
// keeps its own title/gutter/actions and never reuses failure language.
func TestCancelledReceiptIsNotFailure(t *testing.T) {
	plain := stripANSI(renderReceiptCard(cancelledReceiptCard("code_writer", 3, "0m48s"), 100, presentation.GlyphTierASCII))
	if strings.Contains(plain, "FAILED") {
		t.Fatalf("cancelled card leaked failure language:\n%s", plain)
	}
	if !strings.Contains(plain, "not applied") {
		t.Fatalf("cancelled card must distinguish staged from applied:\n%s", plain)
	}
}

// TestReceiptActionsDropWhole pins DoD 18 on the action row: segments drop
// whole under width pressure; keys are never cut mid-word.
func TestReceiptActionsDropWhole(t *testing.T) {
	card := verifiedReceiptCard(nil, "", "", "")
	wide := stripANSI(renderReceiptActions(card.actions, 80))
	if !strings.Contains(wide, "[O] open diff") || !strings.Contains(wide, "[E] export receipt") {
		t.Fatalf("wide action row = %q", wide)
	}
	tight := stripANSI(renderReceiptActions(card.actions, 16))
	if !strings.Contains(tight, "[O] open diff") {
		t.Fatalf("tight action row must keep the first action, got %q", tight)
	}
	if strings.Contains(tight, "…") {
		t.Fatalf("action row truncated mid-key: %q", tight)
	}
}

// TestFailedReceiptKeepsFullReason pins the shot-22 fix: the failure reason
// renders in full (wrapped by the card), never ellipsis-truncated.
func TestFailedReceiptKeepsFullReason(t *testing.T) {
	reason := "stream error: provider stream disconnected mid-critique after 2 of 5 findings were written to the plan draft"
	card := failedReceiptCard("critique", reason, "", "2.1s")
	plain := stripANSI(renderReceiptCard(card, 60, presentation.GlyphTierASCII))
	if !strings.Contains(plain, "provider stream disconnected") {
		t.Fatalf("failure reason lost under width:\n%s", plain)
	}
	if strings.Contains(plain, "…") {
		t.Fatalf("receipt truncated the reason:\n%s", plain)
	}
}

// TestReceiptPayloadRoundTrip pins the transcript-payload contract: encode
// then decode reproduces the card data exactly (it must survive as data —
// transcript rows re-render per width).
func TestReceiptPayloadRoundTrip(t *testing.T) {
	original := failedReceiptCard("critique", "provider stream error", "test_runner pass 2", "2.1s")
	payload := receiptTranscriptPayload(original)
	decoded, ok := parseReceiptTranscriptPayload(payload)
	if !ok {
		t.Fatal("payload failed to round-trip")
	}
	if decoded.kind != original.kind || decoded.title != original.title ||
		decoded.elapsed != original.elapsed || len(decoded.actions) != len(original.actions) ||
		len(decoded.lines) != len(original.lines) {
		t.Fatalf("round-trip mismatch: %+v vs %+v", decoded, original)
	}
	if decoded.lines[0] != original.lines[0] || decoded.actions[0][0] != "R" {
		t.Fatalf("round-trip content mismatch: %+v", decoded)
	}
}

// TestErrorRowRendersReceiptNotPayload proves the render path is WIRED:
// an error row carrying a receipt payload renders the card (title +
// recovery row visible), and the raw marker never leaks into the render
// or the /export text.
func TestErrorRowRendersReceiptCard(t *testing.T) {
	m := mouseTestModel()
	card := failedExecutionCard(context.Canceled)
	row := transcriptRow{
		kind: rowError,
		text: receiptTranscriptPayload(card),
	}
	plain := stripANSI(m.renderRowModeUncached(row, 100, rowContext{}, cardRenderOptions{}))
	if !strings.Contains(plain, "CANCELLED") || !strings.Contains(plain, "[R] resume") {
		t.Fatalf("receipt payload did not render as a card:\n%s", plain)
	}
	if strings.Contains(plain, "\x00") {
		t.Fatalf("receipt marker leaked into the render:\n%s", plain)
	}
}

// TestCancelErrorProjectsCancelledReceipt pins the runtime wiring: a
// user-cancel error produces the CANCELLED card, not FAILED — the receipts
// contract's cancelled-is-not-failure rule, wired from run.go's context
// cancellation through to the transcript render.
func TestCancelErrorProjectsCancelledReceipt(t *testing.T) {
	card := failedExecutionCard(context.Canceled)
	if card.kind != receiptCancelled {
		t.Fatalf("canceled error projected %q, want cancelled", card.kind)
	}
	plain := stripANSI(renderReceiptCard(card, 80, presentation.GlyphTierASCII))
	if strings.Contains(plain, "FAILED") {
		t.Fatalf("cancel projected failure language:\n%s", plain)
	}
}

// An unverified run renders the UNVERIFIED card: the title says NOT VERIFIED,
// the body names the reason, and the bare word VERIFIED never appears.
func TestUnverifiedReceiptNeverReadsVerified(t *testing.T) {
	card := unverifiedReceiptCard("no check applied: static_analyzer (No applicable files to verify.)", nil, "3.0s")
	if card.kind != receiptUnverified {
		t.Fatalf("kind = %q, want unverified", card.kind)
	}
	view := plainRender(t, renderReceiptCard(card, 80, presentation.GlyphTierASCII))
	for _, want := range []string{"DONE · NOT VERIFIED", "no check applied: static_analyzer", "[O] open diff"} {
		if !strings.Contains(view, want) {
			t.Fatalf("unverified receipt missing %q:\n%s", want, view)
		}
	}
	if strings.Count(view, "VERIFIED") != strings.Count(view, "NOT VERIFIED") {
		t.Fatalf("unverified receipt reads as VERIFIED:\n%s", view)
	}
}

// A test command that passed with no per-test evidence has its own receipt:
// TEST COMMAND PASSED, with the same kind and visual weight as NOT VERIFIED.
// The renderer picks the style by kind, so the unverified kind proves the card
// never takes the VERIFIED success color.
func TestCommandPassedReceiptIsNotVerified(t *testing.T) {
	card := unverifiedReceiptFromState("test command passed; per-test evidence unavailable", true, nil, time.Time{}, time.Time{})
	if card.kind != receiptUnverified {
		t.Fatalf("kind = %q, want unverified (never the verified style)", card.kind)
	}
	if card.title != "DONE · TEST COMMAND PASSED" {
		t.Fatalf("title = %q, want DONE · TEST COMMAND PASSED", card.title)
	}
	view := plainRender(t, renderReceiptCard(card, 80, presentation.GlyphTierASCII))
	for _, want := range []string{"[!] DONE · TEST COMMAND PASSED", "Per-test evidence is not available. The run is not verified.", "[O] open diff"} {
		if !strings.Contains(view, want) {
			t.Fatalf("command-passed receipt missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "VERIFIED") {
		t.Fatalf("command-passed receipt reads as VERIFIED:\n%s", view)
	}
	decoded, ok := parseReceiptTranscriptPayload(receiptTranscriptPayload(card))
	if !ok || decoded.title != card.title || decoded.kind != receiptUnverified {
		t.Fatalf("round-trip = %+v, want the command-passed card", decoded)
	}

	// The live path: a completion receipt marked command_passed.
	m := mouseTestModel()
	m.sessionStore = testSessionStore(t)
	m.activeRunID = 9
	m.width, m.height, m.altScreen = 100, 40, true
	m.lastState = presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleComplete,
		Completion:    &presentation.CompletionReceipt{Status: "completed", Detail: "test command passed; per-test evidence unavailable", Unverified: true, CommandPassed: true},
	}
	updated, _ := m.Update(planExecutionResultMsg{
		runID:  9,
		result: agent.Result{FinalAnswer: `{"plan_id":"p","status":"completed","completed_tasks":[{"task_id":"t1","run_id":"r1","status":"completed"}]}`},
	})
	next := updated.(model)
	if next.lastTerminalReceipt != receiptUnverified {
		t.Fatalf("lastTerminalReceipt = %q, want unverified", next.lastTerminalReceipt)
	}
	live := plainRender(t, next.View())
	if !strings.Contains(live, "TEST COMMAND PASSED") || !strings.Contains(live, "The run is not verified.") {
		t.Fatalf("live view lacks the command-passed receipt:\n%s", live)
	}
}

func TestUnverifiedReceiptPayloadRoundTrip(t *testing.T) {
	original := unverifiedReceiptCard("no tests ran", nil, "")
	decoded, ok := parseReceiptTranscriptPayload(receiptTranscriptPayload(original))
	if !ok {
		t.Fatal("unverified payload failed to round-trip")
	}
	if decoded.kind != receiptUnverified || decoded.lines[0] != "no tests ran" {
		t.Fatalf("round-trip = %+v, want unverified with its reason", decoded)
	}
}

// A completed run whose receipt is unverified projects the UNVERIFIED card and
// arms its keys, even though the persisted result carries evidence-like JSON.
func TestUnverifiedRunProjectsUnverifiedReceipt(t *testing.T) {
	m := mouseTestModel()
	m.sessionStore = testSessionStore(t)
	m.activeRunID = 9
	m.width, m.height, m.altScreen = 100, 40, true
	m.lastState = presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleComplete,
		Completion:    &presentation.CompletionReceipt{Status: "completed", Detail: "no check applied: static_analyzer (n/a)", Unverified: true},
	}
	updated, _ := m.Update(planExecutionResultMsg{
		runID:  9,
		result: agent.Result{FinalAnswer: `{"plan_id":"p","status":"completed","completed_tasks":[{"task_id":"t1","run_id":"r1","status":"completed"}]}`},
	})
	next := updated.(model)
	if next.lastTerminalReceipt != receiptUnverified {
		t.Fatalf("lastTerminalReceipt = %q, want unverified", next.lastTerminalReceipt)
	}
	view := plainRender(t, next.View())
	if !strings.Contains(view, "NOT VERIFIED") || !strings.Contains(view, "no check applied: static_analyzer") {
		t.Fatalf("live view lacks the unverified receipt:\n%s", view)
	}
	if strings.Contains(view, "design plan p completed") {
		t.Fatalf("unverified run rendered VERIFIED evidence:\n%s", view)
	}
}

func TestReplayArmsUnverifiedReceipt(t *testing.T) {
	state := presentation.State{Completion: &presentation.CompletionReceipt{Status: "completed", Unverified: true}}
	if got := terminalReceiptForState(state); got != receiptUnverified {
		t.Fatalf("terminalReceiptForState = %q, want unverified", got)
	}
	state.Completion.Unverified = false
	if got := terminalReceiptForState(state); got != receiptVerified {
		t.Fatalf("verified completion = %q, want verified", got)
	}
}

// A verified completion shows the VERIFIED card and not the raw plan JSON. A
// result that no card can be built from (not valid JSON) keeps its raw text.
func TestVerifiedRunShowsReceiptNotRawPlanJSON(t *testing.T) {
	const planJSON = `{"plan_id":"p","status":"completed","completed_tasks":[{"task_id":"t1","run_id":"r1","status":"completed"}]}`
	completed := presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleComplete,
		Completion:    &presentation.CompletionReceipt{Status: "completed"},
	}

	m := mouseTestModel()
	m.sessionStore = testSessionStore(t)
	m.activeRunID = 10
	m.lastState = completed
	updated, _ := m.Update(planExecutionResultMsg{runID: 10, result: agent.Result{FinalAnswer: planJSON}})
	next := updated.(model)
	if next.lastTerminalReceipt != receiptVerified {
		t.Fatalf("lastTerminalReceipt = %q, want verified", next.lastTerminalReceipt)
	}
	receiptRows := 0
	for _, row := range next.transcript {
		if row.kind == rowAssistant && strings.HasPrefix(row.text, `{"plan_id"`) {
			t.Fatalf("verified run appended the raw plan JSON: %q", row.text)
		}
		if card, ok := parseReceiptTranscriptPayload(row.text); ok && card.kind == receiptVerified {
			receiptRows++
		}
	}
	if receiptRows != 1 {
		t.Fatalf("verified receipt rows = %d, want 1", receiptRows)
	}

	m = mouseTestModel()
	m.sessionStore = testSessionStore(t)
	m.activeRunID = 11
	m.lastState = completed
	updated, _ = m.Update(planExecutionResultMsg{runID: 11, result: agent.Result{FinalAnswer: "plain answer"}})
	next = updated.(model)
	found := false
	for _, row := range next.transcript {
		if row.kind == rowAssistant && row.text == "plain answer" {
			found = true
		}
	}
	if !found {
		t.Fatal("a result no card can be built from lost its raw text")
	}
}

// TestPipelineReceiptNamesVerificationChecks pins the per-check receipt
// lines. A VERIFIED card names what verified it. A scanner-only verification
// says on its own line that no tests ran, never with the [+] evidence mark.
// The NOT VERIFIED and TEST COMMAND PASSED cards list each check, and only a
// verified run gets the success kind.
func TestPipelineReceiptNamesVerificationChecks(t *testing.T) {
	testsPassed := schemas.VerificationCheck{Name: "test_runner", Kind: schemas.CheckKindTests, Outcome: schemas.CheckPassed, Detail: "12 passed, 0 failed, per-test evidence"}
	testsFailed := schemas.VerificationCheck{Name: "test_runner", Kind: schemas.CheckKindTests, Outcome: schemas.CheckFailed, Detail: "10 passed, 2 failed, per-test evidence"}
	noTests := schemas.VerificationCheck{Name: "test_runner", Kind: schemas.CheckKindTests, Outcome: schemas.CheckNoTests, Detail: "no tests ran"}
	commandPassed := schemas.VerificationCheck{Name: "test_runner", Kind: schemas.CheckKindTests, Outcome: schemas.CheckCommandPassed, Detail: "exit code 0, per-test evidence unavailable"}
	scanner := schemas.VerificationCheck{Name: "security_auditor", Kind: schemas.CheckKindSecurityScan, Outcome: schemas.CheckPassed, Detail: "bandit"}
	static := schemas.VerificationCheck{Name: "static_analyzer", Kind: schemas.CheckKindStaticAnalysis, Outcome: schemas.CheckNotApplicable, Detail: "No applicable files to verify."}

	cases := []struct {
		name      string
		result    schemas.PipelineResult
		wantKind  receiptKind
		wantTitle string
		want      []string
		notWant   []string
	}{
		{
			name:      "tests and scanner verified",
			result:    schemas.PipelineResult{Verified: true, VerificationOutcome: schemas.VerificationOutcomeVerified, VerificationChecks: []schemas.VerificationCheck{testsPassed, scanner}},
			wantKind:  receiptVerified,
			wantTitle: "VERIFIED",
			want:      []string{"[+] Verified by: tests (12 passed, 0 failed, per-test evidence), security scan (bandit)"},
			notWant:   []string{"Tests:", "no tests ran"},
		},
		{
			name:      "scanner only verified",
			result:    schemas.PipelineResult{Verified: true, VerificationOutcome: schemas.VerificationOutcomeVerified, VerificationChecks: []schemas.VerificationCheck{noTests, scanner, static}},
			wantKind:  receiptVerified,
			wantTitle: "VERIFIED",
			want:      []string{"[+] Verified by: security scan (bandit)", "  Tests: no tests ran.", "  Other checks: static analysis: not applicable (No applicable files to verify.)"},
			notWant:   []string{"[+] Tests", "[+] Other", "tests ("},
		},
		{
			name:      "command passed",
			result:    schemas.PipelineResult{VerificationOutcome: schemas.VerificationOutcomeCommandPassed, UnverifiedReason: "test command passed; per-test evidence unavailable", VerificationChecks: []schemas.VerificationCheck{commandPassed}},
			wantKind:  receiptUnverified,
			wantTitle: "DONE · TEST COMMAND PASSED",
			want:      []string{"tests: command passed (exit code 0, per-test evidence unavailable)"},
			notWant:   []string{"[+]"},
		},
		{
			name:      "failed tests with a passed scanner",
			result:    schemas.PipelineResult{VerificationOutcome: schemas.VerificationOutcomeUnverified, UnverifiedReason: "tests failed", VerificationChecks: []schemas.VerificationCheck{testsFailed, scanner}},
			wantKind:  receiptUnverified,
			wantTitle: "DONE · NOT VERIFIED",
			want:      []string{"tests failed", "tests: failed (10 passed, 2 failed, per-test evidence)", "security scan: passed (bandit)"},
			notWant:   []string{"[+]", "Verified by"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.result.RunID, tc.result.Status, tc.result.Tier = "run-1", "completed", schemas.TierLight
			encoded, err := json.Marshal(tc.result)
			if err != nil {
				t.Fatalf("setup: marshal result: %v", err)
			}
			card, ok := pipelineTurnReceipt(string(encoded), "", nil, nil, time.Time{}, time.Time{})
			if !ok {
				t.Fatal("no receipt card")
			}
			if card.kind != tc.wantKind || card.title != tc.wantTitle {
				t.Fatalf("card kind %q title %q, want %q %q", card.kind, card.title, tc.wantKind, tc.wantTitle)
			}
			joined := strings.Join(card.lines, "\n")
			for _, want := range tc.want {
				trimmed := strings.TrimPrefix(want, "  ")
				if !strings.Contains(joined, trimmed) {
					t.Errorf("card lines lack %q:\n%s", trimmed, joined)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(joined, notWant) {
					t.Errorf("card lines contain %q:\n%s", notWant, joined)
				}
			}
			// Narrow terminal: the card wraps and keeps every word.
			view := plainRender(t, renderReceiptCard(card, 48, presentation.GlyphTierASCII))
			for _, word := range []string{"bandit", "tests"} {
				if strings.Contains(joined, word) && !strings.Contains(view, word) {
					t.Errorf("narrow render lost %q:\n%s", word, view)
				}
			}
			decoded, ok := parseReceiptTranscriptPayload(receiptTranscriptPayload(card))
			if !ok || strings.Join(decoded.lines, "\n") != joined {
				t.Fatalf("payload round trip lost the check lines: %+v", decoded)
			}
		})
	}
}

// TestPipelineReceiptShowsAcceptedNestedRun checks a result that ran under an
// outer sandbox the user accepted shows the plain line in every state,
// VERIFIED included, never with the [+] success mark. A result without the
// flag has no such line.
func TestPipelineReceiptShowsAcceptedNestedRun(t *testing.T) {
	passed := schemas.VerificationCheck{Name: "test_runner", Kind: schemas.CheckKindTests, Outcome: schemas.CheckPassed, Detail: "1 passed, 0 failed, per-test evidence"}
	abort := "tests failed twice"
	results := map[string]schemas.PipelineResult{
		"verified":   {Status: "completed", Verified: true, VerificationOutcome: schemas.VerificationOutcomeVerified, VerificationChecks: []schemas.VerificationCheck{passed}},
		"unverified": {Status: "completed", VerificationOutcome: schemas.VerificationOutcomeUnverified, UnverifiedReason: "no tests ran"},
		"failed":     {Status: "aborted", AbortReason: &abort},
		"cancelled":  {Status: "aborted", UserAborted: true},
	}
	for name, result := range results {
		t.Run(name, func(t *testing.T) {
			for _, accepted := range []bool{true, false} {
				result.RunID, result.Tier, result.NestedSandboxAccepted = "run-1", schemas.TierLight, accepted
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Fatalf("setup: marshal result: %v", err)
				}
				card, ok := pipelineTurnReceipt(string(encoded), "", nil, nil, time.Time{}, time.Time{})
				if !ok {
					t.Fatal("no receipt card")
				}
				count := 0
				for _, line := range card.lines {
					if strings.Contains(line, presentation.NestedSandboxAcceptedLine) {
						count++
						if line != presentation.NestedSandboxAcceptedLine {
							t.Errorf("accepted line has extra marks: %q", line)
						}
					}
				}
				if want := map[bool]int{true: 1, false: 0}[accepted]; count != want {
					t.Errorf("accepted=%v: the line appears %d time(s), want %d; lines=%q", accepted, count, want, card.lines)
				}
			}
		})
	}
}

// TestUnverifiedCompletionCarriesChecks pins the presentation path: an
// unverified completion receipt carries its checks to the card.
func TestUnverifiedCompletionCarriesChecks(t *testing.T) {
	checks := []schemas.VerificationCheck{{Name: "test_runner", Kind: schemas.CheckKindTests, Outcome: schemas.CheckNoTests, Detail: "no tests ran"}}
	completion := &presentation.CompletionReceipt{Status: "completed", Detail: "no check applied: tests (no tests ran)", Unverified: true, Checks: checks}
	if err := completion.Validate(); err != nil {
		t.Fatalf("completion: %v", err)
	}
	card, ok := verifiedReceiptCardFor(completion, "", presentation.UsageSummary{}, time.Time{}, time.Time{})
	if !ok || card.kind != receiptUnverified {
		t.Fatalf("card = %+v ok=%v, want unverified", card, ok)
	}
	if got := strings.Join(card.lines, "|"); got != "no check applied: tests (no tests ran)|tests: no tests ran" {
		t.Fatalf("card lines = %q", got)
	}
}
