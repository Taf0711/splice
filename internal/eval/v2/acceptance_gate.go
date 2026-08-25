package v2

import (
	"fmt"
	"strings"
)

// RunQAEvidence bundles the evidence a task curator must collect before
// acceptance. Every field is mandatory for a valid acceptance record.
type RunQAEvidence struct {
	FixtureHashMatchConfirmed        bool                `json:"fixture_hash_match_confirmed"`
	BaselineCommand                  string              `json:"baseline_command"`
	CheckRunSHA256                   string              `json:"check_run_sha256"`
	ReferenceSolutionPassConfirmed   bool                `json:"reference_solution_pass_confirmed"`
	IndependentSolutionPassConfirmed bool                `json:"independent_solution_pass_confirmed"`
	ProbePassed                      bool                `json:"probe_passed"`
	GraderIsolation                  GraderIsolationSpec `json:"grader_isolation"`
}

// TaskAcceptance is the record of one accepted task. It carries the bound
// candidate content hash, so acceptance without the hash is impossible by
// construction.
type TaskAcceptance struct {
	CandidateID   string        `json:"candidate_id"`
	TaskID        string        `json:"task_id"`
	ContentSHA256 string        `json:"content_sha256"`
	QA            RunQAEvidence `json:"qa"`
}

// AcceptTask performs the full acceptance gate. It validates the registry
// history, the sealed task spec, the candidate content hash, every QA
// evidence field, grader isolation, and the task identity's uniqueness
// against the existing set. The function either registers acceptance or
// returns an error without side effects: partial acceptance is impossible.
func AcceptTask(reg *CandidateRegistry, spec TaskSpec, qa TaskAcceptance, existing []TaskSpec) error {
	if reg == nil {
		return fmt.Errorf("acceptance: candidate registry is required")
	}
	// DB1: the full registry history must be valid before anything else.
	if err := reg.Validate(); err != nil {
		return fmt.Errorf("acceptance: registry history is invalid: %w", err)
	}
	// DD1: only sealed tasks can be accepted.
	if !spec.Sealed {
		return fmt.Errorf("acceptance: task %s is not sealed; only sealed tasks can be accepted", spec.ID)
	}
	if qa.TaskID != spec.ID {
		return fmt.Errorf("acceptance: record task id %q does not match spec id %q", qa.TaskID, spec.ID)
	}
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("acceptance: task %s failed sealed validation: %w", spec.ID, err)
	}
	// DC1: the bound candidate content hash must equal the canonical hash of
	// the accepted spec, and both must equal the registered hash.
	contentHash, err := CanonicalTaskHash(spec)
	if err != nil {
		return fmt.Errorf("acceptance: canonicalize task %s: %w", spec.ID, err)
	}
	if qa.ContentSHA256 == "" {
		return fmt.Errorf("acceptance: task %s must carry the candidate content hash", spec.ID)
	}
	if qa.ContentSHA256 != contentHash {
		return fmt.Errorf("acceptance: carried content hash %s does not match canonical hash %s of task %s", qa.ContentSHA256, contentHash, spec.ID)
	}
	candidate, ok := reg.Latest(qa.CandidateID)
	if !ok {
		return fmt.Errorf("acceptance: candidate %s is not registered", qa.CandidateID)
	}
	if candidate.Status == CandidateStatusRejected {
		return fmt.Errorf("acceptance: candidate %s is rejected", qa.CandidateID)
	}
	if candidate.Status == CandidateStatusAccepted {
		return fmt.Errorf("acceptance: candidate %s is already accepted", qa.CandidateID)
	}
	if candidate.ContentSHA256 != contentHash {
		return fmt.Errorf("acceptance: candidate %s registered hash %s does not match canonical hash %s of task %s", qa.CandidateID, candidate.ContentSHA256, contentHash, spec.ID)
	}
	// Validate QA evidence.
	if err := validateQAEvidence(qa.QA); err != nil {
		return fmt.Errorf("acceptance: task %s: %w", spec.ID, err)
	}
	// Duplicate task id against the existing set.
	for _, existingTask := range existing {
		if existingTask.ID == spec.ID {
			return fmt.Errorf("acceptance: task %s already exists", spec.ID)
		}
	}
	// Side effect only after every gate passes.
	if err := reg.SetStatus(qa.CandidateID, CandidateStatusAccepted); err != nil {
		return fmt.Errorf("acceptance: record acceptance: %w", err)
	}
	return nil
}

func validateQAEvidence(qa RunQAEvidence) error {
	if !qa.FixtureHashMatchConfirmed {
		return fmt.Errorf("fixture hash match is not confirmed")
	}
	if !qa.ReferenceSolutionPassConfirmed {
		return fmt.Errorf("reference solution pass is not confirmed")
	}
	if !qa.IndependentSolutionPassConfirmed {
		return fmt.Errorf("independent solution pass is not confirmed")
	}
	if !qa.ProbePassed {
		return fmt.Errorf("mutation probe result is not recorded as passed")
	}
	// DH1: baseline command must be a real command, not whitespace.
	baseline := strings.TrimSpace(qa.BaselineCommand)
	if baseline == "" {
		return fmt.Errorf("baseline command must be a real command, got %q", qa.BaselineCommand)
	}
	if !validHash(qa.CheckRunSHA256) {
		return fmt.Errorf("check run sha256 must be a sha256 hex digest, got %q", qa.CheckRunSHA256)
	}
	if err := qa.GraderIsolation.Validate(); err != nil {
		return fmt.Errorf("grader isolation: %w", err)
	}
	return nil
}
