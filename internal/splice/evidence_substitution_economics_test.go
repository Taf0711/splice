package splice

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// Economic instrument for the evidence-substitution policy.
//
// It measures, per arm, the quantities the cost objective is stated in:
//
//   - turns: provider calls the stage actually issued
//   - prompt chars: the exact content of every request that reached the provider
//   - operations: how many plan operations executed, and how many were
//     satisfied by evidence instead
//   - retries: provider retries the trace recorded
//   - validation cost: the reads and subprocesses spent VALIDATING the evidence,
//     which substitution incurs and a merely-eliminated operation does not
//
// It reports a verdict rather than asserting one. The gate outcome is the
// measurement. Correctness is asserted separately: both arms must complete.

// accountingProvider wraps the existing scripted provider and records the exact
// prompt volume of every request. It delegates the response so the instrument
// cannot drift from the production test's provider behaviour.
type accountingProvider struct {
	inner      *memoryScriptedProvider
	calls      int
	totalChars int
	perCall    []int
}

func (p *accountingProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.calls++
	chars := 0
	for _, m := range request.Messages {
		chars += len(m.Content)
	}
	p.totalChars += chars
	p.perCall = append(p.perCall, chars)
	return p.inner.StreamCompletion(ctx, request)
}

// tokensFromChars is a labelled ESTIMATE, not a provider measurement: the
// scripted provider reports no usage events, so the trace carries no token
// counts. Reported only to give a sense of scale.
func tokensFromChars(chars int) int { return chars / 4 }

type armEconomics struct {
	name       string
	turns      int
	promptChar int
	opsPlan    int
	opsExec    int
	opsByEvid  int
	opsReject  int
	evidReads  int
	evidProcs  int
	retries    int
	memChars   int
	ctxChars   int
	modelOps   int
	completed  bool
}

func reportArm(a armEconomics) {
	fmt.Printf("  %-6s turns=%d prompt_chars=%d (~%d tok est) plan_ops=%d executed=%d satisfied_by_evidence=%d retained_after_reject=%d evidence_validation_reads=%d evidence_validation_procs=%d provider_retries=%d model_chars=%d context_chars=%d model_tool_ops=%d completed=%v\n",
		a.name, a.turns, a.promptChar, tokensFromChars(a.promptChar), a.opsPlan, a.opsExec, a.opsByEvid,
		a.opsReject, a.evidReads, a.evidProcs, a.retries, a.memChars, a.ctxChars, a.modelOps, a.completed)
}

// TestEvidenceSubstitutionEconomics runs both arms through the production entry
// point and reports the cost comparison.
func TestEvidenceSubstitutionEconomics(t *testing.T) {
	workDir, rev, body := prodEvidenceRepo(t)
	mem := prodEvidenceStore(t, workDir, rev, body)

	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "Add RetentionDeficit in internal/audit/retention.go and reuse Apply.",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	options := PipelineConfigFromAgentOptions(agent.Options{})

	// Plan-level accounting: what the substitution changed structurally.
	_, planScope, _, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input: schemas.HarnessStageInput{
			RunID: "econ-plan", StageName: "code_writer", Sequence: 1,
			PlanTier: plan.Tier, RequestIntent: plan.RequestIntent,
		},
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true, PullContext: true}},
		Budget:    schemas.StageBudget{InputMax: 20000, OutputMax: 8192},
		Tier:      plan.Tier,
		Iteration: 1,
		WorkDir:   workDir,
		Options:   options,
		Memory:    mem,
	})
	if err != nil {
		t.Fatalf("plan preparation: %v", err)
	}
	if planScope.Evidence == nil {
		t.Fatal("no evidence plan built; instrument needs an admitted substitution")
	}

	coldOps := planScope.Evidence.Cold.Operations
	warmOps := planScope.Evidence.Warm.Operations
	eliminated := planScope.Evidence.Diff.Eliminated

	// --- cold arm: no retained evidence, improved cold plan only ---
	var coldProbe prodEvidenceProbe
	coldProv := &accountingProvider{inner: &memoryScriptedProvider{}}
	coldTr := newRunTraceAccumulator(nil, "econ-cold", "session", workDir, plan, "active", nil)
	_, _, coldCompleted, err := runPass(context.Background(), "econ-cold", 1, plan,
		stageRegistry{"code_writer": stages.CodeWriter{}}, coldProv, options, workDir,
		prodEvidenceRunner(t, &coldProbe), time.Time{}, nil, nil, coldTr, NewStageExecutionBudget(0))
	if err != nil {
		t.Fatalf("cold arm: %v", err)
	}
	if !coldCompleted {
		t.Fatal("cold arm did not complete")
	}

	// --- warm arm: admitted evidence drives the substituted request ---
	var warmProbe prodEvidenceProbe
	warmProv := &accountingProvider{inner: &memoryScriptedProvider{}}
	warmTr := newRunTraceAccumulator(nil, "econ-warm", "session", workDir, plan, "active", nil)
	_, _, warmCompleted, err := runPass(context.Background(), "econ-warm", 1, plan,
		stageRegistry{"code_writer": stages.CodeWriter{}}, warmProv, options, workDir,
		prodEvidenceRunner(t, &warmProbe), time.Time{}, nil, mem, warmTr, NewStageExecutionBudget(0))
	if err != nil {
		t.Fatalf("warm arm: %v", err)
	}
	if !warmCompleted {
		t.Fatal("warm arm did not complete")
	}

	coldMeta := coldTr.stages[stageKeyFor("code_writer", 1, 0)]
	warmMeta := warmTr.stages[stageKeyFor("code_writer", 1, 0)]

	cold := armEconomics{
		name: "cold", turns: coldProv.calls, promptChar: coldProv.totalChars,
		opsPlan: len(coldOps), opsExec: coldMeta.OperationsExecuted,
		opsByEvid: coldMeta.OperationsSatisfiedByEvidence, opsReject: coldMeta.OperationsRetainedAfterReject,
		evidReads: coldMeta.EvidenceValidationReads, evidProcs: coldMeta.EvidenceValidationSubprocesses,
		retries: coldMeta.ProviderRetries, memChars: coldMeta.MemoryChars, ctxChars: coldMeta.ContextChars,
		modelOps: len(coldProbe.tools), completed: coldCompleted,
	}
	warm := armEconomics{
		name: "warm", turns: warmProv.calls, promptChar: warmProv.totalChars,
		opsPlan: len(warmOps), opsExec: warmMeta.OperationsExecuted,
		opsByEvid: warmMeta.OperationsSatisfiedByEvidence, opsReject: warmMeta.OperationsRetainedAfterReject,
		evidReads: warmMeta.EvidenceValidationReads, evidProcs: warmMeta.EvidenceValidationSubprocesses,
		retries: warmMeta.ProviderRetries, memChars: warmMeta.MemoryChars, ctxChars: warmMeta.ContextChars,
		modelOps: len(warmProbe.tools), completed: warmCompleted,
	}

	fmt.Printf("\n\n## Evidence-substitution economics\n\n")
	reportArm(cold)
	reportArm(warm)
	fmt.Printf("\n  plan: cold_ops=%d warm_ops=%d eliminated=%v\n", len(coldOps), len(warmOps), eliminated)
	fmt.Printf("  cold_ops_detail=%v\n", opNames(coldOps))
	fmt.Printf("  warm_ops_detail=%v\n", opNames(warmOps))

	// --- gate ---
	type check struct {
		name string
		pass bool
		note string
	}
	checks := []check{
		{"correctness both arms complete", coldCompleted && warmCompleted, ""},
		{"turns not increased", warm.turns <= cold.turns, fmt.Sprintf("cold=%d warm=%d", cold.turns, warm.turns)},
		{"prompt chars reduced", warm.promptChar < cold.promptChar,
			fmt.Sprintf("cold=%d warm=%d delta=%+d", cold.promptChar, warm.promptChar, warm.promptChar-cold.promptChar)},
		// Plan operations are comparable across arms. Executed counts are NOT:
		// the cold arm has no evidence plan, so it records no operation
		// decisions at all and its executed count is absent, not zero.
		{"plan operations reduced", len(warmOps) < len(coldOps),
			fmt.Sprintf("cold=%d warm=%d", len(coldOps), len(warmOps))},
		{"model tool ops not increased", warm.modelOps <= cold.modelOps,
			fmt.Sprintf("cold=%d warm=%d", cold.modelOps, warm.modelOps)},
		{"retries not increased", warm.retries <= cold.retries,
			fmt.Sprintf("cold=%d warm=%d", cold.retries, warm.retries)},
		{"expensive op eliminated (read/list/search, not only symbol)", expensiveOpEliminated(eliminated),
			fmt.Sprintf("eliminated=%v", eliminated)},
	}

	failed := 0
	fmt.Printf("\n  GATE\n")
	for _, c := range checks {
		mark := "PASS"
		if !c.pass {
			mark = "FAIL"
			failed++
		}
		fmt.Printf("    [%s] %s%s\n", mark, c.name, noteSuffix(c.note))
	}
	if failed == 0 {
		fmt.Printf("\n  VERDICT: gate satisfied\n")
	} else {
		fmt.Printf("\n  VERDICT: gate NOT satisfied (%d/%d checks failed)\n", failed, len(checks))
	}
	fmt.Printf("\n")
}

func noteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return "  (" + note + ")"
}

func opNames(ops []ColdOperation) []string {
	out := make([]string, 0, len(ops))
	for _, o := range ops {
		out = append(out, o.Name)
	}
	return out
}

// expensiveOpEliminated reports whether any eliminated operation is a read,
// search, or listing. Eliminating only a symbol lookup does not satisfy the
// economic gate: it removes the cheapest operation class.
func expensiveOpEliminated(eliminated []string) bool {
	for _, op := range eliminated {
		switch {
		case strings.HasPrefix(op, "read:"), strings.HasPrefix(op, "search:"), strings.HasPrefix(op, "list:"):
			return true
		}
	}
	return false
}
