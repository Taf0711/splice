// Package v2 holds the Eval v2 protocol types: closed enums, strict
// validators, and the locked-manifest contract for claim-grade experiments.
// It is schemas only at this checkpoint (EV2-0): scheduling, persistence,
// isolation, telemetry collection, and analysis arrive in later checkpoints.
//
// Validation is loud. Every rule violation names the offending field, and no
// validator substitutes a default for missing data.
package v2

import (
	"fmt"
	"math"
	"regexp"
)

// ProtocolFamily is the fixed protocol family identifier.
const ProtocolFamily = "splice.eval.v2"

// ProtocolVersion is the fixed serialized protocol version.
const ProtocolVersion = "splice.eval.v2"

// ArmName values are exact strings shared with manifests, schedules, and
// traces. The three primary arms come from protocol section 4.1.
const (
	ArmEmptyControl   = "empty_control"
	ArmHardPlacebo    = "hard_placebo"
	ArmRelevantFrozen = "relevant_frozen"
)

// Safety-matrix arm names from SAFETY_PREREGISTRATION.md section 6. They
// run under a separate contract and never enter the primary estimate.
const (
	ArmSafetyNone             = "none"
	ArmSafetyCurrentOnly      = "current_only"
	ArmSafetyStaleOnly        = "stale_only"
	ArmSafetyCurrentThenStale = "current_then_stale"
	ArmSafetyStaleThenCurrent = "stale_then_current"
	ArmSafetyConflicting      = "conflicting_current"
	ArmSafetyInstructionLike  = "instruction_like"
)

// PrimaryArms returns the three primary arm names in protocol order.
func PrimaryArms() []string {
	return []string{ArmEmptyControl, ArmHardPlacebo, ArmRelevantFrozen}
}

// SafetyArms returns the seven safety arms in preregistration order.
func SafetyArms() []string {
	return []string{ArmSafetyNone, ArmSafetyCurrentOnly, ArmSafetyStaleOnly,
		ArmSafetyCurrentThenStale, ArmSafetyStaleThenCurrent,
		ArmSafetyConflicting, ArmSafetyInstructionLike}
}

// ValidArm reports whether name is a known primary or safety arm.
func ValidArm(name string) bool {
	for _, arm := range append(PrimaryArms(), SafetyArms()...) {
		if name == arm {
			return true
		}
	}
	return false
}

// ExperimentKind selects which decision contract an experiment uses.
type ExperimentKind string

const (
	ExperimentPrimary ExperimentKind = "primary"
	ExperimentSafety  ExperimentKind = "safety"
)

// ValidExperimentKind reports whether kind is known.
func ValidExperimentKind(kind ExperimentKind) bool {
	return kind == ExperimentPrimary || kind == ExperimentSafety
}

// RunMode distinguishes claim runs from development work. Claim mode denies
// observation and exemplar writes.
type RunMode string

const (
	RunModeDevelopment RunMode = "development"
	RunModeClaim       RunMode = "claim"
)

// ValidRunMode reports whether mode is known.
func ValidRunMode(mode RunMode) bool {
	return mode == RunModeDevelopment || mode == RunModeClaim
}

// GateName values are the five primary decision gates in their locked
// fixed-sequence order.
const (
	GateSuccessNonInferiority = "success_non_inferiority"
	GateNetEfficiency         = "net_efficiency"
	GateContentRelevance      = "content_relevance"
)

// LockedGateOrder returns the mandatory gate sequence. The sequence is part
// of the primary error control and cannot be reordered.
func LockedGateOrder() []string {
	return []string{GateArtifactValidity, GateSampleCompleteness,
		GateSuccessNonInferiority, GateNetEfficiency, GateContentRelevance}
}

const (
	GateArtifactValidity     = "artifact_validity"
	GateSampleCompleteness   = "sample_completeness"
	GateMixedContextSuccess  = "mixed_context_success"
	GateStaleReferenceHarm   = "stale_reference_harm"
	GateForbiddenInstruction = "forbidden_instruction_action"
	GatePrimaryMultiplicity  = "primary_multiplicity"
)

// LockedSafetyGateOrder returns the separate safety decision sequence.
func LockedSafetyGateOrder() []string {
	return []string{GateArtifactValidity, GateSampleCompleteness,
		GateMixedContextSuccess, GateStaleReferenceHarm,
		GateForbiddenInstruction, GatePrimaryMultiplicity}
}

// IntervalMethod names the locked bootstrap interval algorithm.
type IntervalMethod string

const (
	IntervalPercentile IntervalMethod = "percentile"
	IntervalBCa        IntervalMethod = "bca"
)

// ValidIntervalMethod reports whether method is known.
func ValidIntervalMethod(method IntervalMethod) bool {
	return method == IntervalPercentile || method == IntervalBCa
}

// MinBootstrapResamples is the protocol floor for interval resamples.
const MinBootstrapResamples = 10000

// sha256Hex matches a lowercase hexadecimal SHA-256 digest.
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// validHash reports whether s looks like a SHA-256 hex digest.
func validHash(s string) bool { return sha256Hex.MatchString(s) }

// ArmSpec describes one experimental arm and its delivered-memory shape.
type ArmSpec struct {
	Name string `json:"name"`
	// DeliveredMemory describes what the arm delivers: one of "none",
	// "matched_placebo", "relevant_frozen", or a safety-matrix description.
	DeliveredMemory string `json:"delivered_memory"`
}

// Validate checks the arm declaration and its locked memory shape.
func (a ArmSpec) Validate() error {
	if !ValidArm(a.Name) {
		return fmt.Errorf("unknown arm %q", a.Name)
	}
	want := map[string]string{
		ArmEmptyControl:           "none",
		ArmHardPlacebo:            "matched_placebo",
		ArmRelevantFrozen:         "relevant_frozen",
		ArmSafetyNone:             "none",
		ArmSafetyCurrentOnly:      "current_only",
		ArmSafetyStaleOnly:        "stale_only",
		ArmSafetyCurrentThenStale: "current_then_stale",
		ArmSafetyStaleThenCurrent: "stale_then_current",
		ArmSafetyConflicting:      "conflicting_current",
		ArmSafetyInstructionLike:  "instruction_like",
	}[a.Name]
	if a.DeliveredMemory != want {
		return fmt.Errorf("arm %s: delivered_memory must be %q, got %q", a.Name, want, a.DeliveredMemory)
	}
	return nil
}

// DecisionMargins holds the owner-locked product margins. Planning defaults
// are not approvals: a locked protocol needs explicit values.
type DecisionMargins struct {
	// DeltaSuccess is the success non-inferiority margin. The gate passes
	// when the lower interval bound exceeds -DeltaSuccess.
	DeltaSuccess float64 `json:"delta_success"`
	// MinTokenGainEmpty is the minimum useful token gain versus empty control.
	MinTokenGainEmpty float64 `json:"min_token_gain_empty"`
	// MinTokenGainPlacebo is the minimum content gain versus hard placebo.
	MinTokenGainPlacebo float64 `json:"min_token_gain_placebo"`
	// ConfidenceLevel is the interval coverage, for example 0.95.
	ConfidenceLevel float64 `json:"confidence_level"`
	// AlphaTarget bounds false support for the fixed-sequence procedure.
	AlphaTarget float64 `json:"alpha_target"`
}

// Validate checks the efficacy margins are present and finite.
func (m DecisionMargins) Validate() error {
	for _, bad := range []struct {
		name  string
		value float64
	}{
		{"delta_success", m.DeltaSuccess},
		{"min_token_gain_empty", m.MinTokenGainEmpty},
		{"min_token_gain_placebo", m.MinTokenGainPlacebo},
	} {
		if !finite(bad.value) || bad.value < 0 || bad.value >= 1 {
			return fmt.Errorf("%s must be finite and in [0,1), got %v", bad.name, bad.value)
		}
	}
	if !finite(m.ConfidenceLevel) || m.ConfidenceLevel <= 0 || m.ConfidenceLevel >= 1 {
		return fmt.Errorf("confidence_level must be finite and in (0,1), got %v", m.ConfidenceLevel)
	}
	if !finite(m.AlphaTarget) || m.AlphaTarget <= 0 || m.AlphaTarget >= 1 {
		return fmt.Errorf("alpha_target must be finite and in (0,1), got %v", m.AlphaTarget)
	}
	return nil
}

// SafetyMargins holds the separate safety preregistration limits.
type SafetyMargins struct {
	DeltaSuccess           float64 `json:"delta_safety_success"`
	MaxStaleReferenceRate  float64 `json:"max_stale_reference_rate"`
	MaxForbiddenActionRate float64 `json:"max_forbidden_action_rate"`
	ConfidenceLevel        float64 `json:"confidence_level"`
	AlphaTarget            float64 `json:"alpha_target"`
}

// Validate checks the safety margins.
func (m SafetyMargins) Validate() error {
	for _, field := range []struct {
		name  string
		value float64
	}{
		{"delta_safety_success", m.DeltaSuccess},
		{"max_stale_reference_rate", m.MaxStaleReferenceRate},
		{"max_forbidden_action_rate", m.MaxForbiddenActionRate},
	} {
		if !finite(field.value) || field.value < 0 || field.value > 1 {
			return fmt.Errorf("%s must be finite and in [0,1], got %v", field.name, field.value)
		}
	}
	if !finite(m.ConfidenceLevel) || m.ConfidenceLevel <= 0 || m.ConfidenceLevel >= 1 {
		return fmt.Errorf("safety confidence_level must be finite and in (0,1), got %v", m.ConfidenceLevel)
	}
	if !finite(m.AlphaTarget) || m.AlphaTarget <= 0 || m.AlphaTarget >= 1 {
		return fmt.Errorf("safety alpha_target must be finite and in (0,1), got %v", m.AlphaTarget)
	}
	return nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

// Protocol is the top-level experiment definition. It fixes the question,
// arms, margins, gate order, repetition count, seed, and spend cap before
// any provider call.
type Protocol struct {
	Family          string           `json:"family"`
	Version         string           `json:"version"`
	ExperimentID    string           `json:"experiment_id"`
	Kind            ExperimentKind   `json:"kind"`
	Mode            RunMode          `json:"mode"`
	Arms            []ArmSpec        `json:"arms"`
	Margins         *DecisionMargins `json:"margins,omitempty"`
	SafetyMargins   *SafetyMargins   `json:"safety_margins,omitempty"`
	GateOrder       []string         `json:"gate_order"`
	IntervalMethod  IntervalMethod   `json:"interval_method"`
	Resamples       int              `json:"resamples"`
	SecondaryHolm   bool             `json:"secondary_holm"`
	Seed            int64            `json:"seed"`
	Repetitions     int              `json:"repetitions"`
	HardSpendCapUSD float64          `json:"hard_spend_cap_usd"`
}

// Validate checks the protocol definition. It enforces the closed enums, the
// locked gate order, and positive bounded values.
func (p Protocol) Validate() error {
	if p.Family != ProtocolFamily {
		return fmt.Errorf("family must be %q, got %q", ProtocolFamily, p.Family)
	}
	if p.Version != ProtocolVersion {
		return fmt.Errorf("version must be %q, got %q", ProtocolVersion, p.Version)
	}
	if p.ExperimentID == "" {
		return fmt.Errorf("experiment_id is required")
	}
	if !ValidExperimentKind(p.Kind) {
		return fmt.Errorf("invalid experiment kind %q", p.Kind)
	}
	if !ValidRunMode(p.Mode) {
		return fmt.Errorf("invalid run mode %q", p.Mode)
	}
	if p.Mode == RunModeClaim && p.Kind != ExperimentPrimary && p.Kind != ExperimentSafety {
		return fmt.Errorf("claim mode requires a primary or safety experiment")
	}
	if len(p.Arms) == 0 {
		return fmt.Errorf("at least one arm is required")
	}
	seen := make(map[string]bool, len(p.Arms))
	for i, arm := range p.Arms {
		if seen[arm.Name] {
			return fmt.Errorf("duplicate arm %q", arm.Name)
		}
		if err := arm.Validate(); err != nil {
			return fmt.Errorf("arms[%d]: %w", i, err)
		}
		seen[arm.Name] = true
	}
	wantArms := PrimaryArms()
	wantGates := LockedGateOrder()
	if p.Kind == ExperimentSafety {
		wantArms = SafetyArms()
		wantGates = LockedSafetyGateOrder()
	}
	if len(p.Arms) != len(wantArms) {
		return fmt.Errorf("%s experiment must contain exactly %d arms", p.Kind, len(wantArms))
	}
	for _, want := range wantArms {
		if !seen[want] {
			return fmt.Errorf("%s experiment is missing arm %q", p.Kind, want)
		}
	}
	for _, arm := range p.Arms {
		if p.Kind == ExperimentPrimary && contains(SafetyArms(), arm.Name) {
			return fmt.Errorf("primary experiment cannot contain safety arm %q", arm.Name)
		}
		if p.Kind == ExperimentSafety && contains(PrimaryArms(), arm.Name) {
			return fmt.Errorf("safety experiment cannot contain primary arm %q", arm.Name)
		}
	}
	if p.Kind == ExperimentPrimary {
		if p.Margins == nil {
			return fmt.Errorf("margins are required for a primary experiment")
		}
		if err := p.Margins.Validate(); err != nil {
			return fmt.Errorf("margins: %w", err)
		}
		if p.SafetyMargins != nil {
			return fmt.Errorf("primary experiment cannot carry safety_margins")
		}
		if !p.SecondaryHolm {
			return fmt.Errorf("primary experiment requires Holm correction")
		}
	} else {
		if p.Margins != nil {
			return fmt.Errorf("safety experiment cannot carry efficacy margins")
		}
		if p.SafetyMargins == nil {
			return fmt.Errorf("safety_margins are required for a safety experiment")
		}
		if err := p.SafetyMargins.Validate(); err != nil {
			return fmt.Errorf("safety_margins: %w", err)
		}
		if !p.SecondaryHolm {
			return fmt.Errorf("safety experiment requires Holm correction")
		}
	}
	if len(p.GateOrder) != len(wantGates) {
		return fmt.Errorf("gate_order must contain exactly %d gates", len(wantGates))
	}
	for i, gate := range wantGates {
		if p.GateOrder[i] != gate {
			return fmt.Errorf("gate_order[%d] = %q, want %q; the fixed sequence is locked", i, p.GateOrder[i], gate)
		}
	}
	if !ValidIntervalMethod(p.IntervalMethod) {
		return fmt.Errorf("invalid interval method %q", p.IntervalMethod)
	}
	if p.Resamples < MinBootstrapResamples {
		return fmt.Errorf("resamples must be at least %d, got %d", MinBootstrapResamples, p.Resamples)
	}
	if p.Seed == 0 {
		return fmt.Errorf("seed is required and must be nonzero")
	}
	if p.Repetitions <= 0 {
		return fmt.Errorf("repetitions must be positive, got %d", p.Repetitions)
	}
	if !finite(p.HardSpendCapUSD) || p.HardSpendCapUSD <= 0 {
		return fmt.Errorf("hard_spend_cap_usd must be finite and positive, got %v", p.HardSpendCapUSD)
	}
	return nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
