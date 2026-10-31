package doctor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/oauth"
	"github.com/Taf0711/splice/internal/providers/decisions"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// stubEvaluator stands in for the run's evaluator. The check builds no request,
// so the stub is never called.
type stubEvaluator struct{}

func (stubEvaluator) Evaluate(context.Context, zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
	return zeroruntime.DecisionResult{}, nil
}

func noEnv(string) string { return "" }

func withInactiveNote(section config.DecisionAdvisorConfig) config.DecisionAdvisorConfig {
	section.ExplicitInactive = true
	return section
}

func TestTrajectoryClassifierLine(t *testing.T) {
	classifierOn := flags.Set{flags.TrajectoryClassifierEnabled: true}
	bothOn := flags.Set{flags.TrajectoryClassifierEnabled: true, flags.DecisionAdvisorEnabled: true}
	defaultSection := config.DecisionAdvisorConfig{TrajectoryDefault: true, ProviderProfile: "chatgpt-61", Profile: "gpt-6.1-sol", Mode: "active_experimental", Backend: "provider", QuestionPack: "traj_action_v2"}
	explicit := config.DecisionAdvisorConfig{Mode: "shadow", Backend: "provider", ProviderProfile: "chatgpt-61", Profile: "gpt-6.1-sol"}
	cases := []struct {
		name       string
		set        flags.Set
		section    config.DecisionAdvisorConfig
		evaluator  zeroruntime.DecisionEvaluator
		credential bool
		want       string
		notWant    string
	}{
		{"default with usable credential", classifierOn, defaultSection, stubEvaluator{}, true, "on via ChatGPT sign-in profile chatgpt-61 (model gpt-6.1-sol)", ""},
		// Dormant explicit section replaced by the default: the default line plus the note.
		{"dormant section replaced by default", classifierOn, withInactiveNote(defaultSection), stubEvaluator{}, true, "on via ChatGPT sign-in profile chatgpt-61 (model gpt-6.1-sol); the decision_advisor section is inactive until pipeline.decision_advisor is on", "configured in decision_advisor"},
		{"dormant section replaced, no usable credential", classifierOn, withInactiveNote(defaultSection), stubEvaluator{}, false, "rules only (ChatGPT sign-in has no usable credential); the decision_advisor section is inactive", ""},
		{"default without usable credential", classifierOn, defaultSection, stubEvaluator{}, false, "rules only (ChatGPT sign-in has no usable credential)", ""},
		{"default without an evaluator", classifierOn, defaultSection, nil, true, "rules only (the ChatGPT sign-in profile could not start)", ""},
		// Case 1: an explicit section with pipeline.decision_advisor off is inactive.
		{"explicit section, selector off", classifierOn, explicit, stubEvaluator{}, true, "rules only (the decision_advisor section is inactive until pipeline.decision_advisor is on)", "configured in decision_advisor"},
		// Case 2: the same section with pipeline.decision_advisor on is configured.
		{"explicit section, selector on", bothOn, explicit, stubEvaluator{}, true, "configured in decision_advisor (mode shadow)", ""},
		{"explicit mode off is the opt-out", classifierOn, config.DecisionAdvisorConfig{Mode: "off"}, stubEvaluator{}, true, "off (decision_advisor mode is off)", "rules only"},
		{"no section", classifierOn, config.DecisionAdvisorConfig{}, nil, true, "rules only (no ChatGPT sign-in)", ""},
		{"flag off", flags.Set{flags.TrajectoryClassifierEnabled: false}, defaultSection, stubEvaluator{}, true, "off (pipeline.trajectory_classifier is off)", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := trajectoryClassifierCheck(tc.set, tc.section, tc.evaluator, noEnv, tc.credential)
			if got.ID != "trajectory.classifier" || !strings.Contains(got.Message, tc.want) {
				t.Fatalf("check = %+v, want message containing %q", got, tc.want)
			}
			if tc.notWant != "" && strings.Contains(got.Message, tc.notWant) {
				t.Fatalf("check = %+v, must not contain %q", got, tc.notWant)
			}
		})
	}
}

// The credential check reads the same stored sign-in login the decision
// provider reads. Without a stored login the classifier line must say rules
// only. With one, it must say on.
func TestChatGPTCredentialLoadableReadsStoredLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth-tokens.json")
	t.Setenv("SPLICE_OAUTH_TOKENS_PATH", path)
	t.Setenv(config.OAuthStorageEnv, "file")
	profile := config.ProviderProfile{Name: "chatgpt-61", CatalogID: "chatgpt"}
	if chatGPTCredentialLoadable(profile) {
		t.Fatal("a profile with no stored login must not be loadable")
	}
	store, err := oauth.NewStore(oauth.StoreOptions{Storage: "file", FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(oauth.ProviderKey("chatgpt"), oauth.Token{AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if !chatGPTCredentialLoadable(profile) {
		t.Fatal("a stored sign-in login under the catalog id must be loadable")
	}
}

// Doctor and a run share one credential predicate. With no stored login the
// default cannot start: doctor reports rules only and the run builds no
// evaluator. With a stored login both say the classifier is on.
func TestDoctorAndRunAgreeOnClassifierCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth-tokens.json")
	t.Setenv("SPLICE_OAUTH_TOKENS_PATH", path)
	t.Setenv(config.OAuthStorageEnv, "file")
	profile := config.ProviderProfile{Name: "chatgpt-61", CatalogID: "chatgpt", Model: "gpt-6.1-sol"}
	section := config.DecisionAdvisorConfig{TrajectoryDefault: true, Backend: "provider", ProviderProfile: "chatgpt-61", Profile: "gpt-6.1-sol", Mode: "active_experimental", QuestionPack: "traj_action_v2"}
	resolved := config.ResolvedConfig{Providers: []config.ProviderProfile{profile}, Flags: flags.Set{flags.TrajectoryClassifierEnabled: true}}
	none := func(string) string { return "" }

	check := func(t *testing.T, wantOn bool) {
		t.Helper()
		doctorLoadable := chatGPTCredentialLoadable(profile)
		evaluator, err := decisions.NewEvaluator(section, resolved)
		if err != nil {
			t.Fatal(err)
		}
		runOn := evaluator != nil
		if doctorLoadable != wantOn || runOn != wantOn {
			t.Fatalf("doctor loadable=%v, run evaluator=%v, want both %v", doctorLoadable, runOn, wantOn)
		}
		line := trajectoryClassifierCheck(resolved.Flags, section, evaluator, none, doctorLoadable).Message
		if wantOn && !strings.Contains(line, "on via ChatGPT sign-in profile") {
			t.Fatalf("doctor line = %q, want on", line)
		}
		if !wantOn && !strings.Contains(line, "rules only") {
			t.Fatalf("doctor line = %q, want rules only", line)
		}
	}

	t.Run("without a credential", func(t *testing.T) { check(t, false) })

	store, err := oauth.NewStore(oauth.StoreOptions{Storage: "file", FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(oauth.ProviderKey("chatgpt"), oauth.Token{AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	t.Run("with a stored sign-in", func(t *testing.T) { check(t, true) })
}
