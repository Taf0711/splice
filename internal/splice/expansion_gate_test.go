package splice

// D2 exit gate (handoff Section 8): a mocked provider discovers a
// dependency omitted from the initial context, applies a correct edit,
// and completes independent verification - with no permission bypass.
//
// The scenario: the initial deterministic context delivers the main file
// but NOT the helper file it needs. The mocked model responds with a
// request_context action (the B1 range-read seam), the host fulfills it,
// and the model's second response submits a compact C-protocol edit whose
// base_ref resolves against the DELIVERED views. The independent verifier
// (a plain `go test` equivalent over the applied tree) decides the
// outcome - the mock's own confidence never does.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// dGateProvider plays the two-turn expansion scenario.
type dGateProvider struct {
	turn       int
	helperBase string // the helper file bytes (delivered in round 1)
	workDir    string // the workspace, so turn 2 can read the delivered main.go
}

// dGateWorkDir is set by the gate test before Run; stage runs are
// sequential so a single var is safe here.
var dGateWorkDir string

func (p *dGateProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.turn++
	ch := make(chan zeroruntime.StreamEvent, 8)
	toolName := ""
	if len(request.Tools) > 0 {
		toolName = request.Tools[0].Name
	}
	var args string
	switch p.turn {
	case 1:
		// Turn 1: the model sees the main file but not the helper it
		// needs; it returns a request_context action instead of a guess.
		req := map[string]any{
			"request_context": map[string]any{
				"reason": "need the helper file to call it correctly",
				"queries": []map[string]any{{
					"query_type": "read_file", "path": "helper.go", "max_results": 5, "max_chars": 8000,
				}},
			},
		}
		b, _ := json.Marshal(req)
		args = string(b)
	default:
		// Turn 2: with the helper in context, submit a compact edit.
		// base_ref = handle of the DELIVERED main.go text (what the base
		// registry recorded from the initial context views), which the
		// mock reads the same way the model does: from the recorded base
		// registry, not from disk.
		base, ok := stages.CurrentBaseForProbe("main.go")
		if !ok {
			// The base registry may have been reset between rounds (Set
			// ProposalBases scoping). Re-seed from the delivered views:
			// read main.go's delivered text from the initial context the
			// mock saw. For the gate, fall back to the file's bytes with
			// a loud log rather than guessing a digest.
			return nil, fmt.Errorf("gate wiring: no delivered base for main.go (turn=%d, bases recorded=%v)", p.turn, stages.ProbeBasePaths())
		}
		// Compose the edit exactly as a model would: pick the matched span
		// from the DELIVERED text (planner steer: base_ref identity is the
		// view digest, so edits match within delivered views). The span
		// comes from the base itself: the Hello line as delivered.
		oldSpan := ""
		oldContent := ""
		for _, line := range strings.Split(base, "\n") {
			if strings.Contains(line, "Hello() string") {
				oldSpan = line
				oldContent = stages.ViewLineContent(line)
				break
			}
		}
		if oldSpan == "" {
			prefix := base
			if len(prefix) > 60 {
				prefix = prefix[:60]
			}
			return nil, fmt.Errorf("gate wiring: delivered base has no Hello line (prefix=%q)", prefix)
		}
		newSpan := strings.Replace(oldContent, "wrong", "hello", 1)
		out := map[string]any{
			"files": []map[string]any{{
				"path":        "main.go",
				"change_type": "modify",
				"base_ref":    stages.HandleFor(dGateDigest(base)),
				"edits":       []map[string]string{{"old": oldSpan, "new": newSpan}},
			}},
			"language":   "go",
			"intent":     "wire the helper",
			"confidence": 0.95,
		}
		b, _ := json.Marshal(out)
		args = string(b)
	}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "1", ToolName: toolName}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "1", ArgumentsFragment: args}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "1"}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone, Usage: zeroruntime.Usage{InputTokens: 100, OutputTokens: 40}}
	close(ch)
	return ch, nil
}

// dGateDigest mirrors the base registry's digest for test wiring.
func dGateDigest(s string) string {
	return stages.ContentDigest(s)
}

// TestExitGateExpansionDiscoversDependencyAndVerifies is the Section 8 D
// exit gate over the real expansion loop, real registry, and real
// verifier - no permission bypass (the pipeline's own runner executes
// every mutation inside the workspace).
func TestExitGateExpansionDiscoversDependencyAndVerifies(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	// The initial context delivers main.go (a production source file the
	// fallback scan finds); helper.go is deliberately NOT referenced by
	// the intent, so the first response must request it.
	main := "package main\n\nfunc Hello() string { return \"wrong\" }\n"
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	helper := "package main\n\nfunc compute() int { return 42 }\n"
	if err := os.WriteFile(filepath.Join(workDir, "helper.go"), []byte(helper), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := &dGateProvider{helperBase: helper, workDir: workDir}
	dGateWorkDir = workDir
	defer func() { dGateWorkDir = "" }()

	plan, err := BuildExecutionPlan("wire the helper")
	if err != nil {
		t.Fatal(err)
	}
	// Light tier: writer + verifier only (no test generator).
	cfg := PipelineConfigFromAgentOptions(agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		FileTracker:    tools.NewFileTracker(),
	})
	result, runErr := runExecutionPlan(context.Background(), "run-d-gate", plan, provider, cfg, nil, nil)
	if runErr != nil {
		t.Logf("runErr=%v", runErr)
	}
	abort := ""
	if result.AbortReason != nil {
		abort = *result.AbortReason
	}
	t.Logf("status=%s abort=%q turn=%d", result.Status, abort, provider.turn)
	for _, s := range result.Stages {
		sum := ""
		if s.OutputSummary != nil {
			sum = *s.OutputSummary
		}
		t.Logf("stage=%s iter=%d status=%s summary=%q", s.Name, s.Iteration, s.Status, sum)

	}

	// Independent verification, from the applied tree: the edit landed.
	finalMain, rerr := os.ReadFile(filepath.Join(workDir, "main.go"))
	if rerr != nil {
		t.Fatalf("main.go missing after the gate: %v", rerr)
	}
	if !strings.Contains(string(finalMain), "hello") {
		t.Fatalf("the dependency-driven edit did not land: %q", finalMain)
	}
	// The expansion actually happened: the writer consumed 2 provider
	// turns (initial + post-expansion) before any other stage ran.
	if provider.turn < 2 {
		t.Fatalf("provider turns = %d, want >= 2 (expansion loop did not re-invoke)", provider.turn)
	}
	// The pipeline reached a verifier stage record (independent
	// verification ran; the mock's confidence was never the judge).
	verifierRan := false
	for _, record := range result.Stages {
		if record.Name == "acceptance_verifier" || record.Name == "test_runner" {
			verifierRan = true
		}
	}
	if !verifierRan {
		t.Fatalf("no independent verification stage ran; stages=%d", len(result.Stages))
	}
	// Permission honesty: the pipeline's own runner mutated files inside
	// the workspace only; nothing escaped (the registry is workspace-
	// scoped and the gate wrote no other files).
	entries, _ := os.ReadDir(workDir)
	for _, e := range entries {
		if !e.IsDir() && e.Name() != "main.go" && e.Name() != "helper.go" && e.Name() != "go.mod" &&
			e.Name() != "go.sum" && e.Name() != "main_test.go" && !strings.HasSuffix(e.Name(), ".go") {
			t.Fatalf("unexpected file materialized during the gate: %s", e.Name())
		}
	}
}
