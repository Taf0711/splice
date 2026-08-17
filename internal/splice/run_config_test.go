package splice

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/hooks"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/streamjson"
	"github.com/Taf0711/splice/internal/tools"
)

func TestPipelineRunConfigClassifiesEveryAgentOption(t *testing.T) {
	consumed := pipelineConsumedAgentOptionNames()
	for name := range consumed {
		if reason, ignored := pipelineIgnoredAgentOptionReasons[name]; ignored {
			t.Errorf("agent.Options.%s is both consumed by PipelineConfigFromAgentOptions and listed in pipelineIgnoredAgentOptionReasons (%q). Keep it in exactly one place.", name, reason)
		}
	}

	optsType := reflect.TypeOf(agent.Options{})
	for i := 0; i < optsType.NumField(); i++ {
		field := optsType.Field(i)
		name := field.Name
		_, consumedOK := consumed[name]
		reason, ignoredOK := pipelineIgnoredAgentOptionReasons[name]
		switch {
		case consumedOK && ignoredOK:
			// already reported above
		case consumedOK:
			continue
		case ignoredOK:
			if strings.TrimSpace(reason) == "" {
				t.Errorf("agent.Options.%s is listed as ignored but has an empty reason. Write a non-empty reason in pipelineIgnoredAgentOptionReasons.", name)
			}
		default:
			t.Errorf("unclassified agent.Options.%s. Either copy it in PipelineConfigFromAgentOptions (consumed) or add it to pipelineIgnoredAgentOptionReasons with a non-empty reason. A new field must be classified before CI can pass.", name)
		}
	}

	for name := range pipelineIgnoredAgentOptionReasons {
		if _, ok := optsType.FieldByName(name); !ok {
			t.Errorf("pipelineIgnoredAgentOptionReasons lists %s, but agent.Options has no such field. Remove the stale entry.", name)
		}
	}
	for name := range consumed {
		if _, ok := optsType.FieldByName(name); !ok {
			t.Errorf("PipelineConfigFromAgentOptions copies %s, but agent.Options has no such field. Remove the stale copy.", name)
		}
	}
}

// pipelineAgentOptionsRequiredFields must remain on the reverse copy that
// hooks, filters, and tools.RunOptions consume. Add a newly consumed field
// here and in agentOptions() or this test fails.
var pipelineAgentOptionsRequiredFields = []string{
	"SessionID", "Model", "ReasoningEffort", "Cwd", "Registry",
	"PermissionMode", "Autonomy", "TrustedWorkspace", "Sandbox", "FileTracker",
	"Hooks", "EnabledTools", "DisabledTools", "OnToolProgress", "OnToolOutput",
	"FileDiagnostics",
}

func TestPipelineAgentOptionsCopiesConsumedPolicyFields(t *testing.T) {
	cfg := populatedPipelineRunConfig(t)
	got := cfg.agentOptions()
	gotVal := reflect.ValueOf(got)
	cfgVal := reflect.ValueOf(cfg)
	consumed := pipelineConsumedAgentOptionNames()
	for _, name := range pipelineAgentOptionsRequiredFields {
		if _, ok := consumed[name]; !ok {
			t.Errorf("required reverse field %s is not consumed by PipelineConfigFromAgentOptions. Add it to PipelineRunConfig.", name)
			continue
		}
		field := gotVal.FieldByName(name)
		if !field.IsValid() {
			t.Errorf("agentOptions() does not copy agent.Options.%s. Add it to PipelineRunConfig.agentOptions so hooks and filters keep the field.", name)
			continue
		}
		if field.IsZero() && !cfgVal.FieldByName(name).IsZero() {
			t.Errorf("agentOptions() dropped %s. Copy it from PipelineRunConfig.", name)
		}
	}
}

// TestPipelineAgentOptionsDoesNotReverseCopyProjectRoot pins that ProjectRoot is
// pipeline-internal: it keys memory identity and must not leak into the
// agent.Options that hooks, filters, and tools.RunOptions consume (they execute
// in Cwd, the worktree). If this ever fails, someone added ProjectRoot to
// agentOptions(); reconsider before widening the reverse copy.
func TestPipelineAgentOptionsDoesNotReverseCopyProjectRoot(t *testing.T) {
	cfg := populatedPipelineRunConfig(t)
	if cfg.ProjectRoot == "" {
		t.Fatal("sentinel must set a non-empty ProjectRoot to make the pin meaningful")
	}
	if got := cfg.agentOptions().ProjectRoot; got != "" {
		t.Errorf("agentOptions() reverse-copied ProjectRoot %q; it must stay pipeline-internal", got)
	}
}

func populatedPipelineRunConfig(t *testing.T) PipelineRunConfig {
	t.Helper()
	return PipelineRunConfig{
		SessionID:        "session-1",
		Model:            "model-1",
		ReasoningEffort:  "high",
		Cwd:              t.TempDir(),
		ProjectRoot:      "/repo/root",
		Registry:         tools.NewRegistry(),
		PermissionMode:   agent.PermissionModeAsk,
		Autonomy:         "supervised",
		TrustedWorkspace: true,
		Sandbox:          sandbox.NewEngine(sandbox.EngineOptions{}),
		FileTracker:      tools.NewFileTracker(),
		Hooks:            hooks.NewDispatcher(hooks.DispatcherOptions{}),
		EnabledTools:     []string{"read_file"},
		DisabledTools:    []string{"bash"},
		OnToolProgress:   func(string, streamjson.Event) {},
		OnToolOutput:     func(tools.OutputSnapshot) {},
		FileDiagnostics:  func(context.Context, string) string { return "" },
	}
}

func pipelineConsumedAgentOptionNames() map[string]struct{} {
	src := reflect.TypeOf(agent.Options{})
	dst := reflect.TypeOf(PipelineRunConfig{})
	consumed := make(map[string]struct{})
	for i := 0; i < dst.NumField(); i++ {
		name := dst.Field(i).Name
		if _, ok := src.FieldByName(name); ok {
			consumed[name] = struct{}{}
		}
	}
	return consumed
}
