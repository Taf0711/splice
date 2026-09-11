package splice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

const prodEvidencePath = "internal/audit/retention.go"

func prodEvidenceRepo(t *testing.T) (string, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	body := []byte("package audit\n\n// Apply drops events outside the retention policy.\nfunc Apply() {}\n")
	if err := os.MkdirAll(filepath.Join(dir, "internal", "audit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(prodEvidencePath)), body, 0o644); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	return dir, gitHead(t, dir), body
}

func prodEvidenceRecord(t *testing.T, workspace, revision string, body []byte) *ReuseRecord {
	t.Helper()
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	subject := prodEvidencePath + "#Apply"
	rec := &ReuseRecord{
		SchemaVersion:      ReuseRecordSchemaVersion,
		Kind:               "fact",
		Identity:           recordIdentity(workspace, "fact", subject),
		AnsweredNeed:       NeedLocateNamedOperation + ":" + subject,
		Applicability:      "location of Apply in " + prodEvidencePath + " at the verified revision",
		Conclusion:         "located Apply in " + prodEvidencePath,
		Supporting:         []SourceRef{{Path: prodEvidencePath, Symbol: "Apply", Digest: digest}},
		ProducerRun:        "run-A",
		CaptureOrigin:      CaptureOriginRuntime,
		WorktreeIdentity:   revision,
		VerificationStatus: VerificationStatusPassed,
		Status:             "active",
	}
	rec.ContentVersion = contentVersionDigest(*rec)
	return rec
}

func prodEvidenceStore(t *testing.T, workspace, revision string, body []byte) MemoryStore {
	t.Helper()
	rec := prodEvidenceRecord(t, workspace, revision, body)
	return prodEvidenceStoreForRecord(t, workspace, revision, rec)
}

func prodEvidenceStoreForRecord(t *testing.T, workspace, revision string, rec *ReuseRecord) MemoryStore {
	t.Helper()
	_, client := newFakeSidecar(t)
	if _, err := client.UpsertGraphNode(context.Background(), memd.GraphUpsertInput{
		Kind:             "fact",
		Claim:            "internal/audit/retention.go defines Apply",
		Scope:            "project",
		ProjectPath:      workspace,
		Status:           "active",
		SourceRunID:      "run-A",
		VerifiedRevision: revision,
		Anchors: []memd.GraphAnchor{
			{Kind: "file", Value: prodEvidencePath},
			{Kind: "symbol", Value: prodEvidencePath + "#Apply"},
		},
		Metadata: recordToMetadata(rec),
	}); err != nil {
		t.Fatalf("upsert evidence node: %v", err)
	}
	return NewGraphMemoryStore(client)
}

type prodEvidenceProbe struct {
	reads    []string
	rawReads int
	tools    []string
}

func prodEvidenceRunner(t *testing.T, probe *prodEvidenceProbe) ToolRunner {
	t.Helper()
	return ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		probe.tools = append(probe.tools, name)
		switch name {
		case "read_file":
			if p, ok := args["path"].(string); ok {
				probe.reads = append(probe.reads, p)
			}
			return ToolResult{OK: true, Output: "package audit\n\nfunc Apply() {}\n"}, nil
		case "raw_file_read":
			probe.rawReads++
			return ToolResult{OK: true, Output: "package audit\n\nfunc Apply() {}\n"}, nil
		case "list_directory":
			return ToolResult{OK: true, Output: "internal/\n  audit/\n    retention.go\n"}, nil
		case "grep":
			return ToolResult{OK: true, Output: "internal/audit/retention.go:3:func Apply() {}\n"}, nil
		default:
			return ToolResult{OK: true, Output: ""}, nil
		}
	})
}

func TestProductionEvidenceSubstitutionOmitsPlannedSymbolLookup(t *testing.T) {
	if _, err := os.Stat(".git"); err != nil {
		// The production test runs inside the repository test tree; git is
		// needed for freshness validation, not for the workspace fixture.
	}
	workDir, rev, body := prodEvidenceRepo(t)
	mem := prodEvidenceStore(t, workDir, rev, body)
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "Add RetentionDeficit in internal/audit/retention.go and reuse Apply.",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	options := PipelineConfigFromAgentOptions(agent.Options{})
	provider := &memoryScriptedProvider{}

	// Plan inspection: the improved cold plan contains the symbol-resolution
	// operation, and admission removes it in the warm plan.
	prepared, planScope, _, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input: schemas.HarnessStageInput{
			RunID: "prod-evidence-plan", StageName: "code_writer", Sequence: 1,
			PlanTier: plan.Tier, RequestIntent: plan.RequestIntent,
		},
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true, PullContext: true}},
		Budget:    schemas.StageBudget{InputMax: 20000, OutputMax: 8192},
		Tier:      plan.Tier,
		Iteration: 1,
		WorkDir:   workDir,
		Options:   options,
		Memory:    mem,
	})
	if err != nil || prepared.StageName != "code_writer" {
		t.Fatalf("plan preparation: err=%v prepared=%+v", err, prepared)
	}
	if planScope.Evidence == nil {
		t.Fatal("no evidence plan built for the production fixture")
	}
	if !prodEvidenceOpsContain(planScope.Evidence.Cold.Operations, "symbol:") {
		t.Fatalf("cold plan lost its symbol-resolution operation: %+v", planScope.Evidence.Cold.Operations)
	}
	if prodEvidenceOpsContain(planScope.Evidence.Warm.Operations, "symbol:") {
		t.Fatalf("warm plan retained a substituted symbol operation: %+v", planScope.Evidence.Warm.Operations)
	}
	if len(planScope.Evidence.Diff.Eliminated) == 0 {
		t.Fatalf("plan diff recorded no eliminated operation: %+v", planScope.Evidence.Diff)
	}

	// Cold control: no retained evidence, the default request still performs
	// the required body read. It is not required to execute the improved-cold
	// symbol plan; the plan proof is the trace\/plan assertion above.
	var coldProbe prodEvidenceProbe
	coldRunner := prodEvidenceRunner(t, &coldProbe)
	coldRecords, _, coldCompleted, err := runPass(context.Background(), "prod-evidence-cold", 1, plan,
		stageRegistry{"code_writer": stages.CodeWriter{}}, provider, options, workDir, coldRunner, time.Time{}, nil, nil, nil, NewStageExecutionBudget(0))
	if err != nil || !coldCompleted {
		t.Fatalf("cold run: completed=%v err=%v records=%+v", coldCompleted, err, coldRecords)
	}
	if !prodEvidenceReadSeen(coldProbe.reads, prodEvidencePath) {
		t.Fatalf("cold control did not perform the required read; reads=%v", coldProbe.reads)
	}

	// Warm: admitted evidence drives the transformed context request. The body
	// read survives; the substituted symbol operation does not appear in the
	// executed decisions.
	var warmProbe prodEvidenceProbe
	warmRunner := prodEvidenceRunner(t, &warmProbe)
	tr := newRunTraceAccumulator(nil, "prod-evidence-warm", "session", workDir, plan, "active", nil)
	warmRecords, _, warmCompleted, err := runPass(context.Background(), "prod-evidence-warm", 1, plan,
		stageRegistry{"code_writer": stages.CodeWriter{}}, provider, options, workDir, warmRunner, time.Time{}, nil, mem, tr, NewStageExecutionBudget(0))
	if err != nil || !warmCompleted {
		detail := ""
		if len(warmRecords) > 0 && warmRecords[0].OutputSummary != nil {
			detail = *warmRecords[0].OutputSummary
		}
		t.Fatalf("warm run: completed=%v err=%v detail=%q records=%+v", warmCompleted, err, detail, warmRecords)
	}
	meta := tr.stages[stageKeyFor("code_writer", 1, 0)]
	if !prodEvidenceReadSeen(warmProbe.reads, prodEvidencePath) {
		t.Fatalf("body read must survive location evidence; reads=%v", warmProbe.reads)
	}
	if meta.OperationDecisions == nil {
		t.Fatalf("warm trace recorded no operation decisions: %+v", meta)
	}
	foundSatisfied := false
	for _, d := range *meta.OperationDecisions {
		if d.Disposition == schemas.OperationSatisfiedByEvidence && strings.Contains(d.Operation, prodEvidencePath) {
			foundSatisfied = true
		}
		if d.Disposition == schemas.OperationExecuted && strings.Contains(d.Operation, "symbol:") {
			t.Fatalf("warm executed a substituted symbol operation: %+v", d)
		}
	}
	if !foundSatisfied {
		t.Fatalf("warm trace has no satisfied-by-evidence decision for %s: %+v", prodEvidencePath, *meta.OperationDecisions)
	}
	if meta.OperationsExecuted == 0 {
		t.Fatalf("warm trace did not count the retained operations: %+v", meta)
	}
}

func prodEvidenceOpsContain(ops []ColdOperation, prefix string) bool {
	for _, op := range ops {
		if strings.Contains(op.Name, prefix) {
			return true
		}
	}
	return false
}

func prodEvidenceReadSeen(reads []string, path string) bool {
	for _, r := range reads {
		if r == path || strings.HasSuffix(r, "/"+path) {
			return true
		}
	}
	return false
}

// TestProductionEvidenceSubstitutionStaleRecordRetainsColdRead is the
// adversarial production pair: the graph node is fresh by anchor, but the
// typed record's byte digest is stale. Admission must reject it, the read
// operation must stay in the plan, and the trace must record the rejection.
func TestProductionEvidenceSubstitutionStaleRecordRetainsColdRead(t *testing.T) {
	workDir, rev, body := prodEvidenceRepo(t)
	rec := prodEvidenceRecord(t, workDir, rev, body)
	rec.Supporting[0].Digest = "0000000000000000000000000000000000000000000000000000000000000000"
	rec.ContentVersion = contentVersionDigest(*rec)
	mem := prodEvidenceStoreForRecord(t, workDir, rev, rec)

	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "Add RetentionDeficit in internal/audit/retention.go and reuse Apply.",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	options := PipelineConfigFromAgentOptions(agent.Options{})
	provider := &memoryScriptedProvider{}
	var probe prodEvidenceProbe
	runner := prodEvidenceRunner(t, &probe)
	tr := newRunTraceAccumulator(nil, "prod-evidence-stale", "session", workDir, plan, "active", nil)
	records, _, completed, err := runPass(context.Background(), "prod-evidence-stale", 1, plan,
		stageRegistry{"code_writer": stages.CodeWriter{}}, provider, options, workDir, runner, time.Time{}, nil, mem, tr, NewStageExecutionBudget(0))
	if err != nil || !completed {
		t.Fatalf("warm stale run: completed=%v err=%v records=%+v", completed, err, records)
	}
	if !prodEvidenceReadSeen(probe.reads, prodEvidencePath) {
		t.Fatalf("stale evidence wrongly omitted the required read; reads=%v", probe.reads)
	}
	meta := tr.stages[stageKeyFor("code_writer", 1, 0)]
	if meta.OperationsRetainedAfterReject == 0 {
		t.Fatalf("stale admission was not recorded as retained-after-reject: %+v", meta)
	}
}
