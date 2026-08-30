package tui

import (
	"context"
	"strings"
	"testing"
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
			contains: []string{"FAILED", "1m02s", "acceptance_verifier", "2 failing", "restorable", "[R]", "restore", "[I]", "intervene", "[L]", "logs"},
		},
		{
			name:     "cancelled",
			card:     cancelledReceiptCard("code_writer", 3, "0m48s"),
			contains: []string{"CANCELLED", "0m48s", "stopped by you at code_writer", "3 file(s) staged, not applied", "nothing was written", "[A]", "apply staged", "[D]", "discard", "[R]", "resume"},
		},
	}
	for _, tc := range cards {
		t.Run(tc.name, func(t *testing.T) {
			plain := stripANSI(renderReceiptCard(tc.card, 100))
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
	plain := stripANSI(renderReceiptCard(cancelledReceiptCard("code_writer", 3, "0m48s"), 100))
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
	plain := stripANSI(renderReceiptCard(card, 60))
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
	plain := stripANSI(renderReceiptCard(card, 80))
	if strings.Contains(plain, "FAILED") {
		t.Fatalf("cancel projected failure language:\n%s", plain)
	}
}
