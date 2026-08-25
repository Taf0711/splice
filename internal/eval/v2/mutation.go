package v2

import (
	"fmt"
	"strings"
)

// MutationResultMustFailOnMutant is the only accepted expected check result:
// a mutation probe applies a deliberate defect and must make the task's
// check fail. Executing probes belongs to the QA harness at EV2-9
// preparation; this checkpoint stores and validates the record.
const MutationResultMustFailOnMutant = "must_fail_on_mutant"

// minProbeDescriptionLength is the floor for a mutation probe description.
// A description must state what the mutation breaks; a fragment shorter than
// this cannot do so.
const minProbeDescriptionLength = 20

// MutationProbe records one deliberate defect applied to the reference
// solution and the check outcome it must produce.
type MutationProbe struct {
	TaskID              string `json:"task_id"`
	ProbeDescription    string `json:"probe_description"`
	ExpectedCheckResult string `json:"expected_check_result"`
}

// Validate checks the probe contract: valid task linkage, the closed expected
// result, and a description that states what the mutation breaks. A label or
// a blank string is rejected.
func (p MutationProbe) Validate() error {
	if p.TaskID == "" {
		return fmt.Errorf("task_id is required")
	}
	if p.ExpectedCheckResult != MutationResultMustFailOnMutant {
		return fmt.Errorf("expected_check_result must be %q, got %q", MutationResultMustFailOnMutant, p.ExpectedCheckResult)
	}
	description := strings.TrimSpace(p.ProbeDescription)
	if description == "" {
		return fmt.Errorf("probe_description is required")
	}
	if len(description) < minProbeDescriptionLength {
		return fmt.Errorf("probe_description must be at least %d characters and state what the mutation breaks", minProbeDescriptionLength)
	}
	if len(strings.Fields(description)) < 2 {
		return fmt.Errorf("probe_description must be a phrase that states what the mutation breaks, not a single label")
	}
	return nil
}
