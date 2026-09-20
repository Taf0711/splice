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

// TestSharedResolverMintsTestSymbolNeedForColdAndWarm is the production
// chain for the shared resolver: the read task names the identifier, the
// shared source index confirms it in the test source for EVERY arm (cold
// derivation included), and the cold plan carries the symbol operation the
// handshake resolves. The need is cold-resolved, so warm claims no credit:
// no substitution fires for it, and the handshake delivers the symbol to
// both arms without memory.
func TestSharedResolverMintsTestSymbolNeedForColdAndWarm(t *testing.T) {
	t.Setenv(EvidenceSubstitutionEnvVar, "on")
	workDir, rev, body := testSymbolRepo(t, nil)
	mem := testSymbolVouchedStore(t, workDir, rev, body)
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: vouchedReadIdent,
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	options := PipelineConfigFromAgentOptions(agent.Options{})

	// Plan inspection: the need exists with the cold-resolved origin, and
	// the cold plan carries the symbol operation. No substitution fires.
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
			if n.Origin != NeedOriginSymbolIndex {
				t.Fatalf("test-symbol need origin = %q, want %q (shared resolver, cold-resolved)", n.Origin, NeedOriginSymbolIndex)
			}
		}
	}
	if !foundNeed {
		t.Fatalf("no shared-resolver locate need for %s in %+v", wantSubject, planScope.Evidence.Needs)
	}
	if !prodEvidenceOpsContain(planScope.Evidence.Cold.Operations, "symbol:"+wantSubject) {
		t.Fatalf("cold plan lost the shared-resolver test-symbol operation: %+v", planScope.Evidence.Cold.Operations)
	}
	// The retained record substitutes the located operation and delivers
	// its view, so the warm request omits the lookup.
	if planScope.Evidence.SubstitutionCount() == 0 {
		t.Fatalf("no substitution recorded for the matched record: %+v", planScope.Evidence.Diff)
	}
	if prodEvidenceOpsContain(planScope.Evidence.Warm.Operations, "symbol:"+wantSubject) {
		t.Fatalf("warm plan retained the substituted test-symbol operation: %+v", planScope.Evidence.Warm.Operations)
	}

	// Cold derivation alone (no memory store) mints the same need.
	coldNeeds := DeriveContextNeeds(vouchedReadIdent, workDir, nil, nil)
	foundCold := false
	for _, n := range coldNeeds {
		if n.Kind == NeedLocateNamedOperation && n.Subject == wantSubject {
			foundCold = true
			if n.Origin != NeedOriginSymbolIndex {
				t.Fatalf("cold-derived origin = %q, want %q", n.Origin, NeedOriginSymbolIndex)
			}
		}
	}
	if !foundCold {
		t.Fatalf("cold derivation missed the test-source symbol: %+v", coldNeeds)
	}

	// The warm plan still executes, and the handshake delivers the symbol
	// operation in both arms: the plan retains it.
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

// TestDecoyTestFileKeepsTheNeedAmbiguous is the adversarial case the
// original exclusion guarded: two test files declare the identifier. The
// shared resolver must keep the need ambiguous (unresolvable to one file)
// instead of silently resolving to the decoy.
func TestDecoyTestFileKeepsTheNeedAmbiguous(t *testing.T) {
	decoys := map[string]string{
		"decoy_test.go": "package main\n\nimport \"testing\"\n\nfunc runClockTable(t *testing.T) {}\n",
	}
	workDir, _, _ := testSymbolRepo(t, decoys)
	needs := deriveContextNeeds(vouchedReadIdent, workDir, nil, nil)
	foundAmbiguous := false
	for _, n := range needs {
		if n.Kind == NeedLocateNamedOperation && strings.Contains(n.Subject, testSymbolName+" (declared in ") {
			foundAmbiguous = true
		}
		if strings.HasSuffix(n.Subject, "#"+testSymbolName) {
			t.Fatalf("decoy resolved to a single file: %+v", needs)
		}
	}
	if !foundAmbiguous {
		t.Fatalf("ambiguous same-named declarations did not stay ambiguous: %+v", needs)
	}
}

// TestVouchedProductionSymbolStillColdResolved is the no-double-credit
// guard: an identifier the shared index confirms keeps its cold-resolved
// origin whichever source role declares it, so warm can never claim credit
// for a lookup cold resolves anyway.
func TestSharedResolverProductionSymbolStillColdResolved(t *testing.T) {
	workDir, _, _ := prodEvidenceRepo(t)
	needs := deriveContextNeeds("reuse Apply from "+prodEvidencePath, workDir, nil, nil)
	for _, n := range needs {
		if n.Subject == prodEvidencePath+"#Apply" && n.Origin != NeedOriginSymbolIndex {
			t.Fatalf("production symbol origin = %q, want %q", n.Origin, NeedOriginSymbolIndex)
		}
	}
}
