package decisionreplay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// fixtureRecord hand-builds one judged boundary: the recorded (HEAD) history,
// and the pass-builder shadow for each entry. The history entries share the
// given state hashes, so the cycle rule stays quiet.
func fixtureRecord(run string, iteration int, history []schemas.IterationState, shadows []schemas.PassEvidence) schemas.DecisionDispositionRecord {
	return schemas.DecisionDispositionRecord{
		Disposition:    schemas.DispositionShadowOnly,
		Reason:         "fixture",
		EvidenceShadow: shadows,
		Snapshot: schemas.SemanticSnapshot{
			RunID:              run,
			Stage:              "code_writer",
			Iteration:          iteration,
			SnapshotSchema:     schemas.SnapshotSchemaV1,
			QuestionPack:       splice.QuestionPackTrajActionV1,
			HostPolicy:         "trajectory_actions_v1",
			CanonicalProjectID: "fixture",
			SourceDigest:       "fixture-digest",
			DistilledIntent:    "Fix the test.",
			UnresolvedRelation: "the next trajectory action",
			CandidateRefs:      []schemas.CandidateRef{{Ref: "boundary", Origin: "trajectory boundary", Claim: "Select the next action."}},
			Trajectory: &schemas.TrajectorySnapshot{Boundary: schemas.TrajectoryBoundary{
				RunID: run, ProjectID: "fixture", Intent: "Fix the test.", Iteration: iteration, History: history,
				Rules: schemas.TrajectoryDecision{Action: schemas.ActionContinue, Reason: "recorded"},
			}, Scores: []float64{}, HashRepeats: map[string]int{}},
		},
	}
}

// stateAt is a judged history entry: iteration n, HEAD files, HEAD-builder progress flag.
func stateAt(n int, files []string, progress bool) schemas.IterationState {
	return schemas.IterationState{Iteration: n, Timestamp: float64(n), StateHash: "h" + string(rune('a'+n)), Confidence: 0.9, TestsPassing: n, FilesChanged: files, PassChanged: &progress}
}

// shadowAt is the pass builder's view of one entry.
func shadowAt(n int, files ...string) schemas.PassEvidence {
	changed := len(files) > 0
	return schemas.PassEvidence{Iteration: n, Builder: "pass", FilesChanged: files, PassChanged: &changed}
}

// stallHistory is four judged entries whose HEAD lists are the same and whose
// recorded progress flag says the pass changed. The pass shadows decide what
// the swap sees.
func stallHistory(headFiles []string) []schemas.IterationState {
	var out []schemas.IterationState
	for n := 1; n <= 4; n++ {
		// Failed-boundary entries carry no recorded PassChanged, so the HEAD
		// list alone decides the recorded progress.
		state := stateAt(n, headFiles, true)
		state.PassChanged = nil
		out = append(out, state)
	}
	return out
}

// promptEvaluator answers a step_back when the request mentions the marker,
// and continue otherwise. It stands in for a live classifier.
func promptEvaluator(marker string) evaluatorFunc {
	return func(_ context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
		raw, err := json.Marshal(req)
		if err != nil {
			return zeroruntime.DecisionResult{}, err
		}
		label := "continue"
		if marker != "" && strings.Contains(string(raw), marker) {
			label = "step_back"
		}
		return choice(req, label), nil
	}
}

func compareFixture(t *testing.T, records []schemas.DecisionDispositionRecord, marker string) Comparison {
	t.Helper()
	comparison, err := Compare(context.Background(), records, promptEvaluator(marker), Options{BackendConfig: replayConfig()})
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	return comparison
}

// TestCompareClassifiesEachDisagreement builds one boundary per class. The
// rules disagreements come from the swap making no_progress fire, which gives
// preexisting_dirt and stale_progress. A classifier disagreement whose shadow
// file fits no class gives unexplained.
func TestCompareClassifiesEachDisagreement(t *testing.T) {
	stale := fixtureRecord("stale", 4, stallHistory([]string{"a.go"}), []schemas.PassEvidence{
		shadowAt(1, "a.go"), shadowAt(2), shadowAt(3), shadowAt(4),
	})
	dirt := fixtureRecord("dirt", 4, stallHistory([]string{"dirty.txt"}), []schemas.PassEvidence{
		shadowAt(1), shadowAt(2), shadowAt(3), shadowAt(4),
	})
	unexplained := fixtureRecord("unexplained", 1, []schemas.IterationState{stateAt(1, []string{"a.go"}, true)}, []schemas.PassEvidence{shadowAt(1, "a.go", "pass_only.go")})
	comparison := compareFixture(t, []schemas.DecisionDispositionRecord{stale, dirt, unexplained}, "pass_only.go")

	want := map[string]string{"stale": ClassStaleProgress, "dirt": ClassPreexistingDirt, "unexplained": ClassUnexplained}
	got := map[string]string{}
	for _, d := range comparison.Disagreements {
		if d.Recorded == d.Swapped {
			t.Fatalf("disagreement %+v has equal actions", d)
		}
		if d.Kind == "rules" {
			got[d.RunID] = d.Class
		} else if d.RunID == "unexplained" {
			got[d.RunID] = d.Class
		}
	}
	for run, class := range want {
		if got[run] != class {
			t.Fatalf("run %s class = %q, want %q (all: %+v)", run, got[run], class, comparison.Disagreements)
		}
	}
	if comparison.RulesDisagree != 2 || comparison.ClassifierDisagree != 1 {
		t.Fatalf("rules disagree=%d classifier disagree=%d, want 2 and 1", comparison.RulesDisagree, comparison.ClassifierDisagree)
	}
	if comparison.Classes[ClassStaleProgress] != 1 || comparison.Classes[ClassPreexistingDirt] != 1 || comparison.Classes[ClassUnexplained] != 1 {
		t.Fatalf("classes = %v", comparison.Classes)
	}
}

// TestCompareAgreesWhenEvidenceAgrees checks that identical evidence gives no
// disagreement.
func TestCompareAgreesWhenEvidenceAgrees(t *testing.T) {
	agree := fixtureRecord("agree", 1, []schemas.IterationState{stateAt(1, []string{"a.go"}, true)}, []schemas.PassEvidence{shadowAt(1, "a.go")})
	comparison := compareFixture(t, []schemas.DecisionDispositionRecord{agree}, "")
	if comparison.Boundaries != 1 || comparison.RulesAgree != 1 || comparison.RulesDisagree != 0 || len(comparison.Disagreements) != 0 {
		t.Fatalf("comparison = %+v", comparison)
	}
}

// TestCompareClassifierTopLabelDisagreement changes only the request. The pass
// shadow adds a file the recorded evidence lacks, and the fake classifier
// answers on that file.
func TestCompareClassifierTopLabelDisagreement(t *testing.T) {
	rec := fixtureRecord("classifier", 1, []schemas.IterationState{stateAt(1, []string{"a.go"}, true)}, []schemas.PassEvidence{shadowAt(1, "a.go", "pass_only.go")})
	comparison := compareFixture(t, []schemas.DecisionDispositionRecord{rec}, "pass_only.go")
	if comparison.ClassifierDisagree != 1 || comparison.ClassifierAgree != 0 {
		t.Fatalf("classifier agree=%d disagree=%d, want one disagreement", comparison.ClassifierAgree, comparison.ClassifierDisagree)
	}
	if d := comparison.Disagreements; len(d) != 1 || d[0].Kind != "classifier" || d[0].Recorded != "continue" || d[0].Swapped != "step_back" || d[0].Class != ClassUnexplained {
		t.Fatalf("disagreements = %+v, want one unexplained classifier disagreement", d)
	}
}

// TestCompareListsSkippedRecords covers the two skip kinds: a record with no
// evidence shadow, and a projection failure. The recorded run still answers
// the record without a shadow, and the swap run skips it.
func TestCompareListsSkippedRecords(t *testing.T) {
	noShadow := fixtureRecord("noshadow", 2, []schemas.IterationState{stateAt(1, []string{"a.go"}, true), stateAt(2, []string{"a.go"}, true)}, nil)
	projection := schemas.DecisionDispositionRecord{
		Disposition: schemas.DispositionProviderFailure,
		Reason:      "trajectory boundary projection: code_writer_output",
	}
	records := []schemas.DecisionDispositionRecord{noShadow, projection}
	comparison := compareFixture(t, records, "")
	if comparison.Skipped != 2 || len(comparison.SkippedItems) != 2 {
		t.Fatalf("skipped = %d items = %+v, want two", comparison.Skipped, comparison.SkippedItems)
	}
	reasons := []string{comparison.SkippedItems[0].Reason, comparison.SkippedItems[1].Reason}
	if !slices.Contains(reasons, "no evidence shadow") || !slices.ContainsFunc(reasons, func(s string) bool { return strings.HasPrefix(s, "projection failure") }) {
		t.Fatalf("skip reasons = %q", reasons)
	}
	for _, item := range comparison.SkippedItems {
		if item.RunID == "noshadow" && (item.Iteration != 2 || item.Repair != 0) {
			t.Fatalf("skip for noshadow lists iteration %d repair %d, want 2/0", item.Iteration, item.Repair)
		}
	}
	if comparison.Boundaries != 0 {
		t.Fatalf("boundaries = %d, want none compared (the swap run skipped the only boundary)", comparison.Boundaries)
	}
}

// TestSwapReplaysPassValuesAndNeverSilentlyUsesRecorded checks the swap
// evidence in the row itself, and that a mismatched shadow is skipped rather
// than replayed with recorded values.
func TestSwapReplaysPassValuesAndNeverSilentlyUsesRecorded(t *testing.T) {
	good := fixtureRecord("good", 4, stallHistory([]string{"a.go"}), []schemas.PassEvidence{
		shadowAt(1, "a.go"), shadowAt(2), shadowAt(3), shadowAt(4),
	})
	short := fixtureRecord("short", 4, stallHistory([]string{"a.go"}), []schemas.PassEvidence{shadowAt(1, "a.go")})
	var out bytes.Buffer
	summary, err := Replay(context.Background(), []schemas.DecisionDispositionRecord{good, short}, promptEvaluator(""), Options{BackendConfig: replayConfig(), Evidence: EvidenceSwap}, &out)
	if err != nil {
		t.Fatal(err)
	}
	rows := readRows(t, &out)
	if len(rows) != 1 || rows[0].RunID != "good" || rows[0].Evidence != EvidenceSwap {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].RulesAction != schemas.ActionStepBack {
		t.Fatalf("swap rules action = %q, want step_back from the pass values", rows[0].RulesAction)
	}
	if summary.Skipped != 1 || summary.SkippedItems[0].RunID != "short" || !strings.Contains(summary.SkippedItems[0].Reason, "entries for a 4-entry history") {
		t.Fatalf("summary = %+v", summary)
	}

	if _, err := Replay(context.Background(), nil, promptEvaluator(""), Options{BackendConfig: replayConfig(), Evidence: "bogus"}, &out); err == nil {
		t.Fatal("an unknown evidence source must fail loudly")
	}
}

func TestWriteTableNamesEachClass(t *testing.T) {
	stale := fixtureRecord("stale", 4, stallHistory([]string{"a.go"}), []schemas.PassEvidence{
		shadowAt(1, "a.go"), shadowAt(2), shadowAt(3), shadowAt(4),
	})
	comparison := compareFixture(t, []schemas.DecisionDispositionRecord{stale}, "")
	var table bytes.Buffer
	if err := WriteTable(&table, comparison); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"class preexisting_dirt", "class stale_progress", "class unexplained", "stale"} {
		if !strings.Contains(table.String(), want) {
			t.Fatalf("table lacks %q:\n%s", want, table.String())
		}
	}
}

// TestDegradedShadowIsSkippedNotSwapped: a pass builder that degraded has no
// measured change. The swap skips it with the reason, and counts it. The clean
// record in another run is still compared, and the run count shows it.
func TestDegradedShadowIsSkippedNotSwapped(t *testing.T) {
	degraded := fixtureRecord("degraded", 1, []schemas.IterationState{stateAt(1, []string{"a.go"}, true)}, []schemas.PassEvidence{
		{Iteration: 1, Builder: "pass", DegradedReason: "git status failed"},
	})
	clean := fixtureRecord("clean", 1, []schemas.IterationState{stateAt(1, []string{"a.go"}, true)}, []schemas.PassEvidence{shadowAt(1, "a.go")})
	comparison := compareFixture(t, []schemas.DecisionDispositionRecord{degraded, clean}, "")
	if comparison.DegradedShadows != 1 || comparison.Skipped != 1 {
		t.Fatalf("degraded=%d skipped=%d, want one each", comparison.DegradedShadows, comparison.Skipped)
	}
	if got := comparison.SkippedItems[0].Reason; !strings.Contains(got, "pass builder degraded") || !strings.Contains(got, "git status failed") {
		t.Fatalf("skip reason = %q", got)
	}
	if comparison.Boundaries != 1 || comparison.Runs != 1 {
		t.Fatalf("boundaries=%d runs=%d, want the clean record only", comparison.Boundaries, comparison.Runs)
	}
}

// TestExplainDisagreementConsidersOnlyDifferingEntries checks each branch of
// the class rules on hand-built entries.
func TestExplainDisagreementConsidersOnlyDifferingEntries(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name    string
		history []schemas.IterationState
		shadows []schemas.PassEvidence
		want    string
	}{
		{
			// Entry 1 holds dirt, but both sources show progress there, so the
			// entry agrees and is ignored. Entry 2 differs: the pass shows no
			// change while the record lists a.go, which is stale.
			name: "agreeing entry ignored",
			history: []schemas.IterationState{
				{Iteration: 1, FilesChanged: []string{"dirty.txt"}},
				{Iteration: 2, FilesChanged: []string{"a.go"}},
			},
			shadows: []schemas.PassEvidence{
				{Iteration: 1, Builder: "pass", FilesChanged: []string{"x.go"}},
				{Iteration: 2, Builder: "pass", PassChanged: &no},
			},
			want: ClassStaleProgress,
		},
		{
			name: "swap shows progress the record lacks",
			history: []schemas.IterationState{
				{Iteration: 1},
			},
			shadows: []schemas.PassEvidence{{Iteration: 1, Builder: "pass", FilesChanged: []string{"a.go"}}},
			want:    ClassUnexplained,
		},
		{
			name: "recorded PassChanged set on a differing entry",
			history: []schemas.IterationState{
				{Iteration: 1, FilesChanged: []string{"a.go"}, PassChanged: &yes},
			},
			shadows: []schemas.PassEvidence{{Iteration: 1, Builder: "pass"}},
			want:    ClassUnexplained,
		},
		{
			name: "dirt in iteration 1 and in no pass list",
			history: []schemas.IterationState{
				{Iteration: 1, FilesChanged: []string{"dirty.txt"}},
				{Iteration: 2, FilesChanged: []string{"dirty.txt", "a.go"}},
			},
			shadows: []schemas.PassEvidence{
				{Iteration: 1, Builder: "pass"},
				{Iteration: 2, Builder: "pass", FilesChanged: []string{"a.go"}},
			},
			want: ClassPreexistingDirt,
		},
		{
			name: "pass shadow not from the pass builder",
			history: []schemas.IterationState{
				{Iteration: 1, FilesChanged: []string{"a.go"}},
			},
			shadows: []schemas.PassEvidence{{Iteration: 1, Builder: "head"}},
			want:    ClassUnexplained,
		},
		{
			name: "nothing differs in any entry",
			history: []schemas.IterationState{
				{Iteration: 1, FilesChanged: []string{"a.go"}, PassChanged: &no},
			},
			shadows: []schemas.PassEvidence{{Iteration: 1, Builder: "pass", FilesChanged: []string{"a.go"}}},
			want:    ClassUnexplained,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := explainDisagreement(tc.history, tc.shadows); got != tc.want {
				t.Fatalf("explainDisagreement = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSwapRequestCarriesRebuiltRules: the classifier input must carry the rules
// rebuilt from the replayed History, not the recorded decision. The recorded
// fixture says "recorded", and the swapped history stalls, so its rules say so.
func TestSwapRequestCarriesRebuiltRules(t *testing.T) {
	record := fixtureRecord("rules", 4, stallHistory([]string{"dirty.txt"}), []schemas.PassEvidence{
		shadowAt(1), shadowAt(2), shadowAt(3), shadowAt(4),
	})
	requestFor := func(mode string) string {
		var seen string
		evaluator := evaluatorFunc(func(_ context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
			raw, err := json.Marshal(req)
			if err != nil {
				return zeroruntime.DecisionResult{}, err
			}
			seen = string(raw)
			return choice(req, "continue"), nil
		})
		if _, err := Replay(context.Background(), []schemas.DecisionDispositionRecord{record}, evaluator, Options{BackendConfig: replayConfig(), Evidence: mode}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		return seen
	}
	recorded := requestFor(EvidenceRecorded)
	swapped := requestFor(EvidenceSwap)
	if strings.Contains(swapped, `"reason":"recorded"`) {
		t.Fatal("the swap request still carries the recorded rules")
	}
	if !strings.Contains(swapped, "no workspace change") {
		t.Fatalf("the swap request lacks the rules rebuilt from the swapped history")
	}
	if strings.Contains(recorded, "no workspace change") {
		t.Fatal("the recorded request should not see a stall in its rebuilt rules")
	}
}

// TestCompareSelfAgreementMeasuresBackendNoise replays the recorded evidence
// twice. A deterministic backend gives S of one. A backend that alternates its
// answer gives S of zero.
func TestCompareSelfAgreementMeasuresBackendNoise(t *testing.T) {
	rec := fixtureRecord("self", 1, []schemas.IterationState{stateAt(1, []string{"a.go"}, true)}, []schemas.PassEvidence{shadowAt(1, "a.go")})
	records := []schemas.DecisionDispositionRecord{rec}
	options := Options{BackendConfig: replayConfig(), Concurrency: 1}

	steady, err := Compare(context.Background(), records, promptEvaluator(""), options)
	if err != nil {
		t.Fatal(err)
	}
	if steady.SelfCompared != 1 || steady.SelfAgreement != 1 {
		t.Fatalf("steady self = %d/%d (%.2f), want one boundary at 1", steady.SelfAgree, steady.SelfCompared, steady.SelfAgreement)
	}

	calls := 0
	flipping := evaluatorFunc(func(_ context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
		calls++
		label := "continue"
		if calls%2 == 1 {
			label = "step_back"
		}
		return choice(req, label), nil
	})
	noisy, err := Compare(context.Background(), records, flipping, options)
	if err != nil {
		t.Fatal(err)
	}
	if noisy.SelfCompared != 1 || noisy.SelfAgree != 0 {
		t.Fatalf("flipping self = %d/%d, want 0 of 1", noisy.SelfAgree, noisy.SelfCompared)
	}
}

// TestSwapSelectsTheRecordedModeRecord: a boundary with two records replays
// the first one in both modes. When the first record has no shadow, the swap
// skips the boundary. It does not replay the second record instead.
func TestSwapSelectsTheRecordedModeRecord(t *testing.T) {
	first := fixtureRecord("dup", 1, []schemas.IterationState{stateAt(1, []string{"a.go"}, true)}, nil)
	second := fixtureRecord("dup", 1, []schemas.IterationState{stateAt(1, []string{"b.go"}, true)}, []schemas.PassEvidence{shadowAt(1, "b.go")})
	rows, summary, err := ReplayRows(context.Background(), []schemas.DecisionDispositionRecord{first, second}, promptEvaluator(""), Options{BackendConfig: replayConfig(), Evidence: EvidenceSwap})
	if err != nil {
		t.Fatalf("ReplayRows: %v", err)
	}
	if len(rows) != 0 || summary.Skipped != 2 {
		t.Fatalf("swap rows=%+v skipped=%d, want the boundary skipped, not replayed from the second record", rows, summary.Skipped)
	}
	if got := summary.SkippedItems; got[0].Reason != "no evidence shadow" || !strings.HasPrefix(got[1].Reason, "later record of a boundary") {
		t.Fatalf("skip reasons = %+v", got)
	}
}

// TestCompareAccountsForEveryRecord reproduces the p2h smoke read attempt: the
// run verified at iteration 1, so it had no boundary, and its records file held
// only stage-input selector records (dependency_relation_v1, no
// evidence_shadow key). The compare must list each such record as skipped. A
// boundary whose replay fails is skipped with the error, not dropped.
func TestCompareAccountsForEveryRecord(t *testing.T) {
	selector := func(stage string, invocation int) schemas.DecisionDispositionRecord {
		return schemas.DecisionDispositionRecord{
			Disposition: schemas.DispositionShadowOnly,
			Reason:      "bought and recorded; shadow never acts",
			Snapshot: schemas.SemanticSnapshot{
				RunID: "read", Stage: stage, StageInvocation: invocation, Iteration: 1,
				SnapshotSchema: schemas.SnapshotSchemaV1, QuestionPack: schemas.QuestionPackDependencyRelationV1,
				HostPolicy:    schemas.HostPolicyBoundedPrefetchV1,
				CandidateRefs: []schemas.CandidateRef{{Ref: "cmd/server/startup.go", Origin: "graph:fact", Claim: "fact"}},
			},
		}
	}
	compared := fixtureRecord("write", 1, []schemas.IterationState{stateAt(1, []string{"a.go"}, true)}, []schemas.PassEvidence{shadowAt(1, "a.go")})
	failing := fixtureRecord("write", 2, []schemas.IterationState{stateAt(1, []string{"a.go"}, true), stateAt(2, []string{"fail_me.go"}, true)}, []schemas.PassEvidence{shadowAt(1, "a.go"), shadowAt(2, "fail_me.go")})
	records := []schemas.DecisionDispositionRecord{compared, failing, selector("code_writer", 0), selector("test_generator", 0), selector("code_writer", 1)}
	evaluator := evaluatorFunc(func(ctx context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
		raw, _ := json.Marshal(req)
		if strings.Contains(string(raw), "fail_me.go") {
			return zeroruntime.DecisionResult{}, errors.New("transport down")
		}
		return choice(req, "continue"), nil
	})
	comparison, err := Compare(context.Background(), records, evaluator, Options{BackendConfig: replayConfig()})
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if comparison.Records != 5 || comparison.Boundaries != 1 || comparison.Skipped != 4 || len(comparison.SkippedItems) != 4 {
		t.Fatalf("records=%d boundaries=%d skipped=%d items=%+v, want 5 = 1 + 4", comparison.Records, comparison.Boundaries, comparison.Skipped, comparison.SkippedItems)
	}
	notBoundary, failed := 0, 0
	for _, item := range comparison.SkippedItems {
		switch {
		case strings.HasPrefix(item.Reason, "not a boundary: dependency_relation_v1"):
			notBoundary++
		case item.RunID == "write" && item.Iteration == 2 && strings.Contains(item.Reason, "transport down"):
			failed++
		}
	}
	if notBoundary != 3 || failed != 1 {
		t.Fatalf("skip reasons = %+v, want 3 not-a-boundary and 1 replay failure", comparison.SkippedItems)
	}
	var table bytes.Buffer
	if err := WriteTable(&table, comparison); err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(table.String(), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 {
			rows[fields[0]] = fields[1]
		}
	}
	if rows["records"] != "5" || rows["skipped"] != "4" {
		t.Fatalf("table does not account for every record:\n%s", table.String())
	}
}

// TestLoadTraceEvidenceRefusesBadTraces: a JSON array file, and a record with
// no run_id or schema_version, are errors that name the file. A trace with no
// pass-builder record loads, and the table warns about it.
func TestLoadTraceEvidenceRefusesBadTraces(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, tc := range []struct{ name, body, want string }{
		{"array.json", `[{"schema_version":"1","run_id":"r1"}]`, "holds a JSON array"},
		{"norun.jsonl", "{\"schema_version\":\"1\",\"run_id\":\"r1\"}\n{\"schema_version\":\"1\"}\n", "record 2: no run_id"},
		{"noschema.json", `{"run_id":"r1"}`, "record 1 (run r1): no schema_version"},
	} {
		path := write(tc.name, tc.body)
		if _, err := LoadTraceEvidence([]string{path}); err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: err = %v, want %q with the path", tc.name, err, tc.want)
		}
	}
	path := write("empty.json", `{"schema_version":"1","run_id":"quiet"}`)
	ev, err := LoadTraceEvidence([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Runs != 1 || ev.Passes != 0 || !slices.Equal(ev.NoPasses, []string{"quiet"}) {
		t.Fatalf("evidence = %+v, want one run with no passes", ev)
	}
	var table bytes.Buffer
	if err := WriteTable(&table, Comparison{Classes: map[string]int{}, Traces: &ev}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(table.String(), "warning: trace quiet has 0 passes") {
		t.Fatalf("table lacks the zero-pass warning:\n%s", table.String())
	}
}
