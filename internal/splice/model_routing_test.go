package splice

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type routingTestProvider struct {
	model string
}

func (*routingTestProvider) StreamCompletion(context.Context, zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	return nil, nil
}

func TestBuildStageModelResolvers(t *testing.T) {
	configFile := schemas.StageModelConfigFile{
		Default:    schemas.StageModelConfig{ProviderProfile: "local", Model: "qwen-default", ReasoningEffort: "low"},
		Escalation: &schemas.StageModelConfig{ProviderProfile: "cloud", Model: "cloud-large", ReasoningEffort: "high"},
		Stages: map[string]schemas.StageModelConfig{
			"code_writer": {ProviderProfile: "local", Model: "qwen-coder", ReasoningEffort: "medium"},
		},
	}
	profiles := []config.ProviderProfile{{Name: "local", Model: "old-local"}, {Name: "cloud", Model: "old-cloud"}}
	builds := map[string]int{}
	stageResolver, escalationResolver := BuildStageModelResolvers(configFile, profiles, func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
		builds[profile.Name+"/"+profile.Model]++
		return &routingTestProvider{model: profile.Model}, nil
	}, TierResolverConfig{})

	selection, err := stageResolver("code_writer")
	if err != nil {
		t.Fatal(err)
	}
	if got := selection.Provider.(*routingTestProvider).model; got != "qwen-coder" || selection.ProviderName != "local" || selection.Model != "qwen-coder" || selection.ReasoningEffort != "medium" {
		t.Fatalf("specific route = %+v, provider model %q", selection, got)
	}

	defaultSelection, err := stageResolver("test_generator")
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultSelection.Provider.(*routingTestProvider).model; got != "qwen-default" || defaultSelection.ProviderName != "local" || defaultSelection.Model != "qwen-default" || defaultSelection.ReasoningEffort != "low" {
		t.Fatalf("default route = %+v, provider model %q", defaultSelection, got)
	}
	cachedSelection, err := stageResolver("static_analyzer")
	if err != nil {
		t.Fatal(err)
	}
	if cachedSelection.Provider != defaultSelection.Provider || builds["local/qwen-default"] != 1 {
		t.Fatalf("default provider was not cached: builds=%v", builds)
	}

	escalation, err := escalationResolver()
	if err != nil {
		t.Fatal(err)
	}
	if got := escalation.Provider.(*routingTestProvider).model; got != "cloud-large" || escalation.ProviderName != "cloud" || escalation.Model != "cloud-large" || escalation.ReasoningEffort != "high" {
		t.Fatalf("escalation route = %+v, provider model %q", escalation, got)
	}
}

func TestBuildStageModelResolversAbsentConfigIsNoOp(t *testing.T) {
	stageResolver, escalationResolver := BuildStageModelResolvers(schemas.StageModelConfigFile{}, nil, nil, TierResolverConfig{})
	selection, err := stageResolver("code_writer")
	if err != nil || selection != (agent.ModelSelection{}) {
		t.Fatalf("absent stage config = (%+v, %v), want no-op", selection, err)
	}
	selection, err = escalationResolver()
	if err != nil || selection != (agent.ModelSelection{}) {
		t.Fatalf("absent escalation = (%+v, %v), want no-op", selection, err)
	}
}

func TestBuildStageModelResolversErrorsNameRoute(t *testing.T) {
	configFile := schemas.StageModelConfigFile{
		Default: schemas.StageModelConfig{ProviderProfile: "missing", Model: "model"},
		Stages: map[string]schemas.StageModelConfig{
			"code_writer": {ProviderProfile: "broken", Model: "model"},
		},
	}
	stageResolver, _ := BuildStageModelResolvers(configFile, []config.ProviderProfile{{Name: "broken"}}, func(config.ProviderProfile) (zeroruntime.Provider, error) {
		return nil, errors.New("factory failed")
	}, TierResolverConfig{})
	if _, err := stageResolver("test_generator"); err == nil || !strings.Contains(err.Error(), `stage "test_generator" references unknown provider profile "missing"`) {
		t.Fatalf("unknown-profile error = %v", err)
	}
	if _, err := stageResolver("code_writer"); err == nil || !strings.Contains(err.Error(), `build provider for stage "code_writer": factory failed`) {
		t.Fatalf("factory error = %v", err)
	}
}

func TestBuildStageModelResolversExplicitOverrideWinsOverTier(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	configFile := schemas.StageModelConfigFile{
		Stages: map[string]schemas.StageModelConfig{
			"code_writer": {ProviderProfile: "primary", Model: "explicit-model", ReasoningEffort: "medium"},
		},
	}
	primaryProfile := config.ProviderProfile{Name: "primary", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-sol"}
	stageResolver, _ := BuildStageModelResolvers(
		configFile,
		[]config.ProviderProfile{primaryProfile},
		func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			return &routingTestProvider{model: profile.Model}, nil
		},
		TierResolverConfig{PrimaryProfile: primaryProfile, Registry: &registry},
	)
	selection, err := stageResolver("code_writer")
	if err != nil {
		t.Fatalf("stageResolver: %v", err)
	}
	if selection.Model != "explicit-model" || selection.ProviderName != "primary" {
		t.Fatalf("selection = %+v, want explicit primary route", selection)
	}
	if got := selection.Provider.(*routingTestProvider).model; got != "explicit-model" {
		t.Fatalf("provider model = %q, want explicit-model", got)
	}
}

func TestBuildStageModelResolversTierFallbackUsedWhenNoOverride(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	primaryProfile := config.ProviderProfile{Name: "primary", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-sol"}
	stageResolver, _ := BuildStageModelResolvers(
		schemas.StageModelConfigFile{},
		[]config.ProviderProfile{primaryProfile},
		func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			return &routingTestProvider{model: profile.Model}, nil
		},
		TierResolverConfig{PrimaryProfile: primaryProfile, Registry: &registry},
	)
	selection, err := stageResolver("code_writer")
	if err != nil {
		t.Fatalf("stageResolver: %v", err)
	}
	if selection.Provider == nil || selection.Model == "" || selection.ProviderName != "primary" {
		t.Fatalf("expected tier fallback, got %+v", selection)
	}
	if selection.Model != "qwen/qwen3-coder-30b-a3b-instruct" {
		t.Fatalf("tier fallback model = %q, want qwen/qwen3-coder-30b-a3b-instruct", selection.Model)
	}
}

func TestBuildStageModelResolversNoTierLabelFallsBackToPrimary(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	primaryProfile := config.ProviderProfile{Name: "primary", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-sol"}
	stageResolver, _ := BuildStageModelResolvers(
		schemas.StageModelConfigFile{},
		[]config.ProviderProfile{primaryProfile},
		func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			return &routingTestProvider{model: profile.Model}, nil
		},
		TierResolverConfig{PrimaryProfile: primaryProfile, Registry: &registry},
	)
	selection, err := stageResolver("static_analyzer")
	if err != nil {
		t.Fatalf("stageResolver: %v", err)
	}
	if selection.Provider != nil || selection.Model != "" {
		t.Fatalf("deterministic stage = %+v, want zero selection", selection)
	}
}
