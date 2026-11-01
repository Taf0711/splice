package decisionreplay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// Disagreement classes. A class names the known cause of a disagreement
// between the recorded (HEAD) evidence and the swapped (pass) evidence.
const (
	// ClassPreexistingDirt: a HEAD file that was already in iteration 1's list
	// and that no pass builder list ever holds.
	ClassPreexistingDirt = "preexisting_dirt"
	// ClassStaleProgress: a HEAD file that the pass builder says did not change
	// in the pass of its History entry.
	ClassStaleProgress = "stale_progress"
	// ClassUnexplained: no known class fits.
	ClassUnexplained = "unexplained"
)

// Disagreement is one boundary where the two evidence sources give different
// answers. Kind is "rules" (the rules action) or "classifier" (the top label).
type Disagreement struct {
	Kind      string `json:"kind"`
	RunID     string `json:"run_id"`
	Iteration int    `json:"iteration"`
	Repair    int    `json:"repair"`
	Recorded  string `json:"recorded"`
	Swapped   string `json:"swapped"`
	Class     string `json:"class"`
}

// Comparison is the drift summary of one recorded run against one swap run.
type Comparison struct {
	// Boundaries counts the boundaries answered under both evidence sources.
	Boundaries int `json:"boundaries"`
	// Runs counts the distinct runs among those boundaries.
	Runs int `json:"runs"`
	// SelfCompared counts the boundaries that a second recorded replay answered
	// too. SelfAgreement is the share of them with the same classifier top label.
	// It measures the backend's own noise (S in the campaign gate).
	SelfCompared  int     `json:"self_compared"`
	SelfAgree     int     `json:"self_agree"`
	SelfAgreement float64 `json:"self_agreement"`
	// Records counts the input records. Every record is compared or skipped:
	// Records equals Boundaries plus Skipped.
	Records int `json:"records"`
	// Skipped counts the records that were not compared: the swap run did not
	// evaluate them, or a replay of the boundary failed.
	Skipped      int    `json:"skipped"`
	SkippedItems []Skip `json:"skipped_items,omitempty"`
	// DegradedShadows counts the skipped records with a degraded pass builder.
	DegradedShadows    int `json:"degraded_shadows"`
	RulesAgree         int `json:"rules_agree"`
	RulesDisagree      int `json:"rules_disagree"`
	ClassifierAgree    int `json:"classifier_agree"`
	ClassifierDisagree int `json:"classifier_disagree"`
	// Classes counts the disagreements of both kinds by class.
	Classes       map[string]int `json:"classes"`
	Disagreements []Disagreement `json:"disagreements,omitempty"`
	// Traces counts the pass-builder records in the run traces, every pass
	// and not only the passes at a boundary. It is nil when no trace was read.
	Traces *TraceEvidence `json:"traces,omitempty"`
}

// TraceEvidence counts the pass-builder records of run traces
// (RunOutcome.PassEvidence). Degraded lists each pass whose builder degraded.
// NoPasses lists the runs whose trace has no pass-builder record, so the
// table can warn that those runs give no pass evidence.
type TraceEvidence struct {
	Runs     int      `json:"runs"`
	Passes   int      `json:"passes"`
	Degraded []Skip   `json:"degraded,omitempty"`
	NoPasses []string `json:"no_passes,omitempty"`
}

// LoadTraceEvidence reads run traces (RunOutcome JSON objects) from files and
// from the direct .json and .jsonl children of directories. A file can hold
// one object or one object per line. A JSON array, or a record with no run_id
// or schema_version, is an error that names the file and the record.
func LoadTraceEvidence(paths []string) (TraceEvidence, error) {
	var ev TraceEvidence
	files, err := inputFiles(paths, ".json", ".jsonl")
	if err != nil {
		return ev, fmt.Errorf("traces: %w", err)
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return ev, fmt.Errorf("open trace %q: %w", path, err)
		}
		if bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
			return ev, fmt.Errorf("trace %q: the file holds a JSON array; want one RunOutcome object, or one object per line", path)
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		for record := 1; ; record++ {
			var trace schemas.RunOutcome
			if err := dec.Decode(&trace); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return ev, fmt.Errorf("trace %q record %d: %w", path, record, err)
			}
			if trace.RunID == "" {
				return ev, fmt.Errorf("trace %q record %d: no run_id", path, record)
			}
			if trace.SchemaVersion == "" {
				return ev, fmt.Errorf("trace %q record %d (run %s): no schema_version", path, record, trace.RunID)
			}
			ev.Runs++
			passes := 0
			for _, pass := range trace.PassEvidence {
				if pass.Builder != "pass" {
					continue
				}
				passes++
				if pass.DegradedReason != "" {
					ev.Degraded = append(ev.Degraded, Skip{RunID: trace.RunID, Iteration: pass.Iteration, Reason: pass.DegradedReason})
				}
			}
			ev.Passes += passes
			if passes == 0 {
				ev.NoPasses = append(ev.NoPasses, trace.RunID)
			}
		}
	}
	return ev, nil
}

// Compare replays the records twice, once with the recorded evidence and once
// with the swapped evidence, and compares each boundary's rules action and
// classifier top label. Each disagreement is explained by a known class.
func Compare(ctx context.Context, records []schemas.DecisionDispositionRecord, evaluator zeroruntime.DecisionEvaluator, options Options) (Comparison, error) {
	comparison := Comparison{Classes: map[string]int{}, Records: len(records)}
	recorded := options
	recorded.Evidence = EvidenceRecorded
	recordedRows, _, err := ReplayRows(ctx, records, evaluator, recorded)
	if err != nil {
		return comparison, fmt.Errorf("recorded evidence: %w", err)
	}
	repeatRows, _, err := ReplayRows(ctx, records, evaluator, recorded)
	if err != nil {
		return comparison, fmt.Errorf("recorded evidence, repeat: %w", err)
	}
	repeated := map[boundaryID]Row{}
	for _, row := range repeatRows {
		repeated[idOf(row)] = row
	}
	for _, rec := range recordedRows {
		other, ok := repeated[idOf(rec)]
		if !ok || rec.Error != "" || other.Error != "" || rec.TopLabel == "" || other.TopLabel == "" {
			continue
		}
		comparison.SelfCompared++
		if rec.TopLabel == other.TopLabel {
			comparison.SelfAgree++
		}
	}
	if comparison.SelfCompared > 0 {
		comparison.SelfAgreement = float64(comparison.SelfAgree) / float64(comparison.SelfCompared)
	}
	swap := options
	swap.Evidence = EvidenceSwap
	swapRows, swapSummary, err := ReplayRows(ctx, records, evaluator, swap)
	if err != nil {
		return comparison, fmt.Errorf("swap evidence: %w", err)
	}
	comparison.Skipped = swapSummary.Skipped
	comparison.SkippedItems = swapSummary.SkippedItems
	comparison.DegradedShadows = swapSummary.DegradedShadows
	runs := map[string]bool{}

	// The swap run's skips are a superset of the recorded run's skips, so the
	// recorded run has no extra exclusions to report.
	boundaries := boundaryRecords(records)
	swapped := map[boundaryID]Row{}
	for _, row := range swapRows {
		swapped[idOf(row)] = row
	}
	for _, rec := range recordedRows {
		other, ok := swapped[idOf(rec)]
		if !ok {
			// The swap run lists this boundary in its skips.
			continue
		}
		if rec.Error != "" || other.Error != "" {
			reason := "replay failed: recorded: " + rec.Error
			if rec.Error == "" {
				reason = "replay failed: swap: " + other.Error
			}
			comparison.Skipped++
			comparison.SkippedItems = append(comparison.SkippedItems, Skip{RunID: rec.RunID, Iteration: rec.Iteration, Repair: rec.Repair, Reason: reason})
			continue
		}
		comparison.Boundaries++
		runs[rec.RunID] = true
		record, hasRecord := boundaries[idOf(rec)]
		var history []schemas.IterationState
		var shadows []schemas.PassEvidence
		if hasRecord && record.Snapshot.Trajectory != nil {
			history = record.Snapshot.Trajectory.Boundary.History
			shadows = record.EvidenceShadow
		}
		class := ""
		if rec.RulesAction != "" && other.RulesAction != "" {
			if rec.RulesAction == other.RulesAction {
				comparison.RulesAgree++
			} else {
				comparison.RulesDisagree++
				class = explainDisagreement(history, shadows)
				comparison.addDisagreement(Disagreement{Kind: "rules", RunID: rec.RunID, Iteration: rec.Iteration, Repair: rec.Repair,
					Recorded: string(rec.RulesAction), Swapped: string(other.RulesAction), Class: class})
			}
		}
		if rec.TopLabel != "" && other.TopLabel != "" {
			if rec.TopLabel == other.TopLabel {
				comparison.ClassifierAgree++
			} else {
				comparison.ClassifierDisagree++
				class = explainDisagreement(history, shadows)
				comparison.addDisagreement(Disagreement{Kind: "classifier", RunID: rec.RunID, Iteration: rec.Iteration, Repair: rec.Repair,
					Recorded: string(rec.TopLabel), Swapped: string(other.TopLabel), Class: class})
			}
		}
	}
	comparison.Runs = len(runs)
	if comparison.Boundaries+comparison.Skipped != comparison.Records {
		return comparison, fmt.Errorf("compare accounted for %d of %d records (%d boundaries, %d skipped)", comparison.Boundaries+comparison.Skipped, comparison.Records, comparison.Boundaries, comparison.Skipped)
	}
	sort.SliceStable(comparison.Disagreements, func(i, j int) bool {
		a, b := comparison.Disagreements[i], comparison.Disagreements[j]
		if a.RunID != b.RunID {
			return a.RunID < b.RunID
		}
		if a.Iteration != b.Iteration {
			return a.Iteration < b.Iteration
		}
		if a.Repair != b.Repair {
			return a.Repair < b.Repair
		}
		return a.Kind < b.Kind
	})
	return comparison, nil
}

func (c *Comparison) addDisagreement(d Disagreement) {
	c.Disagreements = append(c.Disagreements, d)
	c.Classes[d.Class]++
}

type boundaryID struct {
	runID     string
	iteration int
	repair    int
}

func idOf(row Row) boundaryID {
	return boundaryID{row.RunID, row.Iteration, row.Repair}
}

// boundaryRecords maps each boundary to its first eligible record, as Replay
// keeps it.
func boundaryRecords(records []schemas.DecisionDispositionRecord) map[boundaryID]schemas.DecisionDispositionRecord {
	out := map[boundaryID]schemas.DecisionDispositionRecord{}
	for _, record := range records {
		snap := record.Snapshot
		if !eligible(snap) {
			continue
		}
		id := boundaryID{snap.RunID, snap.Iteration, snap.StageInvocation}
		if _, ok := out[id]; !ok {
			out[id] = record
		}
	}
	return out
}

// explainDisagreement names the class of one disagreement. Only the History
// entries where the two sources differ on workspace progress are considered.
// Within those entries, a recorded PassChanged means the two content measures
// disagree, which is unexplained. A swap that shows progress where the record
// shows none is unexplained. Otherwise the preexisting_dirt and stale_progress
// checks read only the entry's own files. preexisting_dirt takes precedence.
func explainDisagreement(history []schemas.IterationState, shadows []schemas.PassEvidence) string {
	if len(history) == 0 || len(shadows) != len(history) {
		return ClassUnexplained
	}
	passAll := map[string]bool{}
	for _, shadow := range shadows {
		if shadow.Builder != "pass" {
			return ClassUnexplained
		}
		for _, path := range shadow.FilesChanged {
			passAll[path] = true
		}
	}
	firstHead := map[string]bool{}
	for _, state := range history {
		if state.Iteration == 1 {
			for _, path := range state.FilesChanged {
				firstHead[path] = true
			}
		}
	}
	stale := false
	for k, rec := range history {
		shadow := shadows[k]
		swapped := rec
		swapped.FilesChanged = shadow.FilesChanged
		swapped.PassChanged = shadow.PassChanged
		recProgress := splice.WorkspaceProgress(rec)
		swapProgress := splice.WorkspaceProgress(swapped)
		if recProgress == swapProgress {
			continue
		}
		if swapProgress {
			return ClassUnexplained
		}
		if rec.PassChanged != nil {
			return ClassUnexplained
		}
		inPass := map[string]bool{}
		for _, path := range shadow.FilesChanged {
			inPass[path] = true
		}
		for _, path := range rec.FilesChanged {
			if firstHead[path] && !passAll[path] {
				return ClassPreexistingDirt
			}
		}
		for _, path := range rec.FilesChanged {
			if !inPass[path] {
				stale = true
			}
		}
	}
	if stale {
		return ClassStaleProgress
	}
	return ClassUnexplained
}

// WriteTable prints the comparison as a short human table.
func WriteTable(w io.Writer, c Comparison) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "records\t%d\n", c.Records)
	fmt.Fprintf(tw, "boundaries\t%d\n", c.Boundaries)
	fmt.Fprintf(tw, "runs\t%d\n", c.Runs)
	fmt.Fprintf(tw, "self agreement S\t%.3f\t(%d boundaries)\n", c.SelfAgreement, c.SelfCompared)
	fmt.Fprintf(tw, "skipped\t%d\n", c.Skipped)
	fmt.Fprintf(tw, "degraded shadows\t%d\t(boundary passes only)\n", c.DegradedShadows)
	if c.Traces != nil {
		fmt.Fprintf(tw, "degraded trace passes\t%d\t(%d passes in %d traces)\n", len(c.Traces.Degraded), c.Traces.Passes, c.Traces.Runs)
	} else {
		fmt.Fprintf(tw, "degraded trace passes\tnot read\t(records only; pass --traces to cover every pass)\n")
	}
	fmt.Fprintf(tw, "rules\tagree %d\tdisagree %d\n", c.RulesAgree, c.RulesDisagree)
	fmt.Fprintf(tw, "classifier\tagree %d\tdisagree %d\n", c.ClassifierAgree, c.ClassifierDisagree)
	for _, class := range []string{ClassPreexistingDirt, ClassStaleProgress, ClassUnexplained} {
		fmt.Fprintf(tw, "class %s\t%d\n", class, c.Classes[class])
	}
	if len(c.Disagreements) > 0 {
		fmt.Fprintln(tw, "\nRUN\tITER\tREP\tKIND\tRECORDED\tSWAPPED\tCLASS")
		for _, d := range c.Disagreements {
			fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%s\t%s\n", d.RunID, d.Iteration, d.Repair, d.Kind, d.Recorded, d.Swapped, d.Class)
		}
	}
	if c.Traces != nil {
		for _, run := range c.Traces.NoPasses {
			fmt.Fprintf(tw, "warning: trace %s has 0 passes\t(no pass evidence for this run)\n", run)
		}
	}
	if c.Traces != nil && len(c.Traces.Degraded) > 0 {
		fmt.Fprintln(tw, "\nDEGRADED RUN\tITER\tREASON")
		for _, d := range c.Traces.Degraded {
			fmt.Fprintf(tw, "%s\t%d\t%s\n", d.RunID, d.Iteration, d.Reason)
		}
	}
	if len(c.SkippedItems) > 0 {
		fmt.Fprintln(tw, "\nSKIPPED RUN\tITER\tREP\tREASON")
		for _, s := range c.SkippedItems {
			run := s.RunID
			if run == "" {
				run = "(unknown)"
			}
			fmt.Fprintf(tw, "%s\t%d\t%d\t%s\n", run, s.Iteration, s.Repair, s.Reason)
		}
	}
	return tw.Flush()
}
