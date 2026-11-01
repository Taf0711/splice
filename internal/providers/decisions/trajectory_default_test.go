package decisions

import (
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
)

// The default classifier falls back to the rules when its profile cannot be
// built, and an explicit section with the decision flag off builds nothing.
func TestDefaultTrajectoryEvaluatorFallsBackToRules(t *testing.T) {
	defaultSection := config.DecisionAdvisorConfig{Backend: "provider", ProviderProfile: "missing", Mode: "active_experimental", TrajectoryDefault: true}
	resolved := config.ResolvedConfig{Flags: flags.Set{flags.TrajectoryClassifierEnabled: true}}
	evaluator, err := NewEvaluator(defaultSection, resolved)
	if evaluator != nil || err != nil {
		t.Fatalf("default that cannot start must fall back to rules: evaluator=%v err=%v", evaluator, err)
	}
	explicit := config.DecisionAdvisorConfig{Backend: "provider", ProviderProfile: "missing", Mode: "shadow"}
	if evaluator, err := NewEvaluator(explicit, config.ResolvedConfig{Flags: flags.Set{flags.TrajectoryClassifierEnabled: true}}); evaluator != nil || err != nil {
		t.Fatalf("explicit section with only the classifier flag must build nothing: %v %v", evaluator, err)
	}
	if _, err := NewEvaluator(explicit, config.ResolvedConfig{Flags: flags.Set{flags.DecisionAdvisorEnabled: true}}); err == nil {
		t.Fatal("explicit section with the decision advisor flag on must build, and a missing profile is an error")
	}
	if evaluator, err := NewEvaluator(explicit, config.ResolvedConfig{Flags: flags.Set{flags.DecisionAdvisorEnabled: false, flags.TrajectoryClassifierEnabled: false}}); evaluator != nil || err != nil {
		t.Fatalf("explicit section with both flags off must build nothing: %v %v", evaluator, err)
	}
	if evaluator, _ := NewEvaluator(defaultSection, config.ResolvedConfig{Flags: flags.Set{flags.TrajectoryClassifierEnabled: false}}); evaluator != nil {
		t.Fatal("default section with the classifier flag off must build nothing")
	}
}

// The default classifier runs on its validated model, whatever the profile's
// chat model is. An explicit section keeps the profile's model.
func TestDecisionProfileKeepsValidatedModelForDefault(t *testing.T) {
	profile := config.ProviderProfile{Name: "chatgpt-61", CatalogID: "chatgpt", Model: "gpt-5"}
	if got := decisionProfile(config.DecisionAdvisorConfig{TrajectoryDefault: true, Profile: "gpt-6.1-sol"}, profile); got.Model != "gpt-6.1-sol" || got.Name != "chatgpt-61" {
		t.Fatalf("default model = %q", got.Model)
	}
	if got := decisionProfile(config.DecisionAdvisorConfig{Profile: "gpt-5"}, profile); got.Model != "gpt-5" {
		t.Fatalf("explicit model = %q", got.Model)
	}
	if profile.Model != "gpt-5" {
		t.Fatal("the caller's profile must not change")
	}
}
