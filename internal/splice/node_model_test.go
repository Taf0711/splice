package splice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

var errNodeModelUnavailable = errors.New("node model unavailable")

// TestCompileTopologyCarriesNodeModel pins the first wiring step: a node's
// explicit model reaches the compiled execution stage.
func TestCompileTopologyCarriesNodeModel(t *testing.T) {
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "models",
		Nodes: []schemas.PipelineNode{
			{Name: "code_writer", Type: "code_writer", Model: &schemas.StageModelConfig{ProviderProfile: "local", Model: "qwen-node", ReasoningEffort: "high"}},
			{Name: "test_generator", Type: "test_generator"},
		},
	}
	compiled, err := CompileTopology(topology, schemas.TierStandard)
	if err != nil {
		t.Fatalf("CompileTopology: %v", err)
	}
	byName := make(map[string]schemas.ExecutionStage, len(compiled.Stages))
	for _, stage := range compiled.Stages {
		byName[stage.Name] = stage
	}
	writer := byName["code_writer"].Model
	if writer == nil || writer.ProviderProfile != "local" || writer.Model != "qwen-node" || writer.ReasoningEffort != "high" {
		t.Fatalf("code_writer model = %+v, want the node declaration", writer)
	}
	if byName["test_generator"].Model != nil {
		t.Fatalf("test_generator model = %+v, want nil", byName["test_generator"].Model)
	}
	// The compiled plan must not alias the topology's declaration.
	writer.Model = "tampered"
	if topology.Nodes[0].Model.Model != "qwen-node" {
		t.Fatal("compiled model aliases the topology declaration")
	}
}

// TestRunPassNodeModelBeatsStageResolver pins the precedence wiring: a node
// model is used and the per-stage resolver is not consulted for that stage.
func TestRunPassNodeModelBeatsStageResolver(t *testing.T) {
	workDir := t.TempDir()
	capturer := &stageCallCapturer{calls: map[string]capturedStageCall{}}
	registry := stageRegistry{"code_writer": capturer, "test_generator": capturer}
	plan := schemas.ExecutionPlan{Tier: schemas.TierStandard, RequestIntent: "node model"}
	plan.Stages = append(plan.Stages,
		schemas.ExecutionStage{Name: "code_writer", Model: &schemas.StageModelConfig{ProviderProfile: "local", Model: "node-model", ReasoningEffort: "high"}},
		schemas.ExecutionStage{Name: "test_generator"},
	)

	nodeProvider := &namedProvider{name: "node"}
	stageProvider := &namedProvider{name: "stage"}
	var stageCalls []string
	options := PipelineConfigFromAgentOptions(agent.Options{
		Model:           "default-model",
		ProviderName:    "default-provider",
		ReasoningEffort: "medium",
		NodeModelResolver: func(nodeName string, override agent.ModelOverride) (agent.ModelSelection, error) {
			if nodeName != "code_writer" || override.ProviderProfile != "local" || override.Model != "node-model" || override.ReasoningEffort != "high" {
				t.Fatalf("node resolver call = %q %+v", nodeName, override)
			}
			return agent.ModelSelection{Provider: nodeProvider, ProviderName: "node-provider", Model: "node-model", ReasoningEffort: "high"}, nil
		},
		StageModelResolver: func(stageName string) (agent.ModelSelection, error) {
			stageCalls = append(stageCalls, stageName)
			return agent.ModelSelection{Provider: stageProvider, ProviderName: "stage-provider", Model: "stage-model"}, nil
		},
	})

	records, _, completed, err := runPass(context.Background(), "run-node-model", 1, plan, registry, &namedProvider{name: "default"}, options, workDir, nil, time.Time{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("runPass: %v", err)
	}
	if !completed {
		t.Fatalf("pass did not complete: %+v", records)
	}
	writer := capturer.calls["code_writer"]
	if writer.provider != nodeProvider || writer.options.ModelOverride != "node-model" || writer.options.ReasoningEffort != "high" {
		t.Fatalf("code_writer used %T/%q/%q, want the node model", writer.provider, writer.options.ModelOverride, writer.options.ReasoningEffort)
	}
	if strings.Join(stageCalls, ",") != "test_generator" {
		t.Fatalf("stage resolver calls = %v, want only test_generator", stageCalls)
	}
	if generator := capturer.calls["test_generator"]; generator.provider != stageProvider {
		t.Fatalf("test_generator provider = %T, want the stage resolver", generator.provider)
	}
	for _, record := range records {
		if record.Name == "code_writer" {
			if record.Model == nil || *record.Model != "node-model" || record.Provider == nil || *record.Provider != "node-provider" {
				t.Fatalf("code_writer attribution = %+v, want the node model", record)
			}
		}
	}
}

// TestRunPassBrokenNodeModelFailsLoud pins that a node model that cannot be
// built aborts the pass naming the stage instead of silently degrading.
func TestRunPassBrokenNodeModelFailsLoud(t *testing.T) {
	workDir := t.TempDir()
	capturer := &stageCallCapturer{calls: map[string]capturedStageCall{}}
	registry := stageRegistry{"code_writer": capturer}
	plan := schemas.ExecutionPlan{Tier: schemas.TierStandard, RequestIntent: "broken node model"}
	plan.Stages = append(plan.Stages,
		schemas.ExecutionStage{Name: "code_writer", Model: &schemas.StageModelConfig{ProviderProfile: "ghost", Model: "x"}},
	)
	options := PipelineConfigFromAgentOptions(agent.Options{
		NodeModelResolver: func(string, agent.ModelOverride) (agent.ModelSelection, error) {
			return agent.ModelSelection{}, errNodeModelUnavailable
		},
		StageModelResolver: func(string) (agent.ModelSelection, error) {
			t.Fatal("the stage resolver must not be consulted when the node model fails")
			return agent.ModelSelection{}, nil
		},
	})
	_, _, _, err := runPass(context.Background(), "run-broken-node-model", 1, plan, registry, &namedProvider{name: "default"}, options, workDir, nil, time.Time{}, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "stage code_writer model") {
		t.Fatalf("err = %v, want a node-model failure naming the stage", err)
	}
}

func writeWorkspaceTopology(t *testing.T, workDir, name string, nodes ...schemas.PipelineNode) {
	t.Helper()
	topology := schemas.PipelineTopology{Version: schemas.TopologySchemaVersion, Name: name, Nodes: nodes}
	data, err := json.MarshalIndent(topology, "", "  ")
	if err != nil {
		t.Fatalf("marshal topology: %v", err)
	}
	path := filepath.Join(workDir, ".splice", "pipeline.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write topology: %v", err)
	}
}

// TestRunUsesWorkspaceNodeModel is the end-to-end proof: a trusted workspace
// topology file declares a node model, Run resolves and compiles it, and the
// executor routes that node through the node resolver, never the per-stage
// resolver.
func TestRunUsesWorkspaceNodeModel(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	writeWorkspaceTopology(t, workDir, "pinned", schemas.PipelineNode{
		Name:  "code_writer",
		Type:  "code_writer",
		Model: &schemas.StageModelConfig{ProviderProfile: "team", Model: "node-pinned", ReasoningEffort: "high"},
	})

	var fakeProvider zeroruntime.Provider = &runFakeProvider{}
	var nodeCalls, stageCalls []string
	opts := agent.Options{
		Cwd:              workDir,
		Registry:         registry,
		PermissionMode:   agent.PermissionModeAuto,
		MaxTurns:         1,
		TrustedWorkspace: true,
		NodeModelResolver: func(nodeName string, override agent.ModelOverride) (agent.ModelSelection, error) {
			nodeCalls = append(nodeCalls, nodeName+"|"+override.ProviderProfile+"|"+override.Model+"|"+override.ReasoningEffort)
			return agent.ModelSelection{Provider: fakeProvider, ProviderName: "node-provider", Model: override.Model, ReasoningEffort: override.ReasoningEffort}, nil
		},
		StageModelResolver: func(stageName string) (agent.ModelSelection, error) {
			stageCalls = append(stageCalls, stageName)
			return agent.ModelSelection{Provider: fakeProvider, ProviderName: "stage-provider", Model: "stage-model"}, nil
		},
	}

	agentResult, err := Run(context.Background(), "add a Hello function and tests", fakeProvider, opts, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var result schemas.PipelineResult
	if err := json.Unmarshal([]byte(agentResult.FinalAnswer), &result); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if result.TopologyName != "pinned" {
		t.Fatalf("result topology = %q, want pinned (the workspace file was not loaded)", result.TopologyName)
	}
	if len(nodeCalls) == 0 || nodeCalls[0] != "code_writer|team|node-pinned|high" {
		t.Fatalf("node resolver calls = %v, want code_writer with the node declaration", nodeCalls)
	}
	for _, call := range stageCalls {
		if call == "code_writer" {
			t.Fatalf("the per-stage resolver was consulted for code_writer despite the node model: %v", stageCalls)
		}
	}
	var pinned bool
	for _, record := range result.Stages {
		if record.Name == "code_writer" && record.Model != nil && *record.Model == "node-pinned" {
			pinned = true
		}
	}
	if !pinned {
		t.Fatalf("no code_writer record carries the node model: %+v", result.Stages)
	}
}

// TestEscalationOverridesNodePin pins that an escalation reaches a node that
// carries a model pin. The pin wins until escalation; after it, every later
// call uses the escalated model, and the switch message names the pin.
func TestEscalationOverridesNodePin(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "escalation over node pin",
		Stages: []schemas.ExecutionStage{{
			Name:   "code_writer",
			Model:  &schemas.StageModelConfig{ProviderProfile: "team", Model: "node-pinned"},
			Budget: schemas.StageBudget{InputMax: 10000, OutputMax: 10000},
		}},
		TokenBudget: schemas.TokenBudget{
			TotalInputBudget:  100000,
			TotalOutputBudget: 100000,
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000}},
			OverflowPolicy:    "abort",
		},
	}
	cs := &cycleStage{}
	defaultProvider := &namedProvider{name: "default"}
	pinnedProvider := &namedProvider{name: "pinned"}
	escalatedProvider := &namedProvider{name: "escalated"}
	nodeCalls := 0
	cfg := PipelineConfigFromAgentOptions(agent.Options{
		Cwd:      workDir,
		MaxTurns: 5,
		NodeModelResolver: func(string, agent.ModelOverride) (agent.ModelSelection, error) {
			nodeCalls++
			return agent.ModelSelection{Provider: pinnedProvider, ProviderName: "pinned-provider", Model: "node-pinned"}, nil
		},
		EscalationModelResolver: func() (agent.ModelSelection, error) {
			return agent.ModelSelection{Provider: escalatedProvider, ProviderName: "escalated-provider", Model: "escalated-model", ReasoningEffort: "high"}, nil
		},
	})
	var progress []string
	cfg.OnReasoning = func(text string) { progress = append(progress, text) }
	var routes []presentation.NodeRoute
	cfg.stageRoute = func(_ string, _ int, route presentation.NodeRoute) { routes = append(routes, route) }

	if _, err := runIterationLoop(context.Background(), "node-pin-escalation", plan, stageRegistry{"code_writer": cs}, defaultProvider, cfg, workDir, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if len(cs.providers) < 3 {
		t.Fatalf("recorded %d providers, want at least 3", len(cs.providers))
	}
	if cs.providers[0] != pinnedProvider {
		t.Fatalf("iteration 1 provider = %p, want the node pin", cs.providers[0])
	}
	for i := 2; i < len(cs.providers); i++ {
		if cs.providers[i] != escalatedProvider {
			t.Fatalf("iteration %d provider = %p, want escalated (node pin not overridden)", i+1, cs.providers[i])
		}
	}
	joined := strings.Join(progress, "")
	if !strings.Contains(joined, "switched to model escalated-model") || !strings.Contains(joined, "node(s) code_writer") {
		t.Fatalf("progress = %q, want the switch message naming the pinned node", joined)
	}
	// The presentation sees the applied route: the pin first, then the
	// escalated model marked as escalated, with the applied budget.
	if len(routes) < 2 {
		t.Fatalf("routes = %+v, want one per pass", routes)
	}
	if first := routes[0]; first.Model != "node-pinned" || first.Escalated || first.InputBudget != 10000 {
		t.Fatalf("first route = %+v, want the unescalated pin", first)
	}
	if last := routes[len(routes)-1]; last.Model != "escalated-model" || !last.Escalated || last.Provider != "escalated-provider" {
		t.Fatalf("last route = %+v, want the escalated model", last)
	}
}

// TestRunPassPinnedNodeWithoutResolverFailsLoud pins that a pinned node with no
// node resolver fails the pass. It must not run on the primary model.
func TestRunPassPinnedNodeWithoutResolverFailsLoud(t *testing.T) {
	workDir := t.TempDir()
	capturer := &stageCallCapturer{calls: map[string]capturedStageCall{}}
	registry := stageRegistry{"code_writer": capturer}
	plan := schemas.ExecutionPlan{Tier: schemas.TierStandard, RequestIntent: "pinned without resolver"}
	plan.Stages = append(plan.Stages,
		schemas.ExecutionStage{Name: "code_writer", Model: &schemas.StageModelConfig{ProviderProfile: "team", Model: "x"}},
	)
	options := PipelineConfigFromAgentOptions(agent.Options{
		StageModelResolver: func(string) (agent.ModelSelection, error) {
			t.Fatal("the stage resolver must not stand in for an unresolvable node pin")
			return agent.ModelSelection{}, nil
		},
	})
	_, _, _, err := runPass(context.Background(), "run-pin-no-resolver", 1, plan, registry, &namedProvider{name: "default"}, options, workDir, nil, time.Time{}, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "stage code_writer model") || !strings.Contains(err.Error(), "no resolver") {
		t.Fatalf("err = %v, want a pin failure naming the node and the missing resolver", err)
	}
}

// TestRepairSelectionNodePinErrorsSurface pins that repair does not swallow a
// node pin error. A stage route error still falls back, as the pass loop does.
func TestRepairSelectionNodePinErrorsSurface(t *testing.T) {
	pin := &schemas.StageModelConfig{ProviderProfile: "team", Model: "x"}
	pinErr := errors.New("profile team missing")

	_, err := repairSelection(PipelineRunConfig{
		NodeModelResolver: func(string, agent.ModelOverride) (agent.ModelSelection, error) {
			return agent.ModelSelection{}, pinErr
		},
		StageModelResolver: func(string) (agent.ModelSelection, error) {
			t.Fatal("repair must not fall back to the stage resolver for a pinned node")
			return agent.ModelSelection{}, nil
		},
	}, &namedProvider{name: "default"}, "code_writer", pin, false)
	if err == nil || !strings.Contains(err.Error(), "stage code_writer model") || !errors.Is(err, pinErr) {
		t.Fatalf("err = %v, want the node pin error naming the stage", err)
	}

	_, err = repairSelection(PipelineRunConfig{}, &namedProvider{name: "default"}, "code_writer", pin, false)
	if err == nil || !strings.Contains(err.Error(), "no resolver") {
		t.Fatalf("err = %v, want a missing-resolver error for a pinned node", err)
	}

}

// Repair re-entry fits the route effort to the model, as a normal stage does.
// A raw "max" must not reach a model that does not offer it.
func TestRepairSelectionFitsRouteEffort(t *testing.T) {
	modelregistry.EnableModelsDevOverlay()
	reg, err := modelregistry.DefaultRegistry("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	const model = "z-ai/glm-5.2"
	routed := agent.ModelSelection{Provider: &namedProvider{name: "route"}, ProviderName: "route", Model: model, ReasoningEffort: "max"}
	options := PipelineRunConfig{
		Model:         "anthropic/claude-opus-5",
		ModelRegistry: reg,
		StageModelResolver: func(string) (agent.ModelSelection, error) {
			return routed, nil
		},
	}
	sel, err := repairSelection(options, &namedProvider{name: "default"}, "code_writer", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	want := string(reg.RequestEffort(model, modelregistry.ReasoningEffortMax))
	if sel.ReasoningEffort != want || want == "max" {
		t.Fatalf("repair effort = %q, want the fitted %q (not raw max)", sel.ReasoningEffort, want)
	}
	if sel.Model != model {
		t.Fatalf("repair model = %q, want %q", sel.Model, model)
	}
}

// countingProvider counts the requests a run sends. A preflight failure must
// leave the count at zero.
type countingProvider struct {
	runFakeProvider
	requests int
}

func (p *countingProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.requests++
	return p.runFakeProvider.StreamCompletion(ctx, request)
}

// TestRunBadNodePinFailsBeforeFirstRequest pins that a node pin whose profile
// cannot resolve stops the run before any stage sends a request, and that the
// error names the node.
func TestRunBadNodePinFailsBeforeFirstRequest(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	writeWorkspaceTopology(t, workDir, "bad-pin",
		schemas.PipelineNode{Name: "code_writer", Type: "code_writer", Budget: &schemas.StageBudget{InputMax: 1000, OutputMax: 500}},
		schemas.PipelineNode{Name: "test_generator", Type: "test_generator", Budget: &schemas.StageBudget{InputMax: 1000, OutputMax: 500}, Model: &schemas.StageModelConfig{ProviderProfile: "ghost", Model: "x"}},
	)
	provider := &countingProvider{}
	opts := agent.Options{
		Cwd:              workDir,
		Registry:         registry,
		PermissionMode:   agent.PermissionModeAuto,
		MaxTurns:         1,
		TrustedWorkspace: true,
		NodeModelResolver: func(nodeName string, override agent.ModelOverride) (agent.ModelSelection, error) {
			return agent.ModelSelection{}, fmt.Errorf("node %q references unknown provider profile %q", nodeName, override.ProviderProfile)
		},
	}
	_, err := Run(context.Background(), "add a Hello function and tests", provider, opts, nil, nil)
	if err == nil {
		t.Fatal("Run succeeded with an unresolvable node pin")
	}
	for _, want := range []string{"preflight", "test_generator", `"ghost"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
	}
	if provider.requests != 0 {
		t.Fatalf("provider requests = %d, want 0 before the pin resolves", provider.requests)
	}
	if !errors.Is(err, ErrInvalidRunInput) {
		t.Fatalf("err = %v, want ErrInvalidRunInput in the chain", err)
	}
}

// TestRunPinProviderSetupFailureIsNotInputError pins the split between the
// two preflight failures: a pin to an unknown profile is invalid input, and a
// pin to a known profile whose provider cannot start (missing key) is a
// provider failure. Both stop the run before any request.
func TestRunPinProviderSetupFailureIsNotInputError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		profile   string
		wantInput bool
		wantText  string
	}{
		{"unknown profile", "ghost", true, `unknown provider profile "ghost"`},
		{"missing key", "keyless", false, "missing API key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workDir, registry := newRunTestWorkspace(t)
			writeWorkspaceTopology(t, workDir, "pinned",
				schemas.PipelineNode{Name: "code_writer", Type: "code_writer", Budget: &schemas.StageBudget{InputMax: 1000, OutputMax: 500}, Model: &schemas.StageModelConfig{ProviderProfile: tc.profile, Model: "x"}},
			)
			resolvers := BuildStageModelResolvers(schemas.StageModelConfigFile{}, []config.ProviderProfile{{Name: "keyless"}}, func(config.ProviderProfile) (agent.Provider, error) {
				return nil, errors.New("missing API key: set KEYLESS_API_KEY")
			}, TierResolverConfig{})
			provider := &countingProvider{}
			opts := agent.Options{
				Cwd:               workDir,
				Registry:          registry,
				PermissionMode:    agent.PermissionModeAuto,
				MaxTurns:          1,
				TrustedWorkspace:  true,
				NodeModelResolver: resolvers.Node,
			}
			_, err := Run(context.Background(), "add a Hello function and tests", provider, opts, nil, nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantText)
			}
			if got := errors.Is(err, ErrInvalidRunInput); got != tc.wantInput {
				t.Fatalf("errors.Is(err, ErrInvalidRunInput) = %v, want %v (err = %v)", got, tc.wantInput, err)
			}
			if provider.requests != 0 {
				t.Fatalf("provider requests = %d, want 0", provider.requests)
			}
		})
	}
}

// TestRunBrokenStageRouteFailsBeforeFirstRequest pins that a stage route that
// cannot resolve, on a stage after the first, stops the run before any
// provider request and is an input error. A tier fallback does not stop it.
func TestRunBrokenStageRouteFailsBeforeFirstRequest(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	writeWorkspaceTopology(t, workDir, "routed",
		schemas.PipelineNode{Name: "code_writer", Type: "code_writer", Budget: &schemas.StageBudget{InputMax: 1000, OutputMax: 500}},
		schemas.PipelineNode{Name: "test_generator", Type: "test_generator", Budget: &schemas.StageBudget{InputMax: 1000, OutputMax: 500}},
	)
	provider := &countingProvider{}
	opts := agent.Options{
		Cwd:              workDir,
		Registry:         registry,
		PermissionMode:   agent.PermissionModeAuto,
		MaxTurns:         1,
		TrustedWorkspace: true,
		StageModelResolver: func(stage string) (agent.ModelSelection, error) {
			if stage == "test_generator" {
				return agent.ModelSelection{}, fmt.Errorf("stage %q references unknown provider profile %q", stage, "ghost")
			}
			return agent.ModelSelection{}, &stageTierFallback{err: errors.New("tier unavailable")}
		},
	}
	_, err := Run(context.Background(), "add a Hello function and tests", provider, opts, nil, nil)
	if err == nil || !errors.Is(err, ErrInvalidRunInput) {
		t.Fatalf("err = %v, want ErrInvalidRunInput", err)
	}
	for _, want := range []string{"preflight: stage test_generator model", `"ghost"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
	}
	if provider.requests != 0 {
		t.Fatalf("provider requests = %d, want 0 before the route resolves", provider.requests)
	}
}

// TestPreflightStageRoutesSkipsPinnedAndModelFreeStages pins which stages the
// route preflight checks: a model-free stage and a pinned stage never use a
// stage route, so their route is not resolved.
func TestPreflightStageRoutesSkipsPinnedAndModelFreeStages(t *testing.T) {
	var asked []string
	options := PipelineRunConfig{StageModelResolver: func(stage string) (agent.ModelSelection, error) {
		asked = append(asked, stage)
		return agent.ModelSelection{}, errors.New("broken")
	}}
	plan := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{
		{Name: "security_auditor", Caps: &schemas.NodeCapabilities{ModelFree: true}},
		{Name: "code_writer", Model: &schemas.StageModelConfig{ProviderProfile: "team", Model: "m"}},
	}}
	if err := preflightStageRoutes(plan, options); err != nil || len(asked) != 0 {
		t.Fatalf("preflight err = %v asked = %v, want no route resolved", err, asked)
	}
	plan.Stages = append(plan.Stages, schemas.ExecutionStage{Name: "test_generator", Caps: &schemas.NodeCapabilities{}})
	if err := preflightStageRoutes(plan, options); err == nil || !strings.Contains(err.Error(), "stage test_generator model: broken") {
		t.Fatalf("preflight err = %v, want the test_generator route error", err)
	}
	if err := preflightStageRoutes(plan, PipelineRunConfig{}); err != nil {
		t.Fatalf("preflight with no resolver: %v", err)
	}
}

// TestBudgetBucketKeyUsesPinnedModel pins that a learned-budget key names the
// model the node is sent to: the node pin first, then the stage route, then
// the run model.
func TestBudgetBucketKeyUsesPinnedModel(t *testing.T) {
	options := PipelineRunConfig{
		Model: "run-model",
		StageModelResolver: func(string) (agent.ModelSelection, error) {
			return agent.ModelSelection{Provider: &namedProvider{name: "route"}, Model: "route-model"}, nil
		},
	}
	pinned := schemas.ExecutionStage{Name: "code_writer", Model: &schemas.StageModelConfig{ProviderProfile: "team", Model: "pinned-model"}}
	if key := budgetBucketKey(options, pinned, "root", "ph", "tf", "th"); key.Model != "pinned-model" || key.Stage != "code_writer" {
		t.Fatalf("pinned key = %+v, want model pinned-model", key)
	}
	unpinned := schemas.ExecutionStage{Name: "test_generator"}
	if key := budgetBucketKey(options, unpinned, "root", "ph", "tf", "th"); key.Model != "route-model" {
		t.Fatalf("routed key model = %q, want route-model", key.Model)
	}
	if key := budgetBucketKey(PipelineRunConfig{Model: "run-model"}, unpinned, "root", "ph", "tf", "th"); key.Model != "run-model" {
		t.Fatalf("primary key model = %q, want run-model", key.Model)
	}
}
