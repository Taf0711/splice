package cli

// Section-11 three-condition LIVE campaign support for the matched-snapshot
// runner (owner-approved brief: splice-docs/raw/sources/section11-campaign-
// brief.md). The legacy 2-arm behavior stays untouched behind the
// --conditions flag: cold,warm is the default, three-condition is opt-in.
//
// The three campaign conditions (handoff Section 11, warmcost E5 slice):
//
//   - manual: memory ON + records seeded from the FROZEN snapshot bundle
//     (A4 import path), selected by the E4 subject-matching over the needs
//     derived from the target task. Diagnostic only: flagged
//     diagnostic_only=true in every row and never counted toward the
//     automatic-cognition gate. It cannot use Task B's solution or hidden
//     probes: it sees only records matched to Task B's declared needs.
//   - automatic: memory ON + the existing automatic replay seeding
//     (retrieval + admission path, unchanged from the 2-arm warm arm).
//
// NO LIVE SPEND happens in this package's tests: offline validation uses a
// mocked provider at the eval seam.

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice"
)

// Campaign condition labels recorded on every row (row.Condition).
const (
	conditionCold         = "cold"
	conditionWarm         = "warm"
	conditionImprovedCold = "improved-cold"
	conditionManual       = "manual"
	conditionAutomatic    = "automatic"
)

// conditionsFlagColdWarm is the legacy 2-arm selection (the default, so old
// scripts are not silently changed). conditionsFlagThreeCondition selects
// the Section-11 campaign arms.
const (
	conditionsFlagColdWarm             = "cold,warm"
	conditionsFlagThreeCondition       = "three-condition"
	defaultMvpConditions               = conditionsFlagColdWarm
	defaultSchedulingSeed        int64 = 20260909 // Section-11 brief date; deterministic default
)

// campaignArm is one arm of the campaign table: its row/condition labels,
// memory flag, and whether it seeds snapshot cognition.
type campaignArm struct {
	// name is the row arm label (legacy 2-arm naming).
	name string
	// condition is the campaign condition label (row Condition).
	condition string
	// memory is the child --memory flag.
	memory string
	// treatment is the child treatment label (the 2-arm values).
	treatment string
	// seed is true when the arm's cognition is seeded from the frozen
	// snapshot bundle (manual and automatic only).
	seed bool
	// diagnosticOnly marks the manual arm (row DiagnosticOnly).
	diagnosticOnly bool
}

// realizationSignature is the behavior-distinguishing tuple of an arm:
// child memory flag, treatment label, whether it seeds retained evidence,
// and whether the arm is diagnostic-only. Condition names are labels and do
// not participate: two arms with the same signature are the same runtime
// treatment regardless of label.
func (a campaignArm) realizationSignature() string {
	return a.memory + "|" + a.treatment + "|" + fmt.Sprintf("%t", a.seed) + "|" + fmt.Sprintf("%t", a.diagnosticOnly)
}

// campaignArmsFor returns the ordered arm table for the requested
// conditions mode. The returned order is the CANONICAL table order; the
// per-experiment launch order is derived from the scheduling seed by
// shuffledArms.
func campaignArmsFor(conditions string) ([]campaignArm, error) {
	switch conditions {
	case "", conditionsFlagColdWarm:
		return []campaignArm{
			{name: "cold", condition: conditionCold, memory: "off", treatment: "cold"},
			{name: "warm", condition: conditionAutomatic, memory: "on", treatment: "full", seed: true},
		}, nil
	case conditionsFlagThreeCondition:
		// The old improved-cold label was runtime-identical to cold
		// (same memory flag, same treatment, same executable behavior).
		// It is removed rather than kept as a nominal fourth arm.
		return []campaignArm{
			{name: "cold", condition: conditionCold, memory: "off", treatment: "cold"},
			{name: "warm", condition: conditionAutomatic, memory: "on", treatment: "full", seed: true},
			{name: "manual", condition: conditionManual, memory: "on", treatment: "full", seed: true, diagnosticOnly: true},
		}, nil
	default:
		return nil, fmt.Errorf("--conditions %q: want %q or %q", conditions, conditionsFlagColdWarm, conditionsFlagThreeCondition)
	}
}

// shuffledArms derives the per-experiment launch order from the recorded
// scheduling seed (Section 11.2): math/rand with the recorded seed, arms
// still run sequentially. The derivation is deterministic in the seed, so
// the recorded seed plus the canonical table reproduces the run order.
func shuffledArms(arms []campaignArm, schedulingSeed int64) []campaignArm {
	order := make([]campaignArm, len(arms))
	copy(order, arms)
	rng := rand.New(rand.NewSource(schedulingSeed))
	rng.Shuffle(len(order), func(i, j int) {
		order[i], order[j] = order[j], order[i]
	})
	return order
}

// armOrderIndex returns the 1-based position of one arm in the derived
// launch order. It is recorded on every row.
func armOrderIndex(order []campaignArm, name string) int {
	for i, arm := range order {
		if arm.name == name {
			return i + 1
		}
	}
	return 0
}

// armTreatmentFor reuses the 2-arm treatment labeling (memory flag plus the
// ambient SPLICE_TREATMENT resolution) for the extended arm set.
func armTreatmentFor(arm campaignArm) string {
	return armTreatment(arm.name)
}

// seedManualArm seeds the manual arm's project with ONLY the records chosen
// by the E4 subject-matching (RecordSpeaksOfSubject) over the needs derived
// from the target task, from the FROZEN snapshot bundle's exported nodes
// (the A4 import payload). It cannot use Task B's solution or hidden
// probes: selection sees only the bundle and Task B's declared needs.
func seedManualArm(ctx context.Context, client *memd.Client, manualDir string, bundle snapshotBundle, targetIntent string) (int, error) {
	if client == nil {
		return 0, fmt.Errorf("seed manual arm: memory sidecar unavailable")
	}
	if len(bundle.Nodes) == 0 {
		return 0, fmt.Errorf("seed manual arm: snapshot bundle for run %s carries no captures", bundle.ProducerRunID)
	}
	// Needs derived from the TARGET task only: the task text, the current
	// source index, no prior files, no failure evidence (a true cold Task
	// B has neither yet).
	needs := splice.DeriveContextNeeds(targetIntent, manualDir, nil, nil)

	// Parse the records through the restart boundary (node metadata JSON,
	// never process memory) and keep only records matched to a need.
	matched := 0
	for _, node := range bundle.Nodes {
		record := splice.ParseReuseRecordJSON(derefString(node.Node.MetadataJSON))
		if record == nil {
			continue // a hint, never a substitution
		}
		matchedToNeed := false
		for _, need := range needs {
			if need.Kind == splice.NeedOpenDiscovery {
				continue
			}
			if splice.RecordSpeaksOfSubject(record, need.Subject) {
				matchedToNeed = true
				break
			}
		}
		if !matchedToNeed {
			continue
		}
		if _, err := client.ImportCaptureSet(ctx, manualDir, []memd.ExportedCaptureNode{node}); err != nil {
			return matched, fmt.Errorf("seed manual arm: import record %s: %w", node.ClaimHash, err)
		}
		matched++
	}
	if matched == 0 {
		// Fail loud: a manual arm with zero seeded records is a
		// mislabeled cold run, which corrupts the diagnostic reading.
		return 0, fmt.Errorf("seed manual arm: no bundle record matched any target need for run %s", bundle.ProducerRunID)
	}
	return matched, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// conditionCostReport builds the F2 WorkflowCostReport for one condition
// from the attempts the condition produced in one family. Verified
// completions are externally verified successes; every launched attempt's
// spend rides the report. A condition with no rows yields no report.
func conditionCostReport(rows []familyPairRow) (*splice.WorkflowCostReport, error) {
	views := make([]splice.SpendRecordView, 0, len(rows))
	verified := 0
	for _, row := range rows {
		if row.Executed {
			views = append(views, splice.SpendRecordView{
				InputTokens:  row.InputTokens,
				OutputTokens: row.OutputTokens,
			})
		}
		if row.Success {
			verified++
		}
	}
	if len(views) == 0 {
		return nil, nil
	}
	return splice.BuildAttemptSpendReport(views, verified)
}
