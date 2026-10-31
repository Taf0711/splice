package splice

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func trajectoryPackByName(name string) (trajectoryPack, error) {
	switch name {
	case QuestionPackTrajActionV1:
		return actionTrajectoryPack{}, nil
	case QuestionPackTrajActionV2:
		return progressTrajectoryPack{}, nil
	case QuestionPackTrajV1:
		return noulTrajectoryPack{}, nil
	default:
		return nil, fmt.Errorf("trajectory: unknown question pack %q", name)
	}
}

func decisionRequestFromState(snap schemas.SemanticSnapshot, model, state string, questions []zeroruntime.DecisionQuestion) zeroruntime.DecisionRequest {
	return zeroruntime.DecisionRequest{Model: model, State: state, Questions: questions, SessionID: snap.EpisodeID}
}

// TrajectoryDecisionRequest uses the live projection and questions for a saved snapshot.
// An empty pack name preserves the recorded pack and state.
func TrajectoryDecisionRequest(snap schemas.SemanticSnapshot, packName, model string, maxRequestBytes int) (zeroruntime.DecisionRequest, error) {
	if packName == "" {
		packName = snap.QuestionPack
	}
	pack, err := trajectoryPackByName(packName)
	if err != nil {
		return zeroruntime.DecisionRequest{}, err
	}
	if packName != snap.QuestionPack {
		boundary, complete, err := trajectorySnapshotBoundary(snap)
		if err != nil {
			return zeroruntime.DecisionRequest{}, err
		}
		if !complete {
			return zeroruntime.DecisionRequest{}, fmt.Errorf("trajectory: pack override requires complete boundary evidence")
		}
		snap, err = TrajectorySemanticSnapshot(boundary, packName)
		if err != nil {
			return zeroruntime.DecisionRequest{}, err
		}
	}
	state, err := projectSemanticSnapshot(snap, maxRequestBytes)
	if err != nil {
		return zeroruntime.DecisionRequest{}, err
	}
	req := decisionRequestFromState(snap, model, state, pack.Questions())
	return req, req.Validate()
}

// TrajectoryResultProbabilities uses the live pack's answer map.
func TrajectoryResultProbabilities(packName string, result zeroruntime.DecisionResult) (map[TrajectoryLabel]float64, error) {
	pack, err := trajectoryPackByName(packName)
	if err != nil {
		return nil, err
	}
	return pack.Labels(assessmentFromResult(result))
}

// TrajectorySnapshotBoundary restores host evidence from a saved decision.
// Old candidate-only snapshots have conservative defaults for absent guards.
func TrajectorySnapshotBoundary(snap schemas.SemanticSnapshot) (TrajectoryBoundary, error) {
	b, _, err := trajectorySnapshotBoundary(snap)
	return b, err
}

// restoreLatestFailureKind puts the typed failure kind back on the latest
// step. The snapshot keeps the kind only in the failure history, so without
// this a replay would classify a recorded stage_error from its text. The
// classifier input does not change.
func restoreLatestFailureKind(b TrajectoryBoundary) TrajectoryBoundary {
	if b.Latest != nil && b.Latest.FailureKind == "" && len(b.FailureHistory) > 0 {
		latest := *b.Latest
		latest.FailureKind = b.FailureHistory[len(b.FailureHistory)-1].Kind
		b.Latest = &latest
	}
	return b
}

func trajectorySnapshotBoundary(snap schemas.SemanticSnapshot) (TrajectoryBoundary, bool, error) {
	if _, err := trajectoryPackByName(snap.QuestionPack); err != nil {
		return TrajectoryBoundary{}, false, err
	}
	if snap.Trajectory != nil {
		b := restoreLatestFailureKind(snap.Trajectory.Boundary)
		b.IsolatedWorktree = snap.TrajectoryIsolatedWorktree
		return b, true, snap.Trajectory.Validate()
	}
	if snap.QuestionPack == QuestionPackTrajV1 {
		for _, event := range snap.RecentEvents {
			if event.EventID == "boundary" {
				var trajectory schemas.TrajectorySnapshot
				if err := json.Unmarshal([]byte(event.Summary), &trajectory); err != nil {
					return TrajectoryBoundary{}, false, fmt.Errorf("trajectory boundary evidence: %w", err)
				}
				b := restoreLatestFailureKind(trajectory.Boundary)
				b.IsolatedWorktree = snap.TrajectoryIsolatedWorktree
				return b, true, trajectory.Validate()
			}
		}
		previous, latest := false, false
		for _, candidate := range snap.CandidateRefs {
			previous = previous || candidate.Ref == "previous" && candidate.Claim != ""
			latest = latest || candidate.Ref == "latest" && candidate.Claim != ""
		}
		if previous && latest {
			b := TrajectoryBoundary{RunID: snap.RunID, ProjectID: snap.CanonicalProjectID, Intent: snap.DistilledIntent, Iteration: snap.Iteration, Repair: snap.StageInvocation, Rules: schemas.TrajectoryDecision{Action: schemas.ActionContinue, Reason: "No recorded rules decision."}}
			for _, candidate := range snap.CandidateRefs {
				change, failure, ok := strings.Cut(candidate.Claim, "\nTest failure: ")
				if !ok {
					continue
				}
				step := &TrajectoryStep{Change: strings.TrimPrefix(change, "Change: "), Failure: failure}
				if candidate.Ref == "previous" {
					b.Previous = step
				} else if candidate.Ref == "latest" {
					b.Latest = step
				}
			}
			return b, false, b.Validate()
		}
	}
	return TrajectoryBoundary{}, false, fmt.Errorf("trajectory: snapshot lacks boundary evidence")
}

// TopTrajectoryLabel selects the label in the host's fixed tie order.
func TopTrajectoryLabel(probabilities map[TrajectoryLabel]float64) TrajectoryLabel {
	top := LabelContinue
	for _, label := range trajectoryLabels {
		if probabilities[label] > probabilities[top] {
			top = label
		}
	}
	return top
}

// TrajectoryReplayPolicy retains the active host guard state for one run.
type TrajectoryReplayPolicy struct{ policy hostTrajectoryPolicy }

// NewTrajectoryReplayPolicy creates a policy with the supplied thresholds.
func NewTrajectoryReplayPolicy(thresholds map[string]float64) *TrajectoryReplayPolicy {
	return &TrajectoryReplayPolicy{policy: hostTrajectoryPolicy{thresholds: thresholds}}
}

// UsePack enables the guards that belong to the named question pack.
func (p *TrajectoryReplayPolicy) UsePack(name string) {
	p.policy.progressGuard = name == QuestionPackTrajActionV2
}

// Observe restores failure features before a replay request.
func (p *TrajectoryReplayPolicy) Observe(b TrajectoryBoundary) TrajectoryBoundary {
	// The original backend owns the recorded shadow flags.
	b.StepBackUsed, b.Escalated = p.policy.stepBackUsed, p.policy.escalated
	return p.policy.observeFailure(b)
}

// Apply uses the same thresholds and guards as the live host policy.
func (p *TrajectoryReplayPolicy) Apply(b TrajectoryBoundary, c TrajectoryClassification) (schemas.TrajectoryDecision, error) {
	if err := validateTrajectoryProbabilities(c.Probabilities); err != nil {
		return schemas.TrajectoryDecision{}, err
	}
	if len(b.FailureHistory) == 0 {
		b = p.Observe(b)
	}
	p.policy.isolated = b.IsolatedWorktree
	return p.policy.apply(b, c), nil
}

// Fallback resets the stop streak after an error, as the live classifier does.
func (p *TrajectoryReplayPolicy) Fallback(b TrajectoryBoundary) schemas.TrajectoryDecision {
	p.policy.stopStreak = 0
	if b.Rules.Action == schemas.ActionStepBack {
		p.policy.stepBackUsed = true
	}
	return b.Rules
}
