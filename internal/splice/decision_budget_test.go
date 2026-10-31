package splice

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// The advisor defaults have one source: the config default. The selector cap
// is 4, the spec's starting value. No campaign record in the repo names a
// different value.
func TestAdvisorDefaultsShareOneSource(t *testing.T) {
	want := config.DefaultDecisionAdvisorLimits().MaxRequestsPerEpisode
	if got := DefaultAdvisorLimits().MaxRequestsPerEpisode; got != want || want != 4 {
		t.Fatalf("selector cap default = %d, config default = %d, want both 4", got, want)
	}
}

// The selector cap is the same whether the advisor comes from a config
// section or from the environment alone. An env-only advisor has no section
// limits, so it must take the same default.
func TestSelectorCapIsTheSameWithOrWithoutSection(t *testing.T) {
	set := flags.Set{flags.DecisionAdvisorEnabled: true}
	env := func(key string) string {
		switch key {
		case DecisionAdvisorModeEnvVar:
			return "shadow"
		case DecisionAdvisorModelEnvVar:
			return "config-model"
		}
		return ""
	}
	envOnly, err := resolveDecisionAdvisor(set, nil, config.DecisionAdvisorConfig{}, env)
	if err != nil || envOnly == nil {
		t.Fatalf("env-only advisor: advisor=%v err=%v", envOnly, err)
	}
	section := config.DecisionAdvisorConfig{Mode: "shadow", Backend: "http", Profile: "config-model", Endpoint: "http://example.test/decide"}
	withSection, err := resolveDecisionAdvisor(set, nil, section, func(string) string { return "" })
	if err != nil || withSection == nil {
		t.Fatalf("section advisor: advisor=%v err=%v", withSection, err)
	}
	want := config.DefaultDecisionAdvisorLimits().MaxRequestsPerEpisode
	if got := envOnly.config.Limits.MaxRequestsPerEpisode; got != want {
		t.Fatalf("env-only cap = %d, want %d", got, want)
	}
	if got := withSection.config.Limits.MaxRequestsPerEpisode; got != want {
		t.Fatalf("section cap = %d, want %d", got, want)
	}
}

// The classifier budget follows the run's hard limits, with no new numbers.
func TestTrajectoryRequestBudgetFollowsHardLimits(t *testing.T) {
	if want := defaultMaxIterations * (maxLocalRepairs + 1); trajectoryRequestBudget != want {
		t.Fatalf("trajectory budget = %d, want %d", trajectoryRequestBudget, want)
	}
	if trajectoryRequestBudget != 15 {
		t.Fatalf("trajectory budget = %d, want 15 for 5 passes and 2 repairs", trajectoryRequestBudget)
	}
}

// The classifier outlives the selector's cap, and the classifier count does
// not spend the selector's cap.
func TestTrajectoryBudgetIsSeparateFromSelectorCap(t *testing.T) {
	eval := &trajectoryEvaluator{label: "step_back"}
	advisor := classifierAdvisor(t, eval)
	advisor.requests = advisor.config.Limits.MaxRequestsPerEpisode
	classifier := advisorClassifier{advisor: advisor, pack: actionTrajectoryPack{}, budget: trajectoryRequestBudget}
	for i := 1; i <= 5; i++ {
		b := fabricatedBoundary()
		b.Iteration = i
		if _, err := classifier.Classify(context.Background(), b); err != nil {
			t.Fatal(err)
		}
	}
	if eval.calls != 5 || advisor.trajectoryRequests != 5 {
		t.Fatalf("classifier calls = %d, trajectory requests = %d, want 5 each", eval.calls, advisor.trajectoryRequests)
	}
	for _, r := range advisor.Records() {
		if r.Disposition == schemas.DispositionBudgetDeclined {
			t.Fatalf("classifier declined inside its budget: %q", r.Reason)
		}
	}

	// The selector cap still applies to the selector.
	record, _ := advisor.EvaluateGate1(context.Background(), jevSnapshot("b.go#selector"), nil)
	if record.Disposition != schemas.DispositionBudgetDeclined || !strings.Contains(record.Reason, "request cap 4") {
		t.Fatalf("selector cap: %q %q", record.Disposition, record.Reason)
	}
}

// Once the classifier budget is spent, it declines without a call.
func TestTrajectoryBudgetDeclinesAtItsOwnLimit(t *testing.T) {
	eval := &trajectoryEvaluator{label: "step_back"}
	advisor := classifierAdvisor(t, eval)
	classifier := advisorClassifier{advisor: advisor, pack: actionTrajectoryPack{}, budget: 2}
	for i := 1; i <= 3; i++ {
		b := fabricatedBoundary()
		b.Iteration = i
		_, _ = classifier.Classify(context.Background(), b)
	}
	if eval.calls != 2 || advisor.trajectoryRequests != 2 {
		t.Fatalf("budget 2: calls = %d, trajectory requests = %d", eval.calls, advisor.trajectoryRequests)
	}
}
