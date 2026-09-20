package warmcost

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func usdPtr(v float64) *float64 { return &v }

// pricedRequest builds one priced request record.
func pricedRequest(seq int, source string, in, out, cached int, cost float64) RequestRecord {
	return RequestRecord{
		Sequence:     seq,
		Stage:        "code_writer",
		SpendSource:  source,
		InputTokens:  in,
		OutputTokens: out,
		CachedTokens: cached,
		CostUSD:      usdPtr(cost),
		CostStatus:   schemas.CostStatusPriced,
	}
}

// attempt builds one complete attempt from its request records. The billed
// total is the sum of the priced request costs, matching applyRequestLedger.
func attempt(arm Arm, task string, ok bool, reqs ...RequestRecord) Attempt {
	a := Attempt{
		TaskID:         task,
		Arm:            arm,
		RunStatus:      "completed",
		CostCoverage:   schemas.CostCoverageComplete,
		VerifierResult: ok,
		Requests:       reqs,
	}
	for _, r := range reqs {
		a.Totals.Requests++
		a.Totals.InputTokens += r.InputTokens
		a.Totals.OutputTokens += r.OutputTokens
		a.Totals.CachedTokens += r.CachedTokens
		a.Totals.CacheWrite += r.CacheWrite
		a.Totals.Reasoning += r.Reasoning
		if r.CostUSD != nil {
			a.Totals.BilledUSD += *r.CostUSD
			a.Totals.PricedRecords++
		}
	}
	return a
}

func TestParseRetention(t *testing.T) {
	cases := []struct {
		raw     string
		want    RetentionMode
		wantErr bool
	}{
		{raw: "", want: RetentionFresh},
		{raw: "fresh", want: RetentionFresh},
		{raw: "shared", want: RetentionShared},
		{raw: " shared ", want: RetentionShared},
		{raw: "SHARED", wantErr: true},
		{raw: "banana", wantErr: true},
	}
	for _, tc := range cases {
		got, err := ParseRetention(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseRetention(%q) = %q, want an error", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseRetention(%q) error: %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("ParseRetention(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestSidecarClassAssignment(t *testing.T) {
	fresh := sidecarClass(RetentionFresh, ArmWarm, "s1")
	if fresh == sidecarClass(RetentionFresh, ArmWarm, "s2") {
		t.Fatalf("fresh mode must give each attempt its own sidecar class, got %q twice", fresh)
	}
	sharedWarm := sidecarClass(RetentionShared, ArmWarm, "s1")
	sharedRetrieval := sidecarClass(RetentionShared, ArmRetrievalOnly, "s2")
	if sharedWarm != sharedRetrieval {
		t.Fatalf("shared mode must give the warm arms one sidecar, got %q and %q", sharedWarm, sharedRetrieval)
	}
	sharedCold := sidecarClass(RetentionShared, ArmCold, "s1")
	if sharedCold == sharedWarm {
		t.Fatalf("shared mode must keep the cold arm on its own sidecar, got %q", sharedCold)
	}
}

func TestOrderTasksWriteBeforeRead(t *testing.T) {
	tasks := []Task{
		{ID: "read-1", Phase: PhaseRead},
		{ID: "plain"},
		{ID: "write-1", Phase: PhaseWrite},
		{ID: "read-2", Phase: PhaseRead},
		{ID: "write-2", Phase: PhaseWrite},
	}
	got := orderTasks(tasks)
	gotIDs := make([]string, 0, len(got))
	for _, task := range got {
		gotIDs = append(gotIDs, task.ID)
	}
	want := []string{"write-1", "write-2", "read-1", "plain", "read-2"}
	if strings.Join(gotIDs, ",") != strings.Join(want, ",") {
		t.Fatalf("orderTasks = %v, want %v", gotIDs, want)
	}
}

func TestNewRunnerRejectsSharedWithoutSidecarRoot(t *testing.T) {
	_, err := NewRunner(Config{
		Binary:    "/bin/true",
		OutDir:    t.TempDir(),
		Tasks:     []Task{{ID: "t", Prompt: "p", Check: "true"}},
		Repeats:   1,
		Retention: RetentionShared,
	}, nil)
	if err == nil {
		t.Fatal("shared retention without a sidecar root must fail loud")
	}
}

func TestNewRunnerRejectsUnknownTaskPhase(t *testing.T) {
	_, err := NewRunner(Config{
		Binary:  "/bin/true",
		OutDir:  t.TempDir(),
		Tasks:   []Task{{ID: "t", Prompt: "p", Check: "true", Phase: "sideways"}},
		Repeats: 1,
	}, nil)
	if err == nil {
		t.Fatal("an unknown task phase must fail loud")
	}
}

func TestComputeArmMetricsTokenRates(t *testing.T) {
	attempts := []Attempt{
		attempt(ArmWarm, "t1", true, pricedRequest(1, schemas.SpendSourceGeneration, 1000, 100, 600, 0.001)),
		attempt(ArmWarm, "t2", false, pricedRequest(1, schemas.SpendSourceGeneration, 500, 50, 300, 0.0005)),
	}
	m := ComputeArmMetrics(ArmWarm, attempts)
	if m.InputTokens != 1500 {
		t.Fatalf("InputTokens = %d, want 1500", m.InputTokens)
	}
	if m.InputTokensPerAttempt != 750 {
		t.Fatalf("InputTokensPerAttempt = %v, want 750", m.InputTokensPerAttempt)
	}
	if m.InputTokensPerVerifiedCompletion != 1500 {
		t.Fatalf("InputTokensPerVerifiedCompletion = %v, want 1500", m.InputTokensPerVerifiedCompletion)
	}
}

func TestDecomposeBySpendSource(t *testing.T) {
	cold := ComputeArmMetrics(ArmCold, []Attempt{
		attempt(ArmCold, "t1", true,
			pricedRequest(1, schemas.SpendSourceGeneration, 1000, 100, 600, 0.010),
			pricedRequest(2, schemas.SpendSourceRepair, 400, 40, 200, 0.004)),
	})
	warm := ComputeArmMetrics(ArmWarm, []Attempt{
		attempt(ArmWarm, "t1", true,
			pricedRequest(1, schemas.SpendSourceGeneration, 900, 90, 700, 0.009),
			pricedRequest(2, schemas.SpendSourceRepair, 100, 10, 80, 0.001)),
	})
	d, err := Decompose(cold, warm)
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	if len(d.Sources) != 2 {
		t.Fatalf("sources = %d, want 2", len(d.Sources))
	}
	bySource := map[string]SourceDelta{}
	for _, s := range d.Sources {
		bySource[s.Source] = s
	}
	gen, ok := bySource[schemas.SpendSourceGeneration]
	if !ok {
		t.Fatal("generation source missing")
	}
	if gen.DeltaUSD > -0.0009 || gen.DeltaUSD < -0.0011 {
		t.Fatalf("generation delta = %v, want about -0.001", gen.DeltaUSD)
	}
	rep, ok := bySource[schemas.SpendSourceRepair]
	if !ok {
		t.Fatal("repair source missing")
	}
	if rep.DeltaUSD > -0.0029 || rep.DeltaUSD < -0.0031 {
		t.Fatalf("repair delta = %v, want about -0.003", rep.DeltaUSD)
	}
	if want := warm.BilledUSD - cold.BilledUSD; d.TotalDeltaUSD != want {
		t.Fatalf("total delta = %v, want %v", d.TotalDeltaUSD, want)
	}
}

func TestDecomposeFailsOnBrokenSourceIdentity(t *testing.T) {
	cold := ArmMetrics{Arm: ArmCold, BilledUSD: 1.0, BilledUSDBySource: map[string]float64{"generation": 0.5}}
	warm := ArmMetrics{Arm: ArmWarm, BilledUSD: 1.0, BilledUSDBySource: map[string]float64{"generation": 1.0}}
	if _, err := Decompose(cold, warm); err == nil {
		t.Fatal("a per-source split that does not reconstruct the billed total must fail loud")
	}
}

func TestEvaluateClaimWithholdsOnFreshRetention(t *testing.T) {
	decomp := Decomposition{TotalDeltaUSD: -1}
	boot := BootstrapResult{
		BilledUSDPerAttempt: Interval{Lower: -1, Upper: -0.5},
		SuccessRateDelta:    Interval{Lower: 0, Upper: 0},
	}
	if claim := EvaluateClaim(decomp, boot, 0.05, false, true); claim.TotalCostClaimAllowed {
		t.Fatal("fresh retention must withhold the total-cost claim")
	} else if !strings.Contains(claim.Reason, "retention protocol is fresh") {
		t.Fatalf("reason = %q, want the fresh-retention reason", claim.Reason)
	}
	if claim := EvaluateClaim(decomp, boot, 0.05, false, false); !claim.TotalCostClaimAllowed {
		t.Fatalf("a clean shared run with a negative interval should allow the claim, got %q", claim.Reason)
	}
}

func TestRenderMarkdownShowsTokensAndSourceChannels(t *testing.T) {
	coldReqs := []RequestRecord{
		pricedRequest(1, schemas.SpendSourceGeneration, 103763, 9569, 70400, 0.0100),
		pricedRequest(2, schemas.SpendSourceRepair, 2000, 200, 1000, 0.0019),
	}
	warmReqs := []RequestRecord{
		pricedRequest(1, schemas.SpendSourceGeneration, 87616, 9005, 62592, 0.0080),
		pricedRequest(2, schemas.SpendSourceRepair, 800, 80, 500, 0.0021),
	}
	agg := Aggregate{
		Version:     Version,
		RunID:       "unit",
		GeneratedAt: time.Unix(0, 0).UTC(),
		Provenance:  Provenance{BinaryRevision: "abc", SidecarRevision: "abc", ModelID: "test"},
		Retention: RetentionReport{
			Mode:         RetentionShared,
			SidecarRoot:  "/tmp/sc",
			Assignment:   []SidecarAssignment{{Arm: ArmCold, Pattern: "a fresh sidecar per attempt"}},
			OrderingRule: "write before read",
		},
		ArmMetrics: []ArmMetrics{
			ComputeArmMetrics(ArmCold, []Attempt{attempt(ArmCold, "t1", true, coldReqs...)}),
			ComputeArmMetrics(ArmWarm, []Attempt{attempt(ArmWarm, "t1", true, warmReqs...)}),
		},
		Coverage: Coverage{Attempts: 2, Complete: true},
		Claim:    Claim{TotalCostClaimAllowed: false, Reason: "unit"},
	}
	decomp, err := Decompose(agg.ArmMetrics[0], agg.ArmMetrics[1])
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	agg.Decomposition = decomp

	out := renderMarkdown(agg)
	for _, want := range []string{
		"input tok", "output tok", "cached tok", "cache-write tok", "reasoning tok",
		"input tok/attempt", "input tok/verified",
		"105763", "9769", "71400",
		"## Cost decomposition by spend source",
		"| generation |", "| repair |",
		"## Retention protocol", "shared",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report is missing %q\n%s", want, out)
		}
	}
	for _, gone := range []string{"round channel", "payload channel", "RoundChannelUSD", "PayloadChannelUSD"} {
		if strings.Contains(out, gone) {
			t.Fatalf("report still contains the stale label %q", gone)
		}
	}
}

func TestGroupSequencesWriteThenRead(t *testing.T) {
	tasks := []Task{
		{ID: "read-1", Phase: PhaseRead, Fixture: "/fixture"},
		{ID: "plain", Fixture: "/fixture"},
		{ID: "write-1", Phase: PhaseWrite, Fixture: "/fixture"},
		{ID: "read-2", Phase: PhaseRead, Fixture: "/fixture"},
		{ID: "write-2", Phase: PhaseWrite, Fixture: "/fixture"},
	}
	sequences, err := groupSequences(tasks)
	if err != nil {
		t.Fatalf("groupSequences: %v", err)
	}
	if len(sequences) != 2 {
		t.Fatalf("sequences = %d, want 2", len(sequences))
	}
	got := sequences[0].Tasks[0].ID + "," + sequences[1].Tasks[0].ID + "," + sequences[1].Tasks[1].ID + "," + sequences[1].Tasks[2].ID + "," + sequences[1].Tasks[3].ID
	if got != "write-1,write-2,read-1,plain,read-2" {
		t.Fatalf("sequence order = %s", got)
	}
	if !sequences[0].GitInit || !sequences[1].GitInit {
		t.Fatal("a sequence with a write-phase task must initialize a git workspace")
	}
}

func TestGroupSequencesWithoutWriteSplitsPerTask(t *testing.T) {
	tasks := []Task{
		{ID: "a", Phase: PhaseRead, Fixture: "/fixture"},
		{ID: "b", Fixture: "/fixture"},
	}
	sequences, err := groupSequences(tasks)
	if err != nil {
		t.Fatalf("groupSequences: %v", err)
	}
	if len(sequences) != 2 || sequences[0].Tasks[0].ID != "a" || sequences[1].Tasks[0].ID != "b" {
		t.Fatalf("sequences = %+v, want one task each", sequences)
	}
	if sequences[0].GitInit || sequences[1].GitInit {
		t.Fatal("a taskset with no write-phase task must keep the plain workspace behavior")
	}
}

func TestGroupSequencesRejectsMixedFixtures(t *testing.T) {
	_, err := groupSequences([]Task{
		{ID: "write-1", Phase: PhaseWrite, Fixture: "/a"},
		{ID: "read-1", Phase: PhaseRead, Fixture: "/b"},
	})
	if err == nil {
		t.Fatal("a sequence that mixes fixtures must fail loud")
	}
}

func TestRunSequenceSharesOneWorkspace(t *testing.T) {
	fixture := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixture, "seed.txt"), []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Binary:  "/bin/true",
		OutDir:  t.TempDir(),
		Repeats: 1,
		Arms:    []Arm{ArmCold},
		Tasks: []Task{
			{ID: "write-1", Prompt: "write", Check: "test -f write-marker", Fixture: fixture, Phase: PhaseWrite},
			{ID: "read-1", Prompt: "read", Check: "test -f write-marker", Fixture: fixture, Phase: PhaseRead},
		},
	}
	var dirs []string
	seam := func(_ context.Context, req ExecRequest) (ExecResult, error) {
		dirs = append(dirs, req.Dir)
		// The write task leaves a marker; the read task's verifier asserts it
		// is still present, so the two tasks must have shared one workspace.
		if err := os.WriteFile(filepath.Join(req.Dir, "write-marker"), []byte("x"), 0o644); err != nil {
			return ExecResult{}, err
		}
		payload, _ := json.Marshal(map[string]any{
			"run_id": "seq", "status": "completed", "tier": "light",
			"stages": []any{}, "cost_coverage": "not_applicable",
			"total_cost_usd": 0, "priced_request_count": 0,
		})
		line, _ := json.Marshal(map[string]any{"type": "final", "text": string(payload)})
		return ExecResult{Stdout: append(line, '\n')}, nil
	}
	r, err := NewRunner(cfg, seam)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(dirs) != 2 || dirs[0] != dirs[1] {
		t.Fatalf("workspace dirs = %v, want one shared directory for both tasks", dirs)
	}
}

// mockPipelineResult is a minimal valid final stream-json event for the seam.
func mockPipelineResult(runID string) ExecResult {
	payload, _ := json.Marshal(map[string]any{
		"run_id": runID, "status": "completed", "tier": "light",
		"stages": []any{}, "cost_coverage": "not_applicable",
		"total_cost_usd": 0, "priced_request_count": 0,
	})
	line, _ := json.Marshal(map[string]any{"type": "final", "text": string(payload)})
	return ExecResult{Stdout: append(line, '\n')}
}

func sequenceFixture(t *testing.T) string {
	t.Helper()
	fixture := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixture, "seed.txt"), []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestRunSequenceCommitsAndReanchorsAfterVerifiedWrite(t *testing.T) {
	fixture := sequenceFixture(t)
	cfg := Config{
		Binary:  "/bin/true",
		OutDir:  t.TempDir(),
		Repeats: 1,
		Arms:    []Arm{ArmWarm},
		Tasks: []Task{
			{ID: "write-1", Prompt: "write", Check: "true", Fixture: fixture, Phase: PhaseWrite},
			{ID: "read-1", Prompt: "read", Check: "true", Fixture: fixture, Phase: PhaseRead},
		},
	}
	seam := func(_ context.Context, req ExecRequest) (ExecResult, error) {
		prompt := req.Args[len(req.Args)-1]
		if err := os.WriteFile(filepath.Join(req.Dir, "seed.txt"), []byte(prompt), 0o644); err != nil {
			return ExecResult{}, err
		}
		return mockPipelineResult("run-seq-1"), nil
	}
	r, err := NewRunner(cfg, seam)
	if err != nil {
		t.Fatal(err)
	}
	type reanchorCall struct {
		pre, post, producer string
		commits             int
	}
	var calls []reanchorCall
	r.reanchorFn = func(_ context.Context, dir, class, pre, post, producer string) error {
		out, _ := exec.Command("git", "-C", dir, "rev-list", "--count", "HEAD").Output()
		n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
		calls = append(calls, reanchorCall{pre: pre, post: post, producer: producer, commits: n})
		return nil
	}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("reanchor calls = %d, want 1", len(calls))
	}
	call := calls[0]
	if call.pre == "" || call.post == "" || call.pre == call.post {
		t.Fatalf("reanchor revisions = %q -> %q, want two distinct non-empty revisions", call.pre, call.post)
	}
	if call.producer != "run-seq-1" {
		t.Fatalf("producer run id = %q, want run-seq-1", call.producer)
	}
	if call.commits < 2 {
		t.Fatalf("commits at reanchor = %d, want at least 2 (fixture + verified tree)", call.commits)
	}
}

func TestRunSequenceFailedWriteDoesNotCommitOrReanchor(t *testing.T) {
	fixture := sequenceFixture(t)
	cfg := Config{
		Binary:  "/bin/true",
		OutDir:  t.TempDir(),
		Repeats: 1,
		Arms:    []Arm{ArmWarm},
		Tasks: []Task{
			{ID: "write-1", Prompt: "write", Check: "false", Fixture: fixture, Phase: PhaseWrite},
			{ID: "read-1", Prompt: "read", Check: "true", Fixture: fixture, Phase: PhaseRead},
		},
	}
	seam := func(_ context.Context, req ExecRequest) (ExecResult, error) {
		if err := os.WriteFile(filepath.Join(req.Dir, "seed.txt"), []byte("dirty"), 0o644); err != nil {
			return ExecResult{}, err
		}
		return mockPipelineResult("run-seq-2"), nil
	}
	r, err := NewRunner(cfg, seam)
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	r.reanchorFn = func(context.Context, string, string, string, string, string) error {
		called++
		return nil
	}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called != 0 {
		t.Fatalf("reanchor called %d time(s) after a failed write, want 0", called)
	}
}
