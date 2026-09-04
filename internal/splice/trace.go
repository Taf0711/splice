package splice

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/cognition"
	"github.com/Taf0711/splice/internal/splice/memoryreason"
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

	// C1b run-local freshness memo. The cache is created lazily on first use
	// and lives for exactly one run: the accumulator is constructed fresh per
	// splicerun.Run, so no Reset is needed at run start (confirmed: run.go
	// builds the accumulator inside the run loop; it is never reused across
	// runs). Reset stays available for explicit lifecycle control in tests.
	freshnessOnce sync.Once
	freshness     *cognition.FreshnessCache
	// muSig guards the mutation signature fields (the pass loop and repair
	// re-entry can interleave; the cache lock must never be held while
	// reading them).
	muSig          sync.Mutex
	mutationSig    string
	mutationSigSet bool

	// Run-local cognition replay guard. deliveredMemory records the stable
	// IDs of cognition items the model ACTUALLY received per consuming stage
	// (post-admission, post-compaction), keyed (stage, memoryID). Repair
	// re-entry shares this accumulator, so a stage that has already consumed
	// an item does not receive it again during the same run: repair should
	// react to new verifier and failure evidence, not re-read the same prior.
	// The set lives and dies with the run (the accumulator is built fresh per
	// splicerun.Run), so a new run starts with an empty consumed set.
	muDelivered      sync.Mutex
	deliveredMemory  map[deliveredMemoryKey]struct{}
	replaySuppressed int
}

// deliveredMemoryKey identifies one cognition consumption: which stage
// received which stable memory item. The same item stays deliverable to
// other memory-consuming stages; only replay to the same stage is
// suppressed.
type deliveredMemoryKey struct {
	StageName string
	MemoryID  string
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
		deliveredMemory:  make(map[deliveredMemoryKey]struct{}),
	}
}

// filterAlreadyDelivered removes every item the given stage has already
// consumed earlier in this run from the bundle, in place. It is the run-local
// replay guard: retrieval stays real (telemetry records what was retrieved),
// but an item the model has already seen is suppressed from prompt delivery.
// Observations are keyed by memoryreason.StableID (observation:<id>) and
// exemplars by exemplar:<run_id>; content, rank, and retrieval path never
// participate in the identity. Returns the number of items suppressed.
func (tr *runTraceAccumulator) filterAlreadyDelivered(stageName string, bundle *schemas.MemoryBundle) int {
	if tr == nil || bundle == nil || (len(bundle.Observations) == 0 && len(bundle.Exemplars) == 0) {
		return 0
	}
	tr.muDelivered.Lock()
	defer tr.muDelivered.Unlock()
	suppressed := 0
	kept := bundle.Observations[:0]
	for _, obs := range bundle.Observations {
		id := memoryreason.StableID(obs)
		if id == "" {
			// No stable identity: keep the item. Replay suppression keys on
			// stable IDs only; an identity-less item cannot be recognized as
			// a replay, and dropping it here would silently change delivery.
			kept = append(kept, obs)
			continue
		}
		key := deliveredMemoryKey{StageName: stageName, MemoryID: id}
		if _, consumed := tr.deliveredMemory[key]; consumed {
			suppressed++
			continue
		}
		kept = append(kept, obs)
	}
	bundle.Observations = kept
	keptEx := bundle.Exemplars[:0]
	for _, ex := range bundle.Exemplars {
		id := "exemplar:" + ex.RunID
		key := deliveredMemoryKey{StageName: stageName, MemoryID: id}
		if _, consumed := tr.deliveredMemory[key]; consumed {
			suppressed++
			continue
		}
		keptEx = append(keptEx, ex)
	}
	bundle.Exemplars = keptEx
	tr.replaySuppressed += suppressed
	return suppressed
}

// markDelivered records the stable IDs of the bundle a stage's invocation
// FINALIZED as model-visible (after admission, replay filtering, and
// compaction). Only model-visible items become consumed: an item that
// admission rejected or compaction dropped never reached the model and
// stays eligible for a later legitimate delivery.
func (tr *runTraceAccumulator) markDelivered(stageName string, bundle *schemas.MemoryBundle) {
	if tr == nil || bundle == nil {
		return
	}
	if len(bundle.Observations) == 0 && len(bundle.Exemplars) == 0 {
		return
	}
	tr.muDelivered.Lock()
	defer tr.muDelivered.Unlock()
	for _, obs := range bundle.Observations {
		id := memoryreason.StableID(obs)
		if id == "" {
			continue
		}
		tr.deliveredMemory[deliveredMemoryKey{StageName: stageName, MemoryID: id}] = struct{}{}
	}
	for _, ex := range bundle.Exemplars {
		tr.deliveredMemory[deliveredMemoryKey{StageName: stageName, MemoryID: "exemplar:" + ex.RunID}] = struct{}{}
	}
}

// replaySuppressedCount reports how many cognition items this run suppressed
// as replays. Test/debug seam for proving the mechanism fired; not written
// into traces.
func (tr *runTraceAccumulator) replaySuppressedCount() int {
	if tr == nil {
		return 0
	}
	tr.muDelivered.Lock()
	defer tr.muDelivered.Unlock()
	return tr.replaySuppressed
}

func (tr *runTraceAccumulator) noteStage(stage string, iteration int) {
	tr.currentStage = stage
	tr.currentIter = iteration
}

// freshnessCache returns the run's C1b freshness memo, creating it on first
// use. The accumulator is built once per splicerun.Run and never shared
// across runs, so the cache's lifetime is exactly the run's lifetime and no
// Reset at run start is required.
func (tr *runTraceAccumulator) freshnessCache() *cognition.FreshnessCache {
	if tr == nil {
		return nil
	}
	tr.freshnessOnce.Do(func() {
		tr.freshness = cognition.NewFreshnessCache()
	})
	return tr.freshness
}

// noteSpliceMutation records a Splice-permitted repository mutation by
// bumping the freshness cache's worktree generation when the mutation
// signature changes. The signature is the deterministic join of the
// changed-file paths the pipeline recorded (writer and test generator
// stages): a new, different, or grown record means the working tree moved
// under Splice's own control, so every memoized batch set is invalidated and
// the next classify re-spawns the exact diff. The generation key makes the
// memoization exact: same generation means the tree is unchanged in every
// way the pipeline observed; a new generation means re-prove everything.
func (tr *runTraceAccumulator) noteSpliceMutation(changedFiles map[string][]string) {
	cache := tr.freshnessCache()
	if cache == nil {
		return
	}
	sig := mutationSignature(changedFiles)
	tr.muSig.Lock()
	changed := !tr.mutationSigSet || sig != tr.mutationSig
	tr.mutationSig = sig
	tr.mutationSigSet = true
	tr.muSig.Unlock()
	if changed {
		cache.BumpGeneration()
	}
}

// mutationSignature builds the deterministic mutation signature from the
// changed-file record: stage names and paths, sorted and joined. An absent
// record and an empty record are distinct states, but both mean "no paths
// changed"; the signature only needs to detect DIFFERENT record content.
func mutationSignature(changedFiles map[string][]string) string {
	if len(changedFiles) == 0 {
		return ""
	}
	stages := make([]string, 0, len(changedFiles))
	for stage := range changedFiles {
		stages = append(stages, stage)
	}
	sort.Strings(stages)
	var b strings.Builder
	for _, stage := range stages {
		paths := append([]string(nil), changedFiles[stage]...)
		sort.Strings(paths)
		b.WriteString(stage)
		b.WriteByte('=')
		b.WriteString(strings.Join(paths, ","))
		b.WriteByte(';')
	}
	return b.String()
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

// recordMemoryLookup records the retrieval path for one stage invocation
// (C0.4). mode is "direct" when a fresh cognition fast-path hit was admitted
// and the broad search was skipped, else "search". direct and stale count the
// observations classified fresh/stale on the direct path before admission;
// direct_candidates is their sum (the topic lookup returned them all). The
// fields are consumer-pending: no reader consumes them yet (pairing rule).
func (tr *runTraceAccumulator) recordMemoryLookup(stage string, iteration int, mode string, direct, stale int) {
	key := stageKey{stage, iteration}
	meta := tr.stages[key]
	meta.MemoryLookupMode = mode
	meta.DirectCandidates = direct + stale
	meta.DirectHits = direct
	meta.StaleHits = stale
	tr.stages[key] = meta
}

// recordMissPathDetail records the C1c miss-path telemetry for one stage
// invocation: the number of derived cognition keys, how many topic lookups
// missed, whether the ranked search fell back to plain Search, and how many
// exemplars the retrieval produced before admission. Counts only; no key
// text, path, or content lands in the trace.
func (tr *runTraceAccumulator) recordMissPathDetail(stage string, iteration int, detail MissPathDetail) {
	if tr == nil {
		return
	}
	key := stageKey{stage, iteration}
	meta := tr.stages[key]
	meta.KeysGenerated = detail.KeysGenerated
	meta.LookupMisses = detail.LookupMisses
	meta.FTSFallback = detail.FallbackToPlainSearch
	meta.ExemplarsRetrieved = detail.ExemplarsRetrieved
	tr.stages[key] = meta
}

// recordDiscoveryPlan records the Track C discovery-plan outcome for one
// stage invocation: how many questions the plan saw, how many the task
// itself answered, how many the cognition graph resolved (each counts as one
// conservatively avoided discovery operation), how many stayed unresolved,
// and the anchor freshness validation tally. Counts only; no question text
// or node claims land in the trace.
func (tr *runTraceAccumulator) recordDiscoveryPlan(stage string, iteration int, plan DiscoveryPlan) {
	if tr == nil {
		return
	}
	key := stageKey{stage, iteration}
	meta := tr.stages[key]
	meta.DiscoveryQuestions = len(plan.ResolvedByTask) + len(plan.ResolvedByCognition) + len(plan.Unresolved)
	meta.DiscoveryResolvedTask = len(plan.ResolvedByTask)
	meta.DiscoveryResolvedCog = len(plan.ResolvedByCognition)
	meta.DiscoveryUnresolved = len(plan.Unresolved)
	meta.DiscoveryReadsAvoided = len(plan.ResolvedByCognition)
	meta.AnchorsValidated = plan.AnchorsValidated
	meta.AnchorsFailed = plan.AnchorsFailed
	meta.SemanticHits = plan.SemanticHits
	tr.stages[key] = meta
}

// MissPathDetail is the miss-path telemetry payload for recordMissPathDetail.
type MissPathDetail struct {
	KeysGenerated         int
	LookupMisses          int
	FallbackToPlainSearch int
	ExemplarsRetrieved    int
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
