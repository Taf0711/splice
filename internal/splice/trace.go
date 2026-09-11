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

// stageKey identifies one stage execution within a run. InvocationOrdinal
// distinguishes repeated invocations of the same stage and iteration: 0 is
// the initial pass invocation, 1 and above are repair re-entries in repair
// order, so a re-entry never overwrites the initial invocation's metrics.
type stageKey struct {
	name              string
	iteration         int
	invocationOrdinal int
}

// stageKeyFor builds the accumulator key for one stage invocation. The
// ordinal separates initial-pass records (0) from repair re-entries, so a
// re-entry records its own metrics instead of overwriting the initial pass.
func stageKeyFor(stage string, iteration int, ordinal int) stageKey {
	return stageKey{name: stage, iteration: iteration, invocationOrdinal: ordinal}
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

	stages     map[stageKey]schemas.InputMeta
	stageOrder []stageKey
	// contextRounds holds per-expansion-round context telemetry (D3),
	// keyed separately from the aggregate InputMeta rows so repair and
	// expansion records never overwrite ordinal zero.
	contextRounds map[contextRoundKey]contextRoundMeta
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
		contextRounds:    make(map[contextRoundKey]contextRoundMeta),
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

// recordContextRound records the context telemetry for one EXPANSION
// round (D3) under its own key: {stage, iteration, ordinal, round}.
// Round 0 is the initial handshake and records under the historical
// recordContext path; rounds 1+ are expansion rounds. Separate keys mean
// an expansion never overwrites the initial record and repair/expansion
// records never collide.
func (tr *runTraceAccumulator) recordContextRound(stage string, iteration, ordinal, round int, bundle schemas.ContextBundle) {
	if tr == nil {
		return
	}
	key := contextRoundKey{name: stage, iteration: iteration, ordinal: ordinal, round: round}
	meta := tr.contextRounds[key]
	meta.ContextItems += len(bundle.Items)
	for _, item := range bundle.Items {
		meta.ContextChars += len(item.Summary)
		if item.Error != nil {
			// A failed query is executed work and a delivery miss, never
			// successfully delivered context (same rule as recordContext).
			meta.ContextFailures++
		}
	}
	meta.InvocationOrdinal = ordinal
	meta.ContextRound = round
	tr.contextRounds[key] = meta
}

// contextRoundKey identifies one context round within a stage invocation.
type contextRoundKey struct {
	name      string
	iteration int
	ordinal   int
	round     int
}

// contextRoundMeta is the per-round context telemetry shape (mirrors the
// ContextItems/Chars/Failures slice of InputMeta so consumers read the
// same units).
type contextRoundMeta struct {
	ContextItems      int
	ContextChars      int
	ContextFailures   int
	InvocationOrdinal int
	ContextRound      int
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
	key := stageKeyFor(stage, iteration, 0)
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
	key := stageKeyFor(stage, iteration, 0)
	meta := tr.stages[key]
	meta.ContextItems = len(bundle.Items)
	for _, item := range bundle.Items {
		meta.ContextChars += len(item.Summary)
		if item.Error != nil {
			// A failed query is executed work and a delivery miss, never
			// successfully delivered context. Counting it here keeps the
			// failure visible next to the issued-request total.
			meta.ContextFailures++
		}
	}
	tr.stages[key] = meta
}

func (tr *runTraceAccumulator) recordEdge(stage string, iteration int, bytes int) {
	key := stageKeyFor(stage, iteration, 0)
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
	key := stageKeyFor(stage, iteration, 0)
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
	key := stageKeyFor(stage, iteration, 0)
	meta := tr.stages[key]
	meta.KeysGenerated = detail.KeysGenerated
	meta.LookupMisses = detail.LookupMisses
	meta.FTSFallback = detail.FallbackToPlainSearch
	meta.ExemplarsRetrieved = detail.ExemplarsRetrieved
	tr.stages[key] = meta
}

// recordScopeMetrics records the Part A context-bridge suppression counts
// for one stage invocation: the deterministic counterfactual default
// request size, what the scoped request actually executed, and the
// operations the host structurally omitted. It also carries the scope
// plan's privilege booleans via expansion count. Counts only.
// InvocationOrdinal 0 marks the initial pass; repair re-entries record
// under their own ordinal so a re-entry never overwrites this record.
func (tr *runTraceAccumulator) recordScopeMetrics(stage string, iteration int, sup ScopeSuppression, scope StageScopePlan) {
	tr.recordScopeMetricsOrdinal(stage, iteration, 0, sup, scope)
}

// recordScopeMetricsOrdinal is the ordinal-aware form. The scope plan's
// remaining ExpansionBudget lands in ScopeExpansions (its documented
// meaning); ExpansionsPerformed records what the invocation actually spent,
// as a measured zero when nothing expanded. Expansions are not yet granted
// anywhere in production, so the performed count comes from the scope
// plan's spent field when the planner starts granting; today it records 0.
func (tr *runTraceAccumulator) recordScopeMetricsOrdinal(stage string, iteration, ordinal int, sup ScopeSuppression, scope StageScopePlan) {
	if tr == nil {
		return
	}
	key := stageKeyFor(stage, iteration, ordinal)
	meta := tr.stages[key]
	meta.ContextQueriesDefault = sup.ContextQueriesDefault
	meta.ContextQueriesExecuted = sup.ContextQueriesExecuted
	meta.ContextQueriesSuppressed = sup.ContextQueriesSuppressed
	meta.GlobalListsSuppressed = sup.GlobalListsSuppressed
	meta.FileReadsSuppressed = sup.FileReadsSuppressed
	meta.SearchesSuppressed = sup.SearchesSuppressed
	meta.ScopeExpansions = scope.ExpansionBudget
	meta.ExpansionsPerformed = scope.ExpansionsSpent
	meta.InvocationOrdinal = ordinal
	tr.stages[key] = meta
}

// recordEvidencePlanOrdinal records the production evidence plan's
// operation-level dispositions for one invocation. Every remaining warm-plan
// operation is recorded as executed; every admitted replacement is recorded
// as satisfied-by-evidence with its evidence identity; every rejected
// candidate records the cold operations that stayed after rejection. The
// validation counters estimate validation reads from the supporting and
// dependency references E3 actually re-hashed; they are reported separately
// from executed work.
func (tr *runTraceAccumulator) recordEvidencePlanOrdinal(stage string, iteration, ordinal int, plan *EvidencePlan) {
	if tr == nil || plan == nil {
		return
	}
	key := stageKeyFor(stage, iteration, ordinal)
	meta := tr.stages[key]
	decisions := make([]schemas.OperationDecision, 0, len(plan.Warm.Operations)+len(plan.Admitted)+len(plan.Rejected))
	executed, satisfied, retained := 0, 0, 0
	validationReads := 0
	for _, res := range plan.Admitted {
		reads := 0
		if res.Record != nil {
			reads = len(res.Record.Supporting) + len(res.Record.Dependencies)
		}
		for _, op := range res.Replaced {
			decisions = append(decisions, schemas.OperationDecision{
				Operation:       subjectToOpName(op),
				Disposition:     schemas.OperationSatisfiedByEvidence,
				EvidenceID:      res.Record.Identity,
				Reason:          res.Reason,
				ValidationReads: reads,
			})
			satisfied++
			validationReads += reads
		}
	}
	for _, rej := range plan.Rejected {
		for _, op := range replacedOperationsFor(rej.Need) {
			decisions = append(decisions, schemas.OperationDecision{
				Operation:   subjectToOpName(op),
				Disposition: schemas.OperationRetainedAfterReject,
				Reason:      rej.Reason,
			})
			retained++
		}
	}
	for _, op := range plan.Warm.Operations {
		decisions = append(decisions, schemas.OperationDecision{
			Operation:   op.Name,
			Disposition: schemas.OperationExecuted,
			Reason:      "retained improved cold operation",
		})
		executed++
	}
	meta.OperationDecisions = &decisions
	meta.OperationsExecuted = executed
	meta.OperationsSatisfiedByEvidence = satisfied
	meta.OperationsRetainedAfterReject = retained
	meta.EvidenceValidationReads = validationReads
	meta.InvocationOrdinal = ordinal
	tr.stages[key] = meta
}

// RecordScopeMetrics records the scope-suppression metrics for one stage
// invocation from a caller-supplied ScopeMetrics payload. It writes ONLY the
// scope fields on InputMeta: the discovery-plan counters (and the legacy
// DiscoveryReadsAvoided inferred-savings counter in particular) are never
// touched here, so observed host omissions stay decoupled from resolved
// question tallies. Counts only; no query text or paths land in the trace.
func (tr *runTraceAccumulator) RecordScopeMetrics(stage string, iteration int, m schemas.ScopeMetrics) {
	if tr == nil {
		return
	}
	if err := m.Validate(); err != nil {
		return
	}
	key := stageKeyFor(stage, iteration, 0)
	meta := tr.stages[key]
	meta.ContextQueriesDefault = m.ContextQueriesDefault
	meta.ContextQueriesExecuted = m.ContextQueriesExecuted
	meta.ContextQueriesSuppressed = m.ContextQueriesSuppressed
	meta.GlobalListsSuppressed = m.GlobalListsSuppressed
	meta.SearchesSuppressed = m.SearchesSuppressed
	meta.ScopeExpansions = m.ScopeExpansions
	tr.stages[key] = meta
}

// recordDiscoveryPlan records the Track C discovery-plan outcome for one
// stage invocation: how many questions the plan saw, how many the task
// itself answered, how many the cognition graph resolved (each counts as one
// conservatively avoided discovery operation), how many stayed unresolved,
// and the anchor freshness validation tally. Counts only; no question text
// or node claims land in the trace.
func (tr *runTraceAccumulator) recordDiscoveryPlan(stage string, iteration int, plan DiscoveryPlan) {
	tr.recordDiscoveryPlanOrdinal(stage, iteration, 0, plan)
}

// recordDiscoveryPlanOrdinal records the discovery-plan counters under an
// explicit invocation ordinal. Initial pass (0) and repair re-entries (1+)
// keep separate records instead of overwriting one {stage, iteration} row.
func (tr *runTraceAccumulator) recordDiscoveryPlanOrdinal(stage string, iteration, ordinal int, plan DiscoveryPlan) {
	if tr == nil {
		return
	}
	key := stageKeyFor(stage, iteration, ordinal)
	meta := tr.stages[key]
	meta.DiscoveryQuestions = len(plan.ResolvedByTask) + len(plan.ResolvedByCognition) + len(plan.Unresolved)
	meta.DiscoveryResolvedTask = len(plan.ResolvedByTask)
	meta.DiscoveryResolvedCog = len(plan.ResolvedByCognition)
	meta.DiscoveryUnresolved = len(plan.Unresolved)
	// DiscoveryReadsAvoided is deliberately NOT set here: a resolved
	// question is not a suppressed read. Actual suppression is recorded
	// by recordScopeMetrics from the host decisions in ScopedContextRequest.
	meta.AnchorsValidated = plan.AnchorsValidated
	meta.AnchorsFailed = plan.AnchorsFailed
	meta.SemanticHits = plan.SemanticHits
	meta.InvocationOrdinal = ordinal
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
		meta := tr.stages[stageKeyFor(rec.Name, rec.Iteration, 0)]
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
