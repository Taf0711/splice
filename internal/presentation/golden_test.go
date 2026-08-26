package presentation

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The golden helper implements the owner's combination approach: golden
// files live next to the tests under testdata/, are captured on first run
// and reviewed in diffs like any other file. Set SPLICE_UPDATE_GOLDEN=1 to
// (re)generate them. The helper is test-only and stdlib-only.

// goldenPath returns the golden file path for a fixture name.
func goldenPath(name string) string {
	return filepath.Join("testdata", name+".golden")
}

// updateGolden reports whether SPLICE_UPDATE_GOLDEN is set.
func updateGolden() bool {
	return os.Getenv("SPLICE_UPDATE_GOLDEN") == "1"
}

// checkGolden serializes state as indented JSON and compares it to the
// golden file. The fixture state must be legal (passes Validate) regardless
// of capture mode: a golden that cannot validate would pin an illegal state.
func checkGolden(t *testing.T, name string, state State) {
	t.Helper()
	if err := state.Validate(); err != nil {
		t.Fatalf("golden fixture %s must be a legal state: %v", name, err)
	}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	encoded = append(encoded, '\n')
	path := goldenPath(name)
	if updateGolden() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create testdata dir: %v", err)
		}
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("updated golden %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (create it with SPLICE_UPDATE_GOLDEN=1)", path, err)
	}
	if !bytes.Equal(want, encoded) {
		t.Fatalf("golden %s mismatch (regenerate with SPLICE_UPDATE_GOLDEN=1):\n--- want\n%s--- got\n%s", name, want, encoded)
	}
}

func TestGoldenEmptyState(t *testing.T) {
	checkGolden(t, "empty_state", State{SchemaVersion: PresentationSchemaVersionV1})
}

func TestGoldenAllNodeKindsMidRun(t *testing.T) {
	state := State{
		SchemaVersion: PresentationSchemaVersionV1,
		Lifecycle:     LifecycleExecute,
		Plan:          Plan{Title: "hello service", TaskCount: 8},
		Nodes: []ExecutionNode{
			{ID: "code_writer", Label: "code_writer", Kind: NodeKindWrite, Status: NodeStatusRunning, Progress: 0.4, Iteration: 1},
			{ID: "static_analyzer", Label: "static_analyzer", Kind: NodeKindAnalyze, Status: NodeStatusComplete, Progress: 1, Iteration: 1},
			{ID: "security_auditor", Label: "security_auditor", Kind: NodeKindSecurity, Status: NodeStatusPending, Progress: 0, Iteration: 1},
			{ID: "linter", Label: "linter", Kind: NodeKindLint, Status: NodeStatusDegraded, Progress: 0.5, Iteration: 2},
			{ID: "custom_stage", Label: "custom_stage", Kind: NodeKindCustom, Status: NodeStatusRunning, Progress: 0.25, Iteration: 1},
			{ID: "test_runner", Label: "test_runner", Kind: NodeKindTest, Status: NodeStatusRunning, Progress: 0.75, Iteration: 1},
			{ID: "acceptance_verifier", Label: "acceptance_verifier", Kind: NodeKindVerify, Status: NodeStatusPending, Progress: 0, Iteration: 1},
			{ID: "code_reviewer", Label: "code_reviewer", Kind: NodeKindReview, Status: NodeStatusFailed, Progress: 0.9, Iteration: 1},
		},
	}
	checkGolden(t, "all_node_kinds_mid_run", state)
}

func TestGoldenFailedNodeWithEvidence(t *testing.T) {
	state := State{
		SchemaVersion: PresentationSchemaVersionV1,
		Lifecycle:     LifecycleExecute,
		Plan:          Plan{TaskCount: 2},
		Nodes: []ExecutionNode{
			{ID: "test_runner", Label: "test_runner", Kind: NodeKindTest, Status: NodeStatusFailed, Progress: 1, Iteration: 1},
		},
		Evidence: []EvidenceGroup{
			{
				Label:      "unit checks",
				Status:     EvidenceFailed,
				Passed:     3,
				Failed:     2,
				Incomplete: 0,
				Findings:   []string{"TestHello failed: wrong greeting", "TestShutdown failed: goroutine leak"},
				Duration:   4.2,
			},
		},
		Interventions: []Intervention{
			{
				Kind:         InterventionRetry,
				Reason:       "test suite failed twice with the same evidence",
				TargetNodeID: "test_runner",
				Status:       InterventionProposed,
			},
		},
	}
	checkGolden(t, "failed_node_with_evidence", state)
}

func TestGoldenCompletedRunWithReceipt(t *testing.T) {
	state := State{
		SchemaVersion: PresentationSchemaVersionV1,
		Lifecycle:     LifecycleComplete,
		Plan:          Plan{Title: "hello service", TaskCount: 3},
		Nodes: []ExecutionNode{
			{ID: "code_writer", Label: "code_writer", Kind: NodeKindWrite, Status: NodeStatusComplete, Progress: 1, Iteration: 1},
			{ID: "test_runner", Label: "test_runner", Kind: NodeKindTest, Status: NodeStatusComplete, Progress: 1, Iteration: 1},
			{ID: "security_auditor", Label: "security_auditor", Kind: NodeKindSecurity, Status: NodeStatusComplete, Progress: 1, Iteration: 1},
		},
		Evidence: []EvidenceGroup{
			{Label: "full suite", Status: EvidencePassed, Passed: 12, Failed: 0, Incomplete: 0, Duration: 6.1},
		},
		Files: []FileChangeSummary{
			{Path: "main.go", Status: "modified", Additions: 34, Deletions: 2},
			{Path: "main_test.go", Status: "added", Additions: 58, Deletions: 0},
		},
		Usage: UsageSummary{
			InputTokens:     120000,
			OutputTokens:    24000,
			CachedTokens:    30000,
			ReasoningTokens: 8000,
			CostUSD:         0.42,
			ByNode: map[string]TokenUsage{
				"code_writer": {InputTokens: 50000, OutputTokens: 12000},
				"test_runner": {InputTokens: 70000, OutputTokens: 12000},
			},
		},
		Completion: &CompletionReceipt{Status: "completed", Detail: "all gates green"},
	}
	checkGolden(t, "completed_run_with_receipt", state)
}
