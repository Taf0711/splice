package presentation

import "fmt"

// Lifecycle names the phase of a presentation state. The empty value is the
// initial unstarted sentinel: a state with no plan event yet has no phase.
// Any non-empty value must be one of the closed set below.
type Lifecycle string

const (
	LifecycleDesign      Lifecycle = "design"
	LifecycleCrystallize Lifecycle = "crystallize"
	LifecycleApprove     Lifecycle = "approve"
	LifecycleExecute     Lifecycle = "execute"
	LifecycleRecovery    Lifecycle = "recovery"
	LifecycleComplete    Lifecycle = "complete"
)

// Validate reports an error when the lifecycle is non-empty and not one of
// the known phases. The empty value is valid: it means no phase has been
// entered yet.
func (l Lifecycle) Validate() error {
	switch l {
	case "", LifecycleDesign, LifecycleCrystallize, LifecycleApprove, LifecycleExecute, LifecycleRecovery, LifecycleComplete:
		return nil
	}
	return fmt.Errorf("unknown lifecycle %q", l)
}
