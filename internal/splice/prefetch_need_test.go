package splice

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// recordingEvaluator answers each noul with a fixed probability by
// question ID and keeps the last request.
type recordingEvaluator struct {
	p    map[string]float64
	last zeroruntime.DecisionRequest
}

func (e *recordingEvaluator) Evaluate(ctx context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
	e.last = req
	res := zeroruntime.DecisionResult{RequestID: "gen-need-1", ResolvedModel: "typesafe/jev-1.13", ReportedCostUSD: ptrFloat64(0.0001)}
	for _, q := range req.Questions {
		res.Answers = append(res.Answers, zeroruntime.DecisionAnswer{QuestionID: q.ID, Kind: zeroruntime.DecisionKindNoul, TrueProbability: e.p[q.ID]})
	}
	return res, nil
}

// TestPrefetchNeedJudgesEachFileAndKeepsTheNeededOnes: one noul per ranked
// file over the task and the file head; the scores come back in candidate
// order, and the active selection keeps the files at or above the
// threshold in probability order, or none.
func TestPrefetchNeedJudgesEachFileAndKeepsTheNeededOnes(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"internal/admin/labels.go", "internal/admin/pricing.go", "internal/audit/actions.go"} {
		writeTestFile(t, dir, f, "package x\n\n// "+f+"\nvar ActionLabels = map[string]string{}\n")
	}
	candidates := []SourceFetch{
		{Path: "internal/admin/pricing.go", Reason: "Context for the task \"add a plan\"", RecordRef: "fact:r1"},
		{Path: "internal/audit/actions.go", Reason: "Context for the task \"add an action\"", RecordRef: "fact:r2"},
		{Path: "internal/admin/labels.go", Reason: "Context for the task \"add an action\"", RecordRef: "fact:r2"},
	}
	eval := &recordingEvaluator{p: map[string]float64{"need_0": 0.1, "need_1": 0.7, "need_2": 0.9}}
	config := shadowConfig()
	config.QuestionPack = QuestionPackPrefetchNeedV1
	advisor, err := NewDecisionAdvisor(config, eval)
	if err != nil {
		t.Fatal(err)
	}
	p := stageInputPreparation{WorkDir: dir, Iteration: 1, NowUnix: 1, Options: PipelineRunConfig{Advisor: advisor, SessionID: "s"}}
	input := schemas.HarnessStageInput{StageName: "code_writer", RequestIntent: "In the audit package, add the audit action \"invoice.voided\"."}

	scores, ok := runPrefetchNeed(context.Background(), p, input, candidates)
	if !ok || len(scores) != 3 || scores[2].Path != "internal/admin/labels.go" || scores[2].P != 0.9 {
		t.Fatalf("scores = %v ok=%v", scores, ok)
	}
	if len(eval.last.Questions) != 3 || !strings.Contains(eval.last.Questions[2].Instructions, "candidates[2].Ref") {
		t.Fatalf("questions = %+v", eval.last.Questions)
	}
	if !strings.Contains(eval.last.State, "ActionLabels") {
		t.Fatal("the state does not carry the file head")
	}

	var kept []string
	for _, f := range selectByNeed(candidates, scores, 0.5) {
		kept = append(kept, f.Path)
	}
	if strings.Join(kept, ",") != "internal/admin/labels.go,internal/audit/actions.go" {
		t.Fatalf("kept = %v, want the two needed files, most needed first", kept)
	}
	if got := selectByNeed(candidates, scores, 0.95); len(got) != 0 {
		t.Fatalf("kept %v above every score; the selection may keep none", got)
	}
	if _, set := resolvePrefetchNeedThreshold(func(string) string { return "" }); set {
		t.Fatal("an unset threshold must take no action")
	}
}

// TestTopRecordOverlapSeparatesOwnKindFromOtherFeatures pins the P6 gate on
// the competing corpus's prompts: a record of the same kind of task shares
// 0.61 or more of the task's words, a record of another feature 0.50 or
// less. The 0.55 gate asks Jev only in the second case.
func TestTopRecordOverlapSeparatesOwnKindFromOtherFeatures(t *testing.T) {
	rec := func(task string) memd.GraphNode {
		return memd.GraphNode{Claim: fmt.Sprintf("%s%q: a verified run changed x.go.", workRecordClaimPrefix, task)}
	}
	roles := `In the auth package, add an "auditor" role (RoleAuditor) that users can hold. Auditors hold the permissions "read" and "export".`
	plans := `In the billing package, add a "startup" subscription plan: 900 cents a month, 3 seats. Customers see it between free and team.`
	rolesRead := `In the auth package, add a "billing_admin" role (RoleBillingAdmin) that users can hold. Billing admins hold the permissions "read" and "billing".`
	plansRead := `In the billing package, add an "enterprise" subscription plan: 49900 cents a month, 1000 seats. Customers see it after scale.`

	if got := topRecordOverlap(rolesRead, []memd.GraphNode{rec(plans), rec(roles)}); got < 0.55 {
		t.Errorf("roles read with the roles record: overlap %.2f, want >= 0.55", got)
	}
	if got := topRecordOverlap(plansRead, []memd.GraphNode{rec(roles)}); got >= 0.55 {
		t.Errorf("plans read with only the roles record: overlap %.2f, want < 0.55", got)
	}
	if got := topRecordOverlap(plans, nil); got != 0 {
		t.Errorf("no records: overlap %.2f, want 0", got)
	}
}
