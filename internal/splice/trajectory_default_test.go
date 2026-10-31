package splice

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func defaultChatGPTSection() config.DecisionAdvisorConfig {
	return config.DecisionAdvisorConfig{
		Backend: "provider", ProviderProfile: "chatgpt-61", Profile: "gpt-6.1-sol", Mode: "active_experimental",
		QuestionPack: "traj_action_v2", TrajectoryDefault: true,
		Limits: config.DecisionAdvisorLimits{MaxRequestBytes: 65536, MaxResponseBytes: 65536, TimeoutMS: 120000, MaxAdvisorCostUSD: 1},
	}
}

// The default section runs the classifier alone, with the validated settings.
func TestDefaultTrajectorySectionIsClassifierOnly(t *testing.T) {
	eval := &trajectoryEvaluator{label: "continue"}
	for _, selectorFlag := range []bool{false, true} {
		set := flags.Set{flags.DecisionAdvisorEnabled: selectorFlag, flags.TrajectoryClassifierEnabled: true}
		advisor, err := resolveDecisionAdvisor(set, eval, defaultChatGPTSection(), func(string) string { return "" })
		if err != nil || advisor == nil {
			t.Fatalf("selector=%v: advisor=%v err=%v", selectorFlag, advisor, err)
		}
		if !advisor.selectorOff || advisor.classifierOff {
			t.Fatalf("selector=%v: selectorOff=%v classifierOff=%v", selectorFlag, advisor.selectorOff, advisor.classifierOff)
		}
		c := advisor.config
		if c.Mode != AdvisorModeActiveExperimental || c.QuestionPack != QuestionPackTrajActionV2 || c.Limits.Timeout.Seconds() != 120 || c.Limits.MaxRequestBytes != 65536 {
			t.Fatalf("validated settings not applied: %+v", c)
		}
	}
	off := flags.Set{flags.DecisionAdvisorEnabled: true, flags.TrajectoryClassifierEnabled: false}
	if advisor, _ := resolveDecisionAdvisor(off, eval, defaultChatGPTSection(), func(string) string { return "" }); advisor != nil {
		t.Fatal("classifier flag off must leave the default section off")
	}
	if advisor, _ := resolveDecisionAdvisor(flags.Set{flags.TrajectoryClassifierEnabled: true}, nil, defaultChatGPTSection(), func(string) string { return "" }); advisor != nil {
		t.Fatal("default section without an evaluator must fall back to the rules")
	}
}

// A configured section follows the decision advisor flag. The classifier
// flag alone runs only the default section. With the decision advisor flag
// on, the classifier flag gates the classifier inside the configured section.
func TestExplicitSectionFollowsItsFlags(t *testing.T) {
	eval := &trajectoryEvaluator{label: "continue"}
	explicit := config.DecisionAdvisorConfig{Mode: "shadow", Backend: "http", Profile: "m", QuestionPack: QuestionPackTrajActionV1}
	none := func(string) string { return "" }
	for _, tc := range []struct {
		name                 string
		selector, classifier bool
		wantNil              bool
		selectorOff          bool
		classifierOff        bool
	}{
		{name: "classifier flag only runs no configured section", classifier: true, wantNil: true},
		{name: "both flags", selector: true, classifier: true},
		{name: "decision flag only", selector: true, classifierOff: true},
		{name: "no flags", wantNil: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := flags.Set{flags.DecisionAdvisorEnabled: tc.selector, flags.TrajectoryClassifierEnabled: tc.classifier}
			advisor, err := resolveDecisionAdvisor(set, eval, explicit, none)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantNil {
				if advisor != nil {
					t.Fatal("no flag on must leave the advisor off")
				}
				return
			}
			if advisor == nil || advisor.selectorOff != tc.selectorOff || advisor.classifierOff != tc.classifierOff {
				t.Fatalf("advisor=%v; want selectorOff=%v classifierOff=%v", advisor, tc.selectorOff, tc.classifierOff)
			}
			policy := newHostTrajectoryPolicy(advisor, config.TrajectoryConfig{}, false)
			if (policy == nil) != tc.classifierOff {
				t.Fatalf("classifier policy nil=%v, want %v", policy == nil, tc.classifierOff)
			}
		})
	}
}

// The selector and its prefetch decisions never run on the classifier-only advisor.
func TestTrajectoryOnlyAdvisorBuysNoSelectorDecision(t *testing.T) {
	eval := &trajectoryEvaluator{label: "continue"}
	advisor, err := resolveDecisionAdvisor(flags.Set{flags.TrajectoryClassifierEnabled: true}, eval, defaultChatGPTSection(), func(string) string { return "" })
	if err != nil || advisor == nil {
		t.Fatalf("advisor=%v err=%v", advisor, err)
	}
	candidates := []SourceFetch{{Path: "a.go"}}
	if _, ok := runPrefetchNeed(context.Background(), stageInputPreparation{Options: PipelineRunConfig{Advisor: advisor}}, schemas.HarnessStageInput{}, candidates); ok {
		t.Fatal("prefetch decision bought on a classifier-only advisor")
	}
	if eval.calls != 0 {
		t.Fatalf("selector or prefetch made %d calls", eval.calls)
	}
}

// RealizeDecisionAdvisor reports what a run builds. A dormant configured
// section builds nothing, so the selector and the section stay off. The default
// classifier section builds the classifier alone, with the validated model.
func TestRealizeDecisionAdvisorDormantAndDefault(t *testing.T) {
	eval := &trajectoryEvaluator{label: "continue"}
	none := func(string) string { return "" }
	classifierOn := flags.Set{flags.TrajectoryClassifierEnabled: true}
	dormant := config.DecisionAdvisorConfig{Mode: "shadow", Backend: "http", Profile: "m", QuestionPack: QuestionPackTrajActionV1}
	got, err := RealizeDecisionAdvisor(classifierOn, eval, dormant, none)
	if err != nil || got.Active || got.Selector || got.Classifier {
		t.Fatalf("dormant section realized %+v err=%v, want nothing", got, err)
	}
	got, err = RealizeDecisionAdvisor(classifierOn, eval, defaultChatGPTSection(), none)
	if err != nil {
		t.Fatal(err)
	}
	want := RealizedAdvisor{Active: true, Selector: false, Classifier: true, Mode: AdvisorModeActiveExperimental, Model: "gpt-6.1-sol"}
	if got != want {
		t.Fatalf("default realized %+v, want %+v", got, want)
	}
}
