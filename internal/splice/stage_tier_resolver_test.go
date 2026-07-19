package splice

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type tierTestProvider struct{ model string }

func (p *tierTestProvider) StreamCompletion(context.Context, zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	return nil, nil
}

func TestStageTierResolver(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}

	newProvider := func(profile config.ProviderProfile) (agent.Provider, error) {
		return &tierTestProvider{model: profile.Model}, nil
	}

	cases := []struct {
		name      string
		profile   config.ProviderProfile
		stage     string
		wantModel string
		wantNil   bool
	}{
		{
			name:      "openai code_writer medium from gpt-5.6-sol",
			profile:   config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-sol"},
			stage:     "code_writer",
			wantModel: "qwen/qwen3-coder-30b-a3b-instruct",
		},
		{
			name:      "openai code_writer medium from gpt-5.6-luna",
			profile:   config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-luna"},
			stage:     "code_writer",
			wantModel: "qwen/qwen3-coder-30b-a3b-instruct",
		},
		{
			name:      "openai reasoning from gpt-5.6-luna",
			profile:   config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-luna"},
			stage:     "plan_critic",
			wantModel: "gpt-5.6-sol",
		},
		{
			name:    "openai reasoning already strongest",
			profile: config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-sol"},
			stage:   "plan_critic",
			wantNil: true,
		},
		{
			name:      "anthropic code_writer medium from claude-opus-4.1",
			profile:   config.ProviderProfile{Name: "anthropic", ProviderKind: config.ProviderKindAnthropic, Model: "claude-opus-4.1"},
			stage:     "code_writer",
			wantModel: "claude-haiku-4.5",
		},
		{
			name:    "custom openai-compatible falls back",
			profile: config.ProviderProfile{Name: "custom", ProviderKind: config.ProviderKindOpenAICompatible, Model: "custom-model"},
			stage:   "code_writer",
			wantNil: true,
		},
		{
			name:    "unknown primary falls back",
			profile: config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "not-in-catalog"},
			stage:   "code_writer",
			wantNil: true,
		},
		{
			name:      "gemini tiered pricing",
			profile:   config.ProviderProfile{Name: "google", ProviderKind: config.ProviderKindGoogle, Model: "gemini-2.5-pro"},
			stage:     "code_writer",
			wantModel: "gemini-2.5-flash-lite",
		},
		{
			name:    "deterministic static_analyzer has no tier label",
			profile: config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-sol"},
			stage:   "static_analyzer",
			wantNil: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache := make(map[string]agent.Provider)
			resolver := NewStageTierResolver(tc.profile, registry, newProvider, cache)
			provider, model, effort, err := resolver(tc.stage)
			if err != nil {
				t.Fatalf("resolver: %v", err)
			}
			if tc.wantNil {
				if provider != nil || model != "" || effort != "" {
					t.Fatalf("want nil, got provider=%v model=%q effort=%q", provider, model, effort)
				}
				return
			}
			if provider == nil {
				t.Fatalf("expected provider, got nil")
			}
			if model != tc.wantModel {
				t.Fatalf("model = %q, want %q", model, tc.wantModel)
			}
			if effort != "" {
				t.Fatalf("effort = %q, want empty", effort)
			}
			entry, ok := registry.Resolve(model)
			if !ok {
				t.Fatalf("resolved model %q not in registry", model)
			}
			if got := provider.(*tierTestProvider).model; got != entry.APIModel {
				t.Fatalf("provider model = %q, want api model for %q", got, model)
			}
		})
	}
}

func TestStageTierResolverCachesProvider(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	profile := config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-sol"}
	builds := 0
	newProvider := func(p config.ProviderProfile) (agent.Provider, error) {
		builds++
		return &tierTestProvider{model: p.Model}, nil
	}
	cache := make(map[string]agent.Provider)
	resolver := NewStageTierResolver(profile, registry, newProvider, cache)
	p1, _, _, err := resolver("code_writer")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	p2, _, _, err := resolver("code_writer")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if builds != 1 {
		t.Fatalf("provider built %d times, want 1", builds)
	}
	if p1 != p2 {
		t.Fatal("cached provider was not reused")
	}
}

func TestResolveStageTierModel(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}

	cases := []struct {
		name      string
		profile   config.ProviderProfile
		stage     string
		wantModel string
		wantNil   bool
	}{
		{
			name:      "openai code_writer medium resolves cheapest tool model",
			profile:   config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-sol"},
			stage:     "code_writer",
			wantModel: "qwen/qwen3-coder-30b-a3b-instruct",
		},
		{
			name:    "openai primary already cheapest returns nil",
			profile: config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "qwen/qwen3-coder-30b-a3b-instruct"},
			stage:   "code_writer",
			wantNil: true,
		},
		{
			name:      "anthropic code_writer medium resolves cheapest tool model",
			profile:   config.ProviderProfile{Name: "anthropic", ProviderKind: config.ProviderKindAnthropic, Model: "claude-opus-4.1"},
			stage:     "code_writer",
			wantModel: "claude-haiku-4.5",
		},
		{
			name:      "openai reasoning resolves stronger model",
			profile:   config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-luna"},
			stage:     "plan_critic",
			wantModel: "gpt-5.6-sol",
		},
		{
			name:    "openai reasoning already strongest returns nil",
			profile: config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-sol"},
			stage:   "plan_critic",
			wantNil: true,
		},
		{
			name:    "unknown primary model returns nil",
			profile: config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "not-in-catalog"},
			stage:   "code_writer",
			wantNil: true,
		},
		{
			name:      "gemini tiered pricing resolves flash or flash-lite",
			profile:   config.ProviderProfile{Name: "google", ProviderKind: config.ProviderKindGoogle, Model: "gemini-2.5-pro"},
			stage:     "code_writer",
			wantModel: "gemini-2.5-flash-lite",
		},
		{
			name:    "custom-compatible provider returns nil",
			profile: config.ProviderProfile{Name: "custom", ProviderKind: config.ProviderKindOpenAICompatible, Model: "custom-model"},
			stage:   "code_writer",
			wantNil: true,
		},
		{
			name:    "deterministic stage has no tier label",
			profile: config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-sol"},
			stage:   "static_analyzer",
			wantNil: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := ResolveStageTierModel(tc.stage, tc.profile, registry)
			if tc.wantNil {
				if entry != nil {
					t.Fatalf("want nil, got %q", entry.ID)
				}
				return
			}
			if entry == nil {
				t.Fatalf("expected entry, got nil")
			}
			if entry.ID != tc.wantModel {
				t.Fatalf("model = %q, want %q", entry.ID, tc.wantModel)
			}
		})
	}
}
