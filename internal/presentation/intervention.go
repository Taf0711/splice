package presentation

import (
	"fmt"
	"strings"
)

// InterventionKind is the closed set of intervention actions.
type InterventionKind string

const (
	InterventionContinue InterventionKind = "continue"
	InterventionRetry    InterventionKind = "retry"
	InterventionRollback InterventionKind = "rollback"
	InterventionStepBack InterventionKind = "step_back"
	InterventionEscalate InterventionKind = "escalate"
	InterventionAskUser  InterventionKind = "ask_user"
	InterventionStop     InterventionKind = "stop"
)

// Validate reports an error for any value outside the closed set.
func (k InterventionKind) Validate() error {
	switch k {
	case InterventionContinue, InterventionRetry, InterventionRollback, InterventionStepBack,
		InterventionEscalate, InterventionAskUser, InterventionStop:
		return nil
	}
	return fmt.Errorf("unknown intervention kind %q", k)
}

// InterventionStatus is the closed set of intervention lifecycle statuses.
type InterventionStatus string

const (
	InterventionProposed   InterventionStatus = "proposed"
	InterventionAccepted   InterventionStatus = "accepted"
	InterventionRejected   InterventionStatus = "rejected"
	InterventionApplied    InterventionStatus = "applied"
	InterventionSuperseded InterventionStatus = "superseded"
)

// Validate reports an error for any value outside the closed set.
func (s InterventionStatus) Validate() error {
	switch s {
	case InterventionProposed, InterventionAccepted, InterventionRejected, InterventionApplied, InterventionSuperseded:
		return nil
	}
	return fmt.Errorf("unknown intervention status %q", s)
}

// Intervention is one proposed or applied course correction in a run.
type Intervention struct {
	Kind          InterventionKind   `json:"kind"`
	Reason        string             `json:"reason"`
	SourceNodeID  string             `json:"source_node_id,omitempty"`
	TargetNodeID  string             `json:"target_node_id"`
	EvidenceDelta string             `json:"evidence_delta,omitempty"`
	Status        InterventionStatus `json:"status"`
}

// Validate checks the closed enums, a non-empty reason, and a non-empty
// target node.
func (i Intervention) Validate() error {
	if err := i.Kind.Validate(); err != nil {
		return fmt.Errorf("intervention: %w", err)
	}
	if err := i.Status.Validate(); err != nil {
		return fmt.Errorf("intervention: %w", err)
	}
	if strings.TrimSpace(i.Reason) == "" {
		return fmt.Errorf("intervention reason is required")
	}
	if strings.TrimSpace(i.TargetNodeID) == "" {
		return fmt.Errorf("intervention target node id is required")
	}
	return nil
}
