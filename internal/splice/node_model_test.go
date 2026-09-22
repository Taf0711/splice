package splice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
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

	records, _, completed, err := runPass(context.Background(), "run-node-model", 1, plan, registry, &namedProvider{name: "default"}, options, workDir, nil, time.Time{}, nil, nil, nil)
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
	_, _, _, err := runPass(context.Background(), "run-broken-node-model", 1, plan, registry, &namedProvider{name: "default"}, options, workDir, nil, time.Time{}, nil, nil, nil)
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
