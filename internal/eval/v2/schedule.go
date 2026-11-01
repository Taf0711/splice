package v2

import "fmt"

// TrialKey is the idempotent identity of one attempt: the same key must
// always map to the same scheduled work, and persistence deduplicates on it.
type TrialKey struct {
	ExperimentID     string `json:"experiment_id"`
	TaskID           string `json:"task_id"`
	Arm              string `json:"arm"`
	RepetitionID     int    `json:"repetition_id"`
	EnvironmentBlock int    `json:"environment_block"`
}

// Validate checks the key is complete and well-formed.
func (k TrialKey) Validate() error {
	if k.ExperimentID == "" {
		return fmt.Errorf("experiment_id is required")
	}
	if k.TaskID == "" {
		return fmt.Errorf("task_id is required")
	}
	if !ValidArm(k.Arm) {
		return fmt.Errorf("unknown arm %q", k.Arm)
	}
	if k.RepetitionID <= 0 {
		return fmt.Errorf("repetition_id must be positive, got %d", k.RepetitionID)
	}
	if k.EnvironmentBlock <= 0 {
		return fmt.Errorf("environment_block must be positive, got %d", k.EnvironmentBlock)
	}
	return nil
}

// String renders the canonical identity used in logs and stores.
func (k TrialKey) String() string {
	return k.ExperimentID + "/" + k.TaskID + "/" + k.Arm +
		"/rep" + itoa(k.RepetitionID) + "/env" + itoa(k.EnvironmentBlock)
}

// ValidateFor checks the key against one protocol. Use this method whenever
// a trial is interpreted with protocol context; Validate alone only checks
// the shape of the identity.
func (k TrialKey) ValidateFor(p Protocol) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("protocol: %w", err)
	}
	if err := k.Validate(); err != nil {
		return err
	}
	if k.ExperimentID != p.ExperimentID {
		return fmt.Errorf("trial experiment_id %q does not match protocol %q", k.ExperimentID, p.ExperimentID)
	}
	for _, arm := range p.Arms {
		if k.Arm == arm.Name {
			return nil
		}
	}
	return fmt.Errorf("trial arm %q is not declared by %s protocol", k.Arm, p.Kind)
}

// TrialSpec is one scheduled attempt. The complete schedule exists before
// the first provider call and is stored in the locked manifest.
type TrialSpec struct {
	Key TrialKey `json:"key"`
}

// Validate checks the trial.
func (s TrialSpec) Validate() error { return s.Key.Validate() }

// ValidateFor checks the trial against one protocol.
func (s TrialSpec) ValidateFor(p Protocol) error { return s.Key.ValidateFor(p) }

// Schedule is the realized randomized schedule for an experiment.
type Schedule struct {
	Trials []TrialSpec `json:"trials"`
}

// Validate checks every trial and rejects duplicate identities.
func (s Schedule) Validate() error {
	seen := make(map[TrialKey]bool, len(s.Trials))
	for i, trial := range s.Trials {
		if err := trial.Validate(); err != nil {
			return fmt.Errorf("trials[%d]: %w", i, err)
		}
		if seen[trial.Key] {
			return fmt.Errorf("duplicate trial identity %s", trial.Key.String())
		}
		seen[trial.Key] = true
	}
	return nil
}

// CompleteFor reports whether the schedule covers exactly tasks x arms x
// repetitions and preserves one environment block across every paired arm
// cell.
func (s Schedule) CompleteFor(p Protocol, taskIDs []string) error {
	if err := s.Validate(); err != nil {
		return err
	}
	want := make(map[TrialKey]bool)
	for _, task := range taskIDs {
		for _, arm := range p.Arms {
			for rep := 1; rep <= p.Repetitions; rep++ {
				want[TrialKey{ExperimentID: p.ExperimentID, TaskID: task, Arm: arm.Name, RepetitionID: rep}] = true
			}
		}
	}
	got := make(map[TrialKey]bool, len(s.Trials))
	pairEnvironments := make(map[TrialKey]int)
	for _, trial := range s.Trials {
		if err := trial.ValidateFor(p); err != nil {
			return fmt.Errorf("scheduled trial %s: %w", trial.Key.String(), err)
		}
		base := trial.Key
		base.EnvironmentBlock = 0
		if !want[base] {
			return fmt.Errorf("unexpected trial in schedule: %s", trial.Key.String())
		}
		if got[base] {
			return fmt.Errorf("duplicate scheduled cell %s", trial.Key.String())
		}
		got[base] = true
		pair := base
		pair.Arm = ""
		if previous, ok := pairEnvironments[pair]; ok && previous != trial.Key.EnvironmentBlock {
			return fmt.Errorf("paired trial %s uses environment blocks %d and %d", pair.String(), previous, trial.Key.EnvironmentBlock)
		}
		pairEnvironments[pair] = trial.Key.EnvironmentBlock
	}
	for key := range want {
		if !got[key] {
			return fmt.Errorf("schedule is missing cell %s", key.String())
		}
	}
	return nil
}

// itoa renders a small non-negative integer without importing strconv twice
// in hot paths; correctness matters more than speed here.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
