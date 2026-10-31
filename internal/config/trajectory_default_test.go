package config

import (
	"testing"

	"github.com/Taf0711/splice/internal/flags"
)

func TestTrajectoryClassifierDefaultUsesValidatedChatGPTSettings(t *testing.T) {
	providers := []ProviderProfile{
		{Name: "local", CatalogID: "ollama"},
		{Name: "chatgpt-61", CatalogID: "chatgpt", Model: "gpt-6.1-sol"},
		{Name: "chatgpt-other", CatalogID: "chatgpt", Model: "gpt-5"},
	}
	on := flags.Set{flags.TrajectoryClassifierEnabled: true}
	got := trajectoryClassifierDefault(DecisionAdvisorConfig{}, providers, on)
	if !got.TrajectoryDefault || got.Backend != "provider" || got.ProviderProfile != "chatgpt-61" {
		t.Fatalf("first chatgpt profile not chosen: %+v", got)
	}
	if got.Mode != "active_experimental" || got.QuestionPack != "traj_action_v2" || got.ReasoningEffort != "" {
		t.Fatalf("validated settings changed: %+v", got)
	}
	if got.Profile != "gpt-6.1-sol" {
		t.Fatalf("model must be the validated gpt-6.1-sol, not the profile's chat model: %q", got.Profile)
	}
	if got.Limits.TimeoutMS != 120000 || got.Limits.MaxRequestBytes != 65536 || got.Limits.MaxResponseBytes != 65536 || got.Limits.MaxAdvisorCostUSD != 1.0 {
		t.Fatalf("limits changed: %+v", got.Limits)
	}
}

func TestTrajectoryClassifierDefaultLeavesRulesWhenNoChatGPT(t *testing.T) {
	on := flags.Set{flags.TrajectoryClassifierEnabled: true}
	got := trajectoryClassifierDefault(DecisionAdvisorConfig{}, []ProviderProfile{{Name: "local", CatalogID: "ollama"}}, on)
	if got.TrajectoryDefault || got.Mode != "" {
		t.Fatalf("no chatgpt profile must keep the rules: %+v", got)
	}
}

func TestTrajectoryClassifierDefaultRespectsFlagAndExplicitSection(t *testing.T) {
	providers := []ProviderProfile{{Name: "chatgpt-61", CatalogID: "chatgpt"}}
	off := flags.Set{flags.TrajectoryClassifierEnabled: false}
	if got := trajectoryClassifierDefault(DecisionAdvisorConfig{}, providers, off); got.TrajectoryDefault || got.Mode != "" {
		t.Fatalf("flag off must not default: %+v", got)
	}
	on := flags.Set{flags.TrajectoryClassifierEnabled: true}
	selectorOn := flags.Set{flags.TrajectoryClassifierEnabled: true, flags.DecisionAdvisorEnabled: true}
	// An active explicit section (pipeline.decision_advisor on) wins.
	for _, explicit := range []DecisionAdvisorConfig{{Mode: "shadow", Backend: "http"}, {Mode: "off"}} {
		if got := trajectoryClassifierDefault(explicit, providers, selectorOn); got.TrajectoryDefault || got.Mode != explicit.Mode || got.Backend != explicit.Backend {
			t.Fatalf("active explicit section must win: %+v", got)
		}
	}
	// A dormant explicit section (pipeline.decision_advisor off) is replaced by
	// the default exactly as if no section existed. Its backend and mode are
	// not used, and the note flag is set for doctor.
	for _, explicit := range []DecisionAdvisorConfig{{Mode: "shadow", Backend: "http", ProviderProfile: "other", Profile: "m"}, {Mode: "active_experimental", Backend: "http"}} {
		got := trajectoryClassifierDefault(explicit, providers, on)
		want := trajectoryClassifierDefault(DecisionAdvisorConfig{}, providers, on)
		if !got.TrajectoryDefault || !got.ExplicitInactive || got.Backend != want.Backend || got.ProviderProfile != want.ProviderProfile || got.Profile != want.Profile || got.Mode != want.Mode {
			t.Fatalf("dormant explicit section %+v: got %+v, want the default %+v", explicit, got, want)
		}
	}
	// mode "off" is the opt-out: the section stays as written and no default is
	// built, even with pipeline.decision_advisor off.
	if got := trajectoryClassifierDefault(DecisionAdvisorConfig{Mode: "off"}, providers, on); got.TrajectoryDefault || got.Mode != "off" || got.ExplicitInactive {
		t.Fatalf("mode off must opt out of the default: %+v", got)
	}
	// A dormant section with no ChatGPT profile stays as written, and the rules decide.
	none := []ProviderProfile{{Name: "local", CatalogID: "ollama"}}
	if got := trajectoryClassifierDefault(DecisionAdvisorConfig{Mode: "shadow", Backend: "http"}, none, on); got.TrajectoryDefault || got.Backend != "http" {
		t.Fatalf("dormant section without a ChatGPT profile must be unchanged: %+v", got)
	}
}

// An environment-defined advisor (selector on, mode or model set in the
// environment) keeps its own settings. The default classifier section does
// not replace it. Without the selector, the environment mode does not count.
func TestEnvDefinedAdvisorKeepsTheDefaultOut(t *testing.T) {
	providers := []ProviderProfile{{Name: "chatgpt-61", CatalogID: "chatgpt", Model: "gpt-6.1-sol"}}
	resolve := func(env map[string]string, flagSet map[string]bool) ResolvedConfig {
		t.Helper()
		resolved, err := Resolve(ResolveOptions{Env: env, Overrides: Overrides{Providers: providers, ActiveProvider: "chatgpt-61", Flags: flagSet}})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		return resolved
	}
	selectorAndClassifier := map[string]bool{string(flags.DecisionAdvisorEnabled): true, string(flags.TrajectoryClassifierEnabled): true}
	if got := resolve(map[string]string{DecisionAdvisorModeEnv: "active_experimental"}, selectorAndClassifier); got.DecisionAdvisor.TrajectoryDefault {
		t.Fatal("env mode with the selector on must keep the default out")
	}
	if got := resolve(map[string]string{DecisionAdvisorModelEnv: "typesafe/jev-1.13"}, selectorAndClassifier); got.DecisionAdvisor.TrajectoryDefault {
		t.Fatal("env model with the selector on must keep the default out")
	}
	if got := resolve(map[string]string{DecisionAdvisorModeEnv: "active_experimental"}, map[string]bool{string(flags.TrajectoryClassifierEnabled): true}); !got.DecisionAdvisor.TrajectoryDefault {
		t.Fatal("env mode without the selector must not block the default classifier")
	}
	if got := resolve(map[string]string{}, selectorAndClassifier); !got.DecisionAdvisor.TrajectoryDefault {
		t.Fatal("no env mode: the default classifier must still be built")
	}
}
