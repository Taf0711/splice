package splice

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Taf0711/splice/internal/zeroruntime"
)

// Completion check (D4 of plans/SPEC_JEV_DECISION_LAYER_2026-09-25.md).
// A pass that passes its own gates can still miss the request: with no
// acceptance facts, "completed" means the code builds and old tests pass.
// Before the run completes, code splits the request into requirements and
// the decision model judges each one against the run's diff. A clear miss
// becomes a revision context instead of a completion.

// completionMaxDiffChars bounds the diff in the decision state. Jev loses
// accuracy on a large state (jev-1.13 jaggedness), so a larger diff is cut.
const completionMaxDiffChars = 8000

// completionMaxRequirements bounds the questions per check.
const completionMaxRequirements = 8

// requirementSplit breaks a request at sentence ends, semicolons, and the
// colon that follows a leading "Add X in file" clause.
var requirementSplit = regexp.MustCompile(`(?:[.;!?](?:\s+|$))|(?::\s+)`)

// splitRequirements turns a request into the requirement clauses code asks
// about. Code does the split, not the model: jev-1.13 does not generate.
// A clause with fewer than three words carries no checkable requirement.
func splitRequirements(request string) []string {
	var out []string
	for _, part := range requirementSplit.Split(strings.Join(strings.Fields(request), " "), -1) {
		part = strings.TrimSpace(part)
		if len(strings.Fields(part)) < 3 {
			continue
		}
		out = append(out, part)
		if len(out) == completionMaxRequirements {
			break
		}
	}
	return out
}

// completionStateV1 serializes the request, its requirements, and the
// bounded diff.
func completionStateV1(request string, requirements []string, diff string) (string, error) {
	if len(diff) > completionMaxDiffChars {
		diff = diff[:completionMaxDiffChars]
	}
	raw, err := json.Marshal(struct {
		Request      string   `json:"request"`
		Requirements []string `json:"requirements"`
		Diff         string   `json:"diff"`
	}{request, requirements, diff})
	return string(raw), err
}

// completionQuestionsV1 asks one Noul per requirement. The questions point
// at the state by path, so each judgment reads one requirement and the diff.
func completionQuestionsV1(requirements []string) []zeroruntime.DecisionQuestion {
	questions := make([]zeroruntime.DecisionQuestion, 0, len(requirements))
	for i := range requirements {
		questions = append(questions, zeroruntime.DecisionQuestion{
			ID:           fmt.Sprintf("requirement_%d", i),
			Kind:         zeroruntime.DecisionKindNoul,
			Instructions: fmt.Sprintf("Does the code change in `diff` do what `requirements[%d]` asks, as part of the task in `request`?", i),
			Criteria: []zeroruntime.DecisionCriterion{
				{Label: "false", Description: "The diff does not do this requirement, does it only in part, or does something the requirement rules out."},
				{Label: "true", Description: "The diff does this requirement."},
			},
		})
	}
	return questions
}

// completionUnmetThreshold is the policy: a requirement below it is unmet.
// A false alarm costs one iteration; a miss ships a wrong change. Set on the
// gold and wrong trees (every wrong change scored 0.10 or less, every correct
// one 0.11 or more), then checked out of sample on 238 real completions
// (tests/evals/completion-check/answers-completion_v1-fixed-corpus-*.json):
// the one real false completion scored 0.06 and was caught; 7 of 237 correct
// completions (3.0%) raised a false alarm.
var completionUnmetThreshold = 0.12

// unmetRequirements applies the policy to the answers and returns the
// requirements to name in the revision context.
func unmetRequirements(requirements []string, answers map[string]float64) []string {
	var unmet []string
	for i, req := range requirements {
		if p, ok := answers[fmt.Sprintf("requirement_%d", i)]; ok && p < completionUnmetThreshold {
			unmet = append(unmet, req)
		}
	}
	return unmet
}
