package schemas

import (
	"fmt"
	"strings"
)

// Typed records for the decision advisor (the Jev integration). These follow
// the frozen spec (SPEC_DECISION_ADVISOR_JEV_FOLD_2026-09-20 Section 6.3,
// adopting plan 11.2 and 11.3): a snapshot is the validated host-side state
// a decision may be bought for; the assessment, economic assessment, and
// disposition stay separate records; predicted savings never occupy an
// actual-cost field, and a skipped request is never a logged free call.

// QuestionPackIdentity names a versioned question pack. The pack version is
// part of every snapshot, cache key, and trace: answers taken under one pack
// are never replayed under another.
const QuestionPackDependencyRelationV1 = "dependency_relation_v1"

// HostPolicyIdentity names the versioned host-side decision policy. It bumps
// when gate arithmetic, skipping rules, or action limits change.
const HostPolicyBoundedPrefetchV1 = "bounded_prefetch_v1"

// SnapshotSchemaIdentity is the current snapshot schema version.
const SnapshotSchemaV1 = "semantic_snapshot_v1"

// AdvisorPurposes are the ledger purposes for advisor spend (plan 11.6).
// They refine SpendSourceAuxiliary without extending the fixed SpendSource
// set, and every new purpose must survive writer, reader, aggregate, and
// trace serialization (pinned by pairing tests).
const (
	AdvisorPurposeSelection  = "jev_selection"
	AdvisorPurposeTrajectory = "jev_trajectory"
)

// SemanticSnapshot is the typed, versioned host-side state one decision may
// be bought for. Field groups follow plan 11.2, plus the cache-aware spec
// addition: the snapshot records the prompt layout hash of the request it
// may influence, so staleness checks reject decisions taken under a
// different cacheable prefix.
type SemanticSnapshot struct {
	Trajectory *TrajectorySnapshot `json:",omitempty"`
	// TrajectoryIsolatedWorktree retains the rollback guard without a change to the provider state.
	TrajectoryIsolatedWorktree bool `json:",omitempty"`
	// Identity: which run, episode, stage invocation, iteration, and event
	// produced this snapshot.
	RunID           string
	EpisodeID       string
	Stage           string
	StageInvocation int
	Iteration       int
	EventID         string

	// Version: schema, question pack, host policy, and calibration dataset
	// identities. A decision is only valid within matching versions.
	SnapshotSchema     string
	QuestionPack       string
	HostPolicy         string
	CalibrationDataset string

	// Repository: canonical project identity plus the working-tree source
	// digest. Distinct worktree contents never collapse into one evidence
	// state.
	CanonicalProjectID string
	SourceDigest       string

	// Need: one distilled intent and one unresolved dependency or failure
	// relation. The user's verbose original request never appears here.
	DistilledIntent    string
	UnresolvedRelation string

	// Evidence: bounded candidate references and relevant completed events.
	CandidateRefs []CandidateRef
	RecentEvents  []EventSummary

	// Eligibility: deterministic outcomes the host already computed, so the
	// provider never re-derives them.
	DeterministicOutcomes []string

	// PromptLayoutHash is the stable-prefix layout hash of the request this
	// snapshot may influence. Zero value means unknown, which staleness
	// checks treat as unmatchable.
	PromptLayoutHash string
}

// CandidateRef is one bounded candidate reference inside a snapshot.
type CandidateRef struct {
	// Ref is the evidence reference (path, path#symbol, or record ID).
	Ref string
	// Origin names where the candidate came from (capture, static scan,
	// memory query).
	Origin string
	// Claim is the candidate's own short claim line, bounded.
	Claim string
}

// EventSummary is one bounded completed-event reference inside a snapshot.
type EventSummary struct {
	EventID string
	Summary string
}

// Validate enforces the snapshot contract. Construction happens only after
// tool and source data pass ordinary validation; Validate is the second
// gate before any serialization or spend.
func (s SemanticSnapshot) Validate() error {
	if s.Trajectory != nil {
		if err := s.Trajectory.Validate(); err != nil {
			return err
		}
	}
	if strings.TrimSpace(s.RunID) == "" {
		return fmt.Errorf("semantic snapshot: run id is required")
	}
	if strings.TrimSpace(s.EpisodeID) == "" {
		return fmt.Errorf("semantic snapshot: episode id is required")
	}
	if strings.TrimSpace(s.Stage) == "" {
		return fmt.Errorf("semantic snapshot: stage is required")
	}
	if strings.TrimSpace(s.EventID) == "" {
		return fmt.Errorf("semantic snapshot: event id is required")
	}
	if s.SnapshotSchema != SnapshotSchemaV1 {
		return fmt.Errorf("semantic snapshot: unsupported schema %q (want %q)", s.SnapshotSchema, SnapshotSchemaV1)
	}
	if strings.TrimSpace(s.QuestionPack) == "" {
		return fmt.Errorf("semantic snapshot: question pack is required")
	}
	if strings.TrimSpace(s.HostPolicy) == "" {
		return fmt.Errorf("semantic snapshot: host policy is required")
	}
	if strings.TrimSpace(s.CanonicalProjectID) == "" {
		return fmt.Errorf("semantic snapshot: canonical project identity is required")
	}
	if strings.TrimSpace(s.SourceDigest) == "" {
		return fmt.Errorf("semantic snapshot: source digest is required (distinct worktree contents must not collapse)")
	}
	if strings.TrimSpace(s.DistilledIntent) == "" {
		return fmt.Errorf("semantic snapshot: distilled intent is required")
	}
	if strings.TrimSpace(s.UnresolvedRelation) == "" {
		return fmt.Errorf("semantic snapshot: unresolved relation is required")
	}
	if len(s.CandidateRefs) == 0 {
		return fmt.Errorf("semantic snapshot: at least one candidate reference is required")
	}
	seen := map[string]bool{}
	for _, c := range s.CandidateRefs {
		if strings.TrimSpace(c.Ref) == "" {
			return fmt.Errorf("semantic snapshot: candidate reference without a ref")
		}
		if seen[c.Ref] {
			return fmt.Errorf("semantic snapshot: duplicate candidate reference %q", c.Ref)
		}
		seen[c.Ref] = true
	}
	for _, e := range s.RecentEvents {
		if strings.TrimSpace(e.EventID) == "" {
			return fmt.Errorf("semantic snapshot: recent event without an event id")
		}
	}
	return nil
}

// DecisionAssessment is the provider's raw typed answer, kept separate from
// every host decision (plan 11.3).
type DecisionAssessment struct {
	QuestionID    string             `json:"question_id,omitempty"`
	Kind          string             `json:"kind,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	// Confidence is optional. Absent confidence is recorded as absent, never
	// invented certainty.
	Confidence *float64 `json:"confidence,omitempty"`
	// Answers carries one record per question for a question pack. A
	// single-question request leaves it empty and populates the flat
	// fields instead.
	Answers       []DecisionAnswerRecord `json:"answers,omitempty"`
	ModelVersion  string                 `json:"model_version"`
	Provider      string                 `json:"provider,omitempty"`
	RequestID     string                 `json:"request_id,omitempty"`
	AssessedAtUTC string                 `json:"assessed_at_utc,omitempty"`
}

// DecisionAnswerRecord is one validated answer inside a pack assessment.
type DecisionAnswerRecord struct {
	QuestionID    string             `json:"question_id"`
	Kind          string             `json:"kind"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	ConfidenceSet bool               `json:"confidence_set,omitempty"`
}

// EconomicAssessment is the host's economic arithmetic for one candidate
// action, kept separate from the provider assessment. Predicted savings
// never occupy an actual-cost field.
type EconomicAssessment struct {
	// BaselineContinuationUSD estimates the future cost under the baseline
	// policy (upper and lower bounds; the gate uses the lower baseline
	// estimate conservatively).
	BaselineContinuationHighUSD *float64 `json:"baseline_continuation_high_usd,omitempty"`
	// AdvisorOverheadUSD is the upper estimate of the advisor overhead:
	// the call itself, evidence delivery, extra cache writes, fallback,
	// and mistakes (the cache-aware H term, not a flat delivery cost).
	AdvisorOverheadHighUSD *float64 `json:"advisor_overhead_high_usd,omitempty"`
	// ExpectedSavingsLowUSD is the lower estimate of savings: baseline
	// continuation minus advisor-policy continuation. The conservative
	// gate requires this to exceed AdvisorOverheadHighUSD.
	ExpectedSavingsLowUSD *float64 `json:"expected_savings_low_usd,omitempty"`
	// ActionCostUSD estimates the cost of the specific action under
	// consideration (delivery, replay, repair risk).
	ActionCostUSD *float64 `json:"action_cost_usd,omitempty"`
	// CalibrationProvenance names where the calibrated quantities came
	// from. Empty means uncalibrated.
	CalibrationProvenance string `json:"calibration_provenance,omitempty"`
}

// DecisionDisposition enumerates what the host did with a decision
// opportunity (plan 11.3). A disposition is always named: an unacted
// opportunity is a typed outcome, never silence.
type DecisionDisposition string

const (
	// DispositionApplied means the advisor's answer changed host behavior
	// through the action policy (only reachable in active modes).
	DispositionApplied DecisionDisposition = "applied"
	// DispositionShadowOnly means the decision was bought and recorded but
	// host behavior stayed on the baseline (shadow mode, or a gate
	// refusal in an active mode).
	DispositionShadowOnly DecisionDisposition = "shadow_only"
	// DispositionExactBypass means an exact deterministic answer existed,
	// so no call was made (plan 12.3 first row).
	DispositionExactBypass DecisionDisposition = "exact_bypass"
	// DispositionInsufficientEvidence means the snapshot failed the
	// eligibility bar, so no call was made.
	DispositionInsufficientEvidence DecisionDisposition = "insufficient_evidence"
	// DispositionBudgetDeclined means the allowance (settled plus
	// outstanding) could not cover the conservative reservation.
	DispositionBudgetDeclined DecisionDisposition = "budget_declined"
	// DispositionUncalibrated means no calibrated class estimate exists,
	// so the active economic gate cannot run; the class stays baseline.
	DispositionUncalibrated DecisionDisposition = "uncalibrated"
	// DispositionStale means the source or prompt layout changed after
	// evaluation; the action is discarded while the charge is retained.
	DispositionStale DecisionDisposition = "stale"
	// DispositionProviderFailure means the transport or answer failed;
	// baseline continues with the degradation visible.
	DispositionProviderFailure DecisionDisposition = "provider_failure"
)

// DecisionDispositionRecord pairs a disposition with its named reason and
// the accounting identity of the attempt. A skipped request is never
// logged as a completed free call: zero-usage records carry the
// disposition explaining why nothing was spent.
type DecisionDispositionRecord struct {
	Disposition DecisionDisposition `json:"disposition"`
	Reason      string              `json:"reason"`
	Snapshot    SemanticSnapshot    `json:"snapshot"`
	// Assessment is nil unless a provider answer exists.
	Assessment *DecisionAssessment `json:"assessment,omitempty"`
	// Economic is nil unless the host ran gate arithmetic.
	Economic *EconomicAssessment `json:"economic,omitempty"`
	// RequestID and charge identity are copied from the ledger record when
	// a billable attempt occurred; they stay empty for bypassed calls.
	RequestID     string   `json:"request_id,omitempty"`
	ChargedUSD    *float64 `json:"charged_usd,omitempty"`
	InputTokens   int      `json:"input_tokens,omitempty"`
	OutputTokens  int      `json:"output_tokens,omitempty"`
	RecordedAtUTC string   `json:"recorded_at_utc,omitempty"`
	// EvidenceShadow holds the builder that was not used for the boundary's
	// IterationState values, one entry per History entry. A replay swaps it in
	// to rerun the rules on the other workspace evidence. Outside Snapshot.
	EvidenceShadow []PassEvidence `json:"evidence_shadow,omitempty"`
}

// DecisionDeliveryRecord pins what a decision caused to be delivered
// (plan 11.3). Producers arrive with the bounded selector (M4); the schema
// is fixed here so traces and tests can already key on it.
type DecisionDeliveryRecord struct {
	Snapshot         SemanticSnapshot `json:"snapshot"`
	SelectedRef      string           `json:"selected_ref"`
	MaterializedPath string           `json:"materialized_path,omitempty"`
	ContentDigest    string           `json:"content_digest,omitempty"`
	DeliveryContext  string           `json:"delivery_context,omitempty"`
	RequestID        string           `json:"request_id,omitempty"`
}

// DecisionOutcomeRecord pins what the delivery caused (plan 11.3): the
// observed economic truth a later calibration store learns from.
type DecisionOutcomeRecord struct {
	Snapshot       SemanticSnapshot       `json:"snapshot"`
	Delivery       DecisionDeliveryRecord `json:"delivery"`
	Verification   string                 `json:"verification,omitempty"`
	EpisodeCostUSD *float64               `json:"episode_cost_usd,omitempty"`
	LatencyAddedMS int                    `json:"latency_added_ms,omitempty"`
	Intervention   bool                   `json:"intervention,omitempty"`
	UsedInPayload  bool                   `json:"used_in_payload,omitempty"`
	RecordedAtUTC  string                 `json:"recorded_at_utc,omitempty"`
}
