package splice

// The traj_v1 pack retains two noul questions. The classifier host controls the actions.

import (
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

const (
	// TrajectoryPackEnvVar enables the trajectory judgment ("traj_v1").
	// It needs the decision advisor (flag and mode); unset asks nothing.
	TrajectoryPackEnvVar = "SPLICE_TRAJECTORY_PACK"
	// QuestionPackTrajV1 is the trajectory pack.
	QuestionPackTrajV1 = "traj_v1"

	// trajFailureChars bounds each failure text in the state.
	trajFailureChars = 800
)

// trajV1Questions are the two nouls of the revised pack (review R.2).
func trajV1Questions() []zeroruntime.DecisionQuestion {
	return []zeroruntime.DecisionQuestion{
		{
			ID:   "same_cause",
			Kind: zeroruntime.DecisionKindNoul,
			Instructions: "`candidates[0].Claim` is a failed attempt at the task in `need`, and `candidates[1].Claim` is the next attempt after a repair. " +
				"Each gives the change the model says it made and the test failure that followed. " +
				"Does the failure in `candidates[1].Claim` have the same cause as the failure in `candidates[0].Claim`, even if the error text differs?",
			Criteria: []zeroruntime.DecisionCriterion{
				{Label: "false", Description: "The repair removed the first cause; the new failure comes from a different missing piece or a different mistake."},
				{Label: "true", Description: "The same cause is still there: the repair did not remove it, or removed it in one form and it came back in another."},
			},
		},
		{
			ID:   "new_information",
			Kind: zeroruntime.DecisionKindNoul,
			Instructions: "`candidates[0].Claim` is a failed attempt at the task in `need`, and `candidates[1].Claim` is the next attempt after a repair. " +
				"Does the failure in `candidates[1].Claim` show something about what the task still needs that the failure in `candidates[0].Claim` did not show?",
			Criteria: []zeroruntime.DecisionCriterion{
				{Label: "false", Description: "The new failure tells nothing the first one did not: the same requirement, the same place, or only a different wording."},
				{Label: "true", Description: "The new failure names a requirement, file, value, or place that the first failure did not."},
			},
		},
	}
}

func trajClaim(s TrajectoryStep) string {
	failure := s.Failure
	if len(failure) > trajFailureChars {
		failure = failure[:trajFailureChars]
	}
	return "Change: " + truncateClaim(s.Change, 300) + "\nTest failure: " + failure
}

// passFailure describes why a failed pass failed: the failed stages, then
// the failing tests of its last test run.
func passFailure(records []schemas.StageRecord, outputs []schemas.HarnessStageOutput) string {
	var parts []string
	for _, r := range records {
		if r.Status == schemas.StageFailed {
			parts = append(parts, r.Name+" failed: "+DerefString(r.OutputSummary))
		}
	}
	for _, out := range outputs {
		if outputHasTestFailure(out) {
			results := out.Data["test_results"].(schemas.TestRunResults)
			evidence, _ := extractFailingTests(results)
			parts = append(parts, evidence...)
		}
	}
	return strings.Join(parts, "\n")
}

// passFailureKind classifies a failed pass from typed evidence, in the order
// of the remediation spec (6, design item 4): failing tests are a test
// failure first, then a failed stage carries its kind. The display text is
// never consulted, so a transport error is not called a test failure. A pass
// with neither is "none".
func passFailureKind(records []schemas.StageRecord, outputs []schemas.HarnessStageOutput) string {
	for _, out := range outputs {
		if outputHasTestFailure(out) {
			return schemas.NodeFailureTestFailure
		}
	}
	for _, r := range records {
		if r.Status != schemas.StageFailed {
			continue
		}
		if r.FailureKind != "" {
			return r.FailureKind
		}
		// A failed stage without a typed kind is a stage error by definition.
		return schemas.NodeFailureStageError
	}
	return schemas.NodeFailureNone
}

// passChange is the code writer's own summary of the pass. writer is the plan
// name of the code writer role, which a topology may rename.
func passChange(records []schemas.StageRecord, writer string) string {
	for _, r := range records {
		if r.Name == writer {
			return DerefString(r.OutputSummary)
		}
	}
	return ""
}
