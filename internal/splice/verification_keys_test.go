package splice

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// TestNormalizeVerificationReport pins the capability-aware alias: only a node
// whose capabilities declare produces_verification has its legacy report
// re-keyed, the legacy key survives for the severity counts, and the source
// output is not mutated.
func TestNormalizeVerificationReport(t *testing.T) {
	report := schemas.VerificationReport{Status: schemas.VerificationPassed, Complete: true, Summary: "ok"}
	legacy := schemas.HarnessStageOutput{Data: map[string]any{"static_analyzer_output": report}}
	if _, ok := normalizeVerificationReport(legacy, false).Data[VerificationReportKey]; ok {
		t.Fatal("a node without the capability was promoted to the canonical key")
	}
	got := normalizeVerificationReport(legacy, true)
	if _, ok := got.Data[VerificationReportKey]; !ok {
		t.Fatalf("legacy report not re-keyed: %v", got.Data)
	}
	if _, ok := got.Data["static_analyzer_output"]; !ok {
		t.Fatal("the legacy key must survive for the severity counts")
	}
	if _, ok := legacy.Data[VerificationReportKey]; ok {
		t.Fatal("normalize mutated the source output")
	}
	// An existing canonical report is left alone.
	canonical := schemas.HarnessStageOutput{Data: map[string]any{VerificationReportKey: report}}
	if _, ok := normalizeVerificationReport(canonical, true).Data[VerificationReportKey]; !ok {
		t.Fatal("canonical report lost")
	}
}

func TestVerificationReportReadsCanonicalFirst(t *testing.T) {
	incomplete := schemas.VerificationReport{Status: schemas.VerificationIncomplete, Complete: false, Summary: "partial"}
	if report, ok := verificationReport(schemas.HarnessStageOutput{Data: map[string]any{VerificationReportKey: incomplete}}); !ok || report.Status != schemas.VerificationIncomplete {
		t.Fatalf("canonical read = %+v/%v", report, ok)
	}
	findings := schemas.VerificationReport{Status: schemas.VerificationFindings, Complete: true, Summary: "findings"}
	if report, ok := verificationReport(schemas.HarnessStageOutput{Data: map[string]any{"security_auditor_output": findings}}); !ok || report.Status != schemas.VerificationFindings {
		t.Fatalf("legacy read = %+v/%v", report, ok)
	}
	if _, ok := verificationReport(schemas.HarnessStageOutput{}); ok {
		t.Fatal("an empty output reported a verification report")
	}
}

func TestIsVerificationIncompleteOutputCanonical(t *testing.T) {
	incomplete := schemas.HarnessStageOutput{Data: map[string]any{VerificationReportKey: schemas.VerificationReport{Status: schemas.VerificationIncomplete, Complete: false, Summary: "x"}}}
	if !isVerificationIncompleteOutput(incomplete) {
		t.Fatal("canonical incomplete report not detected")
	}
	passed := schemas.HarnessStageOutput{Data: map[string]any{VerificationReportKey: schemas.VerificationReport{Status: schemas.VerificationPassed, Complete: true, Summary: "x"}}}
	if isVerificationIncompleteOutput(passed) {
		t.Fatal("a passing report is not incomplete")
	}
}

// TestComputeIterationStateCollectsCanonicalVerification pins that a custom
// verification node's canonical report reaches the state vector, and that the
// verification-only hash changes across passes so the cycle detector does not
// abort a legitimate revision loop.
func TestComputeIterationStateCollectsCanonicalVerification(t *testing.T) {
	line := 12
	first := []schemas.HarnessStageOutput{{Summary: "verified", Confidence: 1, Data: map[string]any{
		VerificationReportKey: schemas.VerificationReport{Status: schemas.VerificationIncomplete, Complete: false, Summary: "partial",
			Findings: []schemas.VerificationFinding{{RuleID: "R1", Path: "a.go", Line: &line}}},
	}}}
	state, err := ComputeIterationState(1, first, nil, schemas.ChangeSummary{}, nil)
	if err != nil {
		t.Fatalf("ComputeIterationState: %v", err)
	}
	if state.VerificationIncomplete != 1 {
		t.Fatalf("VerificationIncomplete = %d, want 1", state.VerificationIncomplete)
	}
	if state.StateHash == "" {
		t.Fatal("verification-only graph produced an empty state hash")
	}

	nextLine := 13
	next := []schemas.HarnessStageOutput{{Summary: "verified", Confidence: 1, Data: map[string]any{
		VerificationReportKey: schemas.VerificationReport{Status: schemas.VerificationFindings, Complete: true, Summary: "found",
			Findings: []schemas.VerificationFinding{{RuleID: "R1", Path: "a.go", Line: &nextLine}}},
	}}}
	nextState, err := ComputeIterationState(2, next, nil, schemas.ChangeSummary{}, nil)
	if err != nil {
		t.Fatalf("ComputeIterationState: %v", err)
	}
	if nextState.StateHash == state.StateHash {
		t.Fatalf("verification-only state hash did not change across passes: %s", state.StateHash)
	}
}

// TestStateHashFallback pins the two branches: a code-writer graph hashes files,
// a verification-only graph hashes findings deterministically.
func TestStateHashFallback(t *testing.T) {
	writer := []schemas.CodeWriterOutput{{Files: []schemas.FileChange{{Path: "a.go", ChangeType: "modify", Content: "x"}}}}
	if stateHash(writer, nil) == "" {
		t.Fatal("empty hash with code-writer output")
	}
	reports := []schemas.VerificationReport{{Findings: []schemas.VerificationFinding{{RuleID: "R", Path: "p"}}}}
	if stateHash(nil, reports) != stateHash(nil, reports) {
		t.Fatal("verification fallback hash is not deterministic")
	}
	if stateHash(nil, nil) == "" {
		t.Fatal("empty graph hash must not be empty")
	}
}

// TestPlanHasVerification pins the run-start notice decision.
func TestPlanHasVerification(t *testing.T) {
	if planHasVerification(schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "code_writer"}}}) {
		t.Fatal("code_writer is not a verification node")
	}
	if !planHasVerification(schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "test_runner"}}}) {
		t.Fatal("test_runner must count as verification")
	}
	yes, no := true, false
	if !planHasVerification(schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "custom", Caps: &schemas.NodeCapabilities{ProducesVerification: yes}}}}) {
		t.Fatal("a compiled custom verification node must count")
	}
	if planHasVerification(schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "custom", Caps: &schemas.NodeCapabilities{ProducesVerification: no}}}}) {
		t.Fatal("a compiled node without the capability must not count")
	}
}

// TestCompileWarnsOnVerificationFreeGraph pins the load-time notice.
func TestCompileWarnsOnVerificationFreeGraph(t *testing.T) {
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "noverify",
		Nodes:   []schemas.PipelineNode{{Name: "code_writer", Type: "code_writer"}},
	}
	compiled, err := CompileTopology(topology, schemas.TierStandard)
	if err != nil {
		t.Fatalf("CompileTopology: %v", err)
	}
	if !strings.Contains(strings.Join(compiled.Warnings, "\n"), "no verification node") {
		t.Fatalf("warnings = %v, want the verification-free notice", compiled.Warnings)
	}

	withVerify := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "verify",
		Nodes: []schemas.PipelineNode{
			{Name: "code_writer", Type: "code_writer"},
			{Name: "test_runner", Type: "test_runner"},
		},
		Edges: []schemas.PipelineEdge{{From: "code_writer", To: "test_runner"}},
	}
	compiled, err = CompileTopology(withVerify, schemas.TierStandard)
	if err != nil {
		t.Fatalf("CompileTopology: %v", err)
	}
	if strings.Contains(strings.Join(compiled.Warnings, "\n"), "no verification node") {
		t.Fatalf("warnings = %v, want no verification-free notice", compiled.Warnings)
	}
}
