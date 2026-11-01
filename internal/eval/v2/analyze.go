package v2

import "fmt"

// SensitivityCheck names one locked efficacy sensitivity analysis from
// protocol section 8.7.
type SensitivityCheck string

const (
	SensitivityExcludeNoTask         SensitivityCheck = "exclude_no_task"
	SensitivityTraceVsStreamFallback SensitivityCheck = "trace_vs_stream_fallback"
	SensitivityCampaignVsPairedCost  SensitivityCheck = "campaign_vs_paired_cost"
	SensitivityFixedVsHierarchical   SensitivityCheck = "fixed_vs_hierarchical"
	SensitivityArmOrderDirection     SensitivityCheck = "arm_order_direction"
	SensitivityRepositoryDriver      SensitivityCheck = "repository_driver"

	SensitivitySafetyDispositions      SensitivityCheck = "safety_dispositions"
	SensitivitySafetyInvalidClaims     SensitivityCheck = "safety_invalid_claims"
	SensitivitySafetyEvidenceChecks    SensitivityCheck = "safety_evidence_checks"
	SensitivitySafetyRepairRetry       SensitivityCheck = "safety_repair_retry"
	SensitivitySafetyTokensCostLatency SensitivityCheck = "safety_tokens_cost_latency"
	SensitivitySafetyArmOrder          SensitivityCheck = "safety_arm_order"
	SensitivitySafetyStaleOnlyNone     SensitivityCheck = "safety_stale_only_none"
)

// LockedEfficacySensitivityChecks returns the complete preregistered set.
func LockedEfficacySensitivityChecks() []SensitivityCheck {
	return []SensitivityCheck{
		SensitivityExcludeNoTask,
		SensitivityTraceVsStreamFallback,
		SensitivityCampaignVsPairedCost,
		SensitivityFixedVsHierarchical,
		SensitivityArmOrderDirection,
		SensitivityRepositoryDriver,
	}
}

// LockedSafetySensitivityChecks returns the complete preregistered safety
// diagnostic set.
func LockedSafetySensitivityChecks() []SensitivityCheck {
	return []SensitivityCheck{
		SensitivitySafetyDispositions,
		SensitivitySafetyInvalidClaims,
		SensitivitySafetyEvidenceChecks,
		SensitivitySafetyRepairRetry,
		SensitivitySafetyTokensCostLatency,
		SensitivitySafetyArmOrder,
		SensitivitySafetyStaleOnlyNone,
	}
}

// ValidSensitivityCheck reports whether check is known.
func ValidSensitivityCheck(check SensitivityCheck) bool {
	for _, want := range append(LockedEfficacySensitivityChecks(), LockedSafetySensitivityChecks()...) {
		if check == want {
			return true
		}
	}
	return false
}

// AnalysisPlan locks the uncertainty and gate machinery before the run. The
// fixed sequence is part of error control.
type AnalysisPlan struct {
	IntervalMethod  IntervalMethod     `json:"interval_method"`
	ConfidenceLevel float64            `json:"confidence_level"`
	Resamples       int                `json:"resamples"`
	Seed            int64              `json:"seed"`
	GateOrder       []string           `json:"gate_order"`
	SecondaryHolm   bool               `json:"secondary_holm"`
	Sensitivity     []SensitivityCheck `json:"sensitivity"`
}

// Validate checks the plan against its protocol.
func (a AnalysisPlan) Validate(p Protocol) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("protocol: %w", err)
	}
	if a.IntervalMethod != p.IntervalMethod {
		return fmt.Errorf("analysis interval method %q does not match the locked protocol method %q",
			a.IntervalMethod, p.IntervalMethod)
	}
	if a.ConfidenceLevel != protocolConfidence(p) {
		return fmt.Errorf("analysis confidence_level %v does not match the locked protocol", a.ConfidenceLevel)
	}
	if a.Resamples != p.Resamples {
		return fmt.Errorf("analysis resamples %d does not match the locked protocol resamples %d", a.Resamples, p.Resamples)
	}
	if a.Seed != p.Seed {
		return fmt.Errorf("analysis seed %d does not match the locked protocol seed %d", a.Seed, p.Seed)
	}
	wantGates := LockedGateOrder()
	if p.Kind == ExperimentSafety {
		wantGates = LockedSafetyGateOrder()
	}
	if len(a.GateOrder) != len(wantGates) {
		return fmt.Errorf("gate_order must contain exactly %d gates", len(wantGates))
	}
	for i, gate := range wantGates {
		if a.GateOrder[i] != gate {
			return fmt.Errorf("gate_order[%d] = %q, want %q", i, a.GateOrder[i], gate)
		}
	}
	if a.SecondaryHolm != p.SecondaryHolm {
		return fmt.Errorf("analysis secondary_holm %t does not match the locked protocol", a.SecondaryHolm)
	}
	seen := make(map[SensitivityCheck]bool)
	for i, check := range a.Sensitivity {
		if !ValidSensitivityCheck(check) {
			return fmt.Errorf("sensitivity[%d]: unknown check %q", i, check)
		}
		if seen[check] {
			return fmt.Errorf("sensitivity[%d]: duplicate check %q", i, check)
		}
		seen[check] = true
	}
	if p.Kind == ExperimentPrimary {
		want := LockedEfficacySensitivityChecks()
		if len(a.Sensitivity) != len(want) {
			return fmt.Errorf("efficacy analysis requires exactly %d sensitivity checks", len(want))
		}
		for _, check := range want {
			if !seen[check] {
				return fmt.Errorf("efficacy analysis is missing sensitivity check %q", check)
			}
		}
	} else {
		want := LockedSafetySensitivityChecks()
		if len(a.Sensitivity) != len(want) {
			return fmt.Errorf("safety analysis requires exactly %d sensitivity checks", len(want))
		}
		for _, check := range want {
			if !seen[check] {
				return fmt.Errorf("safety analysis is missing sensitivity check %q", check)
			}
		}
	}
	return nil
}

func protocolConfidence(p Protocol) float64 {
	if p.Kind == ExperimentSafety {
		return p.SafetyMargins.ConfidenceLevel
	}
	return p.Margins.ConfidenceLevel
}

// Verdict is the exact final experiment verdict class.
type Verdict string

const (
	VerdictEfficacySupported    Verdict = "efficacy_supported"
	VerdictEfficacyNotSupported Verdict = "efficacy_not_supported"
	VerdictEfficacyInconclusive Verdict = "efficacy_inconclusive"
	VerdictSafetySupported      Verdict = "safety_supported"
	VerdictSafetyNotSupported   Verdict = "safety_not_supported"
	VerdictSafetyInconclusive   Verdict = "safety_inconclusive"
	VerdictIncomplete           Verdict = "incomplete"
	VerdictInvalid              Verdict = "invalid"
)

// GateOutcome records one locked gate's estimate and interval.
type GateOutcome struct {
	Name            string  `json:"name"`
	Passed          bool    `json:"passed"`
	Estimate        float64 `json:"estimate"`
	LowerBound      float64 `json:"lower_bound"`
	UpperBound      float64 `json:"upper_bound"`
	ConfidenceLevel float64 `json:"confidence_level"`
}

func (g GateOutcome) validate() error {
	if g.Name == "" {
		return fmt.Errorf("gate name is required")
	}
	if !finite(g.Estimate) || !finite(g.LowerBound) || !finite(g.UpperBound) || !finite(g.ConfidenceLevel) {
		return fmt.Errorf("gate %s contains a non-finite value", g.Name)
	}
	if g.LowerBound > g.UpperBound {
		return fmt.Errorf("gate %s lower_bound exceeds upper_bound", g.Name)
	}
	if g.ConfidenceLevel <= 0 || g.ConfidenceLevel >= 1 {
		return fmt.Errorf("gate %s confidence_level must be in (0,1)", g.Name)
	}
	return nil
}

// Report is the private experiment report shell. Later checkpoints fill the
// estimators, but every verdict still carries an honest typed gate set.
type Report struct {
	ExperimentID   string         `json:"experiment_id"`
	Kind           ExperimentKind `json:"kind"`
	ManifestHash   string         `json:"manifest_hash"`
	Verdict        Verdict        `json:"verdict"`
	Gates          []GateOutcome  `json:"gates,omitempty"`
	ExcludedTrials []TrialResult  `json:"excluded_trials,omitempty"`
	Notes          []string       `json:"notes,omitempty"`
}

// Validate checks report structure. A report with gates or excluded trials
// needs protocol context; use ValidateFor before any claim decision.
func (r Report) Validate() error {
	return r.validate(nil)
}

// ValidateFor checks report semantics against the locked protocol. It binds
// verdicts, gates, margins, trial identities, and fixed-sequence stopping.
func (r Report) ValidateFor(p Protocol) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("protocol: %w", err)
	}
	if r.ExperimentID != p.ExperimentID {
		return fmt.Errorf("report experiment_id %q does not match protocol %q", r.ExperimentID, p.ExperimentID)
	}
	if r.Kind != p.Kind {
		return fmt.Errorf("report kind %q does not match protocol kind %q", r.Kind, p.Kind)
	}
	return r.validate(&p)
}

func (r Report) validate(p *Protocol) error {
	if r.ExperimentID == "" {
		return fmt.Errorf("experiment_id is required")
	}
	if !ValidExperimentKind(r.Kind) {
		return fmt.Errorf("invalid report kind %q", r.Kind)
	}
	if !validHash(r.ManifestHash) {
		return fmt.Errorf("manifest_hash must be a sha256 hex digest")
	}
	wantGates := LockedGateOrder()
	if r.Kind == ExperimentSafety {
		wantGates = LockedSafetyGateOrder()
	}
	switch r.Verdict {
	case VerdictEfficacySupported, VerdictEfficacyNotSupported, VerdictEfficacyInconclusive:
		if r.Kind != ExperimentPrimary {
			return fmt.Errorf("verdict %q requires a primary report", r.Verdict)
		}
	case VerdictSafetySupported, VerdictSafetyNotSupported, VerdictSafetyInconclusive:
		if r.Kind != ExperimentSafety {
			return fmt.Errorf("verdict %q requires a safety report", r.Verdict)
		}
	case VerdictIncomplete, VerdictInvalid:
	default:
		return fmt.Errorf("unknown verdict %q", r.Verdict)
	}
	needsGates := r.Verdict != VerdictIncomplete && r.Verdict != VerdictInvalid
	if needsGates {
		if len(r.Gates) != len(wantGates) {
			return fmt.Errorf("report requires exactly %d gate outcomes", len(wantGates))
		}
		if p == nil {
			return fmt.Errorf("%s report requires protocol context; use ValidateFor", r.Verdict)
		}
		allPassed := true
		priorFailed := false
		for i, gate := range r.Gates {
			if gate.Name != wantGates[i] {
				return fmt.Errorf("gates[%d] = %q, want %q", i, gate.Name, wantGates[i])
			}
			if err := gate.validate(); err != nil {
				return fmt.Errorf("gates[%d]: %w", i, err)
			}
			if !closeEnough(gate.ConfidenceLevel, protocolConfidence(*p)) {
				return fmt.Errorf("gates[%d] confidence_level %v does not match protocol %v", i, gate.ConfidenceLevel, protocolConfidence(*p))
			}
			if priorFailed && gate.Passed {
				return fmt.Errorf("gate %q passed after an earlier fixed-sequence gate failed", gate.Name)
			}
			if !priorFailed {
				if expected, checked := lockedGatePasses(gate, *p); checked && gate.Passed != expected {
					return fmt.Errorf("gate %q passed=%t contradicts its locked interval threshold", gate.Name, gate.Passed)
				}
			}
			if !gate.Passed {
				allPassed = false
				priorFailed = true
			}
		}
		if r.Verdict == VerdictEfficacySupported || r.Verdict == VerdictSafetySupported {
			if !allPassed {
				return fmt.Errorf("supported verdict requires every gate to pass")
			}
		} else if allPassed {
			return fmt.Errorf("%s verdict contradicts an all-passed gate sequence", r.Verdict)
		}
	} else if len(r.Gates) != 0 {
		return fmt.Errorf("%s report cannot carry efficacy gate outcomes", r.Verdict)
	}
	if len(r.ExcludedTrials) > 0 && p == nil {
		return fmt.Errorf("excluded trials require protocol context; use ValidateFor")
	}
	for i, trial := range r.ExcludedTrials {
		var err error
		if p == nil {
			err = trial.Validate()
		} else {
			err = trial.ValidateFor(*p)
		}
		if err != nil {
			return fmt.Errorf("excluded_trials[%d]: %w", i, err)
		}
		if trial.Status == TrialValid {
			return fmt.Errorf("excluded_trials[%d] cannot contain a valid trial", i)
		}
	}
	return nil
}

func lockedGatePasses(g GateOutcome, p Protocol) (bool, bool) {
	switch g.Name {
	case GateSuccessNonInferiority:
		return g.LowerBound > -p.Margins.DeltaSuccess, true
	case GateNetEfficiency:
		return g.UpperBound < 1-p.Margins.MinTokenGainEmpty, true
	case GateContentRelevance:
		return g.UpperBound < 1-p.Margins.MinTokenGainPlacebo, true
	case GateMixedContextSuccess:
		return g.LowerBound > -p.SafetyMargins.DeltaSuccess, true
	case GateStaleReferenceHarm:
		return g.UpperBound <= p.SafetyMargins.MaxStaleReferenceRate, true
	case GateForbiddenInstruction:
		return g.UpperBound <= p.SafetyMargins.MaxForbiddenActionRate, true
	default:
		return false, false
	}
}
