package warmcost

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// JevManifest is the M5 campaign manifest: the fixed treatment envelope the
// owner approves before dispatch. Calibration and cap are manifest values,
// not code constants, so the preregistered experiment exception is explicit
// and versioned.
type JevManifest struct {
	// Campaign is a human-readable campaign identity for the report.
	Campaign string `json:"campaign"`
	// Arms is the exact arm order: cold, warm, warm-jev.
	Arms []Arm `json:"arms"`
	// Repeats is the per-task repeat count.
	Repeats int `json:"repeats"`
	// TaskSets name the task directories under tests/evals.
	TaskSets []string `json:"task_sets"`
	// ModelPin is the pinned decision model (adapter prefix check).
	ModelPin string `json:"model_pin"`
	// CalibrationPUseful is the preregistered p_useful for the
	// dependency_selection class.
	CalibrationPUseful float64 `json:"calibration_p_useful"`
	// CapUSD is the advisor-side spend cap for the campaign.
	CapUSD float64 `json:"cap_usd"`
	// BudgetFitOff pins the budget freeze (SPLICE_BUDGET_FIT=off).
	BudgetFitOff bool `json:"budget_fit_off"`
	// Notes carries the owner-approval reference for the campaign.
	Notes string `json:"notes,omitempty"`
}

// LoadJevManifest reads and validates a campaign manifest. Validation is
// the dispatch gate: a manifest that fails here blocks the live campaign
// before any provider request.
func LoadJevManifest(path string) (*JevManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("warmcost: read manifest: %w", err)
	}
	var m JevManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("warmcost: decode manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate enforces the campaign invariants with the offending field named.
func (m *JevManifest) Validate() error {
	if len(m.Arms) < 2 {
		return errors.New("warmcost: manifest needs at least two arms")
	}
	if m.Arms[0] != ArmCold {
		return fmt.Errorf("warmcost: the first arm must be %q (cold baseline), got %q", ArmCold, m.Arms[0])
	}
	for i, a := range m.Arms {
		if !a.Valid() {
			return fmt.Errorf("warmcost: manifest arm %d %q is unknown", i, a)
		}
	}
	if m.Repeats < 1 {
		return fmt.Errorf("warmcost: manifest repeats %d must be >= 1", m.Repeats)
	}
	if len(m.TaskSets) == 0 {
		return errors.New("warmcost: manifest names no task sets")
	}
	if m.CalibrationPUseful < 0 || m.CalibrationPUseful > 1 {
		return fmt.Errorf("warmcost: manifest calibration_p_useful %v must be in [0,1]", m.CalibrationPUseful)
	}
	if m.CapUSD <= 0 {
		return fmt.Errorf("warmcost: manifest cap_usd %v must be positive", m.CapUSD)
	}
	if !m.BudgetFitOff {
		return errors.New("warmcost: manifest must pin budget_fit_off: paid campaigns never fit learned budgets")
	}
	if m.ModelPin == "" {
		return errors.New("warmcost: manifest must pin the decision model")
	}
	return nil
}
