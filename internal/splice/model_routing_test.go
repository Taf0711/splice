package splice

import (
	"context"
	"errors"
	"strings"
	"testing"

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

	provider, model, effort, err := stageResolver("code_writer")
	if err != nil {
		t.Fatal(err)
	}
	if got := provider.(*routingTestProvider).model; got != "qwen-coder" || model != "qwen-coder" || effort != "medium" {
		t.Fatalf("specific route = provider %q, model %q, effort %q", got, model, effort)
	}

	defaultProvider, model, effort, err := stageResolver("test_generator")
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultProvider.(*routingTestProvider).model; got != "qwen-default" || model != "qwen-default" || effort != "low" {
		t.Fatalf("default route = provider %q, model %q, effort %q", got, model, effort)
	}
	cachedProvider, _, _, err := stageResolver("static_analyzer")
	if err != nil {
		t.Fatal(err)
	}
	if cachedProvider != defaultProvider || builds["local/qwen-default"] != 1 {
		t.Fatalf("default provider was not cached: builds=%v", builds)
	}

	escalated, model, effort, err := escalationResolver()
	if err != nil {
		t.Fatal(err)
	}
	if got := escalated.(*routingTestProvider).model; got != "cloud-large" || model != "cloud-large" || effort != "high" {
		t.Fatalf("escalation route = provider %q, model %q, effort %q", got, model, effort)
	}
}

func TestBuildStageModelResolversAbsentConfigIsNoOp(t *testing.T) {
	stageResolver, escalationResolver := BuildStageModelResolvers(schemas.StageModelConfigFile{}, nil, nil, TierResolverConfig{})
	provider, model, effort, err := stageResolver("code_writer")
	if err != nil || provider != nil || model != "" || effort != "" {
		t.Fatalf("absent stage config = (%v, %q, %q, %v), want no-op", provider, model, effort, err)
	}
	provider, model, effort, err = escalationResolver()
	if err != nil || provider != nil || model != "" || effort != "" {
		t.Fatalf("absent escalation = (%v, %q, %q, %v), want no-op", provider, model, effort, err)
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
	if _, _, _, err := stageResolver("test_generator"); err == nil || !strings.Contains(err.Error(), `stage "test_generator" references unknown provider profile "missing"`) {
		t.Fatalf("unknown-profile error = %v", err)
	}
	if _, _, _, err := stageResolver("code_writer"); err == nil || !strings.Contains(err.Error(), `build provider for stage "code_writer": factory failed`) {
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
	provider, model, _, err := stageResolver("code_writer")
	if err != nil {
		t.Fatalf("stageResolver: %v", err)
	}
	if model != "explicit-model" {
		t.Fatalf("model = %q, want explicit-model", model)
	}
	if got := provider.(*routingTestProvider).model; got != "explicit-model" {
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
	provider, model, _, err := stageResolver("code_writer")
	if err != nil {
		t.Fatalf("stageResolver: %v", err)
	}
	if provider == nil || model == "" {
		t.Fatal("expected tier fallback to resolve a model")
	}
	if model != "qwen/qwen3-coder-30b-a3b-instruct" {
		t.Fatalf("tier fallback model = %q, want qwen/qwen3-coder-30b-a3b-instruct", model)
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
	provider, model, _, err := stageResolver("static_analyzer")
	if err != nil {
		t.Fatalf("stageResolver: %v", err)
	}
	if provider != nil || model != "" {
		t.Fatalf("deterministic stage = (%v, %q), want nil/empty", provider, model)
	}
}
