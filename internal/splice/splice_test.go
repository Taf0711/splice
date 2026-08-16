package splice

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestClassifyRequestTierTrivial(t *testing.T) {
	out := ClassifyRequestTyped("Fix typo in README")
	if out.Tier != schemas.TierTrivial {
		t.Fatalf("expected trivial, got %q", out.Tier)
	}
	if out.DesignIntensity != schemas.DesignNone {
		t.Fatalf("expected none design intensity, got %q", out.DesignIntensity)
	}
}

func TestClassifyRequestTierArchitectural(t *testing.T) {
	out := ClassifyRequestTyped("Redesign the multi-provider orchestrator platform")
	if out.Tier != schemas.TierArchitectural {
		t.Fatalf("expected architectural, got %q", out.Tier)
	}
	if out.DesignIntensity != schemas.DesignFull {
		t.Fatalf("expected full design intensity, got %q", out.DesignIntensity)
	}
}

func TestClassifyRequestTierSubstantial(t *testing.T) {
	out := ClassifyRequestTyped("Add OAuth2 authentication to the payment service")
	if out.Tier != schemas.TierSubstantial {
		t.Fatalf("expected substantial, got %q", out.Tier)
	}
	if out.DesignIntensity != schemas.DesignLight {
		t.Fatalf("expected light design intensity, got %q", out.DesignIntensity)
	}
}

func TestClassifyRequestTierLight(t *testing.T) {
	out := ClassifyRequestTyped("Refactor helper functions")
	if out.Tier != schemas.TierLight {
		t.Fatalf("expected light, got %q", out.Tier)
	}
}

func TestRiskDomainDetection(t *testing.T) {
	out := ClassifyRequestTyped("Secure the login session and rotate the database migration keys")
	have := map[schemas.RiskDomain]bool{}
	for _, d := range out.DetectedRiskDomains {
		have[d] = true
	}
	for _, want := range []schemas.RiskDomain{schemas.RiskAuth, schemas.RiskData, schemas.RiskSecurity} {
		if !have[want] {
			t.Fatalf("expected domain %q in %v", want, out.DetectedRiskDomains)
		}
	}
}

func TestStageNamesForTier(t *testing.T) {
	cases := []struct {
		tier schemas.PipelineTier
		want int
	}{
		{schemas.TierTrivial, 1},
		{schemas.TierLight, 4},
		{schemas.TierStandard, 5},
		{schemas.TierSubstantial, 6},
		{schemas.TierArchitectural, 6},
	}
	for _, tc := range cases {
		names, err := StageNamesForTier(tc.tier)
		if err != nil {
			t.Fatalf("tier %q: %v", tc.tier, err)
		}
		if len(names) != tc.want {
			t.Fatalf("tier %q: expected %d stages, got %d (%v)", tc.tier, tc.want, len(names), names)
		}
	}
	if _, err := StageNamesForTier("bogus"); err == nil {
		t.Fatal("expected error for bogus tier")
	}
}

func TestBudgetForTier(t *testing.T) {
	budget, err := BudgetForTier(schemas.TierStandard)
	if err != nil {
		t.Fatalf("budget: %v", err)
	}
	if budget.OverflowPolicy != "abort" {
		t.Fatalf("expected abort policy, got %q", budget.OverflowPolicy)
	}
	if len(budget.PerStage) != 5 {
		t.Fatalf("expected 5 stages, got %d", len(budget.PerStage))
	}
	names, err := StageNamesForTier(schemas.TierStandard)
	if err != nil {
		t.Fatalf("stage names: %v", err)
	}
	for _, name := range names {
		if _, ok := budget.PerStage[name]; !ok {
			t.Fatalf("missing budget for stage %q", name)
		}
	}
	for _, name := range []string{"static_analyzer", "test_runner", "acceptance_verifier"} {
		stageBudget := budget.PerStage[name]
		if stageBudget.InputMax != 0 || stageBudget.OutputMax != 0 {
			t.Fatalf("deterministic stage %q budget = %+v, want zero token budget", name, stageBudget)
		}
	}
	var inputSum, outputSum int
	for _, stageBudget := range budget.PerStage {
		inputSum += stageBudget.InputMax
		outputSum += stageBudget.OutputMax
	}
	if budget.TotalInputBudget != inputSum+budget.Reserve || budget.TotalOutputBudget != outputSum+budget.Reserve {
		t.Fatalf("totals = (%d, %d), want stage sums plus reserve (%d, %d)", budget.TotalInputBudget, budget.TotalOutputBudget, inputSum+budget.Reserve, outputSum+budget.Reserve)
	}

	substantial, err := BudgetForTier(schemas.TierSubstantial)
	if err != nil {
		t.Fatalf("substantial budget: %v", err)
	}
	for _, name := range []string{"static_analyzer", "security_auditor", "test_runner", "acceptance_verifier"} {
		stageBudget := substantial.PerStage[name]
		if stageBudget.InputMax != 0 || stageBudget.OutputMax != 0 {
			t.Fatalf("deterministic stage %q budget = %+v, want zero token budget", name, stageBudget)
		}
	}
	if _, err := BudgetForTier("bogus"); err == nil {
		t.Fatal("expected error for bogus tier")
	}
}

func TestBuildExecutionPlan(t *testing.T) {
	plan, err := BuildExecutionPlan("Update database schema for user profiles")
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.RequestIntent == "" {
		t.Fatal("request_intent must not be empty")
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan validation: %v", err)
	}
	if plan.Tier == "" {
		t.Fatal("tier must not be empty")
	}
}

func TestBuildExecutionPlanForTaskFloorsAutomatedAcceptanceAtLight(t *testing.T) {
	command := "test -f result"
	task := schemas.Task{
		ID:     "task-1",
		Title:  "small change",
		Intent: "Fix typo",
		AcceptanceFacts: []schemas.AcceptanceFact{{
			Statement:             "the result exists",
			AutomatedVerification: true,
			VerificationCommand:   &command,
		}},
	}
	plan, facts, err := BuildExecutionPlanForTaskWithFacts(task)
	if err != nil {
		t.Fatalf("BuildExecutionPlanForTaskWithFacts: %v", err)
	}
	if plan.Tier == schemas.TierTrivial || plan.Tier != schemas.TierLight {
		t.Fatalf("tier = %q, want light floor", plan.Tier)
	}
	if len(plan.AcceptanceFacts) != 1 || len(facts) != 1 || facts[0] != "the result exists" {
		t.Fatalf("plan facts=%+v prose=%v", plan.AcceptanceFacts, facts)
	}
	if plan.RequestIntent != task.Intent {
		t.Fatalf("request intent = %q, want original task intent", plan.RequestIntent)
	}
}

func TestBuildExecutionPlanForTask(t *testing.T) {
	tier := schemas.TierStandard
	task := schemas.Task{ID: "t1", Title: "T", Intent: "add search", EstimatedTier: &tier}
	plan, err := BuildExecutionPlanForTask(task)
	if err != nil {
		t.Fatalf("build plan for task: %v", err)
	}
	if plan.RequestIntent != task.Intent {
		t.Fatalf("intent mismatch: %q vs %q", plan.RequestIntent, task.Intent)
	}
	if plan.Tier != schemas.TierStandard {
		t.Fatalf("expected standard tier, got %q", plan.Tier)
	}
	bogus := schemas.PipelineTier("bogus")
	task2 := schemas.Task{ID: "t2", Title: "T2", Intent: "do it", EstimatedTier: &bogus}
	if _, err := BuildExecutionPlanForTask(task2); err == nil {
		t.Fatal("expected error for bogus tier")
	}
}

func TestDistillRequestIntent(t *testing.T) {
	short := "short request"
	if got := DistillRequestIntent(short); got != short {
		t.Fatalf("short intent changed: %q", got)
	}
	long := strings.Repeat("x", 400)
	got := DistillRequestIntent(long)
	if len(got) > maxIntentChars {
		t.Fatalf("distilled intent too long: %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ellipsis truncation, got %q", got)
	}
}

func TestClassifierUsesRuneCountThresholds(t *testing.T) {
	// 60 emojis: 60 runes, but 240 bytes. Python counts runes.
	emojiRequest := strings.Repeat("😀", 60) + " fix typo"
	out := ClassifyRequestTyped(emojiRequest)
	if out.Tier != schemas.TierTrivial {
		t.Fatalf("expected trivial for rune-count under 120, got %q", out.Tier)
	}
}

func TestDistillRequestIntentIsRuneSafe(t *testing.T) {
	base := strings.Repeat("x", maxIntentChars-2) + "😀end"
	got := DistillRequestIntent(base)
	if !utf8.ValidString(got) {
		t.Fatalf("distilled intent is not valid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ellipsis suffix, got %q", got)
	}
	if len([]rune(got)) > maxIntentChars {
		t.Fatalf("intent exceeded max chars: %d", len([]rune(got)))
	}
}
