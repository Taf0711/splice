package splice

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// Tier routing with a decision model (tier_v1). The keyword classifier in
// classifier.go matches words and reports a fixed confidence that nothing
// measured, and it never returns the standard tier. Here one Choice question
// judges the kind of change a request asks for. The host policy then picks
// the route: code, not the model, owns the cost of a wrong route.

// tierQuestionID is the question ID for the tier_v1 Choice.
const tierQuestionID = "pipeline_tier"

// tierMaxRequestChars bounds the request text in the decision state. The
// judgment needs the kind of change, not every detail of a long request.
const tierMaxRequestChars = 2000

// tierOptionsV1 describes each tier as the situation it fits (Jev matches
// situations, not stage names or degrees). The order is the route order,
// lightest first. Changing a description changes the question: bump the
// version and re-run the labeled evaluation in tests/evals/tier-routing.
var tierOptionsV1 = []zeroruntime.DecisionCriterion{
	{Label: string(schemas.TierTrivial), Description: "No behavior change: only documentation, comments, wording, formatting, or repository housekeeping such as ignore rules."},
	{Label: string(schemas.TierLight), Description: "A focused behavior change in one area where the request itself states the check or the test to write, or a behavior-preserving refactor that existing tests already cover."},
	{Label: string(schemas.TierStandard), Description: "New behavior, or a behavior change, whose correctness needs new tests that the request does not spell out."},
	{Label: string(schemas.TierSubstantial), Description: "A change to security-sensitive behavior (authentication, secrets, sandboxing, permissions, personal data, or limits on untrusted input), or a change that needs coordinated edits across several modules."},
	{Label: string(schemas.TierArchitectural), Description: "Creates or restructures a subsystem, a protocol, a storage layer, or the pipeline orchestration itself."},
}

// Stage needs (needs_v1): one Noul per stage decision instead of one tier
// Choice. A tier is a fixed bundle of stages; each Noul judges one need on
// its own, and code sets each need's threshold from the cost of skipping
// that stage wrongly. The probabilities also explain the plan: each stage
// runs because its need scored above its threshold.
const (
	needCodeChange     = "code_change"
	needNewTests       = "new_tests"
	needSecurityReview = "security_review"
)

func stageNeedQuestionsV1() []zeroruntime.DecisionQuestion {
	noul := func(id, instructions, whenFalse, whenTrue string) zeroruntime.DecisionQuestion {
		return zeroruntime.DecisionQuestion{
			ID:           id,
			Kind:         zeroruntime.DecisionKindNoul,
			Instructions: instructions,
			Criteria: []zeroruntime.DecisionCriterion{
				{Label: "false", Description: whenFalse},
				{Label: "true", Description: whenTrue},
			},
		}
	}
	return []zeroruntime.DecisionQuestion{
		noul(needCodeChange,
			"Does the coding request in `request` change any code, configuration, or prompt text, including a refactor that keeps behavior the same?",
			"Only documentation, comments, wording in docs, formatting, or repository housekeeping such as ignore rules changes.",
			"Any code, configuration, or prompt text changes, even when the intended behavior stays the same."),
		noul(needNewTests,
			"Does the coding request in `request` add or change program behavior without saying how the result will be checked?",
			"The request changes no behavior, keeps behavior the same (a pure refactor), or itself names the test, verifier, or check to use.",
			"The request adds or changes behavior and names no test, verifier, or check for it."),
		noul(needSecurityReview,
			"Does the coding request in `request` change security-sensitive behavior?",
			"The change does not touch authentication, authorization, secrets, sandboxing or isolation, exposure of personal data, or limits on untrusted input.",
			"The change touches authentication, authorization, secrets, sandboxing or isolation, exposure of personal data, or limits on untrusted input."),
	}
}

// stageNeedThresholds are the needs_v1 policy: a stage runs when its need's
// probability is above the threshold. Skipping verification on a code
// change is the worst error, so code_change has the lowest bar. Evaluated
// on tests/evals/tier-routing (answers-needs_v1-20260925.json, labels with
// the owner's 2026-09-25 security policy): 0 needed stages skipped and 5
// unneeded stages run, all on documentation or ignore-file edits. The
// keyword classifier skips 58 needed stages on the same set.
var stageNeedThresholds = map[string]float64{
	needCodeChange:     0.15,
	needNewTests:       0.5,
	needSecurityReview: 0.5,
}

// stageNeedDecision is one stage decision with the probability behind it,
// so the plan can say why each stage runs or does not.
type stageNeedDecision struct {
	Need        string
	Probability float64
	Threshold   float64
	Run         bool
}

// stageNeedsFromNouls applies the policy. Test generation and security
// review only make sense for a code change, so both are gated on it.
func stageNeedsFromNouls(nouls map[string]float64) map[string]stageNeedDecision {
	decide := func(need string, gate bool) stageNeedDecision {
		p, th := nouls[need], stageNeedThresholds[need]
		return stageNeedDecision{Need: need, Probability: p, Threshold: th, Run: gate && p > th}
	}
	code := decide(needCodeChange, true)
	return map[string]stageNeedDecision{
		needCodeChange:     code,
		needNewTests:       decide(needNewTests, code.Run),
		needSecurityReview: decide(needSecurityReview, code.Run),
	}
}

// tierRoute is the route rank a tier compiles to. Substantial and
// architectural compile to the same stages and reserve, so they share a
// route and a confusion between them costs nothing.
func tierRoute(tier schemas.PipelineTier) int {
	switch tier {
	case schemas.TierTrivial:
		return 0
	case schemas.TierLight:
		return 1
	case schemas.TierStandard:
		return 2
	default:
		return 3
	}
}

// tierQuestionV1 returns the tier_v1 Choice question.
func tierQuestionV1() zeroruntime.DecisionQuestion {
	return zeroruntime.DecisionQuestion{
		ID:           tierQuestionID,
		Kind:         zeroruntime.DecisionKindChoice,
		Instructions: "Classify the coding request in `request` by the kind of change it asks for. Judge only what the request text asks for.",
		Criteria:     tierOptionsV1,
	}
}

// tierStateV1 serializes the decision state: the request text only, bounded.
func tierStateV1(request string) (string, error) {
	runes := []rune(request)
	if len(runes) > tierMaxRequestChars {
		runes = runes[:tierMaxRequestChars]
	}
	raw, err := json.Marshal(struct {
		Request string `json:"request"`
	}{string(runes)})
	return string(raw), err
}

// tierFromProbabilities applies the host policy to a tier distribution: the
// router takes the lightest route whose chance of being too light is at most
// underTierRisk. Under-tiering is the expensive error (trivial skips every
// verification stage), so an uncertain answer moves the route up, never
// down. Inside the heavy route, the more probable of substantial and
// architectural names the tier. It returns false when the distribution
// carries no tier probability at all.
func tierFromProbabilities(probs map[string]float64, underTierRisk float64) (schemas.PipelineTier, bool) {
	routeP := [4]float64{}
	total := 0.0
	for _, opt := range tierOptionsV1 {
		p := probs[opt.Label]
		routeP[tierRoute(schemas.PipelineTier(opt.Label))] += p
		total += p
	}
	if total <= 0 {
		return "", false
	}
	route := 3
	heavier := 0.0
	for r := 3; r > 0; r-- {
		heavier += routeP[r] / total
		if heavier > underTierRisk {
			break
		}
		route = r - 1
	}
	switch route {
	case 0:
		return schemas.TierTrivial, true
	case 1:
		return schemas.TierLight, true
	case 2:
		return schemas.TierStandard, true
	}
	if probs[string(schemas.TierArchitectural)] > probs[string(schemas.TierSubstantial)] {
		return schemas.TierArchitectural, true
	}
	return schemas.TierSubstantial, true
}

// classifyComplexityWithDecisions classifies a request through the decision
// evaluator under the tier_v1 policy. Any evaluator failure falls back to
// the keyword classifier and names the fallback in the rationale, so the
// route is never blocked by the decision model.
func classifyComplexityWithDecisions(ctx context.Context, evaluator zeroruntime.DecisionEvaluator, model, request string, underTierRisk float64) schemas.ComplexityClassifierOutput {
	fallback := func(reason string) schemas.ComplexityClassifierOutput {
		out := ClassifyRequestTyped(request)
		out.Rationale = fmt.Sprintf("%s (keyword fallback: %s)", out.Rationale, reason)
		return out
	}
	if evaluator == nil {
		return fallback("no decision evaluator")
	}
	state, err := tierStateV1(request)
	if err != nil {
		return fallback(err.Error())
	}
	result, err := evaluator.Evaluate(ctx, zeroruntime.DecisionRequest{Model: model, State: state, Questions: []zeroruntime.DecisionQuestion{tierQuestionV1()}})
	if err != nil {
		return fallback(err.Error())
	}
	answer, ok := result.AnswerByID(tierQuestionID)
	if !ok {
		return fallback("no answer for the tier question")
	}
	tier, ok := tierFromProbabilities(answer.Probabilities, underTierRisk)
	if !ok {
		return fallback("the tier answer carries no probabilities")
	}
	out := ClassifyRequestTyped(request)
	out.Tier = tier
	out.Confidence = answer.Confidence
	out.Rationale = fmt.Sprintf("tier_v1: model chose %s; policy routed %s at under-tier risk %.2f", answer.Choice, tier, underTierRisk)
	return out
}
