package v2

import "fmt"

// TraceLookupKey is the exact identity of one scheduled attempt and its
// persisted run/session. It is consumed by the future runner trace fetch.
type TraceLookupKey struct {
	ExperimentID     string `json:"experiment_id"`
	TaskID           string `json:"task_id"`
	Arm              string `json:"arm"`
	RepetitionID     int    `json:"repetition_id"`
	EnvironmentBlock int    `json:"environment_block"`
	RunID            string `json:"run_id"`
	SessionID        string `json:"session_id"`
}

// Validate checks the complete lookup identity. Empty fields are never
// treated as wildcards or fallback filters.
func (k TraceLookupKey) Validate() error {
	trial := TrialKey{ExperimentID: k.ExperimentID, TaskID: k.TaskID, Arm: k.Arm, RepetitionID: k.RepetitionID, EnvironmentBlock: k.EnvironmentBlock}
	if err := trial.Validate(); err != nil {
		return fmt.Errorf("trace lookup trial: %w", err)
	}
	if k.RunID == "" || k.SessionID == "" {
		return fmt.Errorf("trace lookup requires run_id and session_id")
	}
	return nil
}

// TraceQuerySpec is the dependency-free mirror of the runner and memd exact
// trace filter. A runner pairing test must assert this JSON shape round-trips
// through memd.Client.QueryTraces unchanged. New filter fields require that
// pairing test to change and fail until both sides are updated.
type TraceQuerySpec struct {
	ExperimentID     string `json:"experiment_id"`
	TaskID           string `json:"task_id"`
	Arm              string `json:"arm"`
	RepetitionID     int    `json:"repetition_id"`
	EnvironmentBlock int    `json:"environment_block"`
	RunID            string `json:"run_id"`
	SessionID        string `json:"session_id"`
}

// LookupFilter constructs an exact, non-prefix trace filter.
func LookupFilter(k TraceLookupKey) TraceQuerySpec {
	return TraceQuerySpec{
		ExperimentID: k.ExperimentID, TaskID: k.TaskID, Arm: k.Arm,
		RepetitionID: k.RepetitionID, EnvironmentBlock: k.EnvironmentBlock,
		RunID: k.RunID, SessionID: k.SessionID,
	}
}

// TraceLookupResult is the minimal identity mirror returned by the future
// trace runner. It avoids importing splice trace schemas in this package.
type TraceLookupResult struct {
	ExperimentID     string `json:"experiment_id"`
	TaskID           string `json:"task_id"`
	Arm              string `json:"arm"`
	RepetitionID     int    `json:"repetition_id"`
	EnvironmentBlock int    `json:"environment_block"`
	RunID            string `json:"run_id"`
	SessionID        string `json:"session_id"`
}

// ValidateLookupResults enforces zero-or-one exact result semantics and names
// duplicate identities. It never falls back to repository or prefix matching.
func ValidateLookupResults(k TraceLookupKey, results []TraceLookupResult) error {
	if err := k.Validate(); err != nil {
		return err
	}
	if len(results) <= 1 {
		if len(results) == 1 && results[0] != (TraceLookupResult(LookupFilter(k))) {
			return fmt.Errorf("trace lookup result identity does not exactly match %s: %+v", k.RunID, results[0])
		}
		return nil
	}
	return fmt.Errorf("duplicate trace lookup identities for experiment=%q task=%q arm=%q repetition=%d environment=%d run=%q session=%q: %v", k.ExperimentID, k.TaskID, k.Arm, k.RepetitionID, k.EnvironmentBlock, k.RunID, k.SessionID, results)
}

// CheckTraceLookupResults is an explicit alias for runner callers.
func CheckTraceLookupResults(k TraceLookupKey, results []TraceLookupResult) error {
	return ValidateLookupResults(k, results)
}
