package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// Each Claude family gets the thinking shape its API accepts. Current models
// reject budget_tokens (HTTP 400) and take adaptive thinking plus
// output_config.effort; Haiku 4.5 and older still take a budget. Forced tool
// use is never combined with thinking.
func TestThinkingShapePerClaudeFamily(t *testing.T) {
	cases := []struct {
		model      string
		effort     string
		adaptive   bool
		wantEffort string
	}{
		{"claude-fable-5-1", "xhigh", true, "xhigh"},
		{"claude-opus-5-5", "max", true, "max"},
		{"claude-opus-5", "", true, ""},
		{"claude-sonnet-5", "low", true, "low"},
		{"claude-opus-4-8", "high", true, "high"},
		{"claude-opus-4-7", "none", true, "low"},
		{"claude-sonnet-4-6", "xhigh", true, "max"},
		{"anthropic/claude-opus-5", "medium", true, "medium"},
		{"anthropic.claude-opus-5", "medium", true, "medium"},
		{"claude-haiku-4-5", "high", false, ""},
		{"claude-sonnet-4-5-20250929", "medium", false, ""},
		{"claude-sonnet-4-20250514", "low", false, ""},
		{"claude-test", "high", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			provider, err := New(Options{Model: tc.model})
			if err != nil {
				t.Fatal(err)
			}
			request, err := provider.anthropicRequest(zeroruntime.CompletionRequest{
				Messages:        []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
				Tools:           []zeroruntime.ToolDefinition{{Name: "emit", Parameters: map[string]any{"type": "object"}}},
				ToolChoice:      "emit",
				ReasoningEffort: tc.effort,
			})
			if err != nil {
				t.Fatal(err)
			}
			wire, _ := json.Marshal(request)
			if tc.adaptive {
				if request.Thinking == nil || request.Thinking.Type != "adaptive" || request.Thinking.BudgetTokens != 0 {
					t.Fatalf("want adaptive thinking without a budget, got %s", wire)
				}
				got := ""
				if request.OutputConfig != nil {
					got = request.OutputConfig.Effort
				}
				if got != tc.wantEffort {
					t.Fatalf("output_config.effort = %q, want %q (%s)", got, tc.wantEffort, wire)
				}
				if request.MaxTokens < adaptiveMinMaxTokens {
					t.Fatalf("max_tokens %d below the adaptive floor", request.MaxTokens)
				}
			} else if request.OutputConfig != nil || (request.Thinking != nil && request.Thinking.Type != "enabled") {
				t.Fatalf("legacy model got the adaptive shape: %s", wire)
			}
			if request.Thinking != nil && request.ToolChoice != nil {
				t.Fatalf("forced tool use sent with thinking: %s", wire)
			}
		})
	}
}

// Pairing test (catalog producer, transport consumer): every Claude model in
// the catalog whose effort ladder comes from an effort enum (it lists "max")
// must get adaptive thinking, and every budget-only model (low/medium/high)
// must not. A new catalog model the transport misclassifies fails here
// instead of returning HTTP 400 in a user's run.
func TestCatalogClaudeModelsGetTheThinkingShapeTheyAccept(t *testing.T) {
	modelregistry.EnableModelsDevOverlay()
	registry, err := modelregistry.DefaultRegistry("anthropic")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range registry.ListByProvider(modelregistry.ProviderAnthropic) {
		efforts := registry.ReasoningEfforts(entry.ID)
		if len(efforts) == 0 {
			continue
		}
		effortEnum := false
		for _, effort := range efforts {
			if effort == modelregistry.ReasoningEffortMax {
				effortEnum = true
			}
		}
		if got := adaptiveThinkingModel(entry.APIModel); got != effortEnum {
			t.Errorf("%s (API %s): efforts %v but adaptive=%v", entry.ID, entry.APIModel, efforts, got)
		}
		checked++
	}
	if checked < 4 {
		t.Fatalf("only %d Claude catalog models checked; the catalog lost its current models", checked)
	}
}
