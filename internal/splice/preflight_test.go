package splice

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/hooks"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
)

// preflightPlan builds a plan containing every stage that has a splice.* tool
// requirement plus the LLM stages.
func preflightPlan() schemas.ExecutionPlan {
	return schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "add a Hello function",
		Stages: []schemas.ExecutionStage{
			{Name: "code_writer", Budget: schemas.StageBudget{InputMax: 100, OutputMax: 100}},
			{Name: "static_analyzer", Budget: schemas.StageBudget{}},
			{Name: "security_auditor", Budget: schemas.StageBudget{}},
			{Name: "test_runner", Budget: schemas.StageBudget{}},
		},
	}
}

func messages(issues []Issue) []string {
	out := make([]string, len(issues))
	for i, issue := range issues {
		out[i] = issue.Message
	}
	return out
}

func TestPreflightPermissionModeAskPrompts(t *testing.T) {
	options := PipelineRunConfig{
		PermissionMode: agent.PermissionModeAsk,
		OnPermissionRequest: func(context.Context, agent.PermissionRequest) (agent.PermissionDecision, error) {
			return agent.PermissionDecision{Action: agent.PermissionDecisionAllow}, nil
		},
	}
	issues := Preflight(preflightPlan(), options)
	want := []Issue{
		{Severity: "warn", Stage: "static_analyzer", Message: "splice.shell may prompt mid-run"},
		{Severity: "warn", Stage: "security_auditor", Message: "splice.shell may prompt mid-run"},
		{Severity: "warn", Stage: "test_runner", Message: "splice.test may prompt mid-run"},
	}
	if !reflect.DeepEqual(issues, want) {
		t.Fatalf("issues = %#v, want %#v", issues, want)
	}
}

func TestPreflightPermissionModeAskDeniesWithoutHandler(t *testing.T) {
	options := PipelineRunConfig{PermissionMode: agent.PermissionModeAsk}
	issues := Preflight(preflightPlan(), options)
	want := []string{
		"splice.shell will be denied",
		"splice.shell will be denied",
		"splice.test will be denied",
	}
	if got := messages(issues); !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
	}
}

func TestPreflightPermissionModeAutoSilent(t *testing.T) {
	for _, mode := range []agent.PermissionMode{
		agent.PermissionModeAuto,
		agent.PermissionModeSpecDraft,
		agent.PermissionModeUnsafe,
	} {
		options := PipelineRunConfig{PermissionMode: mode}
		if issues := Preflight(preflightPlan(), options); len(issues) != 0 {
			t.Fatalf("mode %q: expected no permission issues, got %#v", mode, issues)
		}
	}
}

func TestPreflightHooksInterceptSpliceTools(t *testing.T) {
	dispatcher := hooks.NewDispatcher(hooks.DispatcherOptions{Config: hooks.Config{
		Enabled: true,
		Hooks: []hooks.Definition{
			{ID: "guard-shell", Name: "shell guard", Event: hooks.EventBeforeTool, Matcher: "splice.*", Command: "echo", Enabled: true},
		},
	}})
	options := PipelineRunConfig{Hooks: dispatcher}
	issues := Preflight(preflightPlan(), options)
	want := []string{
		`hook "shell guard" may intercept splice.shell`,
		`hook "shell guard" may intercept splice.test`,
	}
	if got := messages(issues); !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
	}
}

func TestPreflightHooksTargetedMatcherOnly(t *testing.T) {
	dispatcher := hooks.NewDispatcher(hooks.DispatcherOptions{Config: hooks.Config{
		Enabled: true,
		Hooks: []hooks.Definition{
			{ID: "h1", Name: "shell-only", Event: hooks.EventBeforeTool, Matcher: "*shell*", Command: "echo", Enabled: true},
		},
	}})
	options := PipelineRunConfig{Hooks: dispatcher}
	issues := Preflight(preflightPlan(), options)
	want := []string{`hook "shell-only" may intercept splice.shell`}
	if got := messages(issues); !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
	}
}

func TestPreflightHooksIgnoresDisabledAndAfterTool(t *testing.T) {
	dispatcher := hooks.NewDispatcher(hooks.DispatcherOptions{Config: hooks.Config{
		Enabled: true,
		Hooks: []hooks.Definition{
			{ID: "off", Name: "disabled", Event: hooks.EventBeforeTool, Matcher: "splice.*", Command: "echo", Enabled: false},
			{ID: "after", Name: "after", Event: hooks.EventAfterTool, Matcher: "splice.*", Command: "echo", Enabled: true},
		},
	}})
	options := PipelineRunConfig{Hooks: dispatcher}
	if issues := Preflight(preflightPlan(), options); len(issues) != 0 {
		t.Fatalf("expected no hook issues, got %#v", issues)
	}
}

func testModelEntry(id string, caps ...modelregistry.ModelCapability) modelregistry.ModelEntry {
	return modelregistry.ModelEntry{
		ID:            id,
		DisplayName:   id,
		APIModel:      id,
		Provider:      modelregistry.ProviderOpenAI,
		ContextLimits: modelregistry.ContextLimits{ContextWindow: 128000, MaxOutputTokens: 16000},
		Capabilities:  caps,
		Cost: modelregistry.ModelCost{
			Currency: "USD", Unit: "per_1m_tokens",
			InputPerMillion: 1, OutputPerMillion: 1, CachedInputPerMillion: 1,
			Source: "test", SourceLastVerified: "2026-01-01",
		},
		Status:  modelregistry.ModelStatusActive,
		Aliases: []string{id},
	}
}

func TestPreflightProviderCapabilityWarnsNoToolCalling(t *testing.T) {
	registry, err := modelregistry.NewRegistry([]modelregistry.ModelEntry{
		testModelEntry("no-tools", modelregistry.ModelCapabilityChat, modelregistry.ModelCapabilityStreaming),
		testModelEntry("with-tools", modelregistry.ModelCapabilityChat, modelregistry.ModelCapabilityToolCalling),
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	options := PipelineRunConfig{Model: "no-tools", ModelRegistry: registry}
	issues := Preflight(preflightPlan(), options)
	want := []Issue{{Severity: "warn", Stage: "code_writer", Message: "model may not support tool calling"}}
	if !reflect.DeepEqual(issues, want) {
		t.Fatalf("issues = %#v, want %#v", issues, want)
	}

	// A model that supports tool calling is silent.
	options = PipelineRunConfig{Model: "with-tools", ModelRegistry: registry}
	if issues := Preflight(preflightPlan(), options); len(issues) != 0 {
		t.Fatalf("tool-calling model should be silent, got %#v", issues)
	}
}

func TestPreflightProviderCapabilitySkipsUnknown(t *testing.T) {
	registry, err := modelregistry.NewRegistry([]modelregistry.ModelEntry{
		testModelEntry("no-tools", modelregistry.ModelCapabilityChat),
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	// Unknown model: not statically knowable, skip silently.
	options := PipelineRunConfig{Model: "unknown-model", ModelRegistry: registry}
	if issues := Preflight(preflightPlan(), options); len(issues) != 0 {
		t.Fatalf("unknown model should be silent, got %#v", issues)
	}
	// Empty registry: skip silently.
	options = PipelineRunConfig{Model: "no-tools", ModelRegistry: modelregistry.Registry{}}
	if issues := Preflight(preflightPlan(), options); len(issues) != 0 {
		t.Fatalf("empty registry should be silent, got %#v", issues)
	}
	// Empty model id: skip silently.
	options = PipelineRunConfig{Model: "", ModelRegistry: registry}
	if issues := Preflight(preflightPlan(), options); len(issues) != 0 {
		t.Fatalf("empty model should be silent, got %#v", issues)
	}
}

// TestPreflightPassingEmitsNothing: a clean config produces no issues.
func TestPreflightPassingEmitsNothing(t *testing.T) {
	registry, err := modelregistry.NewRegistry([]modelregistry.ModelEntry{
		testModelEntry("with-tools", modelregistry.ModelCapabilityChat, modelregistry.ModelCapabilityToolCalling),
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	options := PipelineRunConfig{
		PermissionMode: agent.PermissionModeAuto,
		Model:          "with-tools",
		ModelRegistry:  registry,
		Hooks:          hooks.NewDispatcher(hooks.DispatcherOptions{}),
	}
	if issues := Preflight(preflightPlan(), options); len(issues) != 0 {
		t.Fatalf("passing preflight should emit nothing, got %#v", issues)
	}
}

// TestPreflightIssuesAreWarnOnly pins that preflight never blocks: every
// finding is advisory, and the plan is never mutated.
func TestPreflightIssuesAreWarnOnly(t *testing.T) {
	plan := preflightPlan()
	before := plan.Stages[0].Budget.InputMax
	options := PipelineRunConfig{PermissionMode: agent.PermissionModeAsk}
	issues := Preflight(plan, options)
	if len(issues) == 0 {
		t.Fatal("expected issues for ask mode without a handler")
	}
	for _, issue := range issues {
		if issue.Severity != IssueSeverityWarn {
			t.Fatalf("issue severity = %q, want %q", issue.Severity, IssueSeverityWarn)
		}
	}
	if plan.Stages[0].Budget.InputMax != before {
		t.Fatal("Preflight mutated the plan")
	}
}

// TestRunPreflightWarnsButCompletes pins the wiring: a run whose permission
// mode would prompt still completes, and the warning surfaces via OnReasoning.
func TestRunPreflightWarnsButCompletes(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreTools(workDir) {
		registry.Register(tool)
	}
	var reasoning []string
	_, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAsk,
		Model:          "model-x",
		OnReasoning:    func(s string) { reasoning = append(reasoning, s) },
		OnPermissionRequest: func(context.Context, agent.PermissionRequest) (agent.PermissionDecision, error) {
			return agent.PermissionDecision{Action: agent.PermissionDecisionAllow}, nil
		},
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	joined := strings.Join(reasoning, "")
	if !strings.Contains(joined, "splice.shell may prompt mid-run") {
		t.Fatalf("expected a preflight prompt warning in reasoning:\n%s", joined)
	}
	if !strings.Contains(joined, "splice.test may prompt mid-run") {
		t.Fatalf("expected a splice.test prompt warning in reasoning:\n%s", joined)
	}
}
