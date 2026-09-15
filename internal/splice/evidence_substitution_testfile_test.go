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

// The evidence-vouched test-source admission is the fix for the retention
// corpus: the shared symbol lives in a *_test.go file the production index
// excludes, so the locate need was never derived and no substitution could
// fire. These tests pin both sides: a vouched test symbol substitutes, and
// an unvouched or decoy test symbol stays rejected.

const (
	testSymbolPath   = "clock_test.go"
	testSymbolName   = "runClockTable"
	testFileBody     = "package main\n\nimport \"testing\"\n\nfunc runClockTable(t *testing.T) {}\n"
	prodSiblingBody  = "package main\n\nfunc NewClockStore() {}\n"
	readTaskIntent   = "Add TestStoreExpiryBoundary that reuses the existing runClockTable helper."
	vouchedReadIdent = readTaskIntent
)

func testSymbolRepo(t *testing.T, extraTestFiles map[string]string) (string, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	body := []byte(testFileBody)
	if err := os.WriteFile(filepath.Join(dir, testSymbolPath), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "clock.go"), []byte(prodSiblingBody), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range extraTestFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitInit(t, dir)
	return dir, gitHead(t, dir), body
}

func testSymbolRecord(t *testing.T, workspace, revision string, body []byte) *ReuseRecord {
	t.Helper()
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	subject := testSymbolPath + "#" + testSymbolName
	rec := &ReuseRecord{
		SchemaVersion:      ReuseRecordSchemaVersion,
		Kind:               "fact",
		Identity:           recordIdentity(workspace, "fact", subject),
		AnsweredNeed:       NeedLocateNamedOperation + ":" + subject,
		Applicability:      "location of " + testSymbolName + " in " + testSymbolPath + " at the verified revision",
		Conclusion:         "located " + testSymbolName + " in " + testSymbolPath,
		Supporting:         []SourceRef{{Path: testSymbolPath, Symbol: testSymbolName, Digest: digest}},
		ProducerRun:        "run-A",
		CaptureOrigin:      CaptureOriginRuntime,
		WorktreeIdentity:   revision,
		VerificationStatus: VerificationStatusPassed,
		Status:             "active",
	}
	rec.ContentVersion = contentVersionDigest(*rec)
	return rec
}

func testSymbolStoreWithAnchors(t *testing.T, workspace, revision string, rec *ReuseRecord, anchors []memd.GraphAnchor) MemoryStore {
	t.Helper()
	_, client := newFakeSidecar(t)
	if _, err := client.UpsertGraphNode(context.Background(), memd.GraphUpsertInput{
		Kind:             "fact",
		Claim:            testSymbolPath + " defines " + testSymbolName,
		Scope:            "project",
		ProjectPath:      workspace,
		Status:           "active",
		SourceRunID:      "run-A",
		VerifiedRevision: revision,
		Anchors:          anchors,
		Metadata:         recordToMetadata(rec),
	}); err != nil {
		t.Fatalf("upsert test-symbol evidence node: %v", err)
	}
	return NewGraphMemoryStore(client)
}

func testSymbolVouchedStore(t *testing.T, workspace, revision string, body []byte) MemoryStore {
	t.Helper()
	rec := testSymbolRecord(t, workspace, revision, body)
	return testSymbolStoreWithAnchors(t, workspace, revision, rec, []memd.GraphAnchor{
		{Kind: "file", Value: testSymbolPath},
		{Kind: "symbol", Value: testSymbolPath + "#" + testSymbolName},
	})
}

// TestEvidenceVouchedTestSymbolMintsNeedAndSubstitutes is the production
// chain: the read task names the identifier, the write's verified record
// vouches for the test file, and the substitution fires through the real
// pass loop with the cold control unchanged.
func TestEvidenceVouchedTestSymbolMintsNeedAndSubstitutes(t *testing.T) {
	t.Setenv(EvidenceSubstitutionEnvVar, "on")
	workDir, rev, body := testSymbolRepo(t, nil)
	mem := testSymbolVouchedStore(t, workDir, rev, body)
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: vouchedReadIdent,
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	options := PipelineConfigFromAgentOptions(agent.Options{})

	// Plan inspection: the evidence-vouched need exists, the cold plan
	// carries the symbol operation it replaces, and the warm plan omits it.
	prepared, planScope, _, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input: schemas.HarnessStageInput{
			RunID: "testfile-evidence-plan", StageName: "code_writer", Sequence: 1,
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
		t.Fatal("no evidence plan built")
	}
	wantSubject := testSymbolPath + "#" + testSymbolName
	foundNeed := false
	for _, n := range planScope.Evidence.Needs {
		if n.Subject == wantSubject {
			foundNeed = true
			if n.Origin != NeedOriginEvidenceVouchedTest {
				t.Fatalf("test-symbol need origin = %q, want %q", n.Origin, NeedOriginEvidenceVouchedTest)
			}
		}
	}
	if !foundNeed {
		t.Fatalf("no evidence-vouched locate need for %s in %+v", wantSubject, planScope.Evidence.Needs)
	}
	if !prodEvidenceOpsContain(planScope.Evidence.Cold.Operations, "symbol:"+wantSubject) {
		t.Fatalf("cold plan lost the vouched test-symbol operation: %+v", planScope.Evidence.Cold.Operations)
	}
	if prodEvidenceOpsContain(planScope.Evidence.Warm.Operations, "symbol:"+wantSubject) {
		t.Fatalf("warm plan retained the substituted test-symbol operation: %+v", planScope.Evidence.Warm.Operations)
	}
	if planScope.Evidence.SubstitutionCount() == 0 {
		t.Fatalf("no substitution recorded: %+v", planScope.Evidence.Diff)
	}

	// Cold control: the same read task without evidence keeps the historical
	// behavior. The cold plan must not contain the test-symbol operation,
	// because cold cannot confirm it.
	coldNeeds := DeriveContextNeeds(vouchedReadIdent, workDir, nil, nil)
	for _, n := range coldNeeds {
		if n.Kind == NeedLocateNamedOperation && strings.Contains(n.Subject, testSymbolName) {
			t.Fatalf("cold derivation minted a need for the unvouched test symbol: %+v", coldNeeds)
		}
	}
	coldPlan := buildColdPlan(vouchedReadIdent, workDir, nil, 8, nil)
	if prodEvidenceOpsContain(coldPlan.Operations, "symbol:"+wantSubject) {
		t.Fatalf("cold plan without evidence grew a test-symbol operation: %+v", coldPlan.Operations)
	}

	// Warm execution: the substitution fires through the real pass loop.
	var warmProbe prodEvidenceProbe
	warmRunner := prodEvidenceRunner(t, &warmProbe)
	tr := newRunTraceAccumulator(nil, "testfile-evidence-warm", "session", workDir, plan, "active", nil)
	warmRecords, _, warmCompleted, err := runPass(context.Background(), "testfile-evidence-warm", 1, plan,
		stageRegistry{"code_writer": stages.CodeWriter{}}, &memoryScriptedProvider{}, options, workDir, warmRunner, time.Time{}, nil, mem, tr, NewStageExecutionBudget(0))
	if err != nil || !warmCompleted {
		detail := ""
		if len(warmRecords) > 0 && warmRecords[0].OutputSummary != nil {
			detail = *warmRecords[0].OutputSummary
		}
		t.Fatalf("warm run: completed=%v err=%v detail=%q records=%+v", warmCompleted, err, detail, warmRecords)
	}
	meta := tr.stages[stageKeyFor("code_writer", 1, 0)]
	foundSatisfied := false
	for _, d := range *meta.OperationDecisions {
		if d.Disposition == schemas.OperationSatisfiedByEvidence && strings.Contains(d.Operation, wantSubject) {
			foundSatisfied = true
		}
	}
	if !foundSatisfied {
		t.Fatalf("warm trace has no satisfied-by-evidence decision for %s: %+v", wantSubject, *meta.OperationDecisions)
	}
}

// TestUnvouchedTestSymbolStaysUnconfirmed is the trap guard: the identifier
// exists only in a test file and nothing vouches for it, so it must not
// mint a need and the cold plan must not grow the operation.
func TestUnvouchedTestSymbolStaysUnconfirmed(t *testing.T) {
	workDir, _, _ := testSymbolRepo(t, nil)
	needs := DeriveContextNeeds(vouchedReadIdent, workDir, nil, nil)
	for _, n := range needs {
		if n.Kind == NeedLocateNamedOperation && strings.Contains(n.Subject, testSymbolName) {
			t.Fatalf("unvouched test symbol minted a need: %+v", needs)
		}
	}
	cold := buildColdPlan(vouchedReadIdent, workDir, nil, 8, nil)
	if prodEvidenceOpsContain(cold.Operations, testSymbolName) {
		t.Fatalf("unvouched test symbol grew the cold plan: %+v", cold.Operations)
	}
}

// TestDecoyTestFileCannotConfirm is the adversarial case the original
// exclusion guarded: two test files declare the identifier and evidence
// vouches only one. The decoy must not turn the confirmation ambiguous or
// resolve the need to the wrong file.
func TestDecoyTestFileCannotConfirm(t *testing.T) {
	decoys := map[string]string{
		"decoy_test.go": "package main\n\nimport \"testing\"\n\nfunc runClockTable(t *testing.T) {}\n",
	}
	workDir, rev, body := testSymbolRepo(t, decoys)
	// The record vouches only clock_test.go, not the decoy.
	rec := testSymbolRecord(t, workDir, rev, body)
	_ = rec
	vouched := vouchedFilesFromNodes([]memd.GraphNode{{
		Kind:    "fact",
		Anchors: []memd.GraphAnchor{{Kind: "file", Value: testSymbolPath}},
	}})
	needs := deriveContextNeeds(vouchedReadIdent, workDir, nil, nil, vouched)
	for _, n := range needs {
		if n.Kind == NeedLocateNamedOperation && strings.Contains(n.Subject, testSymbolName) {
			t.Fatalf("partially vouched decoy confirmed the identifier: %+v", needs)
		}
	}
	cold := buildColdPlan(vouchedReadIdent, workDir, nil, 8, vouched)
	if prodEvidenceOpsContain(cold.Operations, testSymbolName) {
		t.Fatalf("partially vouched decoy grew the cold plan: %+v", cold.Operations)
	}
}

// TestVouchedProductionSymbolStillColdResolved is the no-double-credit
// guard: an identifier the production index confirms keeps its cold-resolved
// origin even when evidence also vouches for its file, so warm can never
// claim credit for a lookup cold resolves anyway.
func TestVouchedProductionSymbolStillColdResolved(t *testing.T) {
	workDir, _, _ := prodEvidenceRepo(t)
	vouched := []string{prodEvidencePath}
	needs := deriveContextNeeds("reuse Apply from "+prodEvidencePath, workDir, nil, nil, vouched)
	for _, n := range needs {
		if n.Subject == prodEvidencePath+"#Apply" && n.Origin != NeedOriginSymbolIndex {
			t.Fatalf("production symbol origin = %q, want %q", n.Origin, NeedOriginSymbolIndex)
		}
	}
}
