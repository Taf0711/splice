package splice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// The decision advisor's host side: snapshot projection, the two economic
// gates, the exact in-run decision cache, allowance reservations, and
// shadow-mode recording. Everything here is host authority: the advisor
// never grants permissions, never writes files, and never changes task
// behavior except through the explicit action policy of an active mode.
// All ingestion is serial and synchronous on the orchestrator's path; the
// advisor must never be called from a detached goroutine.

// AdvisorMode is the configured advisor mode (plan 12.2).
type AdvisorMode string

const (
	AdvisorModeOff                AdvisorMode = "off"
	AdvisorModeShadow             AdvisorMode = "shadow"
	AdvisorModeActiveExperimental AdvisorMode = "active_experimental"
	AdvisorModeActive             AdvisorMode = "active"
)

// AdvisorLimits bound one advisor's spending and footprint. Values are
// proposed starting points from the spec; they are not spending authority.
// The global task budget contains the advisor allowance; it never extends
// it.
type AdvisorLimits struct {
	ReservationUSDPerMillionInput float64
	MaxRequestsPerEpisode         int
	MaxCandidates                 int
	MaxDependencyGroups           int
	MaxSourceFiles                int
	MaxRecentEvents               int
	MaxRequestBytes               int
	MaxResponseBytes              int
	Timeout                       time.Duration
	MaxTotalOptionalWait          time.Duration
	MaxInterventionsPerEpisode    int
	Retries                       int
	MaxAdvisorCostUSD             float64
	MaxFractionOfBaselineCost     float64
}

// DefaultAdvisorLimits returns the spec's proposed starting limits.
func DefaultAdvisorLimits() AdvisorLimits {
	return AdvisorLimits{
		ReservationUSDPerMillionInput: 0.042,
		MaxRequestsPerEpisode:         config.DefaultDecisionAdvisorLimits().MaxRequestsPerEpisode, // one source with the config default; the trajectory classifier has its own budget
		MaxCandidates:                 6,                                                           // prefetch_need_v1 judges 6; M4 caps itself at 4
		MaxDependencyGroups:           1,
		MaxSourceFiles:                2,
		MaxRecentEvents:               4,
		MaxRequestBytes:               16384,
		MaxResponseBytes:              65536,
		Timeout:                       1500 * time.Millisecond,
		MaxTotalOptionalWait:          3000 * time.Millisecond,
		MaxInterventionsPerEpisode:    1,
		Retries:                       0,
		MaxAdvisorCostUSD:             0.001,
		MaxFractionOfBaselineCost:     0.10,
	}
}

// DecisionAdvisorConfig is the resolved advisor configuration.
type DecisionAdvisorConfig struct {
	Backend      string
	Mode         AdvisorMode
	Profile      string
	QuestionPack string
	HostPolicy   string
	Limits       AdvisorLimits
}

// DecisionAdvisor is the host-side advisor for one run. Construct one per
// splicerun.Run: it owns the in-run cache, the allowance, and the
// disposition records for the run's trace.
type DecisionAdvisor struct {
	config    DecisionAdvisorConfig
	evaluator zeroruntime.DecisionEvaluator

	// Allowance accounting: settled charges plus outstanding reservations
	// count against the cap (plan 6.5). Access is serial by contract; the
	// advisor is never touched from concurrent goroutines.
	settledUSD     float64
	outstandingUSD float64
	requests       int
	// trajectoryRequests counts the trajectory classifier's requests, which
	// have their own per-run budget (EvaluateTrajectoryGate1).
	trajectoryRequests int

	// selectorOff keeps the dependency selector and the prefetch decisions
	// off. It is set when only the trajectory classifier runs.
	selectorOff bool
	// classifierOff keeps the trajectory classifier off. It is set when the
	// trajectory classifier flag is off.
	classifierOff bool

	// cache is the exact in-run decision cache (plan 12.4): identical
	// requests only, keyed by profile, resolved model, question pack,
	// policy version, state digest, source digest, permissions, and prompt
	// layout hash. Scope is in-run only.
	cache map[string]cachedDecision

	// records is the disposition trace for this run.
	records []schemas.DecisionDispositionRecord

	// usageSink receives every decision request's reported usage, so the
	// run's request ledger counts the advisor's own tokens and charge. Nil
	// drops them (tests, callers without a ledger).
	usageSink func(pack string, iteration int, result zeroruntime.DecisionResult)

	now func() time.Time
}

type cachedDecision struct {
	result zeroruntime.DecisionResult
	// sourceDigest and promptLayoutHash pin the freshness of the cached
	// answer; a lookup under a different value is a miss, never a reuse.
	sourceDigest     string
	promptLayoutHash string
}

// AdvisorConfigFromResolved converts and validates the resolved config
// section into the runtime advisor configuration. Defaults fill omitted
// limits; validation runs twice by design: once at config resolve time
// (named field errors), once here as the runtime contract.
func AdvisorConfigFromResolved(section config.DecisionAdvisorConfig) (DecisionAdvisorConfig, error) {
	out := DecisionAdvisorConfig{
		Mode:         AdvisorMode(section.Mode),
		Backend:      section.Backend,
		Profile:      section.Profile,
		QuestionPack: section.QuestionPack,
		HostPolicy:   section.Policy,
	}
	if out.Mode == "" {
		out.Mode = AdvisorModeOff
	}
	if out.QuestionPack == "" {
		out.QuestionPack = schemas.QuestionPackDependencyRelationV1
	}
	if out.HostPolicy == "" {
		out.HostPolicy = schemas.HostPolicyBoundedPrefetchV1
	}
	def := DefaultAdvisorLimits()
	out.Limits = AdvisorLimits{
		ReservationUSDPerMillionInput: orDefaultF(section.Limits.ReservationUSDPerMillionInput, def.ReservationUSDPerMillionInput),
		MaxRequestsPerEpisode:         orDefault(section.Limits.MaxRequestsPerEpisode, def.MaxRequestsPerEpisode),
		MaxCandidates:                 orDefault(section.Limits.MaxCandidates, def.MaxCandidates),
		MaxDependencyGroups:           orDefault(section.Limits.MaxDependencyGroups, def.MaxDependencyGroups),
		MaxSourceFiles:                orDefault(section.Limits.MaxSourceFiles, def.MaxSourceFiles),
		MaxRecentEvents:               orDefault(section.Limits.MaxRecentEvents, def.MaxRecentEvents),
		MaxRequestBytes:               orDefault(section.Limits.MaxRequestBytes, def.MaxRequestBytes),
		MaxResponseBytes:              orDefault(section.Limits.MaxResponseBytes, def.MaxResponseBytes),
		Timeout:                       time.Duration(orDefault(section.Limits.TimeoutMS, int(def.Timeout/time.Millisecond))) * time.Millisecond,
		MaxTotalOptionalWait:          time.Duration(orDefault(section.Limits.MaxTotalOptionalWaitMS, int(def.MaxTotalOptionalWait/time.Millisecond))) * time.Millisecond,
		MaxInterventionsPerEpisode:    orDefault(section.Limits.MaxInterventionsPerEpisode, def.MaxInterventionsPerEpisode),
		Retries:                       section.Limits.Retries,
		MaxAdvisorCostUSD:             orDefaultF(section.Limits.MaxAdvisorCostUSD, def.MaxAdvisorCostUSD),
		MaxFractionOfBaselineCost:     orDefaultF(section.Limits.MaxFractionOfBaselineCost, def.MaxFractionOfBaselineCost),
	}
	if err := validateAdvisorConfig(out); err != nil {
		return DecisionAdvisorConfig{}, err
	}
	return out, nil
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func orDefaultF(v, def float64) float64 {
	if v == 0 {
		return def
	}
	return v
}

// NewDecisionAdvisor constructs the advisor for a run. The evaluator may be
// nil: off mode and credential-less runs construct an advisor that records
// typed bypass dispositions instead of calling anything, so the host never
// branches on a nil advisor silently.
func NewDecisionAdvisor(config DecisionAdvisorConfig, evaluator zeroruntime.DecisionEvaluator) (*DecisionAdvisor, error) {
	if err := validateAdvisorConfig(config); err != nil {
		return nil, err
	}
	return &DecisionAdvisor{
		config:    config,
		evaluator: evaluator,
		cache:     map[string]cachedDecision{},
		now:       func() time.Time { return time.Now().UTC() },
	}, nil
}

// validateAdvisorConfig rejects an invalid explicit profile with the
// offending field named (plan 12.3). Off mode accepts any other field
// combination: nothing calls, so nothing else can be wrong at runtime.
func validateAdvisorConfig(config DecisionAdvisorConfig) error {
	switch config.Mode {
	case AdvisorModeOff, AdvisorModeShadow, AdvisorModeActiveExperimental, AdvisorModeActive:
	default:
		return fmt.Errorf("decision_advisor: unknown mode %q (want off, shadow, active_experimental, or active)", config.Mode)
	}
	if config.Mode == AdvisorModeOff {
		// Off accepts the rest untouched: nothing calls, so nothing else
		// can be wrong at runtime.
		return nil
	}
	if price := config.Limits.ReservationUSDPerMillionInput; price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return fmt.Errorf("decision_advisor: limits.reservation_usd_per_million_input must be a finite non-negative price")
	}
	if config.Limits.MaxRequestsPerEpisode < 0 {
		return fmt.Errorf("decision_advisor: limits.max_requests_per_episode must not be negative")
	}
	if config.Limits.MaxRequestBytes <= 0 {
		return fmt.Errorf("decision_advisor: limits.max_request_bytes must be positive")
	}
	if config.Limits.MaxResponseBytes <= 0 {
		return fmt.Errorf("decision_advisor: limits.max_response_bytes must be positive")
	}
	if config.Limits.Timeout <= 0 {
		return fmt.Errorf("decision_advisor: limits.timeout_ms must be positive")
	}
	if config.Limits.Retries != 0 {
		return fmt.Errorf("decision_advisor: limits.retries must be 0: hidden retries are disabled for the advisor")
	}
	if config.Limits.MaxAdvisorCostUSD < 0 {
		return fmt.Errorf("decision_advisor: limits.max_advisor_cost_usd must not be negative")
	}
	if config.Limits.MaxFractionOfBaselineCost < 0 || config.Limits.MaxFractionOfBaselineCost > 1 {
		return fmt.Errorf("decision_advisor: limits.max_fraction_of_baseline_cost must be within [0,1]")
	}
	if config.Mode != AdvisorModeOff && strings.TrimSpace(config.Profile) == "" {
		return fmt.Errorf("decision_advisor: profile is required when mode is not off")
	}
	return nil
}

// Mode reports the configured mode.
func (a *DecisionAdvisor) Mode() AdvisorMode { return a.config.Mode }

// Records returns the disposition records accumulated this run.
func (a *DecisionAdvisor) Records() []schemas.DecisionDispositionRecord {
	out := make([]schemas.DecisionDispositionRecord, len(a.records))
	copy(out, a.records)
	return out
}

// ProjectSnapshot validates and serializes a snapshot into the opaque state
// string the evaluator consumes. The state is compact JSON over the typed
// snapshot: the provider receives the distilled intent, the relation, and
// the bounded evidence, never the user's verbose request and never
// credentials (they are stripped at snapshot construction by the caller;
// serialization here refuses unknown fields so nothing smuggles through).
func (a *DecisionAdvisor) ProjectSnapshot(snap schemas.SemanticSnapshot) (string, error) {
	if a == nil {
		return "", fmt.Errorf("decision_advisor: advisor is not wired")
	}
	if err := snap.Validate(); err != nil {
		return "", err
	}
	if len(snap.CandidateRefs) > a.config.Limits.MaxCandidates {
		return "", fmt.Errorf("decision_advisor: %d candidate refs exceed the limit of %d", len(snap.CandidateRefs), a.config.Limits.MaxCandidates)
	}
	if len(snap.RecentEvents) > a.config.Limits.MaxRecentEvents {
		return "", fmt.Errorf("decision_advisor: %d recent events exceed the limit of %d", len(snap.RecentEvents), a.config.Limits.MaxRecentEvents)
	}
	return projectSemanticSnapshot(snap, a.config.Limits.MaxRequestBytes)
}

func projectSemanticSnapshot(snap schemas.SemanticSnapshot, maxRequestBytes int) (string, error) {
	if err := snap.Validate(); err != nil {
		return "", err
	}
	state := struct {
		Schema        string                 `json:"schema"`
		QuestionPack  string                 `json:"question_pack"`
		Need          string                 `json:"need"`
		Relation      string                 `json:"relation"`
		Candidates    []schemas.CandidateRef `json:"candidates"`
		RecentEvents  []schemas.EventSummary `json:"recent_events"`
		Deterministic []string               `json:"deterministic_outcomes"`
	}{snap.SnapshotSchema, snap.QuestionPack, snap.DistilledIntent, snap.UnresolvedRelation, snap.CandidateRefs, snap.RecentEvents, snap.DeterministicOutcomes}
	var raw []byte
	var err error
	if snap.Trajectory != nil {
		raw, err = json.Marshal(snap.Trajectory)
	} else {
		raw, err = json.Marshal(state)
	}
	if err != nil {
		return "", fmt.Errorf("decision_advisor: serialize snapshot: %w", err)
	}
	if len(raw) > maxRequestBytes {
		return "", fmt.Errorf("decision_advisor: snapshot state %d bytes exceeds the %d byte bound", len(raw), maxRequestBytes)
	}
	return string(raw), nil
}

// dependencyRelationV1 builds the versioned Choice question for the
// dependency-relation pack. The pack is part of the typed seam: question
// wording changes bump the version constant, and old cached answers never
// replay under a new version.
func dependencyRelationV1(candidates []schemas.CandidateRef) zeroruntime.DecisionQuestion {
	criteria := make([]zeroruntime.DecisionCriterion, 0, len(candidates))
	for _, c := range candidates {
		criteria = append(criteria, zeroruntime.DecisionCriterion{Label: c.Ref, Description: c.Origin})
	}
	criteria = append(criteria,
		zeroruntime.DecisionCriterion{Label: "insufficient_evidence", Description: "No candidate establishes the relation."})
	return zeroruntime.DecisionQuestion{
		ID:           "dependency_relation",
		Kind:         zeroruntime.DecisionKindChoice,
		Instructions: "Which candidate, if any, identifies a concrete dependency required for the stated need?",
		Criteria:     criteria,
	}
}

// EvaluateGate1 runs the pre-call gate: should the host buy this decision?
// It returns a disposition and, when a call is warranted, performs it
// (shadow and active modes). Exact bypass, eligibility, cache, and budget
// checks all happen before any provider attempt. In shadow mode the host
// behavior never changes regardless of the answer: the caller receives the
// disposition and the assessment, and the action policy refuses to act.
// EvaluateGate1WithQuestions runs the gate for the selector and prefetch
// callers. They share one request cap, the configured
// max_requests_per_episode.
func (a *DecisionAdvisor) EvaluateGate1WithQuestions(ctx context.Context, snap schemas.SemanticSnapshot, economics *schemas.EconomicAssessment, questions []zeroruntime.DecisionQuestion) (schemas.DecisionDispositionRecord, bool) {
	return a.gate1(ctx, snap, economics, questions, &a.requests, a.config.Limits.MaxRequestsPerEpisode)
}

// EvaluateTrajectoryGate1 runs the gate for the trajectory classifier. The
// classifier counts its own requests against its own per-run budget, so the
// selector's cap never cuts it off, and the reverse holds too.
func (a *DecisionAdvisor) EvaluateTrajectoryGate1(ctx context.Context, snap schemas.SemanticSnapshot, questions []zeroruntime.DecisionQuestion, budget int) (schemas.DecisionDispositionRecord, bool) {
	return a.gate1(ctx, snap, nil, questions, &a.trajectoryRequests, budget)
}

// gate1 is the shared pre-call gate. requests counts the calls of one caller
// class and limit caps them.
func (a *DecisionAdvisor) gate1(ctx context.Context, snap schemas.SemanticSnapshot, economics *schemas.EconomicAssessment, questions []zeroruntime.DecisionQuestion, requests *int, limit int) (schemas.DecisionDispositionRecord, bool) {
	now := time.Now().UTC
	if a != nil && a.now != nil {
		now = a.now
	}
	record := schemas.DecisionDispositionRecord{
		Snapshot:      snap,
		Economic:      economics,
		RecordedAtUTC: now().Format(time.RFC3339),
	}
	fail := func(d schemas.DecisionDisposition, reason string) (schemas.DecisionDispositionRecord, bool) {
		record.Disposition = d
		record.Reason = reason
		if a != nil {
			a.records = append(a.records, record)
		}
		return record, false
	}

	if a == nil || a.config.Mode == AdvisorModeOff {
		return fail(schemas.DispositionExactBypass, "advisor mode is off")
	}
	if len(snap.CandidateRefs) == 0 {
		return fail(schemas.DispositionInsufficientEvidence, "no eligible candidates")
	}
	// Deterministic outcomes already computed by the host skip the call.
	if len(snap.DeterministicOutcomes) > 0 {
		return fail(schemas.DispositionExactBypass, "deterministic outcome already computed by the host")
	}
	// The exact in-run cache: an identical request (profile, pack, policy,
	// state digest, source digest, layout hash) is answered from the cache
	// and adds no fabricated charge (plan 12.4).
	state, err := a.ProjectSnapshot(snap)
	if err != nil {
		return fail(schemas.DispositionInsufficientEvidence, err.Error())
	}
	key := a.cacheKey(snap, state)
	if hit, ok := a.cache[key]; ok && hit.sourceDigest == snap.SourceDigest && hit.promptLayoutHash == snap.PromptLayoutHash {
		assessment := assessmentFromResult(hit.result)
		record.Assessment = &assessment
		record.RequestID = hit.result.RequestID
		if hit.result.ReportedCostUSD != nil {
			c := *hit.result.ReportedCostUSD
			record.ChargedUSD = &c
		}
		return fail(schemas.DispositionShadowOnly, "identical request already assessed this run (cache hit; no new charge)")
	}

	// The full active mode requires the calibrated class estimate from the
	// run's own economic assessment. active_experimental's permission comes
	// from the preregistered manifest calibration and cap instead (the
	// gate-2 store and the allowance), so it may buy without run-level
	// economics; shadow buys under its experiment allowance without either.
	if a.config.Mode == AdvisorModeActive && (economics == nil || strings.TrimSpace(economics.CalibrationProvenance) == "") {
		return fail(schemas.DispositionUncalibrated, "no calibrated class estimate; the active gate requires one")
	}
	// Conservative gate: the lower estimate of savings must exceed the
	// upper estimate of overhead. Missing quantities decline rather than
	// default to permission.
	if a.config.Mode == AdvisorModeActive && economics != nil {
		if economics.ExpectedSavingsLowUSD == nil || economics.AdvisorOverheadHighUSD == nil {
			return fail(schemas.DispositionUncalibrated, "gate quantities absent; refusing to default to permission")
		}
		if *economics.ExpectedSavingsLowUSD <= *economics.AdvisorOverheadHighUSD {
			return fail(schemas.DispositionShadowOnly, "conservative gate: expected savings do not exceed upper overhead")
		}
	}

	// Allowance: settled plus outstanding reservations must cover the
	// conservative reservation (plan 6.5).
	reserve := reservationFor(state, a.config.Limits.ReservationUSDPerMillionInput)
	if *requests >= limit {
		return fail(schemas.DispositionBudgetDeclined, fmt.Sprintf("request cap %d reached", limit))
	}
	if a.settledUSD+a.outstandingUSD+reserve > a.config.Limits.MaxAdvisorCostUSD {
		return fail(schemas.DispositionBudgetDeclined, fmt.Sprintf("allowance $%.6f exhausted (settled $%.6f + outstanding $%.6f + reservation $%.6f)", a.config.Limits.MaxAdvisorCostUSD, a.settledUSD, a.outstandingUSD, reserve))
	}
	if a.evaluator == nil {
		return fail(schemas.DispositionProviderFailure, "no decision evaluator wired (missing credentials?)")
	}

	a.outstandingUSD += reserve
	*requests++
	req := decisionRequestFromState(snap, a.config.Profile, state, questions)
	callCtx, cancel := context.WithTimeout(ctx, a.config.Limits.Timeout)
	defer cancel()
	result, err := a.evaluator.Evaluate(callCtx, req)
	a.outstandingUSD -= reserve
	if err != nil {
		// Degrade visible: record the failure with any valid charge
		// information preserved through the seam, and stay on baseline.
		// A cancelled or failed request may still bill: without preserved
		// usage the conservative reservation stays settled until
		// reconciliation (plan 6.5 unresolved-charge handling).
		if usage := usageFromError(err); usage != nil {
			if a.usageSink != nil {
				a.usageSink(snap.QuestionPack, snap.Iteration, *usage)
			}
			record.RequestID = usage.RequestID
			if usage.ReportedCostUSD != nil {
				c := *usage.ReportedCostUSD
				record.ChargedUSD = &c
				a.settledUSD += c
			} else {
				a.settledUSD += reserve
			}
			record.InputTokens = usage.InputTokens
			record.OutputTokens = usage.OutputTokens
		} else {
			a.settledUSD += reserve
		}
		return fail(schemas.DispositionProviderFailure, err.Error())
	}
	if a.usageSink != nil {
		a.usageSink(snap.QuestionPack, snap.Iteration, result)
	}
	// The exact reported charge replaces the conservative reservation.
	a.settledUSD += reserve
	if result.ReportedCostUSD != nil {
		a.settledUSD += *result.ReportedCostUSD - reserve
	}
	assessment := assessmentFromResult(result)
	record.Assessment = &assessment
	record.RequestID = result.RequestID
	if result.ReportedCostUSD != nil {
		c := *result.ReportedCostUSD
		record.ChargedUSD = &c
	}
	record.InputTokens = result.InputTokens
	record.OutputTokens = result.OutputTokens
	a.cache[key] = cachedDecision{result: result, sourceDigest: snap.SourceDigest, promptLayoutHash: snap.PromptLayoutHash}
	// Shadow buys and records without acting. In an active mode the gate
	// permitting the named mechanism is the disposition; the delivery and
	// outcome records pin what the action actually caused.
	if a.config.Mode == AdvisorModeShadow {
		record.Disposition = schemas.DispositionShadowOnly
		record.Reason = "bought and recorded; shadow never acts"
	} else {
		record.Disposition = schemas.DispositionApplied
		record.Reason = "bought under the configured mode permission"
	}
	a.records = append(a.records, record)
	// Shadow mode never changes host behavior: the caller must treat the
	// boolean as an action permission, and shadow returns false always.
	return record, a.config.Mode != AdvisorModeShadow
}

// cacheKey builds the exact in-run cache key (plan 12.4): profile, question
// pack, policy version, state digest, source digest, and prompt layout
// hash. Episode identity is deliberately excluded: two episodes in one run
// with identical state reuse the decision.
func (a *DecisionAdvisor) cacheKey(snap schemas.SemanticSnapshot, state string) string {
	stateDigest := sha256.Sum256([]byte(state))
	return strings.Join([]string{
		a.config.Profile,
		snap.QuestionPack,
		snap.HostPolicy,
		hex.EncodeToString(stateDigest[:8]),
		snap.SourceDigest,
		snap.PromptLayoutHash,
	}, "|")
}

// reservationFor computes a conservative reservation for one request. A
// rough characters-per-token estimate is not a spending guarantee; it is an
// upper bound wide enough for the alpha profile's token counts.
func reservationFor(state string, prices ...float64) float64 {
	price := 0.042
	if len(prices) > 0 && prices[0] != 0 {
		price = prices[0]
	}
	const charsPerTokenUpperBound = 2.0
	tokens := float64(len(state)) / charsPerTokenUpperBound
	return tokens * price / 1_000_000
}

// assessmentFromResult converts a validated provider result into the typed
// assessment record: Answers carries every question, and the flat fields
// mirror the single answer when exactly one exists.
func assessmentFromResult(result zeroruntime.DecisionResult) schemas.DecisionAssessment {
	a := schemas.DecisionAssessment{
		ModelVersion: result.ResolvedModel,
		Provider:     result.Provider,
		RequestID:    result.RequestID,
	}
	for _, ans := range result.Answers {
		rec := schemas.DecisionAnswerRecord{
			QuestionID:    ans.QuestionID,
			Kind:          string(ans.Kind),
			Choice:        ans.Choice,
			Probabilities: ans.Probabilities,
			ConfidenceSet: ans.ConfidenceSet,
		}
		if ans.ConfidenceSet {
			c := ans.Confidence
			rec.Confidence = &c
		}
		// A noul carries its answer in TrueProbability; without this the
		// record kept no probability at all.
		if ans.Kind == zeroruntime.DecisionKindNoul && rec.Probabilities == nil {
			rec.Probabilities = map[string]float64{"true": ans.TrueProbability, "false": 1 - ans.TrueProbability}
		}
		a.Answers = append(a.Answers, rec)
	}
	if len(result.Answers) == 1 {
		ans := result.Answers[0]
		a.QuestionID = ans.QuestionID
		a.Kind = string(ans.Kind)
		a.Choice = ans.Choice
		a.Probabilities = ans.Probabilities
		if ans.ConfidenceSet {
			c := ans.Confidence
			a.Confidence = &c
		}
	}
	return a
}

// EvaluateGate1 is the single-question form: the M3 dependency-relation
// question over the snapshot's candidates.
func (a *DecisionAdvisor) EvaluateGate1(ctx context.Context, snap schemas.SemanticSnapshot, economics *schemas.EconomicAssessment) (schemas.DecisionDispositionRecord, bool) {
	return a.EvaluateGate1WithQuestions(ctx, snap, economics, []zeroruntime.DecisionQuestion{dependencyRelationV1(snap.CandidateRefs)})
}

// usageFromError extracts preserved accounting from an adapter failure.
// The adapter's ResponseError carries usage; a plain error carries none.
func usageFromError(err error) *zeroruntime.DecisionResult {
	var carrier zeroruntime.DecisionUsageError
	if errors.As(err, &carrier) {
		return carrier.DecisionUsage()
	}
	return nil
}
