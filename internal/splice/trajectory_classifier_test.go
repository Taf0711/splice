package splice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type trajectoryEvaluator struct {
	label string
	bad   bool
	err   error
	calls int
	last  zeroruntime.DecisionRequest
}

func (e *trajectoryEvaluator) Evaluate(_ context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
	e.calls++
	e.last = req
	if e.err != nil {
		return zeroruntime.DecisionResult{}, e.err
	}
	p := map[string]float64{e.label: 1}
	if e.bad {
		p = map[string]float64{"step_back": 0.1}
	}
	return zeroruntime.DecisionResult{ResolvedModel: req.Model, Answers: []zeroruntime.DecisionAnswer{{QuestionID: "trajectory_action", Kind: zeroruntime.DecisionKindChoice, Probabilities: p}}}, nil
}
func classifierAdvisor(t *testing.T, eval zeroruntime.DecisionEvaluator) *DecisionAdvisor {
	t.Helper()
	cfg := shadowConfig()
	cfg.Mode = AdvisorModeActiveExperimental
	cfg.QuestionPack = QuestionPackTrajActionV1
	cfg.Backend = "http"
	advisor, err := NewDecisionAdvisor(cfg, eval)
	if err != nil {
		t.Fatal(err)
	}
	return advisor
}
func fabricatedBoundary() TrajectoryBoundary {
	return TrajectoryBoundary{RunID: "run", ProjectID: "project", Intent: "Fix the test.", Iteration: 1, Rules: schemas.TrajectoryDecision{Action: schemas.ActionContinue, Reason: "rules continue"}, History: []schemas.IterationState{{TestsFailing: 1, StateHash: "a", FilesChanged: []string{"main.go"}}}, Latest: &TrajectoryStep{Change: "Fix the value.", Failure: "wrong value"}}
}
func distribution(label TrajectoryLabel, p float64) TrajectoryClassification {
	probabilities := map[TrajectoryLabel]float64{label: p}
	if p < 1 {
		probabilities[LabelContinue] = 1 - p
	}
	return TrajectoryClassification{Probabilities: probabilities, Backend: "http"}
}
func TestTrajectoryHostGuards(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		label                             TrajectoryLabel
		probability                       float64
		used, isolated, target, escalated bool
		streak                            int
		want                              schemas.TrajectoryAction
	}{
		{name: "step_back threshold", label: LabelStepBack, probability: .59, want: schemas.ActionContinue},
		{name: "step_back accepted", label: LabelStepBack, probability: .6, want: schemas.ActionStepBack},
		{name: "step_back once", label: LabelStepBack, probability: 1, used: true, want: schemas.ActionContinue},
		{name: "second stuck stops", label: LabelStepBack, probability: 1, used: true, streak: 1, want: schemas.ActionSurfaceToUser},
		{name: "stop before replan", label: LabelStop, probability: 1, want: schemas.ActionContinue},
		{name: "stop first boundary", label: LabelStop, probability: 1, used: true, want: schemas.ActionContinue},
		{name: "stop consecutive", label: LabelStop, probability: .8, used: true, streak: 1, want: schemas.ActionSurfaceToUser},
		{name: "stop below threshold", label: LabelStop, probability: .79, used: true, streak: 1, want: schemas.ActionContinue},
		{name: "rollback target absent", label: LabelRollback, probability: 1, isolated: true, want: schemas.ActionContinue},
		{name: "rollback shared directory", label: LabelRollback, probability: 1, target: true, want: schemas.ActionContinue},
		{name: "rollback allowed", label: LabelRollback, probability: .7, isolated: true, target: true, want: schemas.ActionRollback},
		{name: "escalate once", label: LabelEscalate, probability: 1, escalated: true, want: schemas.ActionContinue},
		{name: "escalate allowed", label: LabelEscalate, probability: .7, want: schemas.ActionEscalateCycleDetected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &hostTrajectoryPolicy{stepBackUsed: tc.used, isolated: tc.isolated, escalated: tc.escalated, stopStreak: tc.streak}
			b := fabricatedBoundary()
			b.RollbackTarget = tc.target
			got := p.apply(b, distribution(tc.label, tc.probability))
			if got.Action != tc.want {
				t.Fatalf("decision=%+v", got)
			}
		})
	}
	p := &hostTrajectoryPolicy{stepBackUsed: true, stopStreak: 1}
	p.apply(fabricatedBoundary(), distribution(LabelContinue, 1))
	if p.stopStreak != 0 {
		t.Fatal("continue must reset the stop streak")
	}
	if d := p.apply(fabricatedBoundary(), distribution(LabelStop, 1)); d.Action != schemas.ActionContinue {
		t.Fatal("non-consecutive stop")
	}
	p = &hostTrajectoryPolicy{thresholds: map[string]float64{"step_back": .95}}
	if p.apply(fabricatedBoundary(), distribution(LabelStepBack, .9)).Action != schemas.ActionContinue {
		t.Fatal("custom threshold ignored")
	}
}
func TestTrajectoryHardLimitsBeatClassifier(t *testing.T) {
	for _, kind := range []string{"iteration", "token"} {
		t.Run(kind, func(t *testing.T) {
			eval := &trajectoryEvaluator{label: "stop"}
			advisor := classifierAdvisor(t, eval)
			p := newHostTrajectoryPolicy(advisor, config.TrajectoryConfig{}, false)
			p.maxIterations = 5
			p.tokenBudget = 1000
			b := fabricatedBoundary()
			want := schemas.ActionAbortHardLimit
			if kind == "iteration" {
				b.History = make([]schemas.IterationState, 5)
			} else {
				b.History[0].TokensGenerated = 100000
				want = schemas.ActionAbortBudget
			}
			d, _ := p.decide(context.Background(), b)
			if d.Action != want || eval.calls != 0 {
				t.Fatalf("decision=%+v calls=%d", d, eval.calls)
			}
		})
	}
}
func TestTrajectoryShadowAndRepairDeferral(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "shadow"}[shadow], func(t *testing.T) {
			eval := &trajectoryEvaluator{label: "step_back"}
			advisor := classifierAdvisor(t, eval)
			if shadow {
				advisor.config.Mode = AdvisorModeShadow
			}
			p := newHostTrajectoryPolicy(advisor, config.TrajectoryConfig{}, false)
			b := fabricatedBoundary()
			p.observeFailure(b)
			b.Repair = 1
			d, _ := p.decide(context.Background(), b)
			if shadow {
				if d.Action != b.Rules.Action || p.pending != nil || !strings.Contains(advisor.Records()[0].Reason, "classifier decision=step_back") {
					t.Fatalf("shadow acted: %+v", d)
				}
			} else {
				if d.Action != schemas.ActionStepBack || p.pending == nil {
					t.Fatalf("repair decision=%+v", d)
				}
				b.Repair = 0
				d, _ = p.decide(context.Background(), b)
				if d.Action != schemas.ActionStepBack || eval.calls != 1 {
					t.Fatalf("pending pass decision=%+v calls=%d", d, eval.calls)
				}
			}
		})
	}
}
func TestTrajectoryEveryFallbackRecordsCause(t *testing.T) {
	for _, cause := range []string{"missing evaluator", "budget", "eligibility", "transport", "invalid", "uncalibrated", "cache policy", "unknown pack"} {
		t.Run(cause, func(t *testing.T) {
			eval := &trajectoryEvaluator{label: "continue"}
			advisor := classifierAdvisor(t, eval)
			b := fabricatedBoundary()
			b.Rules = schemas.TrajectoryDecision{Action: schemas.ActionRollback, Reason: "rules rollback", Evidence: []string{"test evidence"}}
			want := ""
			switch cause {
			case "missing evaluator":
				advisor.evaluator = nil
				want = "no decision evaluator"
			case "budget":
				// The classifier's own budget is spent; the selector cap is not used.
				advisor.trajectoryRequests = trajectoryRequestBudget
				want = "request cap"
			case "eligibility":
				advisor.config.Limits.MaxRequestBytes = 1
				want = "bound"
			case "transport":
				eval.err = errors.New("test transport failure")
				want = "transport failure"
			case "invalid":
				eval.bad = true
				want = "invalid trajectory answer"
			case "uncalibrated":
				advisor.config.Mode = AdvisorModeActive
				want = "calibrated"
			case "cache policy":
				want = "cache hit"
				seed := advisorClassifier{advisor: advisor, pack: actionTrajectoryPack{}, budget: trajectoryRequestBudget}
				if _, err := seed.Classify(context.Background(), (&hostTrajectoryPolicy{}).observeFailure(b)); err != nil {
					t.Fatal(err)
				}
			case "unknown pack":
				advisor.config.QuestionPack = "unknown"
				want = "unknown question pack"
			}
			p := newHostTrajectoryPolicy(advisor, config.TrajectoryConfig{}, false)
			d, acted := p.decide(context.Background(), b)
			if !reflect.DeepEqual(d, b.Rules) || acted {
				t.Fatalf("fallback changed rules: %+v", d)
			}
			records := advisor.Records()
			if len(records) == 0 || !strings.Contains(records[len(records)-1].Reason, want) {
				t.Fatalf("missing cause %q: %+v", want, records)
			}
		})
	}
}
func TestTrajectoryPacks(t *testing.T) {
	req := zeroruntime.DecisionRequest{Model: "m", State: "{}", Questions: actionTrajectoryPack{}.Questions()}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(req.Questions) != 1 || len(req.Questions[0].Criteria) != 5 {
		t.Fatal("action pack contract")
	}
	for _, same := range []float64{0, .2, .9, 1} {
		for _, fresh := range []float64{0, .1, .7, 1} {
			assessment := schemas.DecisionAssessment{Answers: []schemas.DecisionAnswerRecord{{QuestionID: "same_cause", Probabilities: map[string]float64{"true": same}}, {QuestionID: "new_information", Probabilities: map[string]float64{"true": fresh}}}}
			p, err := (noulTrajectoryPack{}).Labels(assessment)
			if err != nil || math.Abs(p[LabelStepBack]-same*(1-fresh)) > 1e-12 || math.Abs(p[LabelContinue]+p[LabelStepBack]-1) > 1e-12 {
				t.Fatalf("mapping=%v error=%v", p, err)
			}
			if len(p) != 5 || p[LabelRollback] != 0 || p[LabelEscalate] != 0 || p[LabelStop] != 0 {
				t.Fatal("complete distribution required")
			}
		}
	}
	if _, err := (noulTrajectoryPack{}).Labels(schemas.DecisionAssessment{}); err == nil {
		t.Fatal("missing nouls accepted")
	}
	if _, err := (actionTrajectoryPack{}).Labels(schemas.DecisionAssessment{Answers: []schemas.DecisionAnswerRecord{{QuestionID: "trajectory_action", Probabilities: map[string]float64{"stop": .1}}}}); err == nil {
		t.Fatal("invalid sum accepted")
	}
}
func TestTrajectoryLabelsPairWithRunActions(t *testing.T) {
	source, err := os.ReadFile("run.go")
	if err != nil {
		t.Fatal(err)
	}
	names := map[schemas.TrajectoryAction]string{schemas.ActionContinue: "ActionContinue", schemas.ActionStepBack: "ActionStepBack", schemas.ActionRollback: "ActionRollback", schemas.ActionEscalateCycleDetected: "ActionEscalateCycleDetected", schemas.ActionSurfaceToUser: "ActionSurfaceToUser"}
	if len(trajectoryLabels) != len(trajectoryActions) || len(trajectoryActions) != 5 {
		t.Fatal("label/action drift")
	}
	for _, label := range trajectoryLabels {
		action, ok := trajectoryActions[label]
		name := names[action]
		if !ok || name == "" || !strings.Contains(string(source), "decision.Action == schemas."+name) {
			t.Fatalf("unhandled label %q action %q", label, action)
		}
	}
}
func TestTrajectoryBoundaryStateIsBoundedAndComplete(t *testing.T) {
	eval := &trajectoryEvaluator{label: "continue"}
	advisor := classifierAdvisor(t, eval)
	b := fabricatedBoundary()
	b.Previous = &TrajectoryStep{Change: "previous", Failure: strings.Repeat("p", 2000)}
	b.Latest.Failure = strings.Repeat("f", 2000)
	b.History = append(b.History, b.History[0])
	b.StepBackUsed = true
	b.RollbackTarget = true
	_, err := (advisorClassifier{advisor: advisor, pack: actionTrajectoryPack{}, budget: trajectoryRequestBudget}).Classify(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"scores", "hash_repeats", "tests_failing", "files_changed", "Rules", "StepBackUsed", "RollbackTarget"} {
		if !strings.Contains(eval.last.State, field) {
			t.Errorf("missing %s", field)
		}
	}
	if strings.Contains(eval.last.State, strings.Repeat("f", trajFailureChars+1)) || len(eval.last.State) > advisor.config.Limits.MaxRequestBytes {
		t.Fatal("failure text or state exceeds bound")
	}
	if len(b.Latest.Failure) != 2000 {
		t.Fatal("projection changed caller state")
	}
}
func TestRulesClassifierMatchesJudgmentTable(t *testing.T) {
	for _, history := range [][]schemas.IterationState{nil, {{StateHash: "a"}}, {{StateHash: "a"}, {StateHash: "a"}}, {{StateHash: "a"}, {StateHash: "b"}, {StateHash: "c"}}} {
		c, err := (rulesClassifier{}).Classify(context.Background(), TrajectoryBoundary{History: history})
		if err != nil {
			t.Fatal(err)
		}
		d := EvaluateTrajectory(history, 100, nil)
		label := LabelContinue
		switch d.Action {
		case schemas.ActionStepBack:
			label = LabelStepBack
		case schemas.ActionEscalateCycleDetected, schemas.ActionEscalateOscillation:
			label = LabelEscalate
		case schemas.ActionRollback:
			label = LabelRollback
		case schemas.ActionSurfaceToUser, schemas.ActionAbortNoProgress:
			label = LabelStop
		}
		if c.Backend != "rules" || c.Probabilities[label] != 1 {
			t.Fatalf("classification=%+v rules=%+v", c, d)
		}
	}
}

func TestTrajectoryClassifierRedactsBoundaryCredentials(t *testing.T) {
	eval := &trajectoryEvaluator{label: "continue"}
	advisor := classifierAdvisor(t, eval)
	b := fabricatedBoundary()
	b.Latest.Failure = "Authorization: Bearer secret-token-value\napi_key=another-secret"
	p := newHostTrajectoryPolicy(advisor, config.TrajectoryConfig{}, false)
	p.decide(context.Background(), b)
	if eval.calls != 1 || len(advisor.Records()[0].Snapshot.Trajectory.Boundary.FailureHistory) != 1 {
		t.Fatal("credential fixture did not reach the classifier with failure history")
	}
	if strings.Contains(eval.last.State, "secret-token-value") || strings.Contains(eval.last.State, "another-secret") {
		t.Fatal("credentials in boundary state")
	}
}
func TestTrajectoryHardLimitCountsFailedPasses(t *testing.T) {
	eval := &trajectoryEvaluator{label: "continue"}
	advisor := classifierAdvisor(t, eval)
	p := newHostTrajectoryPolicy(advisor, config.TrajectoryConfig{}, false)
	p.maxIterations = 5
	p.tokenBudget = 1000
	b := fabricatedBoundary()
	b.Iteration = 5
	d, _ := p.decide(context.Background(), b)
	if d.Action != schemas.ActionAbortHardLimit || eval.calls != 0 || d.IterationCount != 5 {
		t.Fatalf("decision=%+v calls=%d", d, eval.calls)
	}
}

func TestTrajectoryReplayRequestMatchesLive(t *testing.T) {
	for _, pack := range []trajectoryPack{actionTrajectoryPack{}, progressTrajectoryPack{}, noulTrajectoryPack{}} {
		t.Run(pack.Name(), func(t *testing.T) {
			var evaluator zeroruntime.DecisionEvaluator
			actionEval := &trajectoryEvaluator{label: "continue"}
			noulEval := &recordingEvaluator{p: map[string]float64{"same_cause": .9, "new_information": .1}}
			if pack.Name() == QuestionPackTrajV1 {
				evaluator = noulEval
			} else {
				evaluator = actionEval
			}
			advisor := classifierAdvisor(t, evaluator)
			advisor.config.Mode = AdvisorModeShadow
			boundary := fabricatedBoundary()
			boundary.IsolatedWorktree = true
			boundary.Previous = &TrajectoryStep{Change: "previous", Failure: "failure"}
			if _, err := (advisorClassifier{advisor: advisor, pack: pack, budget: trajectoryRequestBudget}).Classify(context.Background(), boundary); err != nil {
				t.Fatal(err)
			}
			snap := advisor.Records()[0].Snapshot
			req, err := TrajectoryDecisionRequest(snap, "", advisor.config.Profile, advisor.config.Limits.MaxRequestBytes)
			if err != nil {
				t.Fatal(err)
			}
			live := actionEval.last
			if pack.Name() == QuestionPackTrajV1 {
				live = noulEval.last
			}
			if !reflect.DeepEqual(req, live) {
				t.Fatalf("replay request differs: %+v != %+v", req, live)
			}
			restored, err := TrajectorySnapshotBoundary(snap)
			if err != nil || restored.Iteration != boundary.Iteration || restored.Previous.Change != boundary.Previous.Change || !restored.IsolatedWorktree {
				t.Fatalf("boundary=%+v error=%v", restored, err)
			}
		})
	}
}

func TestTrajectoryFailureSignature(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"numbers", "Expected 42 got -3.14 on line 99", "expected # got -# on line #"},
		{"hex", "Address 0xDEADBEEF and 0x12ab", "address # and #"},
		{"paths", "Failed /tmp/a-b/main.go and src/foo.py", "failed <path> and src<path>"},
		{"whitespace", "  FAIL\n\tWrong   VALUE\r\n", "fail wrong value"},
		{"replacement order", "/tmp/run12/main.go:123", "<path>#<path>:#"},
		{"unicode", "Ошибка /путь/文件.py ١٢", "ошибка <path> #"},
		{"empty", "", ""},
		{"cap", strings.Repeat("É", 301), strings.Repeat("é", 300)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := trajectoryFailureSignature(tc.input); got != tc.want {
				t.Fatalf("signature=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestTrajectoryFailureHistory(t *testing.T) {
	p := &hostTrajectoryPolicy{}
	failures := []string{"alpha 1", "alpha 2", "beta", "alpha 3", "beta", "gamma", "gamma", "alpha", "beta", "delta", "alpha 7"}
	wantNew := []bool{true, false, true, false, false, true, false, true, false, true, false}
	wantSince := []int{0, 1, 0, 1, 2, 0, 1, 0, 1, 0, 1}
	wantID := []int{0, 0, 1, 0, 1, 2, 2, 3, 1, 4, 0}
	for i, failure := range failures {
		b := fabricatedBoundary()
		b.Iteration, b.Repair = i+1, i%2
		b.Latest.Failure = failure
		// Recorded features must not control the run-local history.
		b.DistinctFailures = 100
		b.FailureHistory = []schemas.TrajectoryFailure{{IsNew: false}}
		b = p.observeFailure(b)
		latest := b.FailureHistory[len(b.FailureHistory)-1]
		if latest.IsNew != wantNew[i] || latest.SignatureID != wantID[i] || b.BoundariesSinceNewFailure != wantSince[i] ||
			latest.Iteration != i+1 || latest.Repair != i%2 || latest.Kind != "test_failure" ||
			len(b.FailureHistory) != min(i+1, 8) || b.DistinctFailures != len(p.failureSignatures) {
			t.Fatalf("boundary %d: %+v", i, b)
		}
		if err := b.Validate(); err != nil {
			t.Fatal(err)
		}
		b.FailureHistory[0].Signature = "mutated"
		if p.failureHistory[0].Signature == "mutated" {
			t.Fatal("boundary shares the policy history")
		}
	}
	if len(p.failureHistory) != 8 || p.failureHistory[0].Iteration != 4 || len(p.failureSignatures) != 5 {
		t.Fatalf("bounded history lost run counts: %+v", p)
	}
	for _, tc := range []struct{ failure, kind string }{
		{"code_writer failed: splice.stageRunError: context expansion budget exhausted", "stage_error"},
		{"splice.stageRunError: tool failed", "stage_error"},
		{"tests failed", "test_failure"},
		{"", "none"},
	} {
		b := fabricatedBoundary()
		b.Latest.Failure = tc.failure
		b = p.observeFailure(b)
		if got := b.FailureHistory[len(b.FailureHistory)-1].Kind; got != tc.kind {
			t.Fatalf("kind=%s want=%s", got, tc.kind)
		}
	}
	b := fabricatedBoundary()
	b.Latest = nil
	b = p.observeFailure(b)
	if last := b.FailureHistory[len(b.FailureHistory)-1]; last.Kind != "none" || last.IsNew {
		t.Fatalf("nil failure must repeat the empty signature: %+v", last)
	}
}

func TestTrajectoryFailureKindUsesTypedEvidence(t *testing.T) {
	p := &hostTrajectoryPolicy{}
	b := fabricatedBoundary()
	// The text looks like a test failure, but the stage record says it was an
	// infrastructure error. The typed kind must win.
	b.Latest = &TrajectoryStep{
		Change:      "call the provider",
		Failure:     "tests failed",
		FailureKind: schemas.NodeFailureStageError,
	}
	b = p.observeFailure(b)
	if got := b.FailureHistory[len(b.FailureHistory)-1].Kind; got != schemas.NodeFailureStageError {
		t.Fatalf("kind = %q, want %q", got, schemas.NodeFailureStageError)
	}
}

func TestTrajectoryFailureHistoryRetainsRunCounts(t *testing.T) {
	p := &hostTrajectoryPolicy{}
	b := fabricatedBoundary()
	for _, failure := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta", "iota", "kappa"} {
		b.Latest.Failure = failure
		b = p.observeFailure(b)
	}
	for i := 1; i <= 12; i++ {
		b.Latest.Failure = "alpha"
		b = p.observeFailure(b)
		last := b.FailureHistory[len(b.FailureHistory)-1]
		if last.IsNew || last.SignatureID != 0 || b.DistinctFailures != 10 || b.BoundariesSinceNewFailure != i || len(b.FailureHistory) != 8 {
			t.Fatalf("boundary %d lost run counts: %+v", i, b)
		}
	}
}

func TestTrajectoryFailureHistoryProjection(t *testing.T) {
	b := fabricatedBoundary()
	b.Latest.Failure = strings.Repeat("É", 301)
	b = (&hostTrajectoryPolicy{}).observeFailure(b)
	snap, err := TrajectorySemanticSnapshot(b, QuestionPackTrajActionV2)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Trajectory.Boundary.FailureHistory[0].Signature; got != strings.Repeat("é", 300) {
		t.Fatal("projection changed the normalized signature")
	}
	snap.Trajectory.Boundary.FailureHistory[0].Signature = "mutated"
	if b.FailureHistory[0].Signature != strings.Repeat("é", 300) {
		t.Fatal("projection changed the caller history")
	}
}

func TestTrajectoryFailureHistoryValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    TrajectoryBoundary
	}{
		{"negative since", TrajectoryBoundary{BoundariesSinceNewFailure: -1}},
		{"negative distinct", TrajectoryBoundary{DistinctFailures: -1}},
		{"oversize history", TrajectoryBoundary{FailureHistory: make([]schemas.TrajectoryFailure, 9)}},
		{"unknown kind", TrajectoryBoundary{FailureHistory: []schemas.TrajectoryFailure{{Kind: "invalid"}}}},
		{"negative iteration", TrajectoryBoundary{FailureHistory: []schemas.TrajectoryFailure{{Kind: "none", Iteration: -1}}}},
		{"negative repair", TrajectoryBoundary{FailureHistory: []schemas.TrajectoryFailure{{Kind: "none", Repair: -1}}}},
		{"negative signature id", TrajectoryBoundary{FailureHistory: []schemas.TrajectoryFailure{{Kind: "none", SignatureID: -1}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.b.Validate(); err == nil {
				t.Fatal("invalid failure history accepted")
			}
		})
	}
}

func TestTrajectoryNewFailureGuard(t *testing.T) {
	for _, guard := range []bool{true, false} {
		for _, backend := range []string{"http", "provider", "openrouter_decisions", "rules"} {
			for _, label := range []TrajectoryLabel{LabelStepBack, LabelStop} {
				for _, fresh := range []bool{true, false} {
					t.Run(fmt.Sprintf("guard=%t/%s/%s/new=%t", guard, backend, label, fresh), func(t *testing.T) {
						p := &hostTrajectoryPolicy{progressGuard: guard}
						b := fabricatedBoundary()
						if !fresh {
							p.observeFailure(b)
						}
						b = p.observeFailure(b)
						if label == LabelStop {
							p.stepBackUsed, p.stopStreak = true, 1
						}
						c := distribution(label, 1)
						c.Backend = backend
						d := p.apply(b, c)
						want := trajectoryActions[label]
						if guard && fresh && backend != "rules" {
							want = schemas.ActionContinue
							if d.Reason != "the latest failure is new to the run: the run is making progress" || p.stopStreak != 0 {
								t.Fatalf("new failure guard: %+v streak=%d", d, p.stopStreak)
							}
							if label == LabelStepBack && p.stepBackUsed {
								t.Fatal("blocked step-back consumed the allowance")
							}
						}
						if d.Action != want {
							t.Fatalf("decision=%+v want=%s", d, want)
						}
					})
				}
			}
		}
	}
}

func TestTrajectoryNewFailureShadowAndFallback(t *testing.T) {
	for _, mode := range []AdvisorMode{AdvisorModeActiveExperimental, AdvisorModeShadow} {
		eval := &trajectoryEvaluator{label: "step_back"}
		advisor := classifierAdvisor(t, eval)
		advisor.config.Mode = mode
		advisor.config.QuestionPack = QuestionPackTrajActionV2
		p := newHostTrajectoryPolicy(advisor, config.TrajectoryConfig{}, false)
		b := fabricatedBoundary()
		d, acted := p.decide(context.Background(), b)
		if d.Action != schemas.ActionContinue || acted != (mode != AdvisorModeShadow) || p.stepBackUsed {
			t.Fatalf("mode=%s decision=%+v acted=%t", mode, d, acted)
		}
		var state schemas.TrajectorySnapshot
		if err := json.Unmarshal([]byte(eval.last.State), &state); err != nil {
			t.Fatal(err)
		}
		if len(state.Boundary.FailureHistory) != 1 || !state.Boundary.FailureHistory[0].IsNew || state.Boundary.DistinctFailures != 1 {
			t.Fatalf("classifier did not see history: %+v", state.Boundary)
		}
		if mode == AdvisorModeShadow && !strings.Contains(advisor.Records()[0].Reason, "classifier decision=continue") {
			t.Fatal("shadow omitted the guarded decision")
		}
		b.Iteration++
		d, acted = p.decide(context.Background(), b)
		want := schemas.ActionStepBack
		if mode == AdvisorModeShadow {
			want = schemas.ActionContinue
		}
		if d.Action != want || acted != (mode != AdvisorModeShadow) {
			t.Fatalf("repeated failure mode=%s decision=%+v acted=%t", mode, d, acted)
		}
	}
	for _, action := range []schemas.TrajectoryAction{schemas.ActionStepBack, schemas.ActionSurfaceToUser} {
		advisor := classifierAdvisor(t, &trajectoryEvaluator{err: errors.New("transport failed")})
		p := newHostTrajectoryPolicy(advisor, config.TrajectoryConfig{}, false)
		b := fabricatedBoundary()
		b.Rules.Action = action
		d, acted := p.decide(context.Background(), b)
		if !reflect.DeepEqual(d, b.Rules) || acted {
			t.Fatalf("new failure changed rules fallback: %+v", d)
		}
	}
}

func TestTrajectoryProgressPackAndDefault(t *testing.T) {
	pack, err := trajectoryPackByName(QuestionPackTrajActionV2)
	if err != nil || pack.Name() != QuestionPackTrajActionV2 {
		t.Fatalf("pack=%v error=%v", pack, err)
	}
	req := zeroruntime.DecisionRequest{Model: "m", State: "{}", Questions: pack.Questions()}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(req.Questions) != 1 || len(req.Questions[0].Criteria) != len(trajectoryLabels) {
		t.Fatal("v2 label contract")
	}
	for _, field := range []string{"failure_history", "boundaries_since_new_failure", "distinct_failures", "is_new", "signature_id", "Latest.Failure", "StepBackUsed", "RollbackTarget", "Escalated"} {
		if !strings.Contains(req.Questions[0].Instructions, field) {
			t.Errorf("instructions omit %s", field)
		}
	}
	probs, err := pack.Labels(schemas.DecisionAssessment{Answers: []schemas.DecisionAnswerRecord{{QuestionID: "trajectory_action", Probabilities: map[string]float64{"continue": 1}}}})
	if err != nil || probs[LabelContinue] != 1 || len(probs) != 5 {
		t.Fatalf("v2 probabilities=%v error=%v", probs, err)
	}
	t.Setenv(TrajectoryPackEnvVar, "")
	for _, name := range []string{"", schemas.QuestionPackDependencyRelationV1, QuestionPackTrajActionV1, QuestionPackTrajActionV2, QuestionPackTrajV1} {
		advisor := classifierAdvisor(t, &trajectoryEvaluator{label: "continue"})
		advisor.config.QuestionPack = name
		p := newHostTrajectoryPolicy(advisor, config.TrajectoryConfig{}, false)
		want := name
		if name == "" || name == schemas.QuestionPackDependencyRelationV1 {
			want = QuestionPackTrajActionV2
		}
		if got := p.classifier.(advisorClassifier).pack.Name(); got != want {
			t.Fatalf("config=%q pack=%q want=%q", name, got, want)
		}
		if wantGuard := want == QuestionPackTrajActionV2; p.progressGuard != wantGuard {
			t.Fatalf("config=%q progressGuard=%t want=%t", name, p.progressGuard, wantGuard)
		}
	}
}

// TestTrajectoryFlagOffBytesGolden pins the classifier input and the rule
// reason text for fixed histories. P2 pass evidence must leave both
// byte-identical while its flag is off. Regenerate only for an intended change
// to the snapshot schema or the rule text (SPLICE_UPDATE_GOLDEN=1).
func TestTrajectoryFlagOffBytesGolden(t *testing.T) {
	yes, no := true, false
	state := func(iteration int, passChanged *bool, files []string, added, removed int, hash string) schemas.IterationState {
		return schemas.IterationState{
			Iteration:       iteration,
			Timestamp:       1700000000 + float64(iteration),
			TestsFailing:    1,
			StateHash:       hash,
			FilesChanged:    files,
			LinesAdded:      added,
			LinesRemoved:    removed,
			PassChanged:     passChanged,
			CodeSizeBytes:   100 * iteration,
			TokensGenerated: 1000 * iteration,
		}
	}
	histories := map[string][]schemas.IterationState{
		"stall_after_edit": {
			state(1, &yes, []string{"a.go"}, 4, 1, "h1"),
			state(2, &no, []string{"a.go"}, 0, 0, "h2"),
			state(3, &no, []string{"a.go"}, 0, 0, "h3"),
			state(4, &no, []string{"a.go"}, 0, 0, "h4"),
		},
		"head_fallback_no_flag": {
			state(1, nil, []string{"a.go"}, 4, 1, "h1"),
			state(2, nil, nil, 0, 0, "h2"),
			state(3, nil, nil, 0, 0, "h3"),
			state(4, nil, nil, 0, 0, "h4"),
		},
		"progress": {
			state(1, &yes, []string{"a.go"}, 4, 1, "h1"),
			state(2, &yes, []string{"a.go", "b.go"}, 9, 2, "h2"),
		},
	}

	var out strings.Builder
	for _, name := range []string{"stall_after_edit", "head_fallback_no_flag", "progress"} {
		history := histories[name]
		rules := evaluateTrajectoryJudgments(history, nil)
		latest := TrajectoryStep{Change: "wrote a.go", Failure: "1 test failing"}
		prev := TrajectoryStep{Change: "wrote a.go", Failure: "1 test failing"}
		boundary := TrajectoryBoundary{
			RunID:       "golden-run",
			ProjectID:   "/golden/project",
			Intent:      "fix the failing test",
			WriterStage: "code_writer",
			Iteration:   len(history),
			History:     history,
			Previous:    &prev,
			Latest:      &latest,
			Rules:       rules,
			// A non-empty shadow: the json tag must keep it out of the snapshot.
			EvidenceShadow: []schemas.PassEvidence{{Iteration: 1, Builder: "pass", FilesChanged: []string{"a.go"}, LinesAdded: 4}},
		}
		for _, pack := range []string{QuestionPackTrajActionV2, QuestionPackTrajV1} {
			snap, err := TrajectorySemanticSnapshot(boundary, pack)
			if err != nil {
				t.Fatalf("%s/%s: snapshot: %v", name, pack, err)
			}
			raw, err := json.MarshalIndent(snap, "", "  ")
			if err != nil {
				t.Fatalf("%s/%s: marshal: %v", name, pack, err)
			}
			fmt.Fprintf(&out, "== snapshot %s %s\n%s\n", name, pack, raw)
		}
		raw, err := json.MarshalIndent(rules, "", "  ")
		if err != nil {
			t.Fatalf("%s: marshal rules: %v", name, err)
		}
		fmt.Fprintf(&out, "== rules %s\n%s\n", name, raw)
	}

	path := "testdata/trajectory_flag_off_bytes.golden"
	if os.Getenv("SPLICE_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("create testdata dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (create it with SPLICE_UPDATE_GOLDEN=1)", path, err)
	}
	if string(want) != out.String() {
		t.Fatalf("golden %s mismatch: flag-off classifier input or rule text changed", path)
	}
}

// P2-3: a live boundary with a typed stage_error keeps that kind through the
// snapshot and the replay, also when its text looks like a test failure.
func TestTrajectoryReplayKeepsStageErrorKind(t *testing.T) {
	for _, pack := range []string{QuestionPackTrajActionV1, QuestionPackTrajV1} {
		t.Run(pack, func(t *testing.T) {
			live := fabricatedBoundary()
			live.Previous = &TrajectoryStep{Change: "previous", Failure: "FAIL TestOne"}
			live.Latest = &TrajectoryStep{Change: "Fix the value.", Failure: "FAIL TestOne: wrong value", FailureKind: schemas.NodeFailureStageError}
			live = (&hostTrajectoryPolicy{}).observeFailure(live)
			if got := live.FailureHistory[len(live.FailureHistory)-1].Kind; got != schemas.NodeFailureStageError {
				t.Fatalf("live kind = %q, want stage_error", got)
			}
			snap, err := TrajectorySemanticSnapshot(live, pack)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := TrajectorySnapshotBoundary(snap)
			if err != nil {
				t.Fatal(err)
			}
			if restored.Latest == nil || restored.Latest.FailureKind != schemas.NodeFailureStageError {
				t.Fatalf("restored latest = %+v, want failure kind stage_error", restored.Latest)
			}
			replayed := (&hostTrajectoryPolicy{}).observeFailure(restored)
			if got := replayed.FailureHistory[len(replayed.FailureHistory)-1].Kind; got != schemas.NodeFailureStageError {
				t.Fatalf("replay kind = %q, want stage_error", got)
			}
		})
	}
}
