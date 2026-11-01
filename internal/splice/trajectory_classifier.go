package splice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/redaction"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// TrajectoryLabel names one classifier action.
type TrajectoryLabel string

// These labels form the complete trajectory action distribution.
const (
	LabelContinue TrajectoryLabel = "continue"
	LabelStepBack TrajectoryLabel = "step_back"
	LabelRollback TrajectoryLabel = "rollback"
	LabelEscalate TrajectoryLabel = "escalate"
	LabelStop     TrajectoryLabel = "stop"
	// QuestionPackTrajActionV1 names the five-label choice pack.
	QuestionPackTrajActionV1 = "traj_action_v1"
	// QuestionPackTrajActionV2 adds failure history and progress criteria.
	QuestionPackTrajActionV2 = "traj_action_v2"
)

var trajectoryLabels = []TrajectoryLabel{LabelContinue, LabelStepBack, LabelRollback, LabelEscalate, LabelStop}
var trajectoryActions = map[TrajectoryLabel]schemas.TrajectoryAction{
	LabelContinue: schemas.ActionContinue,
	LabelStepBack: schemas.ActionStepBack,
	LabelRollback: schemas.ActionRollback,
	LabelEscalate: schemas.ActionEscalateCycleDetected,
	LabelStop:     schemas.ActionSurfaceToUser,
}

// TrajectoryStep contains the change and failure text at a boundary.
type TrajectoryStep = schemas.TrajectoryStep

// TrajectoryBoundary contains the host evidence for one decision.
type TrajectoryBoundary = schemas.TrajectoryBoundary

// TrajectoryClassification contains action probabilities and their source.
type TrajectoryClassification struct {
	Probabilities  map[TrajectoryLabel]float64
	Backend, Model string
	Evidence       []string
}

// TrajectoryClassifier supplies probabilities. The host controls each action.
type TrajectoryClassifier interface {
	Classify(context.Context, TrajectoryBoundary) (TrajectoryClassification, error)
}
type trajectoryPack interface {
	Name() string
	Questions() []zeroruntime.DecisionQuestion
	Labels(schemas.DecisionAssessment) (map[TrajectoryLabel]float64, error)
}
type actionTrajectoryPack struct{}

func (actionTrajectoryPack) Name() string { return QuestionPackTrajActionV1 }
func (actionTrajectoryPack) Questions() []zeroruntime.DecisionQuestion {
	return []zeroruntime.DecisionQuestion{
		{
			ID:           "trajectory_action",
			Kind:         zeroruntime.DecisionKindChoice,
			Instructions: "Select the next trajectory action from the boundary evidence.",
			Criteria: []zeroruntime.DecisionCriterion{
				{Label: "continue", Description: "Continue the current approach."},
				{Label: "step_back", Description: "Review the cause and choose a new approach."},
				{Label: "rollback", Description: "Restore an earlier state with a higher score."},
				{Label: "escalate", Description: "Use the escalation model."},
				{Label: "stop", Description: "Ask the user for a decision."},
			},
		},
	}
}
func (actionTrajectoryPack) Labels(a schemas.DecisionAssessment) (map[TrajectoryLabel]float64, error) {
	p := map[TrajectoryLabel]float64{}
	for _, answer := range a.Answers {
		if answer.QuestionID == "trajectory_action" {
			for label, v := range answer.Probabilities {
				p[TrajectoryLabel(label)] = v
			}
			return p, validateTrajectoryProbabilities(p)
		}
	}
	return nil, fmt.Errorf("trajectory_action: missing answer")
}

type progressTrajectoryPack struct{ actionTrajectoryPack }

func (progressTrajectoryPack) Name() string { return QuestionPackTrajActionV2 }
func (progressTrajectoryPack) Questions() []zeroruntime.DecisionQuestion {
	return []zeroruntime.DecisionQuestion{{
		ID:   "trajectory_action",
		Kind: zeroruntime.DecisionKindChoice,
		Instructions: "Select the next action from the boundary evidence. " +
			"Read Latest.Failure, Previous, failure_history, boundaries_since_new_failure, and distinct_failures. " +
			"A new failure after a change usually means progress: the change fixed one thing and exposed the next. Continue. " +
			"Use is_new and signature_id in failure_history to distinguish new failures from repeated failures. " +
			"A stage_error such as 'context expansion budget exhausted' means the tool failed before tests ran. " +
			"A stage error alone does not show that the run cannot make progress. " +
			"Choose step_back when the same failure, or the same cause in another form, repeats without new information. " +
			"Choose stop only when StepBackUsed is true and the failure still repeats with nothing new. " +
			"Choose rollback only when RollbackTarget is true and a restorable earlier state scored higher. " +
			"Choose escalate when the model repeats the same mistake and a stronger model may help. Check Escalated.",
		Criteria: []zeroruntime.DecisionCriterion{
			{Label: "continue", Description: "Continue when a new failure shows progress. A stage_error alone does not show a repeated cause."},
			{Label: "step_back", Description: "Review the approach when failure_history shows the same failure or cause without new information. Check boundaries_since_new_failure."},
			{Label: "rollback", Description: "Restore an earlier state only when RollbackTarget shows a restorable state with a higher score."},
			{Label: "escalate", Description: "Use a stronger model when the current model repeats the same mistake and Escalated is false."},
			{Label: "stop", Description: "Ask the user only after StepBackUsed is true and the failure still repeats with nothing new."},
		},
	}}
}

type noulTrajectoryPack struct{}

func (noulTrajectoryPack) Name() string                              { return QuestionPackTrajV1 }
func (noulTrajectoryPack) Questions() []zeroruntime.DecisionQuestion { return trajV1Questions() }
func (noulTrajectoryPack) Labels(a schemas.DecisionAssessment) (map[TrajectoryLabel]float64, error) {
	values := map[string]float64{}
	for _, answer := range a.Answers {
		if p, ok := answer.Probabilities["true"]; ok {
			values[answer.QuestionID] = p
		}
	}
	same, ok := values["same_cause"]
	fresh, ok2 := values["new_information"]
	if !ok || !ok2 || same < 0 || same > 1 || fresh < 0 || fresh > 1 {
		return nil, fmt.Errorf("traj_v1: invalid or missing noul answer")
	}
	stuck := same * (1 - fresh)
	p := map[TrajectoryLabel]float64{LabelStepBack: stuck, LabelContinue: 1 - stuck}
	return p, validateTrajectoryProbabilities(p)
}
func validateTrajectoryProbabilities(p map[TrajectoryLabel]float64) error {
	for label := range p {
		if _, ok := trajectoryActions[label]; !ok {
			return fmt.Errorf("trajectory: unknown label %q", label)
		}
	}
	sum := 0.0
	for _, label := range trajectoryLabels {
		v := p[label]
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return fmt.Errorf("trajectory: invalid probability for %q", label)
		}
		p[label] = v
		sum += v
	}
	if math.Abs(sum-1) > 0.02 {
		return fmt.Errorf("trajectory: probability sum %v outside tolerance", sum)
	}
	return nil
}

type rulesClassifier struct{ cost *RunCostSignal }

func (c rulesClassifier) Classify(_ context.Context, b TrajectoryBoundary) (TrajectoryClassification, error) {
	rc := newTrajectoryRuleContext(b.History, 0, nil, c.cost)
	decision := rc.decision(schemas.ActionContinue, "Trajectory remains within safe bounds.", nil)
	if len(b.History) > 0 {
		for _, rule := range judgmentRules {
			if d := rule.evaluate(rc); d != nil {
				decision = *d
				break
			}
		}
	}
	if b.Rules.Action != "" {
		decision = b.Rules
	}
	label := LabelContinue
	switch decision.Action {
	case schemas.ActionStepBack:
		label = LabelStepBack
	case schemas.ActionRollback:
		label = LabelRollback
	case schemas.ActionEscalateCycleDetected, schemas.ActionEscalateOscillation:
		label = LabelEscalate
	case schemas.ActionSurfaceToUser, schemas.ActionAbortNoProgress:
		label = LabelStop
	}
	return TrajectoryClassification{
		Probabilities: map[TrajectoryLabel]float64{label: 1},
		Backend:       "rules",
		Evidence:      decision.Evidence,
	}, nil
}

type advisorClassifier struct {
	packError error
	advisor   *DecisionAdvisor
	pack      trajectoryPack
	// budget caps the classifier's requests in one run.
	budget int
}

// trajectoryRequestBudget is the classifier's request budget for one run: one
// request for each boundary the hard limits allow. A run has at most
// defaultMaxIterations passes, and each pass has at most maxLocalRepairs
// repairs, so each pass adds up to maxLocalRepairs+1 boundaries.
const trajectoryRequestBudget = defaultMaxIterations * (maxLocalRepairs + 1)

func (c advisorClassifier) Classify(ctx context.Context, b TrajectoryBoundary) (TrajectoryClassification, error) {
	if c.packError != nil {
		return TrajectoryClassification{}, c.packError
	}
	snap, err := TrajectorySemanticSnapshot(b, c.pack.Name())
	if err != nil {
		return TrajectoryClassification{}, err
	}
	record, permitted := c.advisor.EvaluateTrajectoryGate1(ctx, snap, c.pack.Questions(), c.budget)
	if record.Assessment == nil || (c.advisor != nil && c.advisor.Mode() != AdvisorModeShadow && !permitted) {
		return TrajectoryClassification{}, fmt.Errorf("trajectory fallback: %s", record.Reason)
	}
	probabilities, err := c.pack.Labels(*record.Assessment)
	if err != nil {
		if c.advisor != nil {
			bad := record
			bad.Disposition = schemas.DispositionProviderFailure
			bad.Reason = "invalid trajectory answer: " + err.Error()
			c.advisor.records = append(c.advisor.records, bad)
		}
		return TrajectoryClassification{}, err
	}
	backend := "openrouter_decisions"
	model := ""
	if c.advisor != nil {
		if c.advisor.config.Backend != "" {
			backend = c.advisor.config.Backend
		}
		model = record.Assessment.ModelVersion
	}
	return TrajectoryClassification{
		Probabilities: probabilities,
		Backend:       backend,
		Model:         model,
		Evidence:      []string{record.Reason},
	}, nil
}

// TrajectorySemanticSnapshot creates the bounded evidence for a trajectory pack.
func TrajectorySemanticSnapshot(b TrajectoryBoundary, packName string) (schemas.SemanticSnapshot, error) {
	if _, err := trajectoryPackByName(packName); err != nil {
		return schemas.SemanticSnapshot{}, err
	}
	if packName == QuestionPackTrajV1 && (b.Previous == nil || b.Latest == nil) {
		return schemas.SemanticSnapshot{}, fmt.Errorf("traj_v1: no eligible pair of boundary steps")
	}
	b.Intent = truncateClaim(redaction.RedactString(b.Intent, redaction.Options{}), 600)
	bounded := func(step *TrajectoryStep) *TrajectoryStep {
		if step == nil {
			return nil
		}
		return &TrajectoryStep{
			Change:  truncateClaim(redaction.RedactString(step.Change, redaction.Options{}), 300),
			Failure: truncateClaim(redaction.RedactString(step.Failure, redaction.Options{}), trajFailureChars),
		}
	}
	b.Previous = bounded(b.Previous)
	b.Latest = bounded(b.Latest)
	b.FailureHistory = append([]schemas.TrajectoryFailure(nil), b.FailureHistory...)
	for i := range b.FailureHistory {
		signature := []rune(redaction.RedactString(b.FailureHistory[i].Signature, redaction.Options{}))
		b.FailureHistory[i].Signature = string(signature[:min(len(signature), 300)])
	}
	snapshot := schemas.TrajectorySnapshot{Boundary: b, Scores: scores(b.History), HashRepeats: map[string]int{}}
	for _, state := range b.History {
		snapshot.HashRepeats[state.StateHash]++
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return schemas.SemanticSnapshot{}, err
	}
	digest := sha256.Sum256(raw)
	candidates := []schemas.CandidateRef{{Ref: "boundary", Origin: "trajectory boundary", Claim: "Select the next action."}}
	if b.Previous != nil && b.Latest != nil {
		candidates = []schemas.CandidateRef{
			{Ref: "previous", Origin: "trajectory boundary", Claim: trajClaim(*b.Previous)},
			{Ref: "latest", Origin: "trajectory boundary", Claim: trajClaim(*b.Latest)},
		}
	}
	snap := schemas.SemanticSnapshot{
		RunID:                      b.RunID,
		EpisodeID:                  fmt.Sprintf("trajectory:%d:%d", b.Iteration, b.Repair),
		Stage:                      writerStage(b),
		StageInvocation:            b.Repair,
		Iteration:                  b.Iteration,
		EventID:                    fmt.Sprintf("boundary:%d:%d", b.Iteration, b.Repair),
		SnapshotSchema:             schemas.SnapshotSchemaV1,
		QuestionPack:               packName,
		HostPolicy:                 "trajectory_actions_v1",
		CanonicalProjectID:         b.ProjectID,
		SourceDigest:               hex.EncodeToString(digest[:]),
		DistilledIntent:            b.Intent,
		UnresolvedRelation:         "the next trajectory action",
		CandidateRefs:              candidates,
		Trajectory:                 &snapshot,
		TrajectoryIsolatedWorktree: b.IsolatedWorktree,
	}
	if packName == QuestionPackTrajV1 {
		// The noul questions read the candidates and the task need.
		snap.Trajectory = nil
		// Keep the complete boundary as bounded evidence for both nouls.
		snap.RecentEvents = []schemas.EventSummary{{EventID: "boundary", Summary: string(raw)}}
	}
	return snap, nil
}

type hostTrajectoryPolicy struct {
	history                           []schemas.IterationState
	snapshots                         []snapshot
	maxIterations                     int
	tokenBudget                       int
	advisor                           *DecisionAdvisor
	classifier                        TrajectoryClassifier
	thresholds                        map[string]float64
	isolated, stepBackUsed, escalated bool
	stopStreak                        int
	pending                           *schemas.TrajectoryDecision
	lastDecision                      schemas.TrajectoryDecision
	lastBackend                       string
	failureHistory                    []schemas.TrajectoryFailure
	failureSignatures                 map[string]int
	boundariesSinceNewFailure         int
	progressGuard                     bool
}

var trajectoryFailureNumbers = regexp.MustCompile(`0x[0-9a-f]+|[\p{Nd}]+(\.[\p{Nd}]+)?`)
var trajectoryFailurePaths = regexp.MustCompile(`(/[\p{L}\p{N}_.\-]+)+`)

func trajectoryFailureSignature(failure string) string {
	s := trajectoryFailureNumbers.ReplaceAllString(strings.ToLower(failure), "#")
	s = trajectoryFailurePaths.ReplaceAllString(s, "<path>")
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > 300 {
		s = string(runes[:300])
	}
	return s
}

func (p *hostTrajectoryPolicy) observeFailure(b TrajectoryBoundary) TrajectoryBoundary {
	failure := ""
	kind := "none"
	if b.Latest != nil {
		failure = b.Latest.Failure
		kind = b.Latest.FailureKind
	}
	if kind == "" && failure != "" {
		// Legacy evidence carries only text. Replay and older boundaries reach
		// this branch; live boundaries carry the typed kind from the stage
		// records, so a transport error is not mistaken for a test failure.
		kind = "test_failure"
		if strings.Contains(failure, "failed: splice.") || strings.Contains(failure, "stageRunError") {
			kind = "stage_error"
		}
	}
	if kind == "" {
		kind = "none"
	}
	signature := trajectoryFailureSignature(failure)
	if p.failureSignatures == nil {
		p.failureSignatures = make(map[string]int)
	}
	id, seen := p.failureSignatures[signature]
	if !seen {
		id = len(p.failureSignatures)
		p.failureSignatures[signature] = id
		p.boundariesSinceNewFailure = 0
	} else {
		p.boundariesSinceNewFailure++
	}
	p.failureHistory = append(p.failureHistory, schemas.TrajectoryFailure{
		Iteration: b.Iteration, Repair: b.Repair, Kind: kind, Signature: signature, SignatureID: id, IsNew: !seen,
	})
	if len(p.failureHistory) > 8 {
		p.failureHistory = append([]schemas.TrajectoryFailure(nil), p.failureHistory[len(p.failureHistory)-8:]...)
	}
	b.FailureHistory = append([]schemas.TrajectoryFailure(nil), p.failureHistory...)
	b.BoundariesSinceNewFailure = p.boundariesSinceNewFailure
	b.DistinctFailures = len(p.failureSignatures)
	return b
}

func newHostTrajectoryPolicy(advisor *DecisionAdvisor, cfg config.TrajectoryConfig, isolated bool) *hostTrajectoryPolicy {
	if advisor == nil || advisor.Mode() == AdvisorModeOff || advisor.classifierOff {
		return nil
	}
	name := advisor.config.QuestionPack
	if override := strings.TrimSpace(os.Getenv(TrajectoryPackEnvVar)); override != "" {
		name = override
	}
	if name == "" || name == schemas.QuestionPackDependencyRelationV1 {
		name = QuestionPackTrajActionV2
	}
	pack, packError := trajectoryPackByName(name)
	classifier := TrajectoryClassifier(advisorClassifier{advisor: advisor, pack: pack, packError: packError, budget: trajectoryRequestBudget})
	return &hostTrajectoryPolicy{
		advisor:       advisor,
		classifier:    classifier,
		thresholds:    cfg.Thresholds,
		isolated:      isolated,
		progressGuard: packError == nil && name == QuestionPackTrajActionV2,
	}
}
func (p *hostTrajectoryPolicy) decide(ctx context.Context, b TrajectoryBoundary) (schemas.TrajectoryDecision, bool) {
	b = p.observeFailure(b)
	var hard *schemas.TrajectoryDecision
	if p.maxIterations > 0 && (len(b.History) > 0 || b.Iteration >= p.maxIterations) {
		allowance := generationAllowance(p.tokenBudget, len(b.History))
		rc := newTrajectoryRuleContext(b.History, p.maxIterations, &allowance, nil)
		if b.Iteration >= p.maxIterations && len(b.History) < p.maxIterations {
			// Failed passes do not enter the completed-pass history.
			d := rc.decision(schemas.ActionAbortHardLimit, "Maximum iteration count reached.", []string{
				fmt.Sprintf("iterations=%d", b.Iteration),
				fmt.Sprintf("max_iterations=%d", p.maxIterations),
			})
			d.IterationCount = b.Iteration
			hard = &d
		}
		for _, rule := range hardLimitRules {
			if hard != nil {
				break
			}
			if d := rule.evaluate(rc); d != nil {
				hard = d
				break
			}
		}
	}
	if hard != nil {
		p.pending = nil
		p.lastBackend = "rules"
		p.lastDecision = *hard
		return *hard, false
	}
	if p.pending != nil && b.Repair == 0 {
		d := *p.pending
		p.pending = nil
		if !p.progressGuard || !b.FailureHistory[len(b.FailureHistory)-1].IsNew {
			return d, true
		}
	}
	b.StepBackUsed = b.StepBackUsed || p.stepBackUsed
	b.Escalated = b.Escalated || p.escalated
	b.IsolatedWorktree = p.isolated
	before := 0
	if p.advisor != nil {
		before = len(p.advisor.records)
	}
	classification, err := p.classifier.Classify(ctx, b)
	if err != nil {
		if p.advisor != nil && len(p.advisor.records) == before {
			p.advisor.records = append(p.advisor.records, schemas.DecisionDispositionRecord{
				Disposition: schemas.DispositionProviderFailure,
				Reason:      err.Error(),
			})
		}
		p.stopStreak = 0
		p.lastBackend = "rules"
		p.lastDecision = b.Rules
		if b.Rules.Action == schemas.ActionStepBack {
			p.stepBackUsed = true
		}
		return b.Rules, false
	}
	if err := validateTrajectoryProbabilities(classification.Probabilities); err != nil {
		if p.advisor != nil {
			p.advisor.records = append(p.advisor.records, schemas.DecisionDispositionRecord{
				Disposition: schemas.DispositionProviderFailure,
				Reason:      "invalid trajectory answer: " + err.Error(),
			})
		}
		p.stopStreak = 0
		p.lastBackend = "rules"
		p.lastDecision = b.Rules
		return b.Rules, false
	}
	decision := p.apply(b, classification)
	p.lastDecision = decision
	p.lastBackend = classification.Backend
	if p.advisor != nil && len(p.advisor.records) > before {
		r := &p.advisor.records[len(p.advisor.records)-1]
		r.Reason += fmt.Sprintf("; classifier decision=%s", decision.Action)
	}
	if p.advisor != nil && p.advisor.Mode() == AdvisorModeShadow {
		return b.Rules, false
	}

	if b.Repair > 0 && (decision.Action == schemas.ActionStepBack || decision.Action == schemas.ActionSurfaceToUser) {
		p.pending = &decision
	}
	// True identifies an active classifier result, even when the rules agree.
	return decision, true
}
func (p *hostTrajectoryPolicy) apply(b TrajectoryBoundary, c TrajectoryClassification) schemas.TrajectoryDecision {
	p.stepBackUsed = p.stepBackUsed || b.StepBackUsed
	d := b.Rules
	d.Action = schemas.ActionContinue
	top := TopTrajectoryLabel(c.Probabilities)
	defaults := map[TrajectoryLabel]float64{
		LabelContinue: 0,
		LabelStepBack: 0.6,
		LabelRollback: 0.7,
		LabelEscalate: 0.7,
		LabelStop:     0.8,
	}
	threshold := defaults[top]
	if v, ok := p.thresholds[string(top)]; ok {
		threshold = v
	}
	if c.Probabilities[top] < threshold {
		d.Reason = fmt.Sprintf("Trajectory classifier (%s): %s probability %.3f is below threshold %.3f.", c.Backend, top, c.Probabilities[top], threshold)
		p.stopStreak = 0
		return d
	}
	if p.progressGuard && c.Backend != "rules" && (top == LabelStepBack || top == LabelStop) &&
		len(b.FailureHistory) > 0 && b.FailureHistory[len(b.FailureHistory)-1].IsNew {
		d.Reason = "the latest failure is new to the run: the run is making progress"
		p.stopStreak = 0
		return d
	}
	if p.stepBackUsed && (top == LabelStop || top == LabelStepBack) {
		p.stopStreak++
	} else {
		p.stopStreak = 0
	}
	switch top {
	case LabelStepBack:
		if p.stepBackUsed {
			if p.stopStreak < 2 {
				d.Reason = "The prior step-back requires a second consecutive stop or step-back decision."
				return d
			}
			top = LabelStop
		} else {
			p.stepBackUsed = true
		}
	case LabelStop:
		if !p.stepBackUsed || p.stopStreak < 2 {
			d.Reason = "A stop requires a prior step-back and two consecutive stop or step-back decisions."
			return d
		}
	case LabelRollback:
		if !b.RollbackTarget || !p.isolated {
			d.Reason = "A rollback requires an isolated worktree and a snapshot with a higher score."
			return d
		}
	case LabelEscalate:
		if p.escalated || b.Escalated {
			d.Reason = "The run has already used its escalation."
			return d
		}
		if b.Repair == 0 || (p.advisor != nil && p.advisor.Mode() == AdvisorModeShadow) {
			p.escalated = true
		}
	}
	selectedProbability := c.Probabilities[top]
	if top == LabelStop && selectedProbability == 0 {
		selectedProbability = c.Probabilities[LabelStepBack]
	}
	d.Action = trajectoryActions[top]
	d.Reason = fmt.Sprintf("Trajectory classifier (%s) selected %s at probability %.3f.", c.Backend, top, selectedProbability)
	d.Evidence = append([]string{}, c.Evidence...)
	return d
}

func classifyBoundary(ctx context.Context, options PipelineRunConfig, b TrajectoryBoundary, tr *runTraceAccumulator) (schemas.TrajectoryDecision, bool) {
	if options.ClassifierPolicy == nil {
		return b.Rules, false
	}
	before := len(options.Advisor.Records())
	decision, acted := options.ClassifierPolicy.decide(ctx, b)
	// Stamp the other builder's evidence on the records this boundary wrote.
	for i := before; i < len(options.Advisor.records); i++ {
		options.Advisor.records[i].EvidenceShadow = b.EvidenceShadow
	}
	if records := options.Advisor.Records(); tr != nil && len(records) > before {
		tr.recordJevDecisions("trajectory", b.Iteration, b.Repair, records[before:])
	}
	backend := options.ClassifierPolicy.lastBackend

	emitProgress(options, fmt.Sprintf("[trajectory] %s (%s) iteration %d repair %d: classifier=%s action=%s\n", backend, options.Advisor.Mode(), b.Iteration, b.Repair, options.ClassifierPolicy.lastDecision.Action, decision.Action))
	return decision, acted
}

// A failed projection leaves the original repair or stage-failure path in control.
// The record still carries the boundary's evidence shadow, as every record of a
// boundary does.
func recordBoundaryProjectionFailure(options PipelineRunConfig, iteration, repair int, cause error, shadow []schemas.PassEvidence, tr *runTraceAccumulator) {
	if options.ClassifierPolicy == nil {
		return
	}
	record := schemas.DecisionDispositionRecord{
		Disposition:    schemas.DispositionProviderFailure,
		Reason:         fmt.Sprintf("trajectory boundary projection: %v", cause),
		EvidenceShadow: shadow,
	}
	options.Advisor.records = append(options.Advisor.records, record)
	options.ClassifierPolicy.stopStreak = 0
	if tr != nil {
		tr.recordJevDecisions("trajectory", iteration, repair, []schemas.DecisionDispositionRecord{record})
	}
}

// writerStage is the plan name of the code writer for a boundary. A boundary
// built without a plan names the builtin role.
func writerStage(b TrajectoryBoundary) string {
	if b.WriterStage != "" {
		return b.WriterStage
	}
	return "code_writer"
}
