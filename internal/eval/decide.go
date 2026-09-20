// Package eval is the paired-eval harness: the only causal instrument in the
// adaptive-harness system. The store (traces) proposes via correlation; the
// harness disposes via controlled arms. It is release-cadence, never CI, and
// never hooks into normal runs.
package eval

import (
	"fmt"
	"sort"
)

// Verdict vocabulary. Every non-conclusive verdict resolves to the incumbent
// (cold): the burden of proof is on warmth.
const (
	VerdictConclusive   = "conclusive"
	VerdictInconclusive = "inconclusive"
	VerdictRegression   = "regression"
)

// Named thresholds. These are printed in the report so a decision is auditable
// against its constants.
const (
	// PairFloor is the minimum paired-task count before any decision is made.
	PairFloor = 10
	// CostMargin is the required cost improvement: warm must be strictly
	// cheaper than cold * CostMargin (i.e. more than a 10% saving) per success.
	CostMargin = 0.90
	// SuccessTolerance is the depth-guard tolerance: any drop in warm successes
	// is a regression (tolerance 0).
	SuccessTolerance = 0
)

// ArmStats is the aggregated outcome of one arm over the whole task set.
type ArmStats struct {
	Successes             int
	Tokens                int // total tokens (input+output) across all tasks
	WeightedInterventions int // sum of intervention weights across all tasks
}

// CostMeasures separates the cost comparisons that one total-tokens-per-success
// ratio conflates. Each measure is reported on its own.
//
// A lone per-success ratio can improve only because the denominator changed.
// An arm that turns one failure into a success, or that fails more cheaply on
// a task it never passed, moves the ratio without making the same work cheaper.
// Run 7 is the counterexample: its headline was 43.2 percent, while the tasks
// both arms passed cost 0.5 percent MORE warm.
type CostMeasures struct {
	// TotalCold and TotalWarm are raw arm spend over every pair, failures
	// included. Report absolute spend, never a ratio alone.
	TotalCold int `json:"total_cold"`
	TotalWarm int `json:"total_warm"`

	// Mean per-success cost is total tokens divided by that arm's successes.
	// Keep it for continuity. It no longer decides the gate.
	MeanColdPerSuccess float64 `json:"mean_cold_per_success"`
	MeanWarmPerSuccess float64 `json:"mean_warm_per_success"`

	// Median per-success cost covers each arm's successful tasks. One aborted
	// task can dominate a mean. The median says what a typical success cost.
	MedianColdPerSuccess float64 `json:"median_cold_per_success"`
	MedianWarmPerSuccess float64 `json:"median_warm_per_success"`

	// MatchedPairs counts tasks where BOTH arms succeeded. This is the only
	// like-for-like comparison, and the cost gate decides on it.
	MatchedPairs int `json:"matched_pairs"`
	MatchedCold  int `json:"matched_cold_tokens"`
	MatchedWarm  int `json:"matched_warm_tokens"`
}

// ratio returns warm divided by cold. A zero cold value returns 0, which the
// caller reads as undefined.
func ratio(warm, cold float64) float64 {
	if cold == 0 {
		return 0
	}
	return warm / cold
}

// TotalRatio is warm total spend over cold total spend.
func (c CostMeasures) TotalRatio() float64 { return ratio(float64(c.TotalWarm), float64(c.TotalCold)) }

// MeanRatio is warm mean per-success cost over cold.
func (c CostMeasures) MeanRatio() float64 { return ratio(c.MeanWarmPerSuccess, c.MeanColdPerSuccess) }

// MedianRatio is warm median per-success cost over cold.
func (c CostMeasures) MedianRatio() float64 {
	return ratio(c.MedianWarmPerSuccess, c.MedianColdPerSuccess)
}

// MatchedRatio is warm matched-pair spend over cold. This ratio decides the
// cost gate.
func (c CostMeasures) MatchedRatio() float64 {
	return ratio(float64(c.MatchedWarm), float64(c.MatchedCold))
}

// measureCost computes every cost measure from the per-pair rows plus the arm
// aggregates.
func measureCost(tasks []TaskPair, cold, warm ArmStats) CostMeasures {
	m := CostMeasures{TotalCold: cold.Tokens, TotalWarm: warm.Tokens}
	if cold.Successes > 0 {
		m.MeanColdPerSuccess = float64(cold.Tokens) / float64(cold.Successes)
	}
	if warm.Successes > 0 {
		m.MeanWarmPerSuccess = float64(warm.Tokens) / float64(warm.Successes)
	}

	coldSuccess := make([]int, 0, cold.Successes)
	warmSuccess := make([]int, 0, warm.Successes)
	for _, task := range tasks {
		if task.ColdSuccess {
			coldSuccess = append(coldSuccess, task.ColdTokens)
		}
		if task.WarmSuccess {
			warmSuccess = append(warmSuccess, task.WarmTokens)
		}
		if task.ColdSuccess && task.WarmSuccess {
			m.MatchedPairs++
			m.MatchedCold += task.ColdTokens
			m.MatchedWarm += task.WarmTokens
		}
	}
	m.MedianColdPerSuccess = median(coldSuccess)
	m.MedianWarmPerSuccess = median(warmSuccess)
	return m
}

// median returns the middle value of an integer sample. An empty sample is 0.
func median(values []int) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return float64(sorted[mid])
	}
	return (float64(sorted[mid-1]) + float64(sorted[mid])) / 2
}

// conclusiveReason states all four cost measures so a reader never has to
// trust the headline alone.
func conclusiveReason(c CostMeasures) string {
	return fmt.Sprintf(
		"warm wins: matched-pair cost %.1f%% lower at equal-or-better success (matched %d to %d over %d pairs, total %d to %d, mean per success %.0f to %.0f, median per success %.0f to %.0f)",
		(1-c.MatchedRatio())*100,
		c.MatchedCold, c.MatchedWarm, c.MatchedPairs,
		c.TotalCold, c.TotalWarm,
		c.MeanColdPerSuccess, c.MeanWarmPerSuccess,
		c.MedianColdPerSuccess, c.MedianWarmPerSuccess)
}

// DecisionInput is the paired result handed to the decision gates.
type DecisionInput struct {
	Pairs int
	Cold  ArmStats
	Warm  ArmStats
	// Tasks carries the per-pair rows. The cost gate needs them: total tokens
	// per success alone cannot separate a real saving from a denominator
	// change. An empty slice at the cost gate is named in the refusal reason,
	// never silently defaulted.
	Tasks []TaskPair
}

// GateResult is one gate's outcome in the lexicographic trail.
type GateResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Reason string `json:"reason,omitempty"`
}

// Decision is the lexicographic gate outcome.
type Decision struct {
	Verdict string       `json:"verdict"`
	Reason  string       `json:"reason"`
	Gates   []GateResult `json:"gates"`
	// Cost carries every cost measure, so a reader can audit the verdict
	// against the numbers instead of trusting the reason line.
	Cost CostMeasures `json:"cost"`
}

// Decide applies the lexicographic gates over paired arm results. GATE 0
// evidence, GATE 1 success (depth guard), GATE 2 cost margin, GATE 3 burden;
// otherwise conclusive. Ties resolve to the incumbent (cold) by never being
// conclusive.
func Decide(in DecisionInput) Decision {
	gates := make([]GateResult, 0, 4)

	// GATE 0: evidence.
	if in.Pairs < PairFloor {
		gates = append(gates, GateResult{Name: "evidence", Passed: false, Reason: fmt.Sprintf("%d/%d pairs", in.Pairs, PairFloor)})
		return Decision{Verdict: VerdictInconclusive, Reason: fmt.Sprintf("insufficient pairs: %d/%d", in.Pairs, PairFloor), Gates: gates}
	}
	gates = append(gates, GateResult{Name: "evidence", Passed: true})

	// GATE 1: success (depth guard, tolerance 0).
	if in.Warm.Successes < in.Cold.Successes {
		gates = append(gates, GateResult{Name: "success", Passed: false, Reason: fmt.Sprintf("warm %d < cold %d", in.Warm.Successes, in.Cold.Successes)})
		return Decision{Verdict: VerdictRegression, Reason: fmt.Sprintf("warm successes (%d) below cold (%d)", in.Warm.Successes, in.Cold.Successes), Gates: gates}
	}
	gates = append(gates, GateResult{Name: "success", Passed: true})

	// GATE 2: cost margin, decided on matched-success pairs. Total tokens per
	// success is reported but never decides: it can improve only because the
	// denominator changed. Run 7 is the counterexample.
	cost := measureCost(in.Tasks, in.Cold, in.Warm)

	// Division-by-zero guard: a cold arm with no successes cannot establish a
	// cost comparison. Pinned as inconclusive.
	if in.Cold.Successes == 0 {
		gates = append(gates, GateResult{Name: "cost", Passed: false, Reason: "cold arm had 0 successes"})
		return Decision{Verdict: VerdictInconclusive, Reason: fmt.Sprintf("cold arm had 0 successes; cost comparison undefined (%d pairs)", in.Pairs), Gates: gates, Cost: cost}
	}
	if cost.MatchedPairs == 0 {
		gates = append(gates, GateResult{Name: "cost", Passed: false, Reason: "no matched-success pairs"})
		return Decision{Verdict: VerdictInconclusive, Reason: fmt.Sprintf("no task succeeded in both arms; like-for-like cost is undefined (%d pairs)", in.Pairs), Gates: gates, Cost: cost}
	}
	if float64(cost.MatchedWarm) >= float64(cost.MatchedCold)*CostMargin {
		gates = append(gates, GateResult{Name: "cost", Passed: false, Reason: "matched-pair cost not cheaper than the 10% margin"})
		return Decision{Verdict: VerdictInconclusive, Reason: fmt.Sprintf("matched-pair cost not cheaper than the 10%% margin (warm %d vs cold %d over %d matched pairs)", cost.MatchedWarm, cost.MatchedCold, cost.MatchedPairs), Gates: gates, Cost: cost}
	}
	gates = append(gates, GateResult{Name: "cost", Passed: true})

	// GATE 3: burden.
	if in.Warm.WeightedInterventions > in.Cold.WeightedInterventions {
		gates = append(gates, GateResult{Name: "burden", Passed: false, Reason: "cheaper but needier"})
		return Decision{Verdict: VerdictInconclusive, Reason: "cheaper but needier", Gates: gates, Cost: cost}
	}
	gates = append(gates, GateResult{Name: "burden", Passed: true})

	return Decision{Verdict: VerdictConclusive, Reason: conclusiveReason(cost), Gates: gates, Cost: cost}
}
