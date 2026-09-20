package warmcost

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// ArmMetrics aggregates one arm over all tasks and repeats.
type ArmMetrics struct {
	Arm                              Arm                `json:"arm"`
	Attempts                         int                `json:"attempts"`
	VerifiedCompletions              int                `json:"verified_completions"`
	FailedAttempts                   int                `json:"failed_attempts"`
	PartialAttempts                  int                `json:"partial_attempts"`
	Requests                         int                `json:"requests"`
	RequestsPerVerifiedCompletion    float64            `json:"requests_per_verified_completion"`
	BilledUSD                        float64            `json:"billed_usd"`
	BilledUSDPerAttempt              float64            `json:"billed_usd_per_attempt"`
	BilledUSDPerVerifiedCompletion   float64            `json:"billed_usd_per_verified_completion"`
	InputTokens                      int                `json:"input_tokens"`
	OutputTokens                     int                `json:"output_tokens"`
	CachedTokens                     int                `json:"cached_input_tokens"`
	CacheWriteTokens                 int                `json:"cache_write_tokens"`
	ReasoningTokens                  int                `json:"reasoning_tokens"`
	InputTokensPerAttempt            float64            `json:"input_tokens_per_attempt"`
	InputTokensPerVerifiedCompletion float64            `json:"input_tokens_per_verified_completion"`
	BilledUSDBySource                map[string]float64 `json:"billed_usd_by_source"`
	ExpansionRequests                int                `json:"expansion_requests"`
	RoundShare                       float64            `json:"round_share"`
	CacheShare                       float64            `json:"cache_share"`
	CoverageComplete                 bool               `json:"coverage_complete"`
}

// SpendSourceUnspecified buckets a request whose ledger spend source is empty.
// The ledger permits an empty source, so the split must not drop it.
const SpendSourceUnspecified = "unspecified"

// spendSourceKey normalizes a ledger spend source into a split key.
func spendSourceKey(source string) string {
	if strings.TrimSpace(source) == "" {
		return SpendSourceUnspecified
	}
	return source
}

// ComputeArmMetrics aggregates the attempts of one arm.
func ComputeArmMetrics(arm Arm, attempts []Attempt) ArmMetrics {
	m := ArmMetrics{Arm: arm, CoverageComplete: true, BilledUSDBySource: map[string]float64{}}
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
			if r.CostUSD != nil {
				m.BilledUSDBySource[spendSourceKey(r.SpendSource)] += *r.CostUSD
			}
		}
	}
	if m.Attempts > 0 {
		m.BilledUSDPerAttempt = m.BilledUSD / float64(m.Attempts)
		m.InputTokensPerAttempt = float64(m.InputTokens) / float64(m.Attempts)
	}
	if m.VerifiedCompletions > 0 {
		m.RequestsPerVerifiedCompletion = float64(m.Requests) / float64(m.VerifiedCompletions)
		m.BilledUSDPerVerifiedCompletion = m.BilledUSD / float64(m.VerifiedCompletions)
		m.InputTokensPerVerifiedCompletion = float64(m.InputTokens) / float64(m.VerifiedCompletions)
	}
	if m.Requests > 0 {
		m.RoundShare = float64(m.ExpansionRequests) / float64(m.Requests)
	}
	if m.InputTokens > 0 {
		m.CacheShare = float64(m.CachedTokens) / float64(m.InputTokens)
	}
	return m
}

// SourceDelta is one spend source's billed USD in both arms and their
// difference. The source names come from the authoritative ledger, so a
// generation request and a repair request never collapse into one label.
type SourceDelta struct {
	Source   string  `json:"source"`
	ColdUSD  float64 `json:"cold_usd"`
	WarmUSD  float64 `json:"warm_usd"`
	DeltaUSD float64 `json:"delta_usd"`
}

// Decomposition splits the warm-minus-cold billed cost by spend source. The
// cache channel is reported in tokens because the ledger carries no per-token
// cache price, so a USD cache split would be fabricated. The per-source USD
// deltas sum exactly to the total delta.
type Decomposition struct {
	TotalDeltaUSD    float64       `json:"total_delta_usd"`
	Sources          []SourceDelta `json:"sources"`
	CacheTokensDelta int           `json:"cache_tokens_delta"`
	CacheChannelNote string        `json:"cache_channel_note"`
}

// Decompose computes the per-source cost channels from one cold and one warm
// arm. It fails loud when an arm's per-source USD does not sum to its billed
// total, because that means the ledger and the split disagree.
func Decompose(cold, warm ArmMetrics) (Decomposition, error) {
	if err := validateSourceIdentity(cold); err != nil {
		return Decomposition{}, err
	}
	if err := validateSourceIdentity(warm); err != nil {
		return Decomposition{}, err
	}
	keys := map[string]bool{}
	for k := range cold.BilledUSDBySource {
		keys[k] = true
	}
	for k := range warm.BilledUSDBySource {
		keys[k] = true
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	d := Decomposition{
		TotalDeltaUSD: warm.BilledUSD - cold.BilledUSD,
		CacheTokensDelta: (warm.CachedTokens + warm.CacheWriteTokens) -
			(cold.CachedTokens + cold.CacheWriteTokens),
		CacheChannelNote: "ledger carries cache-read and cache-write tokens but no per-token cache price, so the cache channel is reported in tokens and is not folded into the USD split",
	}
	sum := 0.0
	for _, name := range names {
		c := cold.BilledUSDBySource[name]
		w := warm.BilledUSDBySource[name]
		d.Sources = append(d.Sources, SourceDelta{Source: name, ColdUSD: c, WarmUSD: w, DeltaUSD: w - c})
		sum += w - c
	}
	if math.Abs(sum-d.TotalDeltaUSD) > 1e-9 {
		return Decomposition{}, fmt.Errorf("warmcost: source deltas sum to %.9f but the total delta is %.9f", sum, d.TotalDeltaUSD)
	}
	return d, nil
}

// validateSourceIdentity proves the per-source USD split reconstructs the
// arm's billed total. A mismatch is a ledger or split defect, never a value to
// smooth over.
func validateSourceIdentity(m ArmMetrics) error {
	sum := 0.0
	for _, v := range m.BilledUSDBySource {
		sum += v
	}
	if math.Abs(sum-m.BilledUSD) > 1e-9 {
		return fmt.Errorf("warmcost: arm %s per-source USD sums to %.9f but the billed total is %.9f", m.Arm, sum, m.BilledUSD)
	}
	return nil
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
func EvaluateClaim(decomp Decomposition, boot BootstrapResult, margin float64, anyPartial, retentionFresh bool) Claim {
	if anyPartial {
		return Claim{TotalCostClaimAllowed: false, Reason: "at least one run has partial cost coverage, so total cost is unknown"}
	}
	if retentionFresh {
		return Claim{TotalCostClaimAllowed: false, Reason: "the retention protocol is fresh, so the warm arm has no retained experience and a total-cost claim is not available"}
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
