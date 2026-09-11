// Package warmcost implements the paired cold-versus-warm measurement design
// in tests/evals/cognition-families/MEASUREMENT_DESIGN.md.
//
// The package is out of CI: it has no *_test.go files, so `go test ./...`
// never runs it. It is a release-cadence tool. It never selects a provider or
// a model by itself; the operator supplies the exact approved exec command in
// Part 3 of the mission.
package warmcost

import (
	"time"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Arm is one measurement arm.
type Arm string

const (
	// ArmCold is the no-retained-experience control.
	ArmCold Arm = "cold"
	// ArmWarm has retained experience and both mechanisms enabled.
	ArmWarm Arm = "warm"
	// ArmRetrievalOnly retrieves and records but does not deliver, so
	// retrieval overhead is separated from delivery and work removal.
	ArmRetrievalOnly Arm = "warm-retrieval-only"
)

// DefaultArms is the arm order the report uses.
var DefaultArms = []Arm{ArmCold, ArmWarm, ArmRetrievalOnly}

// Env returns the explicit treatment environment for one arm. Both switches
// are always set so an ambient value can never leak into a measurement.
func (a Arm) Env() []string {
	switch a {
	case ArmWarm:
		return []string{"SPLICE_SCOPE_MODE=on", "SPLICE_EVIDENCE_SUBSTITUTION=on"}
	case ArmRetrievalOnly:
		return []string{
			"SPLICE_SCOPE_MODE=on",
			"SPLICE_EVIDENCE_SUBSTITUTION=off",
			"SPLICE_EXEMPLAR_MODE=retrieve-no-prompt",
		}
	case ArmCold:
		return []string{"SPLICE_SCOPE_MODE=off", "SPLICE_EVIDENCE_SUBSTITUTION=off"}
	default:
		return nil
	}
}

// MemoryMode returns the --memory value for one arm. Cold disables the
// sidecar; every other arm enables it.
func (a Arm) MemoryMode() string {
	if a == ArmCold {
		return "off"
	}
	return "on"
}

// Valid reports whether the arm is one of the known arms.
func (a Arm) Valid() bool {
	switch a {
	case ArmCold, ArmWarm, ArmRetrievalOnly:
		return true
	}
	return false
}

// Task is one task in the measurement corpus.
type Task struct {
	ID      string `json:"id"`
	Prompt  string `json:"prompt"`
	Check   string `json:"check"`
	Fixture string `json:"fixture,omitempty"`
}

// ModelSettings records the model configuration an attempt used.
type ModelSettings struct {
	Provider        string `json:"provider,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	MaxTurns        string `json:"max_turns,omitempty"`
}

// Totals is the per-attempt total copied from the authoritative ledger
// fields. It is never recomputed from stage rows.
type Totals struct {
	Requests      int     `json:"requests"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	CachedTokens  int     `json:"cached_input_tokens"`
	CacheWrite    int     `json:"cache_write_tokens"`
	Reasoning     int     `json:"reasoning_tokens"`
	BilledUSD     float64 `json:"billed_usd"`
	PricedRecords int     `json:"priced_records"`
}

// RequestRecord is one provider request in the attempt artifact. It mirrors
// the authoritative ledger record, but the identity fields carry no omitempty,
// so a zero InvocationOrdinal or ContextRound is still recorded explicitly.
type RequestRecord struct {
	Sequence          int      `json:"sequence"`
	Stage             string   `json:"stage"`
	Iteration         int      `json:"iteration"`
	InvocationOrdinal int      `json:"invocation_ordinal"`
	ContextRound      int      `json:"context_round"`
	SpendSource       string   `json:"spend_source"`
	InputTokens       int      `json:"input_tokens"`
	OutputTokens      int      `json:"output_tokens"`
	CachedTokens      int      `json:"cached_input_tokens"`
	CacheWrite        int      `json:"cache_write_tokens"`
	Reasoning         int      `json:"reasoning_tokens"`
	CostUSD           *float64 `json:"cost_usd"`
	CostStatus        string   `json:"cost_status"`
}

// requestRecords maps the authoritative ledger into the explicit artifact
// shape. Nothing is recomputed.
func requestRecords(recs []schemas.PipelineUsageRecord) []RequestRecord {
	out := make([]RequestRecord, 0, len(recs))
	for _, r := range recs {
		out = append(out, RequestRecord{
			Sequence:          r.Sequence,
			Stage:             r.Stage,
			Iteration:         r.Iteration,
			InvocationOrdinal: r.InvocationOrdinal,
			ContextRound:      r.ContextRound,
			SpendSource:       r.SpendSource,
			InputTokens:       r.InputTokens,
			OutputTokens:      r.OutputTokens,
			CachedTokens:      r.CachedTokens,
			CacheWrite:        r.CacheWrite,
			Reasoning:         r.Reasoning,
			CostUSD:           r.CostUSD,
			CostStatus:        r.CostStatus,
		})
	}
	return out
}

// Attempt is the per-attempt JSON artifact. Every number comes from the
// captured ledger or the verifier, never from a hand sum.
type Attempt struct {
	TaskID          string          `json:"task_id"`
	Arm             Arm             `json:"arm"`
	Repeat          int             `json:"repeat"`
	BinaryRevision  string          `json:"binary_revision"`
	SidecarRevision string          `json:"sidecar_revision"`
	FixtureDigest   string          `json:"fixture_digest"`
	ModelID         string          `json:"model_id"`
	ModelSettings   ModelSettings   `json:"model_settings"`
	TreatmentEnv    []string        `json:"treatment_env"`
	SessionID       string          `json:"session_id"`
	RunStatus       string          `json:"run_status"`
	CostCoverage    string          `json:"cost_coverage"`
	VerifierResult  bool            `json:"verifier_result"`
	VerifierOutput  string          `json:"verifier_output,omitempty"`
	Requests        []RequestRecord `json:"requests"`
	Totals          Totals          `json:"totals"`
	LedgerError     string          `json:"ledger_error,omitempty"`
	StartedAt       time.Time       `json:"started_at"`
	EndedAt         time.Time       `json:"ended_at"`
	DurationMS      int64           `json:"duration_ms"`
}

// CompleteCoverage reports whether every captured request carries a priced
// cost. A missing price is unknown, never zero.
func (a Attempt) CompleteCoverage() bool {
	if a.CostCoverage == schemas.CostCoverageComplete {
		return true
	}
	if a.CostCoverage != "" {
		return false
	}
	if len(a.Requests) == 0 {
		return false
	}
	for _, r := range a.Requests {
		if r.CostStatus != schemas.CostStatusPriced || r.CostUSD == nil {
			return false
		}
	}
	return true
}

// Version is the report schema version.
const Version = 1
