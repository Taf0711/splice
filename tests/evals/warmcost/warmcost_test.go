package warmcost

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/tests/evals/decisionreplay"
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

func TestSidecarEnvDisablesMaintenance(t *testing.T) {
	r := &Runner{cfg: Config{SidecarRoot: "/tmp/warmcost-sidecars"}}
	env := r.sidecarEnv("fresh-s1")
	want := map[string]string{
		"SPLICE_MEMD_SOCKET":      "/tmp/warmcost-sidecars/fresh-s1/mem.sock",
		"SPLICE_MEMD_DB":          "/tmp/warmcost-sidecars/fresh-s1/mem.db",
		"SPLICE_MEMD_MAINTENANCE": "0",
	}
	got := make(map[string]string, len(env))
	for _, value := range env {
		key, raw, ok := strings.Cut(value, "=")
		if !ok {
			t.Fatalf("invalid environment override %q", value)
		}
		got[key] = raw
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			t.Fatalf("%s = %q, want %q", key, got[key], wantValue)
		}
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

// sequenceIDs renders each sequence's task IDs, one sequence per element.
func sequenceIDs(sequences []sequence) []string {
	out := make([]string, 0, len(sequences))
	for _, seq := range sequences {
		ids := make([]string, 0, len(seq.Tasks))
		for _, task := range seq.Tasks {
			ids = append(ids, task.ID)
		}
		out = append(out, strings.Join(ids, ","))
	}
	return out
}

// TestGroupSequencesPairsEachReadWithItsPrecursor is the M6 regression: with
// several write tasks, each read runs in its own precursor's workspace, never
// behind the last write.
func TestGroupSequencesPairsEachReadWithItsPrecursor(t *testing.T) {
	tasks := []Task{
		{ID: "fam-01-read", Phase: PhaseRead, Precursor: "fam-01-write", Fixture: "/fixture"},
		{ID: "fam-01-write", Phase: PhaseWrite, Fixture: "/fixture"},
		{ID: "fam-02-read", Phase: PhaseRead, Precursor: "fam-02-write", Fixture: "/fixture"},
		{ID: "fam-02-write", Phase: PhaseWrite, Fixture: "/fixture"},
		{ID: "fam-03-write", Phase: PhaseWrite, Fixture: "/fixture"},
		{ID: "fam-01-extra", Precursor: "fam-01-write", Fixture: "/fixture"},
	}
	sequences, err := groupSequences(tasks)
	if err != nil {
		t.Fatalf("groupSequences: %v", err)
	}
	got := strings.Join(sequenceIDs(sequences), " | ")
	want := "fam-01-write,fam-01-read,fam-01-extra | fam-02-write,fam-02-read | fam-03-write"
	if got != want {
		t.Fatalf("sequences = %s, want %s", got, want)
	}
	for i, seq := range sequences {
		if seq.Index != i || !seq.GitInit {
			t.Fatalf("sequence %d: index %d, git init %t; want index %d and git init", i, seq.Index, seq.GitInit, i)
		}
	}
}

func TestGroupSequencesSingleWriteKeepsLegacyOrder(t *testing.T) {
	tasks := []Task{
		{ID: "read-1", Phase: PhaseRead, Fixture: "/fixture"},
		{ID: "plain", Fixture: "/fixture"},
		{ID: "write-1", Phase: PhaseWrite, Fixture: "/fixture"},
		{ID: "read-2", Phase: PhaseRead, Precursor: "write-1", Fixture: "/fixture"},
	}
	sequences, err := groupSequences(tasks)
	if err != nil {
		t.Fatalf("groupSequences: %v", err)
	}
	got := strings.Join(sequenceIDs(sequences), " | ")
	if got != "write-1,read-1,plain,read-2" {
		t.Fatalf("sequences = %s, want one sequence with the write first", got)
	}
	if !sequences[0].GitInit {
		t.Fatal("a sequence with a write-phase task must initialize a git workspace")
	}
}

func TestGroupSequencesRejectsAmbiguousPairing(t *testing.T) {
	cases := map[string]struct {
		tasks []Task
		want  string
	}{
		"missing precursor with two writes": {
			tasks: []Task{
				{ID: "write-1", Phase: PhaseWrite},
				{ID: "write-2", Phase: PhaseWrite},
				{ID: "read-1", Phase: PhaseRead},
			},
			want: "read-1 declares no precursor",
		},
		"unknown precursor": {
			tasks: []Task{
				{ID: "write-1", Phase: PhaseWrite},
				{ID: "read-1", Phase: PhaseRead, Precursor: "write-9"},
			},
			want: `precursor "write-9"`,
		},
		"precursor names a read": {
			tasks: []Task{
				{ID: "write-1", Phase: PhaseWrite},
				{ID: "write-2", Phase: PhaseWrite},
				{ID: "read-1", Phase: PhaseRead, Precursor: "write-1"},
				{ID: "read-2", Phase: PhaseRead, Precursor: "read-1"},
			},
			want: `precursor "read-1"`,
		},
		"precursor on a write": {
			tasks: []Task{
				{ID: "write-1", Phase: PhaseWrite},
				{ID: "write-2", Phase: PhaseWrite, Precursor: "write-1"},
			},
			want: "write task write-2 declares precursor",
		},
		"precursor with no write": {
			tasks: []Task{
				{ID: "read-1", Phase: PhaseRead, Precursor: "write-1"},
			},
			want: "no write task",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := groupSequences(tc.tasks)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
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

// TestRunSequenceNoCommitWritesLeavesTheWriteUncommitted pins the
// user-realistic mode: a verified write is neither committed nor
// reanchored, so the read runs on the write's uncommitted change.
func TestRunSequenceNoCommitWritesLeavesTheWriteUncommitted(t *testing.T) {
	fixture := sequenceFixture(t)
	cfg := Config{
		Binary:         "/bin/true",
		OutDir:         t.TempDir(),
		Repeats:        1,
		Arms:           []Arm{ArmWarm},
		NoCommitWrites: true,
		Tasks: []Task{
			{ID: "write-1", Prompt: "write", Check: "true", Fixture: fixture, Phase: PhaseWrite},
			{ID: "read-1", Prompt: "read", Check: "git status --porcelain | grep -q seed.txt", Fixture: fixture, Phase: PhaseRead},
		},
	}
	seam := func(_ context.Context, req ExecRequest) (ExecResult, error) {
		if req.Args[len(req.Args)-1] == "write" {
			if err := os.WriteFile(filepath.Join(req.Dir, "seed.txt"), []byte("written"), 0o644); err != nil {
				return ExecResult{}, err
			}
		}
		return mockPipelineResult("run-nc-1"), nil
	}
	r, err := NewRunner(cfg, seam)
	if err != nil {
		t.Fatal(err)
	}
	reanchored := false
	r.reanchorFn = func(context.Context, string, string, string, string, string) error {
		reanchored = true
		return nil
	}
	agg, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reanchored {
		t.Fatal("no-commit mode must not reanchor")
	}
	for _, m := range agg.ArmMetrics {
		if m.VerifiedCompletions != 2 {
			t.Fatalf("verified = %d, want 2: the read check sees the write's change still uncommitted", m.VerifiedCompletions)
		}
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

// TestArmContrastsCompareWarmJevWithWarm proves the aggregate compares
// warm-jev with warm, not only warm with cold.
func TestArmContrastsCompareWarmJevWithWarm(t *testing.T) {
	attempt := func(task string, arm Arm, ok bool, in int) Attempt {
		return Attempt{TaskID: task, Arm: arm, VerifierResult: ok, Totals: Totals{InputTokens: in}}
	}
	attempts := []Attempt{
		attempt("a", ArmCold, false, 900), attempt("a", ArmCold, false, 900),
		attempt("a", ArmWarm, false, 700), attempt("a", ArmWarm, true, 700),
		attempt("a", ArmWarmJev, true, 600), attempt("a", ArmWarmJev, true, 800),
		attempt("b", ArmCold, true, 500), attempt("b", ArmCold, true, 500),
		attempt("b", ArmWarm, true, 400), attempt("b", ArmWarm, true, 400),
		attempt("b", ArmWarmJev, false, 450), attempt("b", ArmWarmJev, true, 450),
	}
	contrasts := armContrasts([]Arm{ArmCold, ArmWarm, ArmWarmJev}, attempts, 200, 1)
	if len(contrasts) != 1 {
		t.Fatalf("contrasts = %d, want 1", len(contrasts))
	}
	c := contrasts[0]
	if c.Treatment != ArmWarmJev || c.Control != ArmWarm {
		t.Fatalf("contrast = %s - %s, want warm-jev - warm", c.Treatment, c.Control)
	}
	// a: warm 1/2, warm-jev 2/2, tokens 700 vs 700. b: warm 2/2,
	// warm-jev 1/2, tokens 400 vs 450.
	want := map[string][2]float64{"a": {0.5, 0}, "b": {-0.5, 50}}
	if len(c.PerTask) != 2 {
		t.Fatalf("per-task effects = %d, want 2", len(c.PerTask))
	}
	for _, e := range c.PerTask {
		w := want[e.TaskID]
		if e.DeltaSuccessRate != w[0] || e.DeltaInputTokensPerAttempt != w[1] {
			t.Fatalf("%s: delta success %v, delta tokens %v; want %v, %v", e.TaskID, e.DeltaSuccessRate, e.DeltaInputTokensPerAttempt, w[0], w[1])
		}
	}
	if c.Bootstrap.Tasks != 2 {
		t.Fatalf("bootstrap tasks = %d, want 2", c.Bootstrap.Tasks)
	}
	if got := armContrasts([]Arm{ArmCold, ArmWarm}, attempts, 200, 1); len(got) != 0 {
		t.Fatalf("contrasts without warm-jev = %d, want 0", len(got))
	}
	if got := armContrasts([]Arm{ArmCold, ArmWarmJev}, attempts, 200, 1); len(got) != 0 {
		t.Fatalf("contrasts without the warm base = %d, want 0", len(got))
	}
}

func TestArmContrastsCompareColdDerivedArmsWithCold(t *testing.T) {
	arms := []Arm{ArmCold, ArmColdWide, ArmLoop}
	attempts := []Attempt{
		attempt(ArmCold, "a", true), attempt(ArmColdWide, "a", true), attempt(ArmLoop, "a", true),
	}
	contrasts := armContrasts(arms, attempts, 20, 1)
	if len(contrasts) != 2 {
		t.Fatalf("contrasts = %d, want two cold-baseline contrasts", len(contrasts))
	}
	for _, contrast := range contrasts {
		if contrast.Control != ArmCold {
			t.Fatalf("contrast %s control = %s, want cold", contrast.Treatment, contrast.Control)
		}
	}
}

func TestNoMemoryPrefetchTurnsPrefetchOffInEveryArm(t *testing.T) {
	r := &Runner{cfg: Config{NoMemoryPrefetch: true}}
	for _, arm := range []Arm{ArmCold, ArmWarm, ArmWarmJev} {
		env := strings.Join(r.armEnv(arm), " ")
		if !strings.Contains(env, "SPLICE_MEMORY_PREFETCH=off") || strings.Contains(env, "SPLICE_MEMORY_PREFETCH=on") {
			t.Fatalf("%s env = %s", arm, env)
		}
	}
	if !strings.Contains(strings.Join((&Runner{}).armEnv(ArmWarm), " "), "SPLICE_MEMORY_PREFETCH=on") {
		t.Fatal("without the ablation the warm arm keeps prefetch on")
	}
}

func TestArmEnvOverridesReplaceOnlyKeysAnArmSets(t *testing.T) {
	r := &Runner{cfg: Config{ArmEnvOverrides: []string{"SPLICE_EVIDENCE_SUBSTITUTION=off", "SPLICE_DECISION_ADVISOR_PUSEFUL=0.5"}}}
	warm := strings.Join(r.armEnv(ArmWarm), " ")
	if !strings.Contains(warm, "SPLICE_EVIDENCE_SUBSTITUTION=off") || strings.Contains(warm, "SPLICE_EVIDENCE_SUBSTITUTION=on") {
		t.Fatalf("warm env = %s", warm)
	}
	if strings.Contains(warm, "PUSEFUL") {
		t.Fatalf("a key the arm does not set must not be added: %s", warm)
	}
	if jev := strings.Join(r.armEnv(ArmWarmJev), " "); !strings.Contains(jev, "SPLICE_DECISION_ADVISOR_PUSEFUL=0.5") {
		t.Fatalf("warm-jev env = %s", jev)
	}
}

func TestParseMemoryHealthReadsTheLastLine(t *testing.T) {
	raw := []byte(`{"delta":"[memory] delivered 1 file(s), 0 changed by the run, 3 requested in expansions that memory did not deliver"}
{"delta":"[memory] delivered 3 file(s), 1 changed by the run, 0 requested in expansions that memory did not deliver"}`)
	got := parseMemoryHealth(raw)
	if got == nil || *got != (MemoryHealth{Delivered: 3, Hits: 1, Misses: 0}) {
		t.Fatalf("memory = %+v", got)
	}
	if parseMemoryHealth([]byte("no memory here")) != nil {
		t.Fatal("no line means nil")
	}
	m := ComputeArmMetrics(ArmWarm, []Attempt{{Memory: got}, {Memory: &MemoryHealth{Delivered: 2, Hits: 2, Misses: 1}}, {}})
	if m.Memory != (MemoryHealth{Delivered: 5, Hits: 3, Misses: 1}) {
		t.Fatalf("arm memory = %+v", m.Memory)
	}
}

func TestArmEnvCanAblateMemoryProse(t *testing.T) {
	r := &Runner{cfg: Config{ArmEnvOverrides: []string{"SPLICE_EXEMPLAR_MODE=retrieve-no-prompt"}}}
	warm := strings.Join(r.armEnv(ArmWarm), " ")
	if !strings.Contains(warm, "SPLICE_EXEMPLAR_MODE=retrieve-no-prompt") || strings.Contains(warm, "SPLICE_EXEMPLAR_MODE=both") {
		t.Fatalf("warm env = %s", warm)
	}
	if !strings.Contains(warm, "SPLICE_MEMORY_PREFETCH=on") {
		t.Fatalf("the prose ablation must keep the prefetch: %s", warm)
	}
}

// TestUnreachableUpstreamRerunsTheSequence: a run whose requests never
// reached the model measures the network, not the arm. The runner waits and
// runs the sequence again, records only the rerun, and stops the campaign
// when the upstream stays unreachable.
func TestUnreachableUpstreamRerunsTheSequence(t *testing.T) {
	outage := ExecResult{Stdout: []byte(`{"code":"incomplete","message":"stream error: upstream unreachable: the model server could not connect to openrouter.ai (no such host)."}`)}
	var sessions []string
	newRunner := func(t *testing.T, failures int) (*Runner, *int, *int, string) {
		t.Helper()
		outDir := t.TempDir()
		calls, waits := 0, 0
		seam := func(_ context.Context, req ExecRequest) (ExecResult, error) {
			calls++
			for i, a := range req.Args {
				if a == "--init-session-id" {
					sessions = append(sessions, req.Args[i+1])
				}
			}
			if calls <= failures {
				return outage, nil
			}
			return telemetryFinal(t, "run-ok", intPtr(0)), nil
		}
		r, err := NewRunner(Config{
			Binary: "/bin/true", OutDir: outDir, Repeats: 1, Arms: []Arm{ArmCold},
			Tasks: []Task{{ID: "t1", Prompt: "do", Check: "true"}},
		}, seam)
		if err != nil {
			t.Fatal(err)
		}
		r.sleep = func(time.Duration) { waits++ }
		return r, &calls, &waits, outDir
	}

	r, calls, waits, outDir := newRunner(t, 2)
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if *calls != 3 || *waits != 2 {
		t.Fatalf("calls = %d waits = %d, want 3 and 2", *calls, *waits)
	}
	// Each rerun starts its own splice session: a reused id is refused.
	if len(sessions) != 3 || sessions[0] == sessions[1] || sessions[1] == sessions[2] {
		t.Fatalf("sessions = %v, want a new id on each rerun", sessions)
	}
	matches, _ := filepath.Glob(filepath.Join(outDir, "*", "attempt-t1-cold-r0.json"))
	if len(matches) != 1 {
		t.Fatalf("attempt files = %v", matches)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	var got Attempt
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.UpstreamUnreachable || got.RunID != "run-ok" {
		t.Fatalf("recorded attempt = %+v, want the rerun", got)
	}

	r, _, _, _ = newRunner(t, networkRetries+1)
	if _, err := r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "stayed unreachable") {
		t.Fatalf("err = %v, want the campaign to stop", err)
	}
}

func TestLoadTasksetRejectsADuplicateTaskID(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	task := `{"name":"cpl-01-read","prompt":"p","check":"true"}`
	for _, name := range []string{"cpl-01-read.json", "cpl-01-read 2.json"} {
		if err := os.WriteFile(filepath.Join(dir, "tasks", name), []byte(task), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadTaskset(dir); err == nil || !strings.Contains(err.Error(), "also declared by") {
		t.Fatalf("err = %v, want a duplicate-id error", err)
	}
}

// TestConnectionResetIsAnOutage: the P6 failure. A run that ends on a
// provider connection reset reruns its sequence; a run that ends on a model
// error does not.
func TestConnectionResetIsAnOutage(t *testing.T) {
	reset := []byte(`{"code":"incomplete","message":"repair: code_writer re-entry: stream error: provider stream error: read tcp 192.168.1.157:56347->104.18.3.115:443: read: connection reset by peer","recoverable":false,"type":"error"}`)
	if !endedOnConnectivity(reset) {
		t.Fatal("a connection reset must count as an outage")
	}
	stalled := []byte(`{"code":"incomplete","message":"stream error: provider stream error: stream idle timeout after 120s","type":"error"}`)
	if endedOnConnectivity(stalled) {
		t.Fatal("a stalled stream is a result, not an outage")
	}
	budget := []byte(`{"code":"incomplete","message":"repair: code_writer re-entry: context expansion budget exhausted while fulfilling the stage context request","type":"error"}`)
	if endedOnConnectivity(budget) {
		t.Fatal("a model or budget failure must not count as an outage")
	}
}

// TestSidecarSocketPathOverTheLimitRefusesToStart: the P6c/P6d fault. A run
// id long enough to push the sidecar socket past the platform limit must
// stop the campaign before it starts, not degrade every warm run.
func TestSidecarSocketPathOverTheLimitRefusesToStart(t *testing.T) {
	cfg := Config{
		Binary: "/bin/true", OutDir: t.TempDir(), Repeats: 5, Arms: []Arm{ArmCold, ArmWarm},
		Tasks:       []Task{{ID: "t1", Prompt: "do", Check: "true"}},
		Retention:   RetentionFresh,
		SidecarRoot: "/tmp/p6c-regression-20260927T224604Z-sidecar",
		RunID:       "p6c-regression-20260927T224604Z",
	}
	if _, err := NewRunner(cfg, nil); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("err = %v, want the socket path refusal", err)
	}
	cfg.RunID, cfg.SidecarRoot = "p6e", "/tmp/p6e-sc"
	if _, err := NewRunner(cfg, nil); err != nil {
		t.Fatalf("a short run id must start: %v", err)
	}
}

// TestMissingCredentialsStopTheCampaign: P6e. Once the key stopped
// resolving, every attempt failed on auth; the campaign must stop at the
// first one instead of recording the rest as wrong answers.
func TestMissingCredentialsStopTheCampaign(t *testing.T) {
	auth := ExecResult{Stdout: []byte(`{"code":"incomplete","message":"repeated unchanged stage failure (auth) in iterations 1 and 2: code_writer: stream error: auth error: your API key is missing or invalid","type":"error"}`)}
	calls := 0
	r, err := NewRunner(Config{
		Binary: "/bin/true", OutDir: t.TempDir(), Repeats: 3, Arms: []Arm{ArmCold},
		Tasks: []Task{{ID: "t1", Prompt: "do", Check: "true"}, {ID: "t2", Prompt: "do", Check: "true"}},
	}, func(context.Context, ExecRequest) (ExecResult, error) { calls++; return auth, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "missing or invalid API key") {
		t.Fatalf("err = %v, want the campaign to stop", err)
	}
	if calls != 1 {
		t.Fatalf("runs = %d, want 1 (stop at the first)", calls)
	}
}

// TestUnavailableMemoryStopsTheCampaign: P6g. With the sidecar binary gone,
// every warm run ran without memory; the campaign must stop at the first.
func TestUnavailableMemoryStopsTheCampaign(t *testing.T) {
	noMemory := ExecResult{Stdout: []byte(`{"message":"memory sidecar unavailable; running without memory injection: spawn splice-memd: no such file","type":"warning"}`)}
	calls := 0
	r, err := NewRunner(Config{
		Binary: "/bin/true", OutDir: t.TempDir(), Repeats: 2, Arms: []Arm{ArmWarm},
		Tasks: []Task{{ID: "t1", Prompt: "do", Check: "true"}},
	}, func(context.Context, ExecRequest) (ExecResult, error) { calls++; return noMemory, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "memory sidecar was unavailable") {
		t.Fatalf("err = %v, want the campaign to stop", err)
	}
	if calls != 1 {
		t.Fatalf("runs = %d, want 1", calls)
	}
}

// Every arm sets SPLICE_FLAGS explicitly, so an operator's ambient value
// cannot reach a measurement. Arms with no classifier treatment turn the
// classifier off. The warm-jev arm sets its one Jev value, with the
// classifier off, and no second entry for the same key.
func TestEveryArmSetsSpliceFlagsExplicitly(t *testing.T) {
	arms := []Arm{ArmCold, ArmColdWide, ArmWarm, ArmRetrievalOnly, ArmLoop, ArmWarmJev}
	for _, arm := range arms {
		var values []string
		for _, kv := range arm.Env() {
			if strings.HasPrefix(kv, "SPLICE_FLAGS=") {
				values = append(values, strings.TrimPrefix(kv, "SPLICE_FLAGS="))
			}
		}
		if len(values) != 1 {
			t.Fatalf("%s sets SPLICE_FLAGS %d times, want exactly once: %v", arm, len(values), values)
		}
		if arm == ArmWarmJev {
			if values[0] != "pipeline.decision_advisor=true,-pipeline.trajectory_classifier" {
				t.Fatalf("warm-jev SPLICE_FLAGS = %q, want the Jev value", values[0])
			}
		} else if values[0] != "-pipeline.trajectory_classifier" {
			t.Fatalf("%s SPLICE_FLAGS = %q, want the classifier off", arm, values[0])
		}
	}
}

// A SPLICE_FLAGS override from --arm-env replaces the arm's flags. It must not
// let the default classifier back into an arm: it adds the flag off unless the
// override already names the classifier.
func TestArmEnvSpliceFlagsOverrideKeepsClassifierOff(t *testing.T) {
	for _, tc := range []struct {
		override, want string
	}{
		{"SPLICE_FLAGS=pipeline.decision_advisor=true", "SPLICE_FLAGS=pipeline.decision_advisor=true,-pipeline.trajectory_classifier"},
		{"SPLICE_FLAGS=-pipeline.trajectory_classifier", "SPLICE_FLAGS=-pipeline.trajectory_classifier"},
		{"SPLICE_FLAGS=pipeline.trajectory_classifier", "SPLICE_FLAGS=pipeline.trajectory_classifier"},
		{"SPLICE_FLAGS=", "SPLICE_FLAGS=-pipeline.trajectory_classifier"},
	} {
		r := &Runner{cfg: Config{ArmEnvOverrides: []string{tc.override}}}
		env := r.armEnv(ArmWarm)
		found := false
		for _, kv := range env {
			if strings.HasPrefix(kv, "SPLICE_FLAGS=") {
				found = true
				if kv != tc.want {
					t.Fatalf("override %q gave %q, want %q", tc.override, kv, tc.want)
				}
			}
		}
		if !found {
			t.Fatalf("override %q: no SPLICE_FLAGS in %v", tc.override, env)
		}
	}
}

// TestMain lets the test binary act as a fake splice-memd: with
// WARMCOST_FAKE_MEMD=1 it serves /health on SPLICE_MEMD_SOCKET until SIGTERM.
func TestMain(m *testing.M) {
	if os.Getenv("WARMCOST_FAKE_MEMD") == "1" {
		os.Exit(serveFakeMemd(os.Getenv("SPLICE_MEMD_SOCKET")))
	}
	os.Exit(m.Run())
}

func serveFakeMemd(socket string) int {
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return 3
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	server := &http.Server{Handler: mux}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	_ = server.Serve(listener)
	return 0
}

// fakeSidecars records each start and stop of the runner's sidecar seam, and
// each exec with the socket it got.
type fakeSidecars struct {
	events []string
	live   map[string]bool
}

func (f *fakeSidecars) start(_ context.Context, socket string, _ []string) (func() error, error) {
	if f.live[socket] {
		return nil, fmt.Errorf("sidecar %s started twice", socket)
	}
	f.live[socket] = true
	f.events = append(f.events, "start "+filepath.Base(filepath.Dir(socket)))
	return func() error {
		if !f.live[socket] {
			return fmt.Errorf("sidecar %s stopped twice", socket)
		}
		delete(f.live, socket)
		f.events = append(f.events, "stop "+filepath.Base(filepath.Dir(socket)))
		return nil
	}, nil
}

func sidecarRunner(t *testing.T, retention RetentionMode, tasks []Task) (*Runner, *fakeSidecars) {
	t.Helper()
	root, err := os.MkdirTemp("", "wcsc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	fake := &fakeSidecars{live: map[string]bool{}}
	r, err := NewRunner(Config{
		RunID: "sc", Binary: "/bin/true", OutDir: t.TempDir(), Repeats: 1, Arms: []Arm{ArmWarm, ArmCold},
		Tasks: tasks, Retention: retention, SidecarRoot: root,
	}, func(_ context.Context, req ExecRequest) (ExecResult, error) {
		socket := envGetter(req.Env)("SPLICE_MEMD_SOCKET")
		event := "exec " + filepath.Base(filepath.Dir(socket))
		if fake.live[socket] {
			event += " (live)"
		}
		fake.events = append(fake.events, event)
		return mockPipelineResult("run"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r.startSidecarFn = fake.start
	return r, fake
}

// TestRunnerStopsTheSidecarsItStarted: with fresh retention each memory-arm
// sequence starts its own sidecar and the runner stops it when the sequence is
// done. The cold arm starts none. Before this, every attempt's splice-memd
// --serve kept running after the campaign.
func TestRunnerStopsTheSidecarsItStarted(t *testing.T) {
	r, fake := sidecarRunner(t, RetentionFresh, []Task{{ID: "t1", Prompt: "do", Check: "true"}, {ID: "t2", Prompt: "do", Check: "true"}})
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		"start fresh-wc-sc-seq00-warm-r0", "exec fresh-wc-sc-seq00-warm-r0 (live)", "stop fresh-wc-sc-seq00-warm-r0",
		"exec fresh-wc-sc-seq00-cold-r0",
		"start fresh-wc-sc-seq01-warm-r0", "exec fresh-wc-sc-seq01-warm-r0 (live)", "stop fresh-wc-sc-seq01-warm-r0",
		"exec fresh-wc-sc-seq01-cold-r0",
	}
	if strings.Join(fake.events, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(fake.events, "\n"), strings.Join(want, "\n"))
	}
	if len(fake.live) != 0 || len(r.sidecarStops) != 0 {
		t.Fatalf("sidecars still running: %v %v", fake.live, r.sidecarStops)
	}
	// A second cleanup does nothing.
	r.stopSidecars()
	if err := r.stopSidecar("fresh-wc-sc-seq00-warm-r0"); err != nil {
		t.Fatalf("second stop: %v", err)
	}
}

// TestRunnerStopsTheSharedSidecarOnError: a shared sidecar starts once, serves
// every warm attempt, and stops when Run returns, also on a harness error.
func TestRunnerStopsTheSharedSidecarOnError(t *testing.T) {
	r, fake := sidecarRunner(t, RetentionShared, []Task{{ID: "t1", Prompt: "do", Check: "true"}, {ID: "t2", Prompt: "do", Check: "true", Setup: "false"}})
	if _, err := r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "setup failed") {
		t.Fatalf("Run error = %v, want the setup failure", err)
	}
	starts, stops := 0, 0
	for _, event := range fake.events {
		starts += strings.Count(event, "start ")
		stops += strings.Count(event, "stop ")
	}
	if starts != 1 || stops != 1 || len(fake.live) != 0 {
		t.Fatalf("starts=%d stops=%d live=%v events=%v, want the shared sidecar started and stopped once", starts, stops, fake.live, fake.events)
	}
}

// TestStartMemdStopsTheDaemon runs the real start and stop against a fake
// daemon (this test binary). After stop the socket no longer answers, and a
// second stop returns at once.
func TestStartMemdStopsTheDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("", "wcm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "mem.sock")
	env := envWithOverrides(os.Environ(), "SPLICE_MEMD_BIN="+os.Args[0], "WARMCOST_FAKE_MEMD=1", "SPLICE_MEMD_SOCKET="+socket)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stop, err := startMemd(ctx, "/nonexistent/splice", socket, env)
	if err != nil {
		t.Fatalf("startMemd: %v", err)
	}
	if err := memd.NewClient(socket).Health(ctx); err != nil {
		t.Fatalf("health after start: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := memd.NewClient(socket).Health(ctx); err == nil {
		t.Fatal("the daemon still answers after stop")
	}
	begin := time.Now()
	if err := stop(); err != nil || time.Since(begin) > time.Second {
		t.Fatalf("second stop: err=%v after %s", err, time.Since(begin))
	}
}

// serveTraceSidecar serves /health and /trace/query on socket in this process,
// with the given traces for every repo root. It returns the stop function.
func serveTraceSidecar(t *testing.T, socket string, traces []schemas.RunOutcome) func() error {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/trace/query", func(w http.ResponseWriter, _ *http.Request) {
		type row struct {
			RunID     string             `json:"run_id"`
			SessionID string             `json:"session_id"`
			Payload   schemas.RunOutcome `json:"payload"`
		}
		var rows []row
		for _, trace := range traces {
			rows = append(rows, row{RunID: trace.RunID, SessionID: trace.SessionID, Payload: trace})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "traces": rows})
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	var once bool
	stop := func() error {
		if once {
			return nil
		}
		once = true
		return server.Close()
	}
	t.Cleanup(func() { _ = stop() })
	return stop
}

// TestRunnerExportsEachMemoryAttemptTrace: each memory-arm attempt writes its
// stored RunOutcome to <run dir>/traces/<session>-trace.json, and
// decision-replay --traces reads that directory with no sqlite step. An
// attempt with no stored trace names the reason.
func TestRunnerExportsEachMemoryAttemptTrace(t *testing.T) {
	r, _ := sidecarRunner(t, RetentionFresh, []Task{{ID: "t1", Prompt: "do", Check: "true"}, {ID: "t2", Prompt: "do", Check: "true"}})
	r.cfg.Arms = []Arm{ArmWarm}
	stored := schemas.RunOutcome{SchemaVersion: schemas.TraceSchemaVersion, RunID: "wc-sc-t1-warm-r0", SessionID: "wc-sc-t1-warm-r0",
		PassEvidence: []schemas.PassEvidence{{Iteration: 1, Builder: "pass"}}}
	other := schemas.RunOutcome{SchemaVersion: schemas.TraceSchemaVersion, RunID: "another-run"}
	r.startSidecarFn = func(_ context.Context, socket string, _ []string) (func() error, error) {
		return serveTraceSidecar(t, socket, []schemas.RunOutcome{other, stored}), nil
	}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	runDir := filepath.Join(r.cfg.OutDir, r.cfg.RunID)
	var first, second Attempt
	for name, into := range map[string]*Attempt{"attempt-t1-warm-r0.json": &first, "attempt-t2-warm-r0.json": &second} {
		data, err := os.ReadFile(filepath.Join(runDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, into); err != nil {
			t.Fatal(err)
		}
	}
	if first.Trace != filepath.Join("traces", "wc-sc-t1-warm-r0-trace.json") || first.TraceUnavailable != "" {
		t.Fatalf("t1 trace=%q unavailable=%q", first.Trace, first.TraceUnavailable)
	}
	if second.Trace != "" || second.TraceUnavailable != "no stored trace for this session" {
		t.Fatalf("t2 trace=%q unavailable=%q, want the reason", second.Trace, second.TraceUnavailable)
	}
	evidence, err := decisionreplay.LoadTraceEvidence([]string{filepath.Join(runDir, "traces")})
	if err != nil {
		t.Fatalf("decision-replay cannot read the exported traces: %v", err)
	}
	if evidence.Runs != 1 || evidence.Passes != 1 {
		t.Fatalf("trace evidence = %+v, want one run with one pass", evidence)
	}
}
