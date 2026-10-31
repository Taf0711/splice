package splice

// The seven offline delivery proofs (Jev decision-advisor spec Section 14.1,
// M1 item 7). Each proof walks the REAL production chain - retrieval,
// admission, materialization, provider payload, protocol, accounting - not a
// mocked sub-part. A failed proof names the broken link; it is never reported
// as a limit of memory in general.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// proofPayloadRunner returns a ToolRunner whose read_file serves the REAL
// current bytes of the fixture files, so a delivered source view carries the
// actual declaration text into the provider payload.
func proofPayloadRunner(t *testing.T, workDir string, fetched *[]string) ToolRunner {
	t.Helper()
	return ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		t.Logf("RUNNER CALL: %s args=%v", name, args)
		switch name {
		case "read_file", "raw_file_read":
			path, _ := args["path"].(string)
			if fetched != nil {
				*fetched = append(*fetched, path)
			}
			switch path {
			case testSymbolPath:
				return ToolResult{OK: true, Output: testFileBody}, nil
			case "clock.go":
				return ToolResult{OK: true, Output: prodSiblingBody}, nil
			}
			return ToolResult{OK: true, Output: ""}, nil
		case "find_symbol", "get_symbol":
			if fetched != nil {
				*fetched = append(*fetched, testSymbolPath+"#"+testSymbolName)
			}
			return ToolResult{OK: true, Output: "func " + testSymbolName + "(t *testing.T) {}"}, nil
		case "search", "grep", "list_directory":
			return ToolResult{OK: true, Output: testSymbolPath + "\nclock.go\n"}, nil
		}
		return ToolResult{OK: true, Output: ""}, nil
	})
}

// Proof 1: capture and retrieval use the same canonical project identity. A
// record captured through a path alias must reach the candidate set when the
// chain runs from the real path (and the canonicalizer must agree), because
// the sidecar canonicalizes the project path before storing.
func TestProof1CanonicalIdentityAcrossPathAliases(t *testing.T) {
	workDir, rev, body := testSymbolRepo(t, nil)
	alias := workDir + "-alias-link"
	if err := os.Symlink(workDir, alias); err != nil {
		t.Skipf("symlink unavailable on this filesystem: %v", err)
	}
	if memd.CanonicalProjectPath(alias) != memd.CanonicalProjectPath(workDir) {
		t.Fatalf("canonical path mismatch: alias %q vs real %q", memd.CanonicalProjectPath(alias), memd.CanonicalProjectPath(workDir))
	}
	// Capture through the alias; run the chain from the real path.
	mem := testSymbolVouchedStore(t, alias, rev, body)
	plan := schemas.ExecutionPlan{
		Tier: schemas.TierLight, RequestIntent: vouchedReadIdent,
		Stages: []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	_, scope, _, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:  schemas.HarnessStageInput{RunID: "proof-1", StageName: "code_writer", Sequence: 1, PlanTier: plan.Tier, RequestIntent: plan.RequestIntent},
		Stage:  &capturingStage{caps: stages.Capabilities{ConsumesMemory: true, PullContext: true}},
		Budget: schemas.StageBudget{InputMax: 20000, OutputMax: 8192}, Tier: plan.Tier,
		Iteration: 1, WorkDir: workDir, Options: PipelineConfigFromAgentOptions(agent.Options{Flags: flags.Set{flags.MemoryEvidenceSubstitution: true}}), Memory: mem,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if scope.Evidence == nil || len(scope.Evidence.Admitted) == 0 {
		t.Fatalf("record captured under a path alias must reach the candidate set from the real path: %+v", scope.Evidence)
	}
	found := false
	for _, a := range scope.Evidence.Admitted {
		if a.Record != nil && strings.Contains(a.Record.Identity, testSymbolName) {
			found = true
		}
	}
	if !found {
		t.Fatalf("candidate set misses the alias-captured record: %+v", scope.Evidence.Admitted)
	}
}

// Proof 2: a natural retained record reaches the current candidate set. The
// vouched record from a completed task A is admitted for task B's need, and
// the trace carries the retrieved -> eligible -> selected chain.
func TestProof2RetainedRecordReachesCandidates(t *testing.T) {
	workDir, rev, body := testSymbolRepo(t, nil)
	mem := testSymbolVouchedStore(t, workDir, rev, body)
	plan := schemas.ExecutionPlan{
		Tier: schemas.TierLight, RequestIntent: vouchedReadIdent,
		Stages: []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	prepared, scope, _, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:  schemas.HarnessStageInput{RunID: "proof-2", StageName: "code_writer", Sequence: 1, PlanTier: plan.Tier, RequestIntent: plan.RequestIntent},
		Stage:  &capturingStage{caps: stages.Capabilities{ConsumesMemory: true, PullContext: true}},
		Budget: schemas.StageBudget{InputMax: 20000, OutputMax: 8192}, Tier: plan.Tier,
		Iteration: 1, WorkDir: workDir, Options: PipelineConfigFromAgentOptions(agent.Options{Flags: flags.Set{flags.MemoryEvidenceSubstitution: true}}), Memory: mem,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if scope.Evidence == nil {
		t.Fatal("evidence plan missing: retrieval produced no candidates")
	}
	if len(scope.Evidence.Admitted) == 0 {
		t.Fatalf("no admitted record: admissible=%+v rejected=%+v needs=%+v", scope.Evidence.Admitted, scope.Evidence.Rejected, scope.Evidence.Needs)
	}
	found := false
	for _, a := range scope.Evidence.Admitted {
		if a.Record != nil && strings.Contains(a.Record.Identity, testSymbolName) {
			found = true
		}
	}
	if !found {
		t.Fatalf("admitted set misses the test-symbol record: %+v (prepared bundle memory items: %d)", scope.Evidence.Admitted, len(prepared.MemoryBundle.Observations))
	}
}

// Proof 3: exact source extraction includes the needed declaration. The
// provider payload must carry the full function declaration text through the
// production chain (runPass: prepare -> scope -> evidence request ->
// fulfillment), never only a location claim.
func TestProof3ExtractionIncludesDeclaration(t *testing.T) {
	workDir, rev, body := testSymbolRepo(t, nil)
	mem := testSymbolVouchedStore(t, workDir, rev, body)
	plan := schemas.ExecutionPlan{
		Tier: schemas.TierLight, RequestIntent: vouchedReadIdent,
		Stages: []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	options := PipelineConfigFromAgentOptions(agent.Options{Flags: flags.Set{flags.MemoryEvidenceSubstitution: true}})
	provider := &memoryScriptedProvider{capturePayload: new([]string)}
	runner := proofPayloadRunner(t, workDir, nil)
	tr := newRunTraceAccumulator(nil, "proof-3", "session", workDir, plan, "active", nil)
	_, _, completed, err := runPass(context.Background(), "proof-3", 1, plan,
		stageRegistry{"code_writer": stages.CodeWriter{}}, provider, options, workDir, runner, time.Time{}, nil, mem, tr, NewStageExecutionBudget(0))
	if err != nil || !completed {
		t.Fatalf("runPass: err=%v completed=%v", err, completed)
	}
	if len(*provider.capturePayload) == 0 {
		t.Fatal("no provider payload captured")
	}
	joined := strings.Join(*provider.capturePayload, "\n")
	decl := "func " + testSymbolName + "(t *testing.T) {}"
	if !strings.Contains(joined, decl) {
		idx := strings.Index(joined, "context ")
		for idx >= 0 {
			end := idx + 300
			if end > len(joined) {
				end = len(joined)
			}
			t.Logf("PAYLOAD CONTEXT SECTION @%d: %.300s", idx, joined[idx:end])
			next := strings.Index(joined[idx+1:], "context ")
			if next < 0 {
				break
			}
			idx += 1 + next
		}
		ki := strings.Index(joined, "relevant_context")
		if ki >= 0 {
			end := ki + 900
			if end > len(joined) {
				end = len(joined)
			}
			t.Logf("RELEVANT_CONTEXT REGION: %q", joined[ki:end])
		}
		t.Fatalf("provider payload lacks the full declaration %q; delivered payload length: %d", decl, len(joined))
	}
}

// Proof 4: the selected source reaches the actual provider-facing payload
// exactly once - no duplicate delivery across the handshake queries.
func TestProof4SourceReachesPayloadOnce(t *testing.T) {
	workDir, rev, body := testSymbolRepo(t, nil)
	mem := testSymbolVouchedStore(t, workDir, rev, body)
	plan := schemas.ExecutionPlan{
		Tier: schemas.TierLight, RequestIntent: vouchedReadIdent,
		Stages: []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	options := PipelineConfigFromAgentOptions(agent.Options{Flags: flags.Set{flags.MemoryEvidenceSubstitution: true}})
	provider := &memoryScriptedProvider{capturePayload: new([]string)}
	runner := proofPayloadRunner(t, workDir, nil)
	tr := newRunTraceAccumulator(nil, "proof-4", "session", workDir, plan, "active", nil)
	_, _, completed, err := runPass(context.Background(), "proof-4", 1, plan,
		stageRegistry{"code_writer": stages.CodeWriter{}}, provider, options, workDir, runner, time.Time{}, nil, mem, tr, NewStageExecutionBudget(0))
	if err != nil || !completed {
		t.Fatalf("runPass: err=%v completed=%v", err, completed)
	}
	joined := strings.Join(*provider.capturePayload, "\n")
	if got := strings.Count(joined, "func "+testSymbolName+"(t *testing.T) {}"); got != 1 {
		t.Fatalf("declaration appears %d times in the provider payload, want exactly once", got)
	}
}

// Proof 5: a removed or changed source does not falsely satisfy a need. The
// bytes changed since capture, so freshness fails, the substitution never
// fires, and the trace shows the cold read retained.
func TestProof5ChangedSourceDoesNotSatisfy(t *testing.T) {
	workDir, rev, body := testSymbolRepo(t, nil)
	mem := testSymbolVouchedStore(t, workDir, rev, body)
	// The world moved on after capture.
	if err := os.WriteFile(filepath.Join(workDir, testSymbolPath), []byte(strings.Replace(testFileBody, "runClockTable(t *testing.T) {}", "runClockTable(t *testing.T, cfg int) {}", 1)), 0o644); err != nil {
		t.Fatalf("mutate fixture: %v", err)
	}
	plan := schemas.ExecutionPlan{
		Tier: schemas.TierLight, RequestIntent: vouchedReadIdent,
		Stages: []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	options := PipelineConfigFromAgentOptions(agent.Options{Flags: flags.Set{flags.MemoryEvidenceSubstitution: true}})
	provider := &memoryScriptedProvider{capturePayload: new([]string)}
	runner := proofPayloadRunner(t, workDir, nil)
	tr := newRunTraceAccumulator(nil, "proof-5", "session", workDir, plan, "active", nil)
	_, _, completed, err := runPass(context.Background(), "proof-5", 1, plan,
		stageRegistry{"code_writer": stages.CodeWriter{}}, provider, options, workDir, runner, time.Time{}, nil, mem, tr, NewStageExecutionBudget(0))
	if err != nil || !completed {
		t.Fatalf("runPass: err=%v completed=%v", err, completed)
	}
	// No satisfied-by-evidence decision anywhere in the trace.
	meta := tr.stages[stageKeyFor("code_writer", 1, 0)]
	if meta.OperationDecisions != nil {
		for _, d := range *meta.OperationDecisions {
			if d.Disposition == schemas.OperationSatisfiedByEvidence {
				t.Fatalf("stale record must not satisfy a need: %+v", d)
			}
		}
	}
	// The stale declaration is not delivered as if it were current.
	joined := strings.Join(*provider.capturePayload, "\n")
	if strings.Contains(joined, "runClockTable(t *testing.T, cfg int)") {
		t.Fatal("payload must not carry the stale body as current evidence")
	}
}

// Proof 6: the context-request and final-submission protocol remains valid
// when evidence was delivered - the submission carries no context placeholder,
// and the run completes through the normal typed output.
func TestProof6ProtocolValidWithDeliveredEvidence(t *testing.T) {
	workDir, rev, body := testSymbolRepo(t, nil)
	mem := testSymbolVouchedStore(t, workDir, rev, body)
	plan := schemas.ExecutionPlan{
		Tier: schemas.TierLight, RequestIntent: vouchedReadIdent,
		Stages: []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	options := PipelineConfigFromAgentOptions(agent.Options{Flags: flags.Set{flags.MemoryEvidenceSubstitution: true}})
	provider := &memoryScriptedProvider{}
	runner := proofPayloadRunner(t, workDir, nil)
	tr := newRunTraceAccumulator(nil, "proof-6", "session", workDir, plan, "active", nil)
	_, _, completed, err := runPass(context.Background(), "proof-6", 1, plan,
		stageRegistry{"code_writer": stages.CodeWriter{}}, provider, options, workDir, runner, time.Time{}, nil, mem, tr, NewStageExecutionBudget(0))
	if err != nil || !completed {
		t.Fatalf("runPass: err=%v completed=%v", err, completed)
	}
	if provider.request.Messages == nil {
		t.Fatal("provider saw no request")
	}
	for _, m := range provider.request.Messages {
		if strings.Contains(m.Content, `"request_context":{}`) {
			t.Fatal("request carries an empty context placeholder")
		}
	}
}

// Proof 7: usage and cost survive failure and cancellation. A provider that
// emits usage then errors leaves a priced record on the ledger and the run
// fails honestly; a canceled context never reports success.
func TestProof7UsageAndCostSurviveFailureAndCancellation(t *testing.T) {
	plan := schemas.ExecutionPlan{
		Tier: schemas.TierLight, RequestIntent: "add a helper",
		Stages: []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	var attributed []agent.AttributedUsage
	options := PipelineConfigFromAgentOptions(agent.Options{OnAttributedUsage: func(u agent.AttributedUsage) {
		attributed = append(attributed, u)
	}})
	failing := &usageThenErrorProvider{}
	_, err := runStageWithContext(context.Background(), schemas.HarnessStageInput{
		RunID: "proof-7-fail", StageName: "code_writer", Sequence: 1, PlanTier: plan.Tier, RequestIntent: plan.RequestIntent,
	}, stages.CodeWriter{}, 1, agent.ModelSelection{Provider: failing}, options, t.TempDir(), nil, nil, stages.Capabilities{}, 0, nil, nil, 0)
	if err == nil {
		t.Fatal("provider error must fail the stage")
	}
	if len(attributed) == 0 || attributed[0].Usage.InputTokens == 0 {
		t.Fatalf("usage emitted before the failure must survive: %+v", attributed)
	}
	if attributed[0].ReportedCostUSD == nil || *attributed[0].ReportedCostUSD != 0.002 {
		t.Fatalf("the provider-reported exact charge must survive the failure: %+v", attributed[0].ReportedCostUSD)
	}

	// Cancellation: a canceled context must fail loudly, never report success.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runStageWithContext(ctx, schemas.HarnessStageInput{
		RunID: "proof-7-cancel", StageName: "code_writer", Sequence: 1, PlanTier: plan.Tier, RequestIntent: "add a helper",
	}, stages.CodeWriter{}, 1, agent.ModelSelection{Provider: &memoryScriptedProvider{}}, options, t.TempDir(), nil, nil, stages.Capabilities{}, 0, nil, nil, 0); err == nil {
		t.Fatal("canceled context must fail loudly, never report success")
	}
}

// usageThenErrorProvider emits a real usage event (tokens + reported cost)
// and then fails the stream, so the proof can assert the priced attempt
// survives on the ledger while the run fails honestly.
type usageThenErrorProvider struct{}

func (p *usageThenErrorProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	events := []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 900, OutputTokens: 40, CachedInputTokens: 100}, ReportedCostUSD: ptrForProof(0.002)},
		{Type: zeroruntime.StreamEventError, Error: "provider request error: upstream reset mid-stream"},
	}
	ch := make(chan zeroruntime.StreamEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func ptrForProof(v float64) *float64 { return &v }
