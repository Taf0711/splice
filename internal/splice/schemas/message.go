package schemas

import (
	"errors"
	"fmt"
)

// MessageKind identifies the kind of an inter-stage message. Exactly one kind
// exists today; more are added later without changing this type's shape.
type MessageKind string

const (
	// MessageKindRevisionRequest is a focused re-entry request: a downstream
	// stage asks the orchestrator to revisit an earlier stage with the specific
	// evidence that failed.
	MessageKindRevisionRequest MessageKind = "revision_request"
)

// Validate checks the message kind. Only the single declared kind is valid.
func (k MessageKind) Validate() error {
	if k != MessageKindRevisionRequest {
		return fmt.Errorf("invalid message kind %q", k)
	}
	return nil
}

// RevisionRequest is the focused re-entry payload carried by a
// MessageKindRevisionRequest message. FailingEvidence names the concrete
// failures that justify the re-entry; ChangedFiles scopes the work; Instruction
// is the instruction to the re-entered stage.
type RevisionRequest struct {
	FailingEvidence []string `json:"failing_evidence,omitempty"`
	ChangedFiles    []string `json:"changed_files,omitempty"`
	Instruction     string   `json:"instruction"`
}

// Validate checks the revision request. A revision request without failing
// evidence names no actionable signal, so it is rejected loudly.
func (r RevisionRequest) Validate() error {
	if len(r.FailingEvidence) == 0 {
		return errors.New("revision request requires at least one failing evidence item")
	}
	for i, item := range r.FailingEvidence {
		if item == "" {
			return fmt.Errorf("revision request failing_evidence[%d] is empty", i)
		}
	}
	return nil
}

// StageMessage is one typed inter-stage message carried on a stage output.
// All fields are typed; no raw maps or raw JSON cross this boundary.
type StageMessage struct {
	ID       string          `json:"id"`
	RunID    string          `json:"run_id"`
	From     string          `json:"from"`
	To       string          `json:"to"`
	Kind     MessageKind     `json:"kind"`
	Subject  string          `json:"subject,omitempty"`
	Evidence []string        `json:"evidence,omitempty"`
	Payload  RevisionRequest `json:"payload"`
}

// Validate checks the stage message: identity and routing fields are required,
// the kind must be the single declared constant, and the payload must be valid.
func (m StageMessage) Validate() error {
	if m.ID == "" {
		return errors.New("message id is required")
	}
	if m.RunID == "" {
		return errors.New("message run_id is required")
	}
	if m.From == "" {
		return errors.New("message from is required")
	}
	if m.To == "" {
		return errors.New("message to is required")
	}
	if err := m.Kind.Validate(); err != nil {
		return err
	}
	if err := m.Payload.Validate(); err != nil {
		return fmt.Errorf("message payload: %w", err)
	}
	return nil
}
