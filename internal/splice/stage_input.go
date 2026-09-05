// stage_input.go enforces per-stage input budgets at composition time. When a
// composed HarnessStageInput exceeds its stage's input allowance, optional
// payload is dropped in a fixed priority order — weakest provenance first —
// before the stage runs, so an over-large context degrades gracefully instead
// of pre-charging the run's token budget toward an abort.
//
// Determinism contract: the same composed input always produces the same
// compacted output. Drops happen one element at a time in a fixed order and
// the measurement is re-run after every drop.

package splice

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/splice/cognition"
	"github.com/Taf0711/splice/internal/splice/learn"
	"github.com/Taf0711/splice/internal/splice/memoryreason"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// bytesPerTokenEstimate converts measured JSON bytes to an approximate token
// count. The budget arithmetic is token-based; exact tokenization is neither
// available nor needed for a bound, but the estimate must stay deterministic,
// which a fixed ratio guarantees.
const bytesPerTokenEstimate = 4

// estimateStageInputTokens measures one composed stage input deterministically
// as ceil(marshaled bytes / bytesPerTokenEstimate). A marshal failure returns
// zero: the struct marshals by construction, and a zero estimate never
// triggers compaction on its own.
func estimateStageInputTokens(input schemas.HarnessStageInput) int {
	encoded, err := json.Marshal(input)
	if err != nil {
		return 0
	}
	return (len(encoded) + bytesPerTokenEstimate - 1) / bytesPerTokenEstimate
}

// inputAllowanceTokens is the per-stage input ceiling: the stage's own input
// budget plus the tier reserve, mirroring how TotalInputBudget folds reserve
// into its allowance.
func inputAllowanceTokens(budget schemas.StageBudget, tier schemas.PipelineTier) int {
	return budget.InputMax + reserveForTier(tier)
}

// compactStageInput trims optional payload from input until its estimated
// token size fits the stage allowance. Drop order, weakest value first:
//
//  1. memory exemplars, last (lowest-rank) first;
//  2. memory observations, last first;
//  3. prior-stage summaries, oldest stage first;
//  4. prior changed-file lists, oldest stage first;
//  5. revision context.
//
// RequestIntent and AcceptanceFacts are required content and are never
// dropped or truncated; if even the stripped input exceeds the allowance, the
// caller gets a loud error naming the stage and both sizes rather than a
// silently truncated prompt. The returned bool reports whether anything was
// dropped, and note receives one deterministic summary line per drop step.
func compactStageInput(stageName string, budget schemas.StageBudget, tier schemas.PipelineTier, input schemas.HarnessStageInput, note func(string)) (schemas.HarnessStageInput, bool, error) {
	allowance := inputAllowanceTokens(budget, tier)
	before := estimateStageInputTokens(input)
	if before <= allowance {
		return input, false, nil
	}

	dropExemplars := func() bool {
		if input.MemoryBundle == nil || len(input.MemoryBundle.Exemplars) == 0 {
			return false
		}
		last := len(input.MemoryBundle.Exemplars) - 1
		input.MemoryBundle.Exemplars = input.MemoryBundle.Exemplars[:last]
		return true
	}
	dropObservation := func() bool {
		if input.MemoryBundle == nil || len(input.MemoryBundle.Observations) == 0 {
			return false
		}
		last := len(input.MemoryBundle.Observations) - 1
		input.MemoryBundle.Observations = input.MemoryBundle.Observations[:last]
		return true
	}
	oldestStage := func(names []string) string {
		if len(names) == 0 {
			return ""
		}
		sort.Strings(names) // lexical fallback keeps determinism for keys outside the plan roster
		for _, seqName := range stageOrder(input.PipelineStages) {
			for _, name := range names {
				if name == seqName {
					return name
				}
			}
		}
		return names[0]
	}
	oldestSummaryStage := func() string {
		names := make([]string, 0, len(input.PriorSummaries))
		for name := range input.PriorSummaries {
			names = append(names, name)
		}
		return oldestStage(names)
	}
	oldestChangedFilesStage := func() string {
		names := make([]string, 0, len(input.PriorChangedFiles))
		for name := range input.PriorChangedFiles {
			names = append(names, name)
		}
		return oldestStage(names)
	}
	dropOldestSummary := func() (string, bool) {
		name := oldestSummaryStage()
		if name == "" {
			return "", false
		}
		delete(input.PriorSummaries, name)
		return name, true
	}
	dropOldestChangedFiles := func() (string, bool) {
		name := oldestChangedFilesStage()
		if name == "" {
			return "", false
		}
		delete(input.PriorChangedFiles, name)
		return name, true
	}
	dropRevisionContext := func() bool {
		if input.RevisionContext == nil {
			return false
		}
		input.RevisionContext = nil
		return true
	}

	compacted := false
	after := before
	for estimateStageInputTokens(input) > allowance {
		switch {
		case dropExemplars():
			compacted = true
			note(fmt.Sprintf("input compact: %s dropped one exemplar", stageName))
		case dropObservation():
			compacted = true
			note(fmt.Sprintf("input compact: %s dropped one memory observation", stageName))
		default:
			if name, ok := dropOldestSummary(); ok {
				compacted = true
				note(fmt.Sprintf("input compact: %s dropped prior summary for %s", stageName, name))
				continue
			}
			if name, ok := dropOldestChangedFiles(); ok {
				compacted = true
				note(fmt.Sprintf("input compact: %s dropped changed files for %s", stageName, name))
				continue
			}
			if dropRevisionContext() {
				compacted = true
				note(fmt.Sprintf("input compact: %s dropped revision context", stageName))
				continue
			}
			return input, compacted, fmt.Errorf(
				"stage %s input overflow: estimated %d tokens exceed the %d-token allowance after dropping all optional payload; distilled intent and acceptance facts are never truncated",
				stageName, estimateStageInputTokens(input), allowance)
		}
	}
	after = estimateStageInputTokens(input)
	if compacted && note != nil {
		note(fmt.Sprintf("input compact: %s settled at ~%d tokens (was ~%d, allowance %d)", stageName, after, before, allowance))
	}
	return input, compacted, nil
}

// stageOrder returns the pipeline roster minus empty entries, preserving run
// order so "oldest summary" means the earliest executed stage.
func stageOrder(pipelineStages []string) []string {
	out := make([]string, 0, len(pipelineStages))
	for _, name := range pipelineStages {
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// stageInputPreparation carries everything one stage invocation needs to
// build its final composed input: the draft input, the stage (for capability
// checks), its budget, and the run's memory/trace seams.
type stageInputPreparation struct {
	Input     schemas.HarnessStageInput
	Stage     stages.Stage
	Budget    schemas.StageBudget
	Tier      schemas.PipelineTier
	Iteration int
	WorkDir   string
	Options   PipelineRunConfig
	Memory    MemoryStore
	Trace     *runTraceAccumulator
	NowUnix   int64
}

// prepareStageInput is the single composition path for both the normal pass
// and repair re-entry. It retrieves memory for consuming stages, applies the
// deterministic admission policy, compacts over-budget inputs, and records
// post-compaction delivered-memory counts on the trace. Admission runs before
// compaction so rejected items never consume allowance, and trace accounting
// runs after compaction so it describes delivered memory rather than
// retrieved memory.
//
// C0 cognition fast path: before the broad Memory.Search, a deterministic
// direct topic lookup is attempted from structural cognition keys. A fresh,
// admitted direct hit skips Search AND exemplar retrieval. Every other
// outcome - no key, no capability, lookup miss/error, stale, unknown, or
// empty after admission - falls back byte-identically to the existing Search
// path below.
func prepareStageInput(ctx context.Context, p stageInputPreparation) (schemas.HarnessStageInput, error) {
	input := p.Input
	caps := p.Stage.Capabilities()
	if p.Memory != nil && caps.ConsumesMemory {
		root := memoryProjectRoot(p.Options, p.WorkDir)

		// C1b: a changed prior-changed-file record means a Splice-permitted
		// mutation (writer or test generator output applied) since the last
		// invocation. Bump the freshness cache's worktree generation so every
		// memoized batch set is re-proven by an exact diff. The signature is
		// computed from the SAME record the stage input carries, so this
		// costs a sort and a join, never a stat or a spawn.
		if p.Trace != nil {
			p.Trace.noteSpliceMutation(input.PriorChangedFiles)
		}

		// Track C: discovery planning over the cognition graph. The plan
		// resolves structural questions from exact anchors (freshness
		// validated) or, when no structural keys derive, from semantic entry
		// nodes plus one bounded hop. Resolved questions deliver their nodes
		// through the same MemoryBundle channel; the broad search below is
		// suppressed only when the plan actually resolved something, so the
		// graph cognition does not stack on top of full FTS redelivery.
		plan, planNodes := planStageDiscovery(ctx, p, input, root)
		if p.Trace != nil {
			p.Trace.recordDiscoveryPlan(input.StageName, p.Iteration, plan)
		}
		if plan.AnchorsFailed > 0 {
			emitProgress(p.Options, fmt.Sprintf("[%s] discovery: %d anchor(s) failed freshness validation\n",
				input.StageName, plan.AnchorsFailed))
		}
		if len(planNodes) > 0 {
			graphObs := cognitionBundleFromNodes(planNodes)
			if mode, modeErr := resolveExemplarMode(); modeErr != nil {
				return schemas.HarnessStageInput{}, modeErr
			} else if mode.deliverToModel() {
				if input.MemoryBundle == nil {
					input.MemoryBundle = &schemas.MemoryBundle{RequestingAgent: input.StageName}
				}
				input.MemoryBundle.Observations = append(input.MemoryBundle.Observations, graphObs...)
			}
			emitProgress(p.Options, fmt.Sprintf("[%s] discovery: %d question(s) resolved by cognition, %d node(s)\n",
				input.StageName, len(plan.ResolvedByCognition), len(planNodes)))
		}
		graphResolved := len(plan.ResolvedByCognition)

		// C0: direct cognition fast path (retrieval only, never control flow).
		// retrieve-no-prompt keeps the retrieval telemetry honest but strips
		// the delivery: the miss path below runs and records what it found,
		// while the bundle never reaches the stage input.
		direct, directOK := p.tryDirectCognition(ctx, input, root)
		if graphResolved == 0 && directOK {
			if mode, modeErr := resolveExemplarMode(); modeErr != nil {
				return schemas.HarnessStageInput{}, modeErr
			} else if !mode.deliverToModel() {
				if p.Trace != nil {
					p.Trace.recordMemoryLookup(input.StageName, p.Iteration, "direct", direct.fresh, direct.stale)
					p.Trace.recordMemory(input.StageName, p.Iteration, direct.bundle)
				}
			} else {
				// Run-local replay guard: a direct hit the stage already
				// consumed this run is NOT a retrieval miss. The hit still
				// counts for telemetry (fresh/stale/direct above), the bundle
				// empties, and because the direct path returned true the
				// broad search below is skipped — suppression must not push
				// the same cognition back through FTS redelivery.
				suppressed := p.Trace.filterAlreadyDelivered(input.StageName, &direct.bundle)
				if suppressed > 0 {
					emitProgress(p.Options, fmt.Sprintf("[%s] cognition: %d already-consumed item(s) suppressed on re-entry\n", input.StageName, suppressed))
				}
				if direct.bundle.Observations == nil && len(direct.bundle.Observations) == 0 {
					direct.bundle.Observations = []schemas.MemoryObservation{}
				}
				input.MemoryBundle = &direct.bundle
				if p.Trace != nil {
					p.Trace.recordMemoryLookup(input.StageName, p.Iteration, "direct", direct.fresh, direct.stale)
				}
			}
		} else if graphResolved == 0 {
			// C1c miss path: rerank candidates deterministically when the
			// store exposes FTS ranks, then admit under the token budget.
			// A store without the capability (or a ranked-search error,
			// including an old sidecar) falls back to plain Search ordering
			// byte-identically; Admit is order-agnostic. When the discovery
			// plan already resolved a question from the graph, the broad
			// search is SKIPPED: cognition answered it, re-searching would
			// duplicate the discovery the graph just eliminated.
			bundle, missDetail, mErr := p.rerankedMissPath(ctx, input, root)
			if p.Trace != nil {
				p.Trace.recordMissPathDetail(input.StageName, p.Iteration, missDetail)
			}
			if mErr != nil {
				emitProgress(p.Options, fmt.Sprintf("[%s] memory retrieval skipped: %v\n", input.StageName, mErr))
				// A mid-run retrieval failure degrades the run's memory status to
				// unavailable (a warm run that failed must not record as cold).
				if p.Trace != nil {
					p.Trace.noteMemorySearchFailed()
				}
			} else {
				bundle.RequestingAgent = input.StageName
				// C1c D4: the ablation mode governs which memory classes the
				// miss path delivers. The default (both) reproduces today's
				// behavior; other modes exist for the benchmark only.
				mode, modeErr := resolveExemplarMode()
				if modeErr != nil {
					return schemas.HarnessStageInput{}, modeErr
				}
				if mode.deliverExemplars() {
					// PC3: append kept-run exemplars. Best-effort and silent on
					// failure; an empty exemplar set is correct, not a bug.
					if querier, ok := p.Memory.(learn.TraceQuerier); ok && querier != nil {
						if exemplars, eErr := retrieveExemplars(ctx, querier, root, input.RequestIntent); eErr == nil {
							bundle.Exemplars = exemplars
							if p.Trace != nil && len(exemplars) > 0 {
								key := stageKey{input.StageName, p.Iteration}
								meta := p.Trace.stages[key]
								meta.ExemplarsRetrieved = len(exemplars)
								p.Trace.stages[key] = meta
							}
							if len(exemplars) > 0 {
								emitProgress(p.Options, fmt.Sprintf("exemplars: %d from kept runs\n", len(exemplars)))
							}
						}
					}
				}
				if !mode.deliverObservations() {
					bundle.Observations = nil
				}
				admitted := memoryreason.Admit(&bundle, root, p.NowUnix)
				if !mode.deliverToModel() {
					// retrieve-no-prompt: record what admission WOULD have
					// delivered, then deliver nothing. The trace keeps the
					// retrieval truth; the stage input stays cognition-free.
					if p.Trace != nil && admitted.Bundle != nil {
						p.Trace.recordMemory(input.StageName, p.Iteration, *admitted.Bundle)
					}
					bundle.Observations = nil
					bundle.Exemplars = nil
					admitted.Bundle = &bundle
				}
				// Run-local replay guard: items this stage already consumed
				// earlier in the run are suppressed from delivery. Retrieval
				// and admission ran for real (counts and miss-path telemetry
				// stay honest); only the prompt replay is removed. Genuinely
				// new cognition stays fully eligible (consumed-set semantics,
				// not a memory-off switch).
				p.Trace.filterAlreadyDelivered(input.StageName, admitted.Bundle)
				input.MemoryBundle = admitted.Bundle
				emitProgress(p.Options, admissionProgressLine(input.StageName, admitted))
			}
			if p.Trace != nil {
				p.Trace.recordMemoryLookup(input.StageName, p.Iteration, "search", 0, 0)
			}
		}
	}

	compactedInput, _, cerr := compactStageInput(input.StageName, p.Budget, p.Tier, input, func(msg string) {
		emitProgress(p.Options, msg+"\n")
	})
	if cerr != nil {
		return input, fmt.Errorf("stage %s: %w", input.StageName, cerr)
	}
	input = compactedInput

	// Post-compaction trace accounting: counts describe delivered memory.
	if p.Trace != nil && input.MemoryBundle != nil {
		p.Trace.recordMemory(input.StageName, p.Iteration, *input.MemoryBundle)
	}
	// Mark consumption AFTER compaction so only model-visible items enter the
	// run-local consumed set: an item compaction dropped never reached the
	// model and stays eligible for a later legitimate delivery. The
	// retrieve-no-prompt mode never reaches this marking (its bundle is
	// emptied above before the post-compaction accounting sees items), which
	// keeps "consumed" meaning model-visible cognition, not retrieval activity.
	if p.Trace != nil {
		p.Trace.markDelivered(input.StageName, input.MemoryBundle)
	}
	return input, nil
}

// directCognitionResult is the outcome of one direct fast-path attempt.
// used is true only when a fresh, admitted bundle was produced; then Search
// is skipped. fresh and stale count the direct observations classified
// before admission (direct_candidates = fresh + stale).
type directCognitionResult struct {
	bundle schemas.MemoryBundle
	used   bool
	fresh  int
	stale  int
}

// acceptanceFactCommands extracts the structured verifier commands from the
// acceptance facts, for strict package-target key derivation.
func acceptanceFactCommands(facts []schemas.AcceptanceFact) []string {
	commands := make([]string, 0, len(facts))
	for _, fact := range facts {
		if fact.VerificationCommand != nil && strings.TrimSpace(*fact.VerificationCommand) != "" {
			commands = append(commands, *fact.VerificationCommand)
		}
	}
	return commands
}

// tryDirectCognition attempts the C0 fast path for one stage invocation:
//
//  1. derive structural cognition keys from the stage context;
//  2. if any keys exist and the store supports direct lookup, query by each
//     key in deterministic order;
//  3. classify each returned observation's freshness against its key's
//     anchor through the run's batched, memoized freshness cache (C1b): the
//     cache groups candidates by source commit and issues ONE porcelain
//     changed-path diff per unique (commit, generation), then classifies
//     in-process; only fresh observations may proceed;
//  4. admit the fresh candidates (the SAME memoryreason.Admit as the search
//     path); if anything is admitted, use it and skip the broad search.
//
// Every other outcome returns used=false, so the caller runs the existing
// Search path byte-identically. The only progress emission on a hit is a
// single direct-hit line naming the stage and item count; observation content
// never reaches progress or traces.
func (p stageInputPreparation) tryDirectCognition(ctx context.Context, input schemas.HarnessStageInput, root string) (directCognitionResult, bool) {
	keys := cognition.DeriveKeys(cognition.DeriveInput{
		RequestIntent:        input.RequestIntent,
		PriorChangedFiles:    input.PriorChangedFiles,
		VerificationCommands: acceptanceFactCommands(input.AcceptanceFacts),
	})
	if len(keys) == 0 {
		return directCognitionResult{}, false
	}
	store, ok := p.Memory.(TopicLookupStore)
	if !ok || store == nil {
		return directCognitionResult{}, false
	}
	var cache *cognition.FreshnessCache
	if p.Trace != nil {
		cache = p.Trace.freshnessCache()
	}

	for _, key := range keys {
		candidates, err := store.LookupTopic(ctx, schemas.MemoryTopicQuery{
			ProjectPath:     root,
			RequestingAgent: input.StageName,
			Scope:           "project",
			TopicKey:        key,
			Limit:           8,
		})
		if err != nil || len(candidates.Observations) == 0 {
			continue
		}
		anchor := cognition.AnchorPathForKey(key)
		if anchor == "" {
			// No resolvable anchor: freshness cannot be proved, fail closed.
			continue
		}
		var fresh, stale []schemas.MemoryObservation
		for _, obs := range candidates.Observations {
			commit := ""
			if obs.SourceCommit != nil {
				commit = *obs.SourceCommit
			}
			// The run's batched, memoized cache classifies freshness with
			// one porcelain diff per unique (commit, generation). Callers
			// without a run trace (nil accumulator) keep the C0 per-key
			// direct path byte-identically.
			var state cognition.FreshnessState
			if cache != nil {
				state = cache.Classify(ctx, root, commit, anchor)
			} else {
				state = cognition.ClassifyFreshness(ctx, root, commit, anchor)
			}
			switch state {
			case cognition.FreshnessFresh:
				fresh = append(fresh, obs)
			case cognition.FreshnessStale:
				stale = append(stale, obs)
			default:
				// Unknown freshness fails closed: the observation is not
				// injected directly.
			}
		}
		if len(fresh) == 0 {
			continue
		}
		bundle := schemas.MemoryBundle{
			RequestingAgent: input.StageName,
			Observations:    fresh,
		}
		admitted := memoryreason.Admit(&bundle, root, p.NowUnix)
		if admitted.Bundle == nil || len(admitted.Bundle.Observations) == 0 {
			continue
		}
		emitProgress(p.Options, fmt.Sprintf("[%s] cognition: direct hit, %d item(s)\n", input.StageName, len(admitted.Bundle.Observations)))
		return directCognitionResult{
			bundle: *admitted.Bundle,
			used:   true,
			fresh:  len(fresh),
			stale:  len(stale),
		}, true
	}
	return directCognitionResult{}, false
}

// stageChangedFilesMap returns the changed-file record one stage output
// declares, keyed by stage name. It is the mutation-evidence form used to
// bump the freshness cache's worktree generation after a Splice-permitted
// mutation (repair re-entry).
func stageChangedFilesMap(output schemas.HarnessStageOutput) map[string][]string {
	if files := stageChangedFiles(output); len(files) > 0 {
		return map[string][]string{"applied": files}
	}
	return nil
}

// admissionProgressLine renders the aggregate admission note. Only counts are
// reported; excluded content and IDs never reach progress or traces.
func admissionProgressLine(stageName string, result memoryreason.AdmissionResult) string {
	supplied := 0
	if result.Bundle != nil {
		supplied = len(result.Bundle.Observations) + len(result.Bundle.Exemplars)
	}
	line := fmt.Sprintf("memory admission: %s supplied %d", stageName, supplied)
	c := result.Rejected
	var parts []string
	if c.Invalid > 0 {
		parts = append(parts, fmt.Sprintf("%d invalid", c.Invalid))
	}
	if c.ReviewDue > 0 {
		parts = append(parts, fmt.Sprintf("%d review-due", c.ReviewDue))
	}
	if c.WrongProject > 0 {
		parts = append(parts, fmt.Sprintf("%d wrong-project", c.WrongProject))
	}
	if c.Duplicate > 0 {
		parts = append(parts, fmt.Sprintf("%d duplicate", c.Duplicate))
	}
	if c.OverLimit > 0 {
		parts = append(parts, fmt.Sprintf("%d over-limit", c.OverLimit))
	}
	if len(parts) > 0 {
		line += "; excluded " + strings.Join(parts, ", ")
	}
	return line + "\n"
}
