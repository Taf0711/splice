package tui

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// P4 lifecycle card fixtures.
func testPlan() schemas.DesignPlan {
	verification := "go test ./internal/http"
	return schemas.DesignPlan{
		Epic:         "retry semantics",
		Requirements: []string{"idempotent retries"},
		Tasks: []schemas.Task{
			{ID: "t1", Title: "classify retries", TargetPaths: []string{"policy.go"}, AcceptanceFacts: []schemas.AcceptanceFact{{Statement: "policy table covers GET/POST", AutomatedVerification: true, VerificationCommand: &verification}}},
			{ID: "t2", Title: "thread deadline", TargetPaths: []string{"client.go"}},
		},
	}
}

func testCritiqueBlocking() schemas.PlanCritique {
	return schemas.PlanCritique{
		Critiques: []schemas.Critique{
			{Category: "correctness", Severity: schemas.SeverityHigh, Issue: "unbounded retry on 5xx", SuggestedMitigation: "cap at 3"},
			{Category: "complexity", Severity: schemas.SeverityMedium, Issue: "jitter unseeded"},
		},
		MustFixBeforeExecution: true,
		OverallAssessment:      "one blocking correctness issue",
	}
}

// TestCritiqueCardRendersTypedFindings pins P4 E4 (§7.4): findings carry
// their REQUIRED/ADVISORY class, category, issue, and fix; required issues
// block approval and the card says so with fold/revise actions.
func TestCritiqueCardRendersTypedFindings(t *testing.T) {
	card := renderCritiqueCard(testPlan(), testCritiqueBlocking(), 90)
	plain := stripANSI(card)
	for _, want := range []string{
		"CRITIQUE", "1 required · 1 advisory",
		"[!]", "REQUIRED", "correctness", "unbounded retry on 5xx", "-> fix: cap at 3",
		"[~]", "ADVISORY", "complexity", "jitter unseeded",
		"BLOCKED by required issues",
		"[F] fold required fixes", "[R] revise",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("critique card missing %q:\n%s", want, plain)
		}
	}
}

// TestCritiqueCardCleanPinsApprovalReady pins the clean-critique verdict:
// no blocking language, approval + revise offered.
func TestCritiqueCardCleanPinsApprovalReady(t *testing.T) {
	clean := schemas.PlanCritique{}
	plain := stripANSI(renderCritiqueCard(testPlan(), clean, 90))
	if strings.Contains(plain, "BLOCKED") {
		t.Fatalf("clean critique shows BLOCKED:\n%s", plain)
	}
	if !strings.Contains(plain, "ready to approve") || !strings.Contains(plain, "[A] approve") {
		t.Fatalf("clean critique missing approval affordance:\n%s", plain)
	}
}

// TestPlanCardRendersTasksAndChecks pins P4 E3 (§7.3): numbered tasks with
// targets, the acceptance-check count, and the explicit-gesture approve row.
func TestPlanCardRendersTasksAndChecks(t *testing.T) {
	plain := stripANSI(renderImplementationPlanCard(testPlan(), true, 90))
	for _, want := range []string{
		"IMPLEMENTATION PLAN", "2 tasks · 1 acceptance checks", "critique clean",
		"01", "classify retries", "policy.go",
		"02", "thread deadline",
		"[A] approve (explicit)", "[R] revise",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("plan card missing %q:\n%s", want, plain)
		}
	}
}

// TestCrystallizingCardNeverOffersApproval pins DoD 7 (§7.2): the
// crystallizing card always states "not a contract yet" and carries no
// approve affordance.
func TestCrystallizingCardNeverOffersApproval(t *testing.T) {
	plain := stripANSI(renderCrystallizingCard(true, true, true, 3, 90))
	if !strings.Contains(plain, "not a contract yet") {
		t.Fatalf("crystallizing card missing the not-a-contract line:\n%s", plain)
	}
	if strings.Contains(strings.ToLower(plain), "approve") {
		t.Fatalf("crystallizing card offers approval (DoD 7):\n%s", plain)
	}
	for _, want := range []string{"[+] settled", "[>] drafting", "[ ] critique", "[ ] acceptance"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("crystallizing card missing %q:\n%s", want, plain)
		}
	}
}

// TestLifecycleCardPayloadRoundTrip pins the tagged-row contract: the card
// text flows through the payload detector and re-renders at a new width
// with content preserved and the marker stripped.
func TestLifecycleCardPayloadRoundTrip(t *testing.T) {
	tagged := planCardTranscriptText(testPlan(), testCritiqueBlocking())
	render, ok := parseLifecycleCardPayload(tagged)
	if !ok {
		t.Fatal("plan card payload not detected")
	}
	wide := stripANSI(render(100))
	narrow := stripANSI(render(60))
	for _, want := range []string{"IMPLEMENTATION PLAN", "classify retries"} {
		if !strings.Contains(wide, want) || !strings.Contains(narrow, want) {
			t.Fatalf("plan card lost %q on reflow (narrow):\n%s", want, narrow)
		}
	}

	taggedCritique := critiqueCardTranscriptText(testPlan(), testCritiqueBlocking())
	renderC, ok := parseLifecycleCardPayload(taggedCritique)
	if !ok {
		t.Fatal("critique card payload not detected")
	}
	if plain := stripANSI(renderC(90)); !strings.Contains(plain, "BLOCKED by required issues") {
		t.Fatalf("critique card reflow lost the verdict:\n%s", plain)
	}

	if _, ok := parseLifecycleCardPayload("ordinary system note"); ok {
		t.Fatal("ordinary system note detected as a lifecycle card")
	}
}

// TestCrystallizeTranscriptWiresCards proves the update path is WIRED:
// a crystallizeResultMsg appends the tagged cards to the transcript, not
// the old flat plan text.
func TestCrystallizeTranscriptWiresCards(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.activeRunID = 1
	plan := testPlan()
	critique := testCritiqueBlocking()
	msg := crystallizeResultMsg{runID: 1, plan: plan, critique: critique}
	updated, _ := m.Update(msg)
	next := updated.(model)

	joined := transcriptText(next.transcript)
	if !strings.Contains(joined, planCardMarker) || !strings.Contains(joined, critiqueCardMarker) {
		t.Fatalf("crystallize transcript missing the P4 card payloads")
	}
	// The blocking verdict must also land: approval stays gated (DoD 8).
	if !strings.Contains(joined, "BLOCKED by required issues") {
		t.Fatalf("crystallize transcript missing the blocked verdict")
	}
}
