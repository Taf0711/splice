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
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"

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

// armTreatmentFor returns the arm table's explicit treatment name. The table
// already carries a resolvable name (cold or full); recomputing it from the
// arm name produced the unresolvable memory_off/memory_on labels. The arm name
// is the fallback only when a future arm leaves the treatment empty.
func armTreatmentFor(arm campaignArm) string {
	if arm.treatment != "" {
		return arm.treatment
	}
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
	// Needs derived from the TARGET task only (the task text, the current
	// source index, no prior files, no failure evidence: a true cold Task B
	// has neither yet), matched to the frozen bundle records through the
	// SAME predicate the pre-B match-matrix gate uses.
	matrix := buildMatchMatrix(targetIntent, manualDir, bundle.Nodes)

	// Parse the records through the restart boundary (node metadata JSON,
	// never process memory) and keep only records matched to a need.
	matched := 0
	for i, node := range bundle.Nodes {
		if i >= len(matrix.Records) || len(matrix.Records[i].MatchedNeedIDs) == 0 {
			continue // a hint or an unmatched record, never a substitution
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

// ---------------------------------------------------------------------------
// ADDENDUM 3 (option 1): the pre-B match-matrix gate.
//
// The staged mechanism observation is only justified when Task A's frozen
// capture actually speaks to a need Task B can derive. The TTL pair failed
// this gate: A captured nothing that matched any B need, so the arms measured
// protocol, not memory. This gate derives the target task's needs against the
// frozen A tree and tests EVERY frozen bundle record against every
// non-open-discovery need with the SAME RecordSpeaksOfSubject predicate the
// manual arm's seedManualArm uses. It runs after Task A verifies and before
// any Task B provider request, records needs x records with reasons, and
// fails loud on an empty matrix.

// matchMatrixNeed is one non-open-discovery need derived from the target task.
type matchMatrixNeed struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Subject  string `json:"subject"`
	Origin   string `json:"origin"`
	Required bool   `json:"required"`
}

// matchMatrixRef is one supporting source reference of a bundle record.
type matchMatrixRef struct {
	Path   string `json:"path"`
	Symbol string `json:"symbol,omitempty"`
}

// matchMatrixRecord is one frozen bundle record and how it matched.
type matchMatrixRecord struct {
	ClaimHash      string           `json:"claim_hash"`
	Supporting     []matchMatrixRef `json:"supporting,omitempty"`
	MatchedNeedIDs []string         `json:"matched_need_ids,omitempty"`
	Reason         string           `json:"reason,omitempty"`
}

// matchMatrix is the recorded needs x records match result. An empty matrix
// (no non-open-discovery need matched any record) stops the run.
type matchMatrix struct {
	Family             string              `json:"family"`
	SnapshotID         string              `json:"snapshot_id"`
	TaskIntent         string              `json:"task_intent"`
	Needs              []matchMatrixNeed   `json:"needs"`
	Records            []matchMatrixRecord `json:"records"`
	MatchedRecords     int                 `json:"matched_records"`
	OpenDiscoveryNeeds int                 `json:"open_discovery_needs"`
}

// buildMatchMatrix derives the target task's needs against the frozen A tree
// and tests EVERY frozen bundle record against every non-open-discovery need
// with the same RecordSpeaksOfSubject predicate seedManualArm uses. Records
// that are not typed reuse records are reported as hints, never matches.
func buildMatchMatrix(targetIntent, workspace string, nodes []memd.ExportedCaptureNode) matchMatrix {
	needs := splice.DeriveContextNeeds(targetIntent, workspace, nil, nil)
	matrix := matchMatrix{TaskIntent: targetIntent}
	nonOpen := make([]splice.ContextNeed, 0, len(needs))
	for _, need := range needs {
		if need.Kind == splice.NeedOpenDiscovery {
			matrix.OpenDiscoveryNeeds++
			continue
		}
		nonOpen = append(nonOpen, need)
		matrix.Needs = append(matrix.Needs, matchMatrixNeed{
			ID: need.ID, Kind: need.Kind, Subject: need.Subject,
			Origin: need.Origin, Required: need.Required,
		})
	}
	for _, node := range nodes {
		row := matchMatrixRecord{ClaimHash: node.ClaimHash}
		record := splice.ParseReuseRecordJSON(derefString(node.Node.MetadataJSON))
		if record == nil {
			row.Reason = "not a typed reuse record; node stays a hint"
			matrix.Records = append(matrix.Records, row)
			continue
		}
		for _, ref := range record.Supporting {
			row.Supporting = append(row.Supporting, matchMatrixRef{Path: ref.Path, Symbol: ref.Symbol})
		}
		for _, need := range nonOpen {
			if splice.RecordSpeaksOfSubject(record, need.Subject) {
				row.MatchedNeedIDs = append(row.MatchedNeedIDs, need.ID)
			}
		}
		if len(row.MatchedNeedIDs) == 0 {
			if len(nonOpen) == 0 {
				row.Reason = "no non-open-discovery need was derived"
			} else {
				row.Reason = "no derived need subject matched this record's supporting refs"
			}
		} else {
			matrix.MatchedRecords++
		}
		matrix.Records = append(matrix.Records, row)
	}
	return matrix
}

// exportMatchMatrix writes the matrix JSON into dir.
func exportMatchMatrix(dir string, matrix matchMatrix) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create match-matrix dir: %w", err)
	}
	data, err := json.MarshalIndent(matrix, "", "  ")
	if err != nil {
		return fmt.Errorf("encode match matrix: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "match-matrix.json"), append(data, '\n'), 0o644)
}

// printMatchMatrix renders the needs x records matrix compactly for stderr.
func printMatchMatrix(w io.Writer, matrix matchMatrix) {
	fmt.Fprintf(w, "match matrix for %s (snapshot %s): %d non-open needs, %d open-discovery, %d records, %d matched\n",
		matrix.Family, matrix.SnapshotID, len(matrix.Needs), matrix.OpenDiscoveryNeeds, len(matrix.Records), matrix.MatchedRecords)
	for _, need := range matrix.Needs {
		fmt.Fprintf(w, "  need %s kind=%s subject=%q origin=%s required=%t\n", need.ID, need.Kind, need.Subject, need.Origin, need.Required)
	}
	for _, row := range matrix.Records {
		status := "UNMATCHED"
		if len(row.MatchedNeedIDs) > 0 {
			status = "MATCHED"
		}
		refs := make([]string, 0, len(row.Supporting))
		for _, ref := range row.Supporting {
			if ref.Symbol != "" {
				refs = append(refs, ref.Path+"#"+ref.Symbol)
			} else {
				refs = append(refs, ref.Path)
			}
		}
		fmt.Fprintf(w, "  record %s [%s] refs=%v needs=%v reason=%q\n", row.ClaimHash, status, refs, row.MatchedNeedIDs, row.Reason)
	}
}

// runMatchMatrixGate computes the needs x records matrix over the frozen A
// tree and bundle, records it, and fails loud when no non-open-discovery need
// matches any record. It runs after Task A verifies and before any Task B
// provider request, so a mis-diagnosed pair cannot spend on B.
func runMatchMatrixGate(w io.Writer, options mvpEvalOptions, family mvpFamilyEntry, snapshotID, frozenTree string, nodes []memd.ExportedCaptureNode) (matchMatrix, error) {
	matrix := buildMatchMatrix(family.TargetTask, frozenTree, nodes)
	matrix.Family = family.ID
	matrix.SnapshotID = snapshotID
	if options.OutDir != "" {
		matrixDir := filepath.Join(options.OutDir, "snapshots", snapshotID)
		if err := exportMatchMatrix(matrixDir, matrix); err != nil {
			return matrix, fmt.Errorf("export match matrix: %w", err)
		}
	}
	if matrix.MatchedRecords == 0 {
		printMatchMatrix(w, matrix)
		return matrix, fmt.Errorf("match matrix gate: no non-open-discovery need matched any frozen capture record for family %s (needs=%d records=%d)", family.ID, len(matrix.Needs), len(matrix.Records))
	}
	return matrix, nil
}
