package decisionreplay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type evaluatorFunc func(context.Context, zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error)

func (f evaluatorFunc) Evaluate(ctx context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
	return f(ctx, req)
}

func replayConfig() config.DecisionAdvisorConfig {
	return config.DecisionAdvisorConfig{Backend: "http", Endpoint: "http://fixture.invalid", Profile: "fixture-model"}
}

func choice(req zeroruntime.DecisionRequest, label string) zeroruntime.DecisionResult {
	return zeroruntime.DecisionResult{ResolvedModel: req.Model, InputTokens: 11, OutputTokens: 3, Answers: []zeroruntime.DecisionAnswer{{QuestionID: "trajectory_action", Kind: zeroruntime.DecisionKindChoice, Choice: label, Probabilities: map[string]float64{label: 1}}}}
}

func readRows(t *testing.T, out *bytes.Buffer) []Row {
	t.Helper()
	var rows []Row
	decoder := json.NewDecoder(out)
	for decoder.More() {
		var row Row
		if err := decoder.Decode(&row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return rows
}

func TestReplayFixtureOrderingGuardsAndErrors(t *testing.T) {
	records, err := LoadRecords([]string{"testdata/records.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	var active, peak atomic.Int32
	evaluator := evaluatorFunc(func(ctx context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		var state schemas.TrajectorySnapshot
		if err := json.Unmarshal([]byte(req.State), &state); err != nil {
			return zeroruntime.DecisionResult{}, err
		}
		b := state.Boundary
		time.Sleep(time.Duration(8-b.Iteration) * time.Millisecond)
		if req.SessionID != fmt.Sprintf("trajectory:%d:%d", b.Iteration, b.Repair) {
			return zeroruntime.DecisionResult{}, errors.New("wrong episode")
		}
		raw, _ := json.Marshal(state)
		if req.State != string(raw) {
			return zeroruntime.DecisionResult{}, errors.New("wrong boundary projection")
		}
		if len(req.Questions) != 1 || req.Questions[0].ID != "trajectory_action" || req.Questions[0].Kind != zeroruntime.DecisionKindChoice || len(req.Questions[0].Criteria) != 5 {
			return zeroruntime.DecisionResult{}, errors.New("wrong questions")
		}
		label := "stop"
		if b.Iteration == 2 && b.Repair == 0 {
			label = "step_back"
		}
		if b.Iteration == 4 {
			label = "continue"
		}
		if b.Iteration == 5 {
			return zeroruntime.DecisionResult{}, errors.New("fixture transport failure")
		}
		return choice(req, label), nil
	})
	var out bytes.Buffer
	summary, err := Replay(context.Background(), records, evaluator, Options{BackendConfig: replayConfig(), Concurrency: 2}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Boundaries != 10 || summary.Answered != 9 || summary.Errors != 1 {
		t.Fatalf("summary: %+v", summary)
	}
	if peak.Load() != 2 {
		t.Fatalf("bounded concurrency peak=%d", peak.Load())
	}
	rows := readRows(t, &out)
	wantActions := []schemas.TrajectoryAction{schemas.ActionContinue, schemas.ActionStepBack, schemas.ActionContinue, schemas.ActionSurfaceToUser, schemas.ActionContinue, schemas.ActionContinue, schemas.ActionContinue, schemas.ActionSurfaceToUser, schemas.ActionContinue, schemas.ActionStepBack}
	wantKeys := []string{"a/1/0", "a/2/0", "a/2/1", "a/3/0", "a/4/0", "a/5/0", "a/6/0", "a/7/0", "b/1/0", "b/2/0"}
	counts := map[schemas.TrajectoryAction]int{}
	for i, row := range rows {
		if fmt.Sprintf("%s/%d/%d", row.RunID, row.Iteration, row.Repair) != wantKeys[i] || row.PolicyAction != wantActions[i] {
			t.Errorf("row %d: %+v", i, row)
		}
		counts[row.PolicyAction]++
		if row.PolicyReason == "" || row.Backend != "http" || row.Pack != splice.QuestionPackTrajActionV1 || row.CostUSD != nil {
			t.Errorf("row metadata: %+v", row)
		}
		if i == 5 {
			if !strings.Contains(row.Error, "fixture transport failure") {
				t.Errorf("missing failure: %+v", row)
			}
		} else if row.Error != "" || row.InputTokens != 11 || row.OutputTokens != 3 {
			t.Errorf("answer: %+v", row)
		}
	}
	if !reflect.DeepEqual(summary.PolicyActions, counts) {
		t.Fatalf("action counts: %+v != %+v", summary.PolicyActions, counts)
	}
}

func TestReplayPackAndGuardVariants(t *testing.T) {
	for _, pack := range []string{splice.QuestionPackTrajActionV1, splice.QuestionPackTrajActionV2, splice.QuestionPackTrajV1} {
		t.Run(pack, func(t *testing.T) {
			b := splice.TrajectoryBoundary{RunID: "run", ProjectID: "fixture", Intent: "Fix the test.", Iteration: 1, Previous: &splice.TrajectoryStep{Change: "previous", Failure: "failure"}, Latest: &splice.TrajectoryStep{Change: "latest", Failure: "failure"}, Rules: schemas.TrajectoryDecision{Action: schemas.ActionContinue, Reason: "rules continue"}}
			snap, err := splice.TrajectorySemanticSnapshot(b, pack)
			if err != nil {
				t.Fatal(err)
			}
			evaluator := evaluatorFunc(func(_ context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
				if pack == splice.QuestionPackTrajV1 {
					if len(req.Questions) != 2 || !strings.Contains(req.State, "\"candidates\"") || !strings.Contains(req.State, "previous") {
						return zeroruntime.DecisionResult{}, errors.New("invalid noul projection")
					}
					return zeroruntime.DecisionResult{ResolvedModel: req.Model, Answers: []zeroruntime.DecisionAnswer{{QuestionID: "same_cause", Kind: zeroruntime.DecisionKindNoul, TrueProbability: 1}, {QuestionID: "new_information", Kind: zeroruntime.DecisionKindNoul, TrueProbability: 0}}}, nil
				}
				return choice(req, "step_back"), nil
			})
			records := []schemas.DecisionDispositionRecord{}
			for i := 1; i <= 3; i++ {
				s := snap
				s.Iteration = i
				s.StageInvocation = 0
				records = append(records, schemas.DecisionDispositionRecord{Snapshot: s})
			}
			var out bytes.Buffer
			summary, err := Replay(context.Background(), records, evaluator, Options{BackendConfig: replayConfig()}, &out)
			if err != nil || summary.Errors != 0 {
				t.Fatalf("summary=%+v error=%v", summary, err)
			}
			rows := readRows(t, &out)
			want := []schemas.TrajectoryAction{schemas.ActionStepBack, schemas.ActionContinue, schemas.ActionSurfaceToUser}
			if pack == splice.QuestionPackTrajActionV2 {
				want = []schemas.TrajectoryAction{schemas.ActionContinue, schemas.ActionStepBack, schemas.ActionContinue}
			}
			for i, action := range want {
				if rows[i].PolicyAction != action {
					t.Fatalf("row %d: %+v", i, rows[i])
				}
			}
		})
	}
}

func TestReplayNewFailureGuardIsPackScoped(t *testing.T) {
	b := splice.TrajectoryBoundary{RunID: "run", ProjectID: "fixture", Intent: "Fix the test.", Iteration: 1, Previous: &splice.TrajectoryStep{Change: "previous", Failure: "failure"}, Latest: &splice.TrajectoryStep{Change: "latest", Failure: "new failure"}, Rules: schemas.TrajectoryDecision{Action: schemas.ActionContinue, Reason: "rules continue"}}
	evaluator := evaluatorFunc(func(_ context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
		return choice(req, "step_back"), nil
	})
	for pack, want := range map[string]schemas.TrajectoryAction{splice.QuestionPackTrajActionV1: schemas.ActionStepBack, splice.QuestionPackTrajActionV2: schemas.ActionContinue} {
		snap, err := splice.TrajectorySemanticSnapshot(b, pack)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		summary, err := Replay(context.Background(), []schemas.DecisionDispositionRecord{{Snapshot: snap}}, evaluator, Options{BackendConfig: replayConfig()}, &out)
		if err != nil || summary.Errors != 0 {
			t.Fatalf("pack=%s summary=%+v error=%v", pack, summary, err)
		}
		if rows := readRows(t, &out); len(rows) != 1 || rows[0].PolicyAction != want {
			t.Fatalf("pack=%s rows=%+v want=%s", pack, rows, want)
		}
	}
}

func TestReplayLimitsAndUsageErrors(t *testing.T) {
	records, err := LoadRecords([]string{"testdata/records.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	records = records[:1]
	for _, kind := range []string{"timeout", "state bound", "invalid answer", "reported usage"} {
		t.Run(kind, func(t *testing.T) {
			cfg := replayConfig()
			var calls int
			evaluator := evaluatorFunc(func(ctx context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
				calls++
				switch kind {
				case "timeout":
					<-ctx.Done()
					return zeroruntime.DecisionResult{}, ctx.Err()
				case "invalid answer":
					result := choice(req, "stop")
					result.Answers[0].Probabilities["stop"] = .2
					return result, nil
				case "reported usage":
					cost := 0.0
					return zeroruntime.DecisionResult{}, &zeroruntime.DecisionResponseError{Cause: errors.New("malformed answer"), Usage: &zeroruntime.DecisionResult{ResolvedModel: req.Model, InputTokens: 17, OutputTokens: 4, ReportedCostUSD: &cost}}
				default:
					return choice(req, "continue"), nil
				}
			})
			if kind == "timeout" {
				cfg.Limits.TimeoutMS = 1
			}
			if kind == "state bound" {
				cfg.Limits.MaxRequestBytes = 1
			}
			var out bytes.Buffer
			summary, err := Replay(context.Background(), records, evaluator, Options{BackendConfig: cfg}, &out)
			if err != nil || summary.Errors != 1 {
				t.Fatalf("summary=%+v error=%v", summary, err)
			}
			row := readRows(t, &out)[0]
			if row.Error == "" {
				t.Fatal("missing error")
			}
			if kind == "state bound" && calls != 0 {
				t.Fatal("oversize request evaluated")
			}
			if kind == "reported usage" && (row.InputTokens != 17 || row.OutputTokens != 4 || row.CostUSD == nil || *row.CostUSD != 0) {
				t.Fatalf("usage lost: %+v", row)
			}
		})
	}
}

func TestLoadRecordsAndSkipUnrelated(t *testing.T) {
	dir := t.TempDir()
	raw, err := os.ReadFile("testdata/records.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "records.jsonl")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	records, err := LoadRecords([]string{dir, path})
	if err != nil || len(records) != 10 {
		t.Fatalf("records=%d error=%v", len(records), err)
	}
	records = append(records, schemas.DecisionDispositionRecord{}, schemas.DecisionDispositionRecord{Snapshot: schemas.SemanticSnapshot{QuestionPack: splice.QuestionPackTrajActionV1}})
	var out bytes.Buffer
	summary, err := Replay(context.Background(), records, evaluatorFunc(func(_ context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
		return choice(req, "continue"), nil
	}), Options{BackendConfig: replayConfig()}, &out)
	if err != nil || summary.Boundaries != 10 || summary.Errors != 0 {
		t.Fatalf("summary=%+v error=%v", summary, err)
	}
	if err := os.WriteFile(path, []byte("{}\ninvalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRecords([]string{dir}); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("malformed record: %v", err)
	}
}

func TestReplayOverridesAndSavedGuards(t *testing.T) {
	for _, tc := range []struct {
		name, recorded, override, label string
		isolated, target, candidateOnly bool
		want                            schemas.TrajectoryAction
		wantError                       bool
	}{
		{name: "action to noul", recorded: splice.QuestionPackTrajActionV1, override: splice.QuestionPackTrajV1, want: schemas.ActionStepBack},
		{name: "noul to action", recorded: splice.QuestionPackTrajV1, override: splice.QuestionPackTrajActionV1, label: "step_back", want: schemas.ActionStepBack},
		{name: "old noul", recorded: splice.QuestionPackTrajV1, candidateOnly: true, want: schemas.ActionStepBack},
		{name: "incomplete override", recorded: splice.QuestionPackTrajV1, override: splice.QuestionPackTrajActionV1, candidateOnly: true, want: schemas.ActionContinue, wantError: true},
		{name: "rollback isolated", recorded: splice.QuestionPackTrajActionV1, label: "rollback", isolated: true, target: true, want: schemas.ActionRollback},
		{name: "rollback shared", recorded: splice.QuestionPackTrajActionV1, label: "rollback", target: true, want: schemas.ActionContinue},
		{name: "rollback absent target", recorded: splice.QuestionPackTrajActionV1, label: "rollback", isolated: true, want: schemas.ActionContinue},
		{name: "saved shadow flags", recorded: splice.QuestionPackTrajActionV1, label: "stop", want: schemas.ActionContinue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := splice.TrajectoryBoundary{RunID: "run", ProjectID: "fixture", Intent: "Fix the test.", Iteration: 1, Previous: &splice.TrajectoryStep{Change: "previous", Failure: "failure"}, Latest: &splice.TrajectoryStep{Change: "latest", Failure: "failure"}, Rules: schemas.TrajectoryDecision{Action: schemas.ActionContinue, Reason: "rules continue"}, IsolatedWorktree: tc.isolated, RollbackTarget: tc.target, StepBackUsed: true, Escalated: true}
			snap, err := splice.TrajectorySemanticSnapshot(b, tc.recorded)
			if err != nil {
				t.Fatal(err)
			}
			if tc.candidateOnly {
				snap.RecentEvents = nil
			}
			calls := 0
			evaluator := evaluatorFunc(func(_ context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
				calls++
				if req.Questions[0].Kind == zeroruntime.DecisionKindNoul {
					var projected struct {
						Candidates []schemas.CandidateRef `json:"candidates"`
						Need       string                 `json:"need"`
					}
					if err := json.Unmarshal([]byte(req.State), &projected); err != nil || len(projected.Candidates) != 2 || projected.Need != b.Intent {
						return zeroruntime.DecisionResult{}, fmt.Errorf("bad noul projection: %s", req.State)
					}
					return zeroruntime.DecisionResult{ResolvedModel: req.Model, Answers: []zeroruntime.DecisionAnswer{{QuestionID: "same_cause", Kind: zeroruntime.DecisionKindNoul, TrueProbability: 1}, {QuestionID: "new_information", Kind: zeroruntime.DecisionKindNoul, TrueProbability: 0}}}, nil
				}
				var projected schemas.TrajectorySnapshot
				if err := json.Unmarshal([]byte(req.State), &projected); err != nil || projected.Boundary.Intent != b.Intent {
					return zeroruntime.DecisionResult{}, fmt.Errorf("bad action projection: %s", req.State)
				}
				return choice(req, tc.label), nil
			})
			second := snap
			second.Iteration = 2
			records := []schemas.DecisionDispositionRecord{{Snapshot: snap}, {Snapshot: snap}, {Snapshot: second}}
			var out bytes.Buffer
			summary, err := Replay(context.Background(), records, evaluator, Options{BackendConfig: replayConfig(), Pack: tc.override}, &out)
			if err != nil || summary.Boundaries != 2 {
				t.Fatalf("summary=%+v error=%v", summary, err)
			}
			rows := readRows(t, &out)
			if rows[0].PolicyAction != tc.want || (rows[0].Error != "") != tc.wantError {
				t.Fatalf("row: %+v", rows[0])
			}
			if tc.wantError && calls != 0 {
				t.Fatal("incomplete override evaluated")
			}
			if tc.name == "saved shadow flags" && rows[1].PolicyAction != schemas.ActionContinue {
				t.Fatalf("another backend's guard flags affected replay: %+v", rows[1])
			}
		})
	}
}

func TestReplayThresholds(t *testing.T) {
	records, err := LoadRecords([]string{"testdata/records.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	evaluator := evaluatorFunc(func(_ context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
		result := choice(req, "step_back")
		result.Answers[0].Probabilities = map[string]float64{"step_back": .65, "continue": .35}
		return result, nil
	})
	var out bytes.Buffer
	summary, err := Replay(context.Background(), records[:1], evaluator, Options{BackendConfig: replayConfig(), Thresholds: map[string]float64{"step_back": .7}}, &out)
	if err != nil || summary.Errors != 0 {
		t.Fatalf("summary=%+v error=%v", summary, err)
	}
	row := readRows(t, &out)[0]
	if row.PolicyAction != schemas.ActionContinue || row.TopLabel != splice.LabelStepBack || !strings.Contains(row.PolicyReason, "below threshold") {
		t.Fatalf("threshold: %+v", row)
	}
}

func TestReplayErrorResetsStopStreak(t *testing.T) {
	records, err := LoadRecords([]string{"testdata/records.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	base := records[0].Snapshot
	var corpus []schemas.DecisionDispositionRecord
	for iteration := 1; iteration <= 5; iteration++ {
		boundary := base.Trajectory.Boundary
		boundary.Iteration = iteration
		boundary.RunID = "run"
		snap, err := splice.TrajectorySemanticSnapshot(boundary, splice.QuestionPackTrajActionV1)
		if err != nil {
			t.Fatal(err)
		}
		corpus = append(corpus, schemas.DecisionDispositionRecord{Snapshot: snap})
	}
	evaluator := evaluatorFunc(func(_ context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
		var state schemas.TrajectorySnapshot
		if err := json.Unmarshal([]byte(req.State), &state); err != nil {
			return zeroruntime.DecisionResult{}, err
		}
		if state.Boundary.Iteration == 3 {
			return zeroruntime.DecisionResult{}, errors.New("failure between stops")
		}
		label := "stop"
		if state.Boundary.Iteration == 1 {
			label = "step_back"
		}
		return choice(req, label), nil
	})
	var out bytes.Buffer
	summary, err := Replay(context.Background(), corpus, evaluator, Options{BackendConfig: replayConfig()}, &out)
	if err != nil || summary.Answered != 4 || summary.Errors != 1 {
		t.Fatalf("summary=%+v error=%v", summary, err)
	}
	rows := readRows(t, &out)
	for i, action := range []schemas.TrajectoryAction{schemas.ActionStepBack, schemas.ActionContinue, schemas.ActionContinue, schemas.ActionContinue, schemas.ActionSurfaceToUser} {
		if rows[i].PolicyAction != action {
			t.Fatalf("row %d: %+v", i, rows[i])
		}
	}
}

func TestReplayErrorResetsStopStreakV2(t *testing.T) {
	records, err := LoadRecords([]string{"testdata/records.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	base := records[0].Snapshot
	var corpus []schemas.DecisionDispositionRecord
	for iteration := 1; iteration <= 6; iteration++ {
		boundary := base.Trajectory.Boundary
		boundary.Iteration = iteration
		boundary.RunID = "run"
		snap, err := splice.TrajectorySemanticSnapshot(boundary, splice.QuestionPackTrajActionV2)
		if err != nil {
			t.Fatal(err)
		}
		corpus = append(corpus, schemas.DecisionDispositionRecord{Snapshot: snap})
	}
	evaluator := evaluatorFunc(func(_ context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
		var state schemas.TrajectorySnapshot
		if err := json.Unmarshal([]byte(req.State), &state); err != nil {
			return zeroruntime.DecisionResult{}, err
		}
		if state.Boundary.Iteration == 4 {
			return zeroruntime.DecisionResult{}, errors.New("failure between stops")
		}
		label := "stop"
		if state.Boundary.Iteration <= 2 {
			label = "step_back"
		}
		return choice(req, label), nil
	})
	var out bytes.Buffer
	summary, err := Replay(context.Background(), corpus, evaluator, Options{BackendConfig: replayConfig(), Pack: splice.QuestionPackTrajActionV2}, &out)
	if err != nil || summary.Answered != 5 || summary.Errors != 1 {
		t.Fatalf("summary=%+v error=%v", summary, err)
	}
	rows := readRows(t, &out)
	for i, action := range []schemas.TrajectoryAction{schemas.ActionContinue, schemas.ActionStepBack, schemas.ActionContinue, schemas.ActionContinue, schemas.ActionContinue, schemas.ActionSurfaceToUser} {
		if rows[i].PolicyAction != action {
			t.Fatalf("row %d: %+v", i, rows[i])
		}
	}
}

func TestReplayRecomputesFailureFeaturesFromLegacyFixture(t *testing.T) {
	records, err := LoadRecords([]string{"testdata/records.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if len(record.Snapshot.Trajectory.Boundary.FailureHistory) != 0 {
			t.Fatal("fixture must predate failure features")
		}
	}
	// The fixture has two interleaved, unordered runs. All failures initially match.
	// Change one failure to test progress and the streak reset after a transport error.
	for i := range records {
		b := &records[i].Snapshot.Trajectory.Boundary
		if b.RunID == "a" && b.Iteration == 4 {
			b.Latest.Failure = "code_writer failed: splice.stageRunError: context expansion budget exhausted 123"
		}
	}
	evaluator := evaluatorFunc(func(_ context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
		var state schemas.TrajectorySnapshot
		if err := json.Unmarshal([]byte(req.State), &state); err != nil {
			return zeroruntime.DecisionResult{}, err
		}
		b := state.Boundary
		if req.Questions[0].Instructions == "Select the next trajectory action from the boundary evidence." || len(b.FailureHistory) == 0 {
			return zeroruntime.DecisionResult{}, errors.New("v2 request omitted progress evidence")
		}
		if b.Iteration == 1 && (len(b.FailureHistory) != 1 || !b.FailureHistory[0].IsNew || b.DistinctFailures != 1) {
			return zeroruntime.DecisionResult{}, errors.New("run history was not isolated")
		}
		if b.RunID == "a" && b.Iteration == 4 {
			latest := b.FailureHistory[len(b.FailureHistory)-1]
			if !latest.IsNew || latest.Kind != "stage_error" || latest.SignatureID != 1 || b.DistinctFailures != 2 || b.BoundariesSinceNewFailure != 0 {
				return zeroruntime.DecisionResult{}, fmt.Errorf("wrong progress features: %+v", b)
			}
			return zeroruntime.DecisionResult{}, errors.New("fixture error on a new failure")
		}
		return choice(req, "step_back"), nil
	})
	var out bytes.Buffer
	summary, err := Replay(context.Background(), records, evaluator, Options{BackendConfig: replayConfig(), Pack: splice.QuestionPackTrajActionV2, Concurrency: 2}, &out)
	if err != nil || summary.Answered != 9 || summary.Errors != 1 {
		t.Fatalf("summary=%+v error=%v output=%s", summary, err, out.String())
	}
	rows := readRows(t, &out)
	wantNew := []bool{true, false, false, false, true, false, false, false, true, false}
	wantSince := []int{0, 1, 2, 3, 0, 1, 2, 3, 0, 1}
	for i, row := range rows {
		if row.FailureIsNew != wantNew[i] || row.BoundariesSinceNewFailure != wantSince[i] || row.Pack != splice.QuestionPackTrajActionV2 {
			t.Errorf("row %d: %+v", i, row)
		}
		if row.FailureIsNew && row.PolicyAction != schemas.ActionContinue {
			t.Errorf("new failure caused intervention: %+v", row)
		}
	}
}
