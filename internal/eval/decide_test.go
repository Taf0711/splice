package eval

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// spread divides total across n parts with an exact sum. The remainder lands on
// the last part, so the per-pair rows always reproduce the arm aggregate.
func spread(total, n int) []int {
	if n <= 0 {
		return nil
	}
	out := make([]int, n)
	base := total / n
	for i := range out {
		out[i] = base
	}
	out[n-1] += total - base*n
	return out
}

// in builds a DecisionInput whose per-pair rows reproduce the arm aggregates
// exactly, so the cost measures and the aggregates can never disagree.
func in(pairs, coldSucc, warmSucc, coldTok, warmTok, coldInt, warmInt int) DecisionInput {
	coldTokens := spread(coldTok, coldSucc)
	warmTokens := spread(warmTok, warmSucc)
	matched := coldSucc
	if warmSucc < matched {
		matched = warmSucc
	}

	tasks := make([]TaskPair, 0, pairs)
	for i := 0; i < matched; i++ {
		tasks = append(tasks, TaskPair{
			Name:        fmt.Sprintf("matched-%d", i),
			ColdSuccess: true,
			WarmSuccess: true,
			ColdTokens:  coldTokens[i],
			WarmTokens:  warmTokens[i],
		})
	}
	for i := matched; i < coldSucc; i++ {
		tasks = append(tasks, TaskPair{Name: fmt.Sprintf("cold-only-%d", i), ColdSuccess: true, ColdTokens: coldTokens[i]})
	}
	for i := matched; i < warmSucc; i++ {
		tasks = append(tasks, TaskPair{Name: fmt.Sprintf("warm-only-%d", i), WarmSuccess: true, WarmTokens: warmTokens[i]})
	}

	return DecisionInput{
		Pairs: pairs,
		Cold:  ArmStats{Successes: coldSucc, Tokens: coldTok, WeightedInterventions: coldInt},
		Warm:  ArmStats{Successes: warmSucc, Tokens: warmTok, WeightedInterventions: warmInt},
		Tasks: tasks,
	}
}

// armStats derives arm aggregates from per-pair rows, the same way the harness
// accumulates them.
func armStats(tasks []TaskPair) (ArmStats, ArmStats) {
	var cold, warm ArmStats
	for _, task := range tasks {
		if task.ColdSuccess {
			cold.Successes++
		}
		if task.WarmSuccess {
			warm.Successes++
		}
		cold.Tokens += task.ColdTokens
		warm.Tokens += task.WarmTokens
	}
	return cold, warm
}

func TestDecideEveryGate(t *testing.T) {
	cases := []struct {
		name    string
		input   DecisionInput
		verdict string
		reason  string
	}{
		{
			name:    "below floor inconclusive",
			input:   in(9, 5, 5, 1000, 800, 0, 0),
			verdict: VerdictInconclusive,
			reason:  "insufficient pairs: 9/10",
		},
		{
			name:    "warm success drop is regression",
			input:   in(10, 8, 7, 1000, 700, 0, 0),
			verdict: VerdictRegression,
			reason:  "warm successes (7) below cold (8)",
		},
		{
			name:    "cost margin not met inconclusive",
			input:   in(10, 8, 8, 1000, 920, 0, 0),
			verdict: VerdictInconclusive,
			reason:  "matched-pair cost not cheaper than the 10% margin (warm 920 vs cold 1000 over 8 matched pairs)",
		},
		{
			name:    "needier inconclusive",
			input:   in(10, 8, 8, 1000, 600, 2, 5),
			verdict: VerdictInconclusive,
			reason:  "cheaper but needier",
		},
		{
			name:    "conclusive warm wins",
			input:   in(10, 8, 8, 1000, 600, 0, 0),
			verdict: VerdictConclusive,
			reason:  "warm wins: matched-pair cost 40.0% lower at equal-or-better success (matched 1000 to 600 over 8 pairs, total 1000 to 600, mean per success 125 to 75, median per success 125 to 75)",
		},
		{
			name:    "tie goes cold",
			input:   in(10, 8, 8, 1000, 1000, 0, 0),
			verdict: VerdictInconclusive,
			reason:  "matched-pair cost not cheaper than the 10% margin (warm 1000 vs cold 1000 over 8 matched pairs)",
		},
		{
			name:    "exactly 10 percent cheaper is inconclusive",
			input:   in(10, 10, 10, 1000, 900, 0, 0),
			verdict: VerdictInconclusive,
			reason:  "matched-pair cost not cheaper than the 10% margin (warm 900 vs cold 1000 over 10 matched pairs)",
		},
		{
			name:    "just over 10 percent cheaper is conclusive",
			input:   in(10, 10, 10, 1000, 899, 0, 0),
			verdict: VerdictConclusive,
			reason:  "warm wins: matched-pair cost 10.1% lower at equal-or-better success (matched 1000 to 899 over 10 pairs, total 1000 to 899, mean per success 100 to 90, median per success 100 to 89)",
		},
		{
			name:    "zero success cold arm is inconclusive",
			input:   in(10, 0, 5, 1000, 500, 0, 0),
			verdict: VerdictInconclusive,
			reason:  "cold arm had 0 successes; cost comparison undefined (10 pairs)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Decide(tc.input)
			if d.Verdict != tc.verdict {
				t.Fatalf("verdict = %q, want %q", d.Verdict, tc.verdict)
			}
			if d.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", d.Reason, tc.reason)
			}
			if d.Verdict == VerdictConclusive {
				if !strings.HasPrefix(d.Reason, "warm wins: ") {
					t.Fatalf("conclusive reason = %q, want warm-wins prefix", d.Reason)
				}
				if !strings.Contains(d.Reason, "matched-pair cost") {
					t.Fatalf("conclusive reason = %q, want the matched-pair measure named", d.Reason)
				}
			}
		})
	}
}

func TestDecideConclusiveReasonCarriesAllFourMeasures(t *testing.T) {
	d := Decide(in(10, 10, 10, 1000, 500, 0, 0))
	if d.Verdict != VerdictConclusive {
		t.Fatalf("verdict = %q, want conclusive", d.Verdict)
	}
	want := "warm wins: matched-pair cost 50.0% lower at equal-or-better success (matched 1000 to 500 over 10 pairs, total 1000 to 500, mean per success 100 to 50, median per success 100 to 50)"
	if d.Reason != want {
		t.Fatalf("reason = %q, want %q", d.Reason, want)
	}
}

func TestDecideGateTrail(t *testing.T) {
	d := Decide(in(10, 8, 8, 1000, 600, 0, 0))
	if len(d.Gates) != 4 {
		t.Fatalf("gates = %d, want 4", len(d.Gates))
	}
	for _, gate := range d.Gates {
		if !gate.Passed {
			t.Fatalf("gate %s unexpectedly failed: %s", gate.Name, gate.Reason)
		}
	}
}

// TestDecideEmptyTasksAtCostGateIsInconclusive pins the fail-loud guard. A
// caller that supplies only arm aggregates cannot support a like-for-like cost
// decision, and the refusal names the missing data instead of defaulting to the
// denominator-sensitive ratio.
func TestDecideEmptyTasksAtCostGateIsInconclusive(t *testing.T) {
	d := Decide(DecisionInput{
		Pairs: 10,
		Cold:  ArmStats{Successes: 8, Tokens: 1000},
		Warm:  ArmStats{Successes: 8, Tokens: 600},
	})
	if d.Verdict != VerdictInconclusive {
		t.Fatalf("verdict = %q, want inconclusive", d.Verdict)
	}
	if !strings.Contains(d.Reason, "no task succeeded in both arms") {
		t.Fatalf("reason = %q, want the missing matched pairs named", d.Reason)
	}
	if d.Cost.MatchedPairs != 0 {
		t.Fatalf("matched pairs = %d, want 0", d.Cost.MatchedPairs)
	}
}

// run7Pairs is the run 7 paired result verbatim from
// ~/Documents/splice-eval-taskset-run7/report/pe-report.md.
func run7Pairs() []TaskPair {
	return []TaskPair{
		{Name: "audit-ring-buffer", ColdSuccess: false, WarmSuccess: false, ColdTokens: 5127, WarmTokens: 5248},
		{Name: "capacity-eviction", ColdSuccess: true, WarmSuccess: true, ColdTokens: 5921, WarmTokens: 5906},
		{Name: "conflict-on-recreate", ColdSuccess: false, WarmSuccess: false, ColdTokens: 5682, WarmTokens: 5121},
		{Name: "count-by-user", ColdSuccess: true, WarmSuccess: true, ColdTokens: 4463, WarmTokens: 4588},
		{Name: "delete-session-endpoint", ColdSuccess: false, WarmSuccess: false, ColdTokens: 3266, WarmTokens: 3102},
		{Name: "healthz-uptime", ColdSuccess: true, WarmSuccess: true, ColdTokens: 5119, WarmTokens: 5170},
		{Name: "list-sessions-sorted", ColdSuccess: true, WarmSuccess: true, ColdTokens: 4814, WarmTokens: 4864},
		{Name: "parse-ttl-from-env", ColdSuccess: true, WarmSuccess: true, ColdTokens: 5520, WarmTokens: 5435},
		{Name: "per-session-ttl", ColdSuccess: false, WarmSuccess: false, ColdTokens: 7235, WarmTokens: 7407},
		{Name: "session-json-tags", ColdSuccess: false, WarmSuccess: true, ColdTokens: 34159, WarmTokens: 5541},
		{Name: "snapshot-persistence", ColdSuccess: false, WarmSuccess: false, ColdTokens: 56449, WarmTokens: 23607},
		{Name: "token-auth-middleware", ColdSuccess: false, WarmSuccess: false, ColdTokens: 62482, WarmTokens: 60530},
	}
}

// TestDecideRun7ShapeRefusesTokensPerSuccessWin is the regression this fix
// exists for. Run 7's headline was "43.2% fewer tokens at equal success", which
// is total tokens divided by successes (200237/5 vs 136519/6). The five tasks
// both arms passed cost 25837 cold and 25963 warm, which is a 0.5 percent
// REGRESSION. The corrected gate must refuse to certify it.
func TestDecideRun7ShapeRefusesTokensPerSuccessWin(t *testing.T) {
	tasks := run7Pairs()
	cold, warm := armStats(tasks)

	// Replay the old headline first, so the test fails loudly if the recorded
	// totals are ever edited to make the point for us.
	coldPer := float64(cold.Tokens) / float64(cold.Successes)
	warmPer := float64(warm.Tokens) / float64(warm.Successes)
	pct := (1 - warmPer/coldPer) * 100
	if got := math.Round(pct*10) / 10; got != 43.2 {
		t.Fatalf("replayed headline = %.1f%%, want 43.2%%", got)
	}

	d := Decide(DecisionInput{Pairs: len(tasks), Cold: cold, Warm: warm, Tasks: tasks})
	if d.Verdict != VerdictInconclusive {
		t.Fatalf("verdict = %q, want inconclusive", d.Verdict)
	}
	if !strings.Contains(d.Reason, "matched-pair cost not cheaper") {
		t.Fatalf("reason = %q, want the matched-pair refusal", d.Reason)
	}

	if d.Cost.MatchedPairs != 5 {
		t.Fatalf("matched pairs = %d, want 5", d.Cost.MatchedPairs)
	}
	if d.Cost.MatchedCold != 25837 || d.Cost.MatchedWarm != 25963 {
		t.Fatalf("matched tokens = %d/%d, want 25837/25963", d.Cost.MatchedCold, d.Cost.MatchedWarm)
	}
	if d.Cost.TotalCold != 200237 || d.Cost.TotalWarm != 136519 {
		t.Fatalf("totals = %d/%d, want 200237/136519", d.Cost.TotalCold, d.Cost.TotalWarm)
	}
	if d.Cost.MedianColdPerSuccess != 5119 {
		t.Fatalf("median cold = %v, want 5119", d.Cost.MedianColdPerSuccess)
	}
	if d.Cost.MedianWarmPerSuccess != 5302.5 {
		t.Fatalf("median warm = %v, want 5302.5", d.Cost.MedianWarmPerSuccess)
	}
	if d.Cost.MatchedRatio() <= 1 {
		t.Fatalf("matched ratio = %v, want > 1 (warm more expensive)", d.Cost.MatchedRatio())
	}
}

// TestCostMeasuresMedianHandlesEvenAndOddSamples pins the median helper at its
// boundary, because the median is a reported measure and a silent off-by-one
// would misreport typical cost.
func TestCostMeasuresMedianHandlesEvenAndOddSamples(t *testing.T) {
	cases := []struct {
		name   string
		values []int
		want   float64
	}{
		{name: "empty", values: nil, want: 0},
		{name: "single", values: []int{7}, want: 7},
		{name: "odd", values: []int{9, 1, 5}, want: 5},
		{name: "even", values: []int{10, 1, 4, 9}, want: 6.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := median(tc.values); got != tc.want {
				t.Fatalf("median(%v) = %v, want %v", tc.values, got, tc.want)
			}
		})
	}
}

// TestMeasureCostDoesNotMutateInput pins that the measure pass copies the
// per-arm success samples instead of sorting the caller's rows.
func TestMeasureCostDoesNotMutateInput(t *testing.T) {
	tasks := []TaskPair{
		{Name: "a", ColdSuccess: true, WarmSuccess: true, ColdTokens: 30, WarmTokens: 10},
		{Name: "b", ColdSuccess: true, WarmSuccess: true, ColdTokens: 10, WarmTokens: 30},
	}
	before := append([]TaskPair(nil), tasks...)
	_ = measureCost(tasks, ArmStats{Successes: 2, Tokens: 40}, ArmStats{Successes: 2, Tokens: 40})
	for i := range tasks {
		if tasks[i] != before[i] {
			t.Fatalf("task %d mutated: %+v, want %+v", i, tasks[i], before[i])
		}
	}
}
