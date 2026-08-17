package splice

import (
	"context"
	"fmt"
	"sort"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// TraceStore persists run outcome traces and post-run verdicts. It is separate
// from MemoryStore; *memd.Client implements both. A nil TraceStore means
// tracing is off and every trace write is skipped silently.
type TraceStore interface {
	UpsertTrace(ctx context.Context, trace schemas.RunOutcome) error
	UpsertVerdict(ctx context.Context, verdict schemas.VerdictRecord) error
}

// stageKey identifies one stage execution within a run.
type stageKey struct {
	name      string
	iteration int
}

// runTraceAccumulator collects the per-stage input metadata, interventions,
// memory stats, and iteration history the RunOutcome needs. It is threaded
// through the pass and stage runners and read by the OnPermission wrapper.
// The trace is written once, after the run settles, by buildRunOutcome.
type runTraceAccumulator struct {
	store       TraceStore // nil means tracing is off
	runID       string
	sessionID   string
	projectRoot string
	plan        schemas.ExecutionPlan
	memoryOn    bool

	stages        map[stageKey]schemas.InputMeta
	stageOrder    []stageKey
	interventions []schemas.InterventionRecord
	memoryItems   int
	memoryChars   int
	currentStage  string
	currentIter   int
	history       []schemas.IterationState
}

func newRunTraceAccumulator(store TraceStore, runID, sessionID, projectRoot string, plan schemas.ExecutionPlan, memoryOn bool) *runTraceAccumulator {
	return &runTraceAccumulator{
		store:       store,
		runID:       runID,
		sessionID:   sessionID,
		projectRoot: projectRoot,
		plan:        plan,
		memoryOn:    memoryOn,
		stages:      make(map[stageKey]schemas.InputMeta),
	}
}

func (tr *runTraceAccumulator) noteStage(stage string, iteration int) {
	tr.currentStage = stage
	tr.currentIter = iteration
}

func (tr *runTraceAccumulator) recordHistory(state schemas.IterationState) {
	tr.history = append(tr.history, state)
}

func (tr *runTraceAccumulator) recordMemory(stage string, iteration int, bundle schemas.MemoryBundle) {
	key := stageKey{stage, iteration}
	meta := tr.stages[key]
	meta.MemoryItems = len(bundle.Observations)
	for _, obs := range bundle.Observations {
		meta.MemoryChars += len(obs.Title) + len(obs.Content)
	}
	tr.stages[key] = meta
	tr.memoryItems += len(bundle.Observations)
	tr.memoryChars += meta.MemoryChars
}

func (tr *runTraceAccumulator) recordContext(stage string, iteration int, bundle schemas.ContextBundle) {
	key := stageKey{stage, iteration}
	meta := tr.stages[key]
	meta.ContextItems = len(bundle.Items)
	for _, item := range bundle.Items {
		meta.ContextChars += len(item.Summary)
	}
	tr.stages[key] = meta
}

func (tr *runTraceAccumulator) recordEdge(stage string, iteration int, bytes int) {
	key := stageKey{stage, iteration}
	meta := tr.stages[key]
	meta.EdgePayloadBytes = bytes
	tr.stages[key] = meta
}

// recordPermission records a permission tap. Only interactive decisions
// (PermissionModeAsk with a real allow/deny choice) count; auto-grants in
// unsafe/auto modes are not taps.
func (tr *runTraceAccumulator) recordPermission(event agent.PermissionEvent) {
	if event.PermissionMode != agent.PermissionModeAsk || event.DecisionAction == "" {
		return
	}
	choice := "allow"
	if event.DecisionAction == agent.PermissionDecisionDeny {
		choice = "deny"
	}
	tr.interventions = append(tr.interventions, schemas.InterventionRecord{
		Type:      schemas.InterventionPermissionTap,
		Weight:    1,
		Stage:     tr.currentStage,
		Iteration: tr.currentIter,
		Summary:   event.ToolName + ": " + event.Reason,
		Choice:    choice,
	})
}

// buildRunOutcome assembles and validates the trace from the final pipeline
// result and the accumulated per-stage metadata. It returns an error on
// schema-validation failure (a malformed trace is a bug, not a write failure).
func (tr *runTraceAccumulator) buildRunOutcome(result schemas.PipelineResult) (schemas.RunOutcome, error) {
	stages := make([]schemas.TracedStage, 0, len(result.Stages))
	for _, rec := range result.Stages {
		meta := tr.stages[stageKey{rec.Name, rec.Iteration}]
		stages = append(stages, schemas.TracedStage{
			StageRecord: rec,
			InputMeta:   meta,
		})
	}

	memoryStatus := "off"
	if tr.memoryOn {
		memoryStatus = "active"
	}

	outcome := schemas.OutcomeRecord{
		Status:       result.Status,
		ChangedFiles: changedFilesUnion(tr.history),
	}
	if result.AbortReason != nil {
		outcome.AbortReason = *result.AbortReason
	}

	trace := schemas.RunOutcome{
		SchemaVersion: schemas.TraceSchemaVersion,
		RunID:         tr.runID,
		SessionID:     tr.sessionID,
		RepoRoot:      tr.projectRoot,
		Intent:        tr.plan.RequestIntent,
		Tier:          string(tr.plan.Tier),
		Plan:          &tr.plan,
		Iterations:    tr.history,
		Stages:        stages,
		Outcome:       outcome,
		Memory: schemas.MemoryRecord{
			Status: memoryStatus,
			Items:  tr.memoryItems,
			Chars:  tr.memoryChars,
		},
		Interventions: tr.interventions,
	}
	if err := trace.Validate(); err != nil {
		return schemas.RunOutcome{}, fmt.Errorf("invalid run outcome for run %s: %w", tr.runID, err)
	}
	return trace, nil
}

// changedFilesUnion returns the sorted unique set of files changed across all
// iterations, so the outcome carries the run's full footprint.
func changedFilesUnion(history []schemas.IterationState) []string {
	seen := make(map[string]struct{})
	for _, state := range history {
		for _, path := range state.FilesChanged {
			seen[path] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}
