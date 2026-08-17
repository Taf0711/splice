// Package eval is the paired-eval harness: the only causal instrument in the
// adaptive-harness system. The store (traces) proposes via correlation; the
// harness disposes via controlled arms. It is release-cadence, never CI, and
// never hooks into normal runs.
package eval

import (
	"fmt"
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

// DecisionInput is the paired result handed to the decision gates.
type DecisionInput struct {
	Pairs int
	Cold  ArmStats
	Warm  ArmStats
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

	// Division-by-zero guard: a cold arm with no successes cannot establish a
	// cost-per-success ratio. Pinned as inconclusive.
	if in.Cold.Successes == 0 {
		gates = append(gates, GateResult{Name: "cost", Passed: false, Reason: "cold arm had 0 successes"})
		return Decision{Verdict: VerdictInconclusive, Reason: fmt.Sprintf("cold arm had 0 successes; cost comparison undefined (%d pairs)", in.Pairs), Gates: gates}
	}

	coldPer := float64(in.Cold.Tokens) / float64(in.Cold.Successes)
	warmPer := float64(in.Warm.Tokens) / float64(in.Warm.Successes)

	// GATE 2: cost margin.
	if warmPer >= coldPer*CostMargin {
		gates = append(gates, GateResult{Name: "cost", Passed: false, Reason: "not cheaper than the 10% margin"})
		return Decision{Verdict: VerdictInconclusive, Reason: "not cheaper than the 10% margin", Gates: gates}
	}
	gates = append(gates, GateResult{Name: "cost", Passed: true})

	// GATE 3: burden.
	if in.Warm.WeightedInterventions > in.Cold.WeightedInterventions {
		gates = append(gates, GateResult{Name: "burden", Passed: false, Reason: "cheaper but needier"})
		return Decision{Verdict: VerdictInconclusive, Reason: "cheaper but needier", Gates: gates}
	}
	gates = append(gates, GateResult{Name: "burden", Passed: true})

	pct := (1 - warmPer/coldPer) * 100
	return Decision{Verdict: VerdictConclusive, Reason: fmt.Sprintf("warm wins: %.1f%% fewer tokens at equal success", pct), Gates: gates}
}
