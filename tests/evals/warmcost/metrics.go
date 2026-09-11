package warmcost

import (
	"math"
	"math/rand"
	"sort"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// ArmMetrics aggregates one arm over all tasks and repeats.
type ArmMetrics struct {
	Arm                            Arm     `json:"arm"`
	Attempts                       int     `json:"attempts"`
	VerifiedCompletions            int     `json:"verified_completions"`
	FailedAttempts                 int     `json:"failed_attempts"`
	PartialAttempts                int     `json:"partial_attempts"`
	Requests                       int     `json:"requests"`
	RequestsPerVerifiedCompletion  float64 `json:"requests_per_verified_completion"`
	BilledUSD                      float64 `json:"billed_usd"`
	BilledUSDPerAttempt            float64 `json:"billed_usd_per_attempt"`
	BilledUSDPerVerifiedCompletion float64 `json:"billed_usd_per_verified_completion"`
	InputTokens                    int     `json:"input_tokens"`
	OutputTokens                   int     `json:"output_tokens"`
	CachedTokens                   int     `json:"cached_input_tokens"`
	CacheWriteTokens               int     `json:"cache_write_tokens"`
	ReasoningTokens                int     `json:"reasoning_tokens"`
	ExpansionRequests              int     `json:"expansion_requests"`
	RoundShare                     float64 `json:"round_share"`
	CacheShare                     float64 `json:"cache_share"`
	CoverageComplete               bool    `json:"coverage_complete"`
}

// ComputeArmMetrics aggregates the attempts of one arm.
func ComputeArmMetrics(arm Arm, attempts []Attempt) ArmMetrics {
	m := ArmMetrics{Arm: arm, CoverageComplete: true}
	for _, a := range attempts {
		m.Attempts++
		if a.VerifierResult {
			m.VerifiedCompletions++
		} else {
			m.FailedAttempts++
		}
		if !a.CompleteCoverage() {
			m.PartialAttempts++
			m.CoverageComplete = false
		}
		m.Requests += len(a.Requests)
		m.InputTokens += a.Totals.InputTokens
		m.OutputTokens += a.Totals.OutputTokens
		m.CachedTokens += a.Totals.CachedTokens
		m.CacheWriteTokens += a.Totals.CacheWrite
		m.ReasoningTokens += a.Totals.Reasoning
		m.BilledUSD += a.Totals.BilledUSD
		for _, r := range a.Requests {
			if r.SpendSource == schemas.SpendSourceExpansion {
				m.ExpansionRequests++
			}
		}
	}
	if m.Attempts > 0 {
		m.BilledUSDPerAttempt = m.BilledUSD / float64(m.Attempts)
	}
	if m.VerifiedCompletions > 0 {
		m.RequestsPerVerifiedCompletion = float64(m.Requests) / float64(m.VerifiedCompletions)
		m.BilledUSDPerVerifiedCompletion = m.BilledUSD / float64(m.VerifiedCompletions)
	}
	if m.Requests > 0 {
		m.RoundShare = float64(m.ExpansionRequests) / float64(m.Requests)
	}
	if m.InputTokens > 0 {
		m.CacheShare = float64(m.CachedTokens) / float64(m.InputTokens)
	}
	return m
}

// Decomposition splits the warm-minus-cold billed cost into a round channel
// and a payload channel. The cache channel is reported in tokens because the
// ledger carries no per-token cache price, so a USD cache split would be
// fabricated. The two USD parts sum exactly to the total delta.
type Decomposition struct {
	TotalDeltaUSD     float64 `json:"total_delta_usd"`
	RoundChannelUSD   float64 `json:"round_channel_usd"`
	PayloadChannelUSD float64 `json:"payload_channel_usd"`
	CacheTokensDelta  int     `json:"cache_tokens_delta"`
	CacheChannelNote  string  `json:"cache_channel_note"`
}

// Decompose computes the cost channels from one cold and one warm arm.
func Decompose(cold, warm ArmMetrics) Decomposition {
	nCold, nWarm := cold.Requests, warm.Requests
	var avgCold, avgWarm float64
	if nCold > 0 {
		avgCold = cold.BilledUSD / float64(nCold)
	}
	if nWarm > 0 {
		avgWarm = warm.BilledUSD / float64(nWarm)
	}
	round := float64(nWarm-nCold) * avgCold
	payload := float64(nWarm) * (avgWarm - avgCold)
	return Decomposition{
		TotalDeltaUSD:     warm.BilledUSD - cold.BilledUSD,
		RoundChannelUSD:   round,
		PayloadChannelUSD: payload,
		CacheTokensDelta: (warm.CachedTokens + warm.CacheWriteTokens) -
			(cold.CachedTokens + cold.CacheWriteTokens),
		CacheChannelNote: "ledger carries cache-read and cache-write tokens but no per-token cache price, so the cache channel is reported in tokens and is not folded into the USD split",
	}
}

// TaskEffect is one task's paired summary over repeats.
type TaskEffect struct {
	TaskID                   string  `json:"task_id"`
	ColdBilledUSDPerAttempt  float64 `json:"cold_billed_usd_per_attempt"`
	WarmBilledUSDPerAttempt  float64 `json:"warm_billed_usd_per_attempt"`
	DeltaBilledUSDPerAttempt float64 `json:"delta_billed_usd_per_attempt"`
	ColdRequestsPerAttempt   float64 `json:"cold_requests_per_attempt"`
	WarmRequestsPerAttempt   float64 `json:"warm_requests_per_attempt"`
	DeltaRequestsPerAttempt  float64 `json:"delta_requests_per_attempt"`
	ColdSuccessRate          float64 `json:"cold_success_rate"`
	WarmSuccessRate          float64 `json:"warm_success_rate"`
	DeltaSuccessRate         float64 `json:"delta_success_rate"`
}

// ComputeTaskEffects builds one paired effect per task. Tasks missing an arm
// are skipped and named by the caller.
func ComputeTaskEffects(attempts []Attempt, cold, warm Arm) ([]TaskEffect, []string) {
	byTask := map[string]map[Arm][]Attempt{}
	for _, a := range attempts {
		if byTask[a.TaskID] == nil {
			byTask[a.TaskID] = map[Arm][]Attempt{}
		}
		byTask[a.TaskID][a.Arm] = append(byTask[a.TaskID][a.Arm], a)
	}
	ids := make([]string, 0, len(byTask))
	for id := range byTask {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []TaskEffect
	var skipped []string
	for _, id := range ids {
		c := byTask[id][cold]
		w := byTask[id][warm]
		if len(c) == 0 || len(w) == 0 {
			skipped = append(skipped, id)
			continue
		}
		e := TaskEffect{TaskID: id}
		e.ColdBilledUSDPerAttempt = meanCostPerAttempt(c)
		e.WarmBilledUSDPerAttempt = meanCostPerAttempt(w)
		e.DeltaBilledUSDPerAttempt = e.WarmBilledUSDPerAttempt - e.ColdBilledUSDPerAttempt
		e.ColdRequestsPerAttempt = meanRequestsPerAttempt(c)
		e.WarmRequestsPerAttempt = meanRequestsPerAttempt(w)
		e.DeltaRequestsPerAttempt = e.WarmRequestsPerAttempt - e.ColdRequestsPerAttempt
		e.ColdSuccessRate = successRate(c)
		e.WarmSuccessRate = successRate(w)
		e.DeltaSuccessRate = e.WarmSuccessRate - e.ColdSuccessRate
		out = append(out, e)
	}
	return out, skipped
}

func meanCostPerAttempt(as []Attempt) float64 {
	if len(as) == 0 {
		return 0
	}
	total := 0.0
	for _, a := range as {
		total += a.Totals.BilledUSD
	}
	return total / float64(len(as))
}

func meanRequestsPerAttempt(as []Attempt) float64 {
	if len(as) == 0 {
		return 0
	}
	total := 0
	for _, a := range as {
		total += len(a.Requests)
	}
	return float64(total) / float64(len(as))
}

func successRate(as []Attempt) float64 {
	if len(as) == 0 {
		return 0
	}
	ok := 0
	for _, a := range as {
		if a.VerifierResult {
			ok++
		}
	}
	return float64(ok) / float64(len(as))
}

// Interval is a bootstrapped confidence interval for a mean delta.
type Interval struct {
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

// BootstrapResult clusters the uncertainty by task: it resamples TASKS with
// replacement, not requests, because requests within one task are not
// independent.
type BootstrapResult struct {
	Unit                string   `json:"unit"`
	Samples             int      `json:"samples"`
	Tasks               int      `json:"tasks"`
	Seed                int64    `json:"seed"`
	BilledUSDPerAttempt Interval `json:"billed_usd_per_attempt_delta_ci"`
	RequestsPerAttempt  Interval `json:"requests_per_attempt_delta_ci"`
	SuccessRateDelta    Interval `json:"success_rate_delta_ci"`
}

// BootstrapTaskDeltas resamples per-task deltas with replacement.
func BootstrapTaskDeltas(effects []TaskEffect, samples int, seed int64) BootstrapResult {
	res := BootstrapResult{Unit: "task", Samples: samples, Tasks: len(effects), Seed: seed}
	if len(effects) == 0 || samples <= 0 {
		return res
	}
	cost := make([]float64, len(effects))
	reqs := make([]float64, len(effects))
	succ := make([]float64, len(effects))
	for i, e := range effects {
		cost[i] = e.DeltaBilledUSDPerAttempt
		reqs[i] = e.DeltaRequestsPerAttempt
		succ[i] = e.DeltaSuccessRate
	}
	rng := rand.New(rand.NewSource(seed))
	boot := func(values []float64) Interval {
		means := make([]float64, 0, samples)
		for i := 0; i < samples; i++ {
			sum := 0.0
			for j := 0; j < len(values); j++ {
				sum += values[rng.Intn(len(values))]
			}
			means = append(means, sum/float64(len(values)))
		}
		sort.Float64s(means)
		return Interval{Lower: percentile(means, 0.025), Upper: percentile(means, 0.975)}
	}
	// One RNG stream per metric keeps the result reproducible for a seed.
	res.BilledUSDPerAttempt = boot(cost)
	res.RequestsPerAttempt = boot(reqs)
	res.SuccessRateDelta = boot(succ)
	return res
}

// percentile returns the q quantile of a sorted sample by linear
// interpolation between the closest ranks.
func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// Claim is the pre-registered win rule outcome. The total-cost claim is
// withheld whenever any included run has partial cost coverage.
type Claim struct {
	TotalCostClaimAllowed bool   `json:"total_cost_claim_allowed"`
	Reason                string `json:"reason"`
}

// EvaluateClaim applies the win rule: warm must be cheaper with an interval
// that excludes zero, and correctness must be noninferior to the margin.
func EvaluateClaim(decomp Decomposition, boot BootstrapResult, margin float64, anyPartial bool) Claim {
	if anyPartial {
		return Claim{TotalCostClaimAllowed: false, Reason: "at least one run has partial cost coverage, so total cost is unknown"}
	}
	if decomp.TotalDeltaUSD >= 0 {
		return Claim{TotalCostClaimAllowed: false, Reason: "warm billed cost is not lower than cold"}
	}
	if boot.BilledUSDPerAttempt.Upper >= 0 {
		return Claim{TotalCostClaimAllowed: false, Reason: "the task-clustered interval for the cost delta does not exclude zero"}
	}
	if boot.SuccessRateDelta.Lower < -margin {
		return Claim{TotalCostClaimAllowed: false, Reason: "correctness is inferior to the pre-registered margin"}
	}
	return Claim{TotalCostClaimAllowed: true, Reason: "warm is cheaper with a task-clustered interval that excludes zero and correctness is noninferior"}
}
