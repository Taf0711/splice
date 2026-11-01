package decisions

import (
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/providers/httpdecisions"
	"github.com/Taf0711/splice/internal/providers/llmdecisions"
	"github.com/Taf0711/splice/internal/providers/openrouterdecisions"
)

func TestEvaluatorFactory(t *testing.T) {
	off, err := NewEvaluator(config.DecisionAdvisorConfig{}, config.ResolvedConfig{})
	if off != nil || err != nil {
		t.Fatalf("flag off: %v %v", off, err)
	}
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	resolved := config.ResolvedConfig{Flags: flags.Set{flags.DecisionAdvisorEnabled: true}, Providers: []config.ProviderProfile{{Name: "local", ProviderKind: config.ProviderKindOpenAICompatible, BaseURL: "http://example.test/v1", Model: "local-model", APIKey: "test-key"}}}
	for _, backend := range []string{"", "openrouter_decisions", "http", "provider"} {
		cfg := config.DecisionAdvisorConfig{Backend: backend, Endpoint: "http://example.test/decide", ProviderProfile: "local"}
		evaluator, err := NewEvaluator(cfg, resolved)
		if err != nil {
			t.Fatal(err)
		}
		switch backend {
		case "", "openrouter_decisions":
			if _, ok := evaluator.(*openrouterdecisions.Client); !ok {
				t.Fatal("default backend changed")
			}
		case "http":
			if _, ok := evaluator.(*httpdecisions.Client); !ok {
				t.Fatal("http backend")
			}
		case "provider":
			if _, ok := evaluator.(*llmdecisions.Client); !ok {
				t.Fatal("provider backend")
			}
		}
	}
	for _, cfg := range []config.DecisionAdvisorConfig{{Backend: "unknown"}, {Backend: "http"}, {Backend: "provider"}, {Backend: "provider", ProviderProfile: "missing"}} {
		if _, err := NewEvaluator(cfg, resolved); err == nil {
			t.Fatalf("invalid config accepted: %+v", cfg)
		}
	}
}
