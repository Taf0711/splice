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
	// memoryStatus is active / off / unavailable. It starts at the run's
	// resolved status and degrades to unavailable when a mid-run retrieval
	// fails (a deliberately-disabled run stays off).
	memoryStatus string

	stages        map[stageKey]schemas.InputMeta
	stageOrder    []stageKey
	interventions []schemas.InterventionRecord
	interactions  []schemas.InteractionRecord
	memoryItems   int
	memoryChars   int
	currentStage  string
	currentIter   int
	history       []schemas.IterationState
	// completedStages holds every StageRecord the run has produced so far, so a
	// partial trace can be built mid-run. eventsPartial is set when an
	// incremental write failed; the final trace records it so consumers know the
	// trace may miss events.
	completedStages []schemas.StageRecord
	eventsPartial   bool
	// warnWriteFailure is the caller's output seam for telemetry loss;
	// warnedWriteFailure enforces warn-exactly-once across all write attempts.
	warnWriteFailure   func(msg string)
	warnedWriteFailure bool

	// LN2 bucket-key identities and provenance, computed at run start and
	// written into the trace so applied budgets always carry their origin.
	toolFingerprint  string
	topologyHash     string
	stagePromptHash  map[string]string
	budgetProvenance map[string]string
}

func newRunTraceAccumulator(store TraceStore, runID, sessionID, projectRoot string, plan schemas.ExecutionPlan, memoryStatus string, warnWriteFailure func(msg string)) *runTraceAccumulator {
	return &runTraceAccumulator{
		store:            store,
		runID:            runID,
		sessionID:        sessionID,
		projectRoot:      projectRoot,
		plan:             plan,
		memoryStatus:     memoryStatus,
		warnWriteFailure: warnWriteFailure,
		stages:           make(map[stageKey]schemas.InputMeta),
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
	meta.MemoryItems += len(bundle.Observations)
	meta.ExemplarItems += len(bundle.Exemplars)
	invocationChars := 0
	for _, obs := range bundle.Observations {
		invocationChars += len(obs.Title) + len(obs.Content)
	}
	meta.MemoryChars += invocationChars
	tr.stages[key] = meta
	tr.memoryItems += len(bundle.Observations)
	tr.memoryChars += invocationChars
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

// noteMemorySearchFailed degrades the run's memory status to unavailable when
// a mid-run retrieval failed. A deliberately-disabled run stays off.
func (tr *runTraceAccumulator) noteMemorySearchFailed() {
	if tr == nil || tr.memoryStatus == "off" {
		return
	}
	tr.memoryStatus = "unavailable"
}

// recordInteraction appends a repair-loop interaction record so the trace
// carries the message lifecycle, not just the TUI events.
func (tr *runTraceAccumulator) recordInteraction(rec schemas.InteractionRecord) {
	if tr == nil {
		return
	}
	tr.interactions = append(tr.interactions, rec)
}

// noteTraceWriteFailed marks the trace as partial after a mid-run incremental
// write failure. The run itself continues; only the trace's completeness is
// degraded. The first failure also fires the caller's warning callback exactly
// once so telemetry loss self-announces instead of surfacing later as silent
// zeros.
func (tr *runTraceAccumulator) noteTraceWriteFailed() {
	if tr == nil {
		return
	}
	tr.eventsPartial = true
	if tr.warnWriteFailure != nil && !tr.warnedWriteFailure {
		tr.warnedWriteFailure = true
		tr.warnWriteFailure("run trace could not be persisted; token telemetry for this session will be missing")
	}
}

// recordStageCompletion appends a finished stage record so a partial trace can
// include it. The caller persists separately via persistPartial.
func (tr *runTraceAccumulator) recordStageCompletion(rec schemas.StageRecord) {
	if tr == nil {
		return
	}
	tr.completedStages = append(tr.completedStages, rec)
}

// replaceStageRecord replaces the completed-stage record matching {Name,
// Iteration}, or appends when absent. Re-invocations (the repair loop) merge
// into one record per iteration, so the trace keeps a single record too.
func (tr *runTraceAccumulator) replaceStageRecord(rec schemas.StageRecord) {
	if tr == nil {
		return
	}
	for i, existing := range tr.completedStages {
		if existing.Name == rec.Name && existing.Iteration == rec.Iteration {
			tr.completedStages[i] = rec
			return
		}
	}
	tr.completedStages = append(tr.completedStages, rec)
}

// persistPartial writes a partial trace with status "running" reflecting the
// stages and iterations completed so far. Best-effort: a build or write failure
// marks the trace partial and never aborts the run.
func (tr *runTraceAccumulator) persistPartial(ctx context.Context) {
	if tr == nil || tr.store == nil {
		return
	}
	trace, err := tr.buildOutcome(tr.completedStages, "running", "")
	if err != nil {
		tr.noteTraceWriteFailed()
		return
	}
	if err := tr.store.UpsertTrace(ctx, trace); err != nil {
		tr.noteTraceWriteFailed()
	}
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

// buildOutcome assembles and validates a trace from the given stage records
// and status. It is shared by the final write and the mid-run partial writes.
func (tr *runTraceAccumulator) buildOutcome(stageRecords []schemas.StageRecord, status, abortReason string) (schemas.RunOutcome, error) {
	stages := make([]schemas.TracedStage, 0, len(stageRecords))
	for _, rec := range stageRecords {
		meta := tr.stages[stageKey{rec.Name, rec.Iteration}]
		stages = append(stages, schemas.TracedStage{
			StageRecord: rec,
			InputMeta:   meta,
			PromptHash:  tr.stagePromptHash[rec.Name],
		})
	}

	memoryStatus := tr.memoryStatus
	if memoryStatus == "" {
		memoryStatus = "off"
	}

	outcome := schemas.OutcomeRecord{
		Status:       status,
		ChangedFiles: changedFilesUnion(tr.history),
	}
	if abortReason != "" {
		outcome.AbortReason = abortReason
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
		Interventions:    tr.interventions,
		Interactions:     tr.interactions,
		ToolFingerprint:  tr.toolFingerprint,
		TopologyHash:     tr.topologyHash,
		BudgetProvenance: tr.budgetProvenance,
	}
	if tr.eventsPartial {
		trace.EventsStatus = schemas.TraceEventsPartial
	} else {
		trace.EventsStatus = schemas.TraceEventsComplete
	}
	if err := trace.Validate(); err != nil {
		return schemas.RunOutcome{}, fmt.Errorf("invalid run outcome for run %s: %w", tr.runID, err)
	}
	return trace, nil
}

// buildRunOutcome assembles and validates the final trace from the pipeline
// result and the accumulated per-stage metadata. It returns an error on
// schema-validation failure (a malformed trace is a bug, not a write failure).
func (tr *runTraceAccumulator) buildRunOutcome(result schemas.PipelineResult) (schemas.RunOutcome, error) {
	return tr.buildOutcome(result.Stages, result.Status, abortReason(result))
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
