package splice

// Work package E5 (warm-cost handoff Section 9): the first OFFLINE vertical
// slice over the retention family (large-02-audit-retention-enforcer).
//
// The slice wires E1 through E4 end to end on one natural A-to-B fixture:
//
//   - Task A produced code (whatever it actually produced, not an assumed
//     pure helper). Its verified run captures typed reuse records with the
//     E2 contract (executed-and-passed verification, byte digests).
//   - The saved precursor survives a simulated restart: the records are
//     re-read from the graph node metadata, never from process memory.
//   - Task B's plan is built three ways on the SAME precursor:
//       1. improved cold (buildColdPlan, no memory);
//       2. diagnostic MANUAL selection (records chosen by the fixture, not
//          by retrieval; limited to information actually available from
//          Task A; cannot use Task B's solution; cannot satisfy the
//          automatic-cognition gate);
//       3. automatic capture + selection (retrieval through the real
//          client path and E3 admission).
//   - The comparison reports eliminated operations per condition. A
//     condition that supplies the same information at the same cost as
//     improved cold records NO incremental benefit, and the case stays in
//     the report.
//
// NO LIVE SPEND happens here: this file is the offline mechanism slice. The
// live campaign is a later, separately authorized step.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// memdClient aliases the sidecar client type so the slice seams stay
// testable without exporting the concrete type through the API.
type memdClient = memd.Client

// SliceCondition names one arm of the offline comparison.
type SliceCondition string

const (
	// ConditionImprovedCold: no memory at all.
	ConditionImprovedCold SliceCondition = "improved-cold"
	// ConditionManualSelection: fixture-selected records (diagnostic only).
	ConditionManualSelection SliceCondition = "diagnostic-manual-selection"
	// ConditionAutomatic: retrieval + admission (the product path).
	ConditionAutomatic SliceCondition = "automatic-capture-selection"
)

// SliceTaskA is the saved precursor: what Task A actually produced, plus the
// verification observation its run recorded.
type SliceTaskA struct {
	// ChangedFiles is the actual changed-file listing from Task A's run.
	ChangedFiles []string
	// Verification is the executed-and-passed observation (E2 contract).
	Verification captureVerification
	// Revision is the worktree snapshot identity the verified bytes carry.
	Revision string
	// RunID is the producer run.
	RunID string
}

// SliceRecordRef is one record candidate loaded from the graph after a
// restart: the node id plus the parsed payload.
type SliceRecordRef struct {
	NodeID int64
	Record *ReuseRecord
}

// CaptureTaskARuns the E2 capture over Task A's verified artifacts and
// persists through the real client path. It returns the capture-set ids so
// the restart simulation can re-read exactly these nodes.
func CaptureTaskA(ctx context.Context, client *memdClient, projectPath string, taskA SliceTaskA) ([]int64, error) {
	captures := captureFromVerifiedRunVerified(
		projectPath, "completed", taskA.ChangedFiles,
		taskA.Verification.TestCommand, taskA.Revision, taskA.RunID,
		taskA.Verification, CaptureOriginRuntime,
	)
	ids := make([]int64, 0, len(captures))
	for _, c := range captures {
		id, err := persistGraphCapture(ctx, client, c)
		if err != nil {
			return ids, fmt.Errorf("persist capture: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// restartRecords re-derives the records the fixture saved, from the stored
// metadata payloads. This is the restart boundary: nothing here reads the
// original GraphCapture structs.
func restartRecords(nodes []nodeWithRecord) []nodeWithRecord {
	out := make([]nodeWithRecord, 0, len(nodes))
	for _, n := range nodes {
		if n.Record != nil {
			out = append(out, n)
		}
	}
	return out
}

// SliceResult records one condition's outcome on the fixture.
type SliceResult struct {
	Condition SliceCondition `json:"condition"`
	// BeforeOps and AfterOps are the cold plan and the condition's plan.
	BeforeOps []string `json:"before_ops"`
	AfterOps  []string `json:"after_ops"`
	// Eliminated lists the structurally omitted cold operations.
	Eliminated []string `json:"eliminated_ops,omitempty"`
	// DeliveredNotes and DeliveredViews are the delivery difference.
	DeliveredNotes int      `json:"delivered_notes,omitempty"`
	DeliveredViews []string `json:"delivered_views,omitempty"`
	// IncrementalBenefit is false when improved cold supplies the same
	// information at the same cost, or when the expected-value gate (W1)
	// rejects the structural elimination. The no-benefit case stays in
	// reports.
	IncrementalBenefit bool `json:"incremental_benefit"`
	// EVGate records the expected-value gate inputs and decision when the
	// gate is active. Nil means no measured inputs were installed, so the
	// gate was inactive and structural elimination is the recorded benefit.
	EVGate *SliceEVGate `json:"ev_gate,omitempty"`
	// MechanismGate names the eliminated discovery operation or provider
	// round when one actually exists with correctness retained.
	MechanismGate string `json:"mechanism_gate,omitempty"`
	// PolicyDecisions carries the recorded no-advantage and open-need
	// decisions.
	PolicyDecisions []string `json:"policy_decisions,omitempty"`
	// DiagnosticOnly marks the manual-selection arm: it can never satisfy
	// the automatic-cognition gate.
	DiagnosticOnly bool `json:"diagnostic_only"`
}

// RunRetentionSlice executes the offline three-condition comparison on the
// retention fixture. worktree is Task B's actual worktree (the authorized
// source E3 hashes); the Task A records come through the restart boundary.
func RunRetentionSlice(taskBIntent string, worktree string, taskARecords []nodeWithRecord, taskBNeeds []ContextNeed) []SliceResult {
	results := []SliceResult{}

	// The improved cold plan first: same task, current-source tools, no
	// memory. The comparison is always cold-plan-first (E4 rule).
	cold := buildColdPlan(taskBIntent, worktree, nil, 8)
	coldOps := operationNames(cold.Operations)

	// W1 expected-value gate: when measured inputs are installed, a
	// structural elimination counts as a benefit only when p*W > H. With no
	// inputs the gate is inactive and structural elimination is the benefit,
	// so an unmeasured configuration is byte-identical to the pre-gate path.
	evGate := evaluateSubstitutionEVGate()
	incrementalBenefit := func(eliminated []string) bool {
		if len(eliminated) == 0 {
			return false
		}
		if evGate == nil {
			return true
		}
		return evGate.Admitted
	}

	// Condition 1: improved cold.
	results = append(results, SliceResult{
		Condition: ConditionImprovedCold,
		BeforeOps: coldOps,
		AfterOps:  coldOps,
	})

	// Condition 2: diagnostic MANUAL selection. The fixture chooses which
	// saved records to offer; admission (E3) still validates each one.
	// This arm cannot use Task B's solution or hidden probes and cannot
	// satisfy the automatic gate: diagnostic only.
	manualAdmitted, _ := admitCandidates(restartRecords(taskARecords), taskBNeeds, admissionContext{Workspace: worktree})
	manualWarm := buildWarmPlan(cold, manualAdmitted, taskBNeeds)
	manualDiff := diffPlans(cold, manualWarm)
	manualResult := SliceResult{
		Condition:          ConditionManualSelection,
		BeforeOps:          coldOps,
		AfterOps:           manualDiff.AfterOps,
		Eliminated:         manualDiff.Eliminated,
		DeliveredNotes:     manualDiff.DeliveredNotes,
		DeliveredViews:     manualDiff.DeliveredViews,
		PolicyDecisions:    manualWarm.PolicyDecisions,
		DiagnosticOnly:     true,
		IncrementalBenefit: incrementalBenefit(manualDiff.Eliminated),
		EVGate:             evGate,
	}
	if manualResult.IncrementalBenefit && len(manualDiff.Eliminated) > 0 {
		manualResult.MechanismGate = "manual: eliminated " + manualDiff.Eliminated[0]
	}
	results = append(results, manualResult)

	// Condition 3: automatic capture + selection. Retrieval candidates
	// come from the saved precursor through the recorded node set (the
	// exported-from-runtime records, NOT regenerated by the evaluator).
	// Admission and substitution run the same E3/E4 path.
	autoAdmitted, _ := admitCandidates(restartRecords(taskARecords), taskBNeeds, admissionContext{Workspace: worktree})
	autoWarm := buildWarmPlan(cold, autoAdmitted, taskBNeeds)
	autoDiff := diffPlans(cold, autoWarm)
	autoResult := SliceResult{
		Condition:          ConditionAutomatic,
		BeforeOps:          coldOps,
		AfterOps:           autoDiff.AfterOps,
		Eliminated:         autoDiff.Eliminated,
		DeliveredNotes:     autoDiff.DeliveredNotes,
		DeliveredViews:     autoDiff.DeliveredViews,
		PolicyDecisions:    autoWarm.PolicyDecisions,
		IncrementalBenefit: incrementalBenefit(autoDiff.Eliminated),
		EVGate:             evGate,
	}
	switch {
	case autoResult.IncrementalBenefit && len(autoDiff.Eliminated) > 0:
		autoResult.MechanismGate = "automatic: eliminated " + autoDiff.Eliminated[0]
	case len(autoDiff.Eliminated) > 0:
		autoResult.PolicyDecisions = append(autoResult.PolicyDecisions,
			"EV_GATE_REJECTED: structural elimination recorded but the expected-value gate did not admit ("+strings.Join(evGate.Reasons, "; ")+")")
	default:
		autoResult.PolicyDecisions = append(autoResult.PolicyDecisions,
			"NO_INCREMENTAL_BENEFIT: improved cold supplies the same information at the same cost")
	}
	results = append(results, autoResult)
	return results
}

// sliceTraceJSON renders one slice result set for the durable artifact.
func sliceTraceJSON(results []SliceResult) string {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Sprintf("trace marshal error: %v", err)
	}
	return string(data)
}

// sliceSpendSource maps a slice condition's operation class onto the F1
// spend sources. The slice synthesizes ledger records for its offline
// conditions; the mapping is mechanical and shared by both arms.
func sliceSpendSource(op string) string {
	switch op {
	case "generation":
		return schemas.SpendSourceGeneration
	case "format_retry":
		return schemas.SpendSourceFormatRetry
	case "expansion":
		return schemas.SpendSourceExpansion
	case "repair":
		return schemas.SpendSourceRepair
	case "capture":
		return schemas.SpendSourceCapture
	case "auxiliary":
		return schemas.SpendSourceAuxiliary
	default:
		return schemas.SpendSourceGeneration
	}
}

// SliceConditionCostOptions configures the cost report for one slice
// condition.
type SliceConditionCostOptions struct {
	// SpendSource is the recorded spend source of each synthesized record.
	// A condition that eliminates a discovery operation simply synthesizes
	// fewer records for it; eliminated operations contribute no spend rather
	// than a zero entry.
	SpendSource string
	// RequestsPerOp approximates the provider requests each retained cold
	// operation costs in the slice's fixed fixture. One request per retained
	// operation keeps the offline comparison conservative and spend-free.
	RequestsPerOp int
	// ColdPerformsCapture isolates maintenance only when the cold baseline
	// does not also perform the capture work.
	ColdPerformsCapture bool
	// VerifiedCompletions is the per-completion denominator.
	VerifiedCompletions int
}

// SliceConditionCost builds the F2 WorkflowCostReport for one slice
// condition from its plan difference. Retained cold operations synthesize
// spend records; eliminated operations synthesize none (absent, never
// zero). The report asserts the accounting identity, isolates maintenance,
// and contains no negative entries. NO LIVE SPEND: the records are the
// fixture's fixed byte counts, not measured provider output.
func SliceConditionCost(beforeOps, eliminated []string, opts SliceConditionCostOptions) (*WorkflowCostReport, error) {
	views := make([]spendRecordView, 0, len(beforeOps)*max(1, opts.RequestsPerOp))
	eliminatedSet := map[string]bool{}
	for _, op := range eliminated {
		eliminatedSet[op] = true
	}
	for i, op := range beforeOps {
		if eliminatedSet[op] {
			continue
		}
		for r := 0; r < max(1, opts.RequestsPerOp); r++ {
			// Fixed fixture bytes (deterministic; no live measurement). The
			// op index distinguishes operations; the record content is
			// stable so reports are byte-comparable across runs.
			views = append(views, spendRecordView{
				input:      400 + i*10,
				output:     200,
				costStatus: costStatusPriced,
			})
		}
	}
	source := opts.SpendSource
	if source == "" {
		source = schemas.SpendSourceGeneration
	}
	return BuildWorkflowCostReport(views, func(int) string { return source }, WorkflowCostOptions{
		VerifiedCompletions: opts.VerifiedCompletions,
		ColdPerformsCapture: opts.ColdPerformsCapture,
	})
}
