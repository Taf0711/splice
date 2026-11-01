// Package decisionreplay evaluates saved trajectory boundaries with another backend.
//
// Evidence selects the workspace values that each History entry carries.
// "recorded" replays the saved values. "swap" replays the values in each
// record's EvidenceShadow. A record without a usable shadow is skipped and
// listed. Every record is either replayed or listed as skipped with a reason,
// including records that are not trajectory boundaries. Compare runs both sources and explains each disagreement as
// preexisting_dirt, stale_progress, or unexplained.
//
// A projection failure record has no snapshot and so no run ID. Skip
// listings show it as "(unknown)".
package decisionreplay

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// Options selects the backend, pack, thresholds, worker count, and the
// evidence source. Evidence is "recorded" (the default: the History values as
// saved) or "swap" (each History entry takes its EvidenceShadow values).
type Options struct {
	BackendConfig config.DecisionAdvisorConfig
	Pack          string
	Thresholds    map[string]float64
	Concurrency   int
	Evidence      string
}

// Evidence source names for Options.Evidence.
const (
	EvidenceRecorded = "recorded"
	EvidenceSwap     = "swap"
)

// Skip names one record the replay did not evaluate, and why. RunID is empty
// for a projection failure, which carries no snapshot.
type Skip struct {
	RunID     string `json:"run_id"`
	Iteration int    `json:"iteration"`
	Repair    int    `json:"repair"`
	Reason    string `json:"reason"`
}

// Row reports one boundary and the active host policy decision.
type Row struct {
	RunID                     string                             `json:"run_id"`
	Iteration                 int                                `json:"iteration"`
	Repair                    int                                `json:"repair"`
	Pack                      string                             `json:"pack"`
	Backend                   string                             `json:"backend"`
	Evidence                  string                             `json:"evidence,omitempty"`
	RulesAction               schemas.TrajectoryAction           `json:"rules_action,omitempty"`
	ResolvedModel             string                             `json:"resolved_model"`
	Probabilities             map[splice.TrajectoryLabel]float64 `json:"probabilities"`
	TopLabel                  splice.TrajectoryLabel             `json:"top_label"`
	PolicyAction              schemas.TrajectoryAction           `json:"policy_action"`
	PolicyReason              string                             `json:"policy_reason"`
	InputTokens               int                                `json:"input_tokens"`
	OutputTokens              int                                `json:"output_tokens"`
	CostUSD                   *float64                           `json:"cost_usd"`
	FailureIsNew              bool                               `json:"failure_is_new"`
	BoundariesSinceNewFailure int                                `json:"boundaries_since_new_failure"`
	Error                     string                             `json:"error,omitempty"`
}

// Summary counts the boundaries, answers, errors, and policy actions.
type Summary struct {
	Boundaries    int                              `json:"boundaries"`
	Answered      int                              `json:"answered"`
	Errors        int                              `json:"errors"`
	PolicyActions map[schemas.TrajectoryAction]int `json:"policy_actions"`
	// Skipped counts the records not evaluated. SkippedItems lists them by run
	// and iteration. A swap replay never evaluates a record without evidence
	// shadow with its recorded values.
	Skipped      int    `json:"skipped"`
	SkippedItems []Skip `json:"skipped_items,omitempty"`
	// DegradedShadows counts the skipped records whose pass builder degraded
	// (a DegradedReason on the shadow). A git campaign should report zero.
	DegradedShadows int `json:"degraded_shadows"`
}

// LoadRecords reads JSONL files and direct JSONL children of directories.
func LoadRecords(paths []string) ([]schemas.DecisionDispositionRecord, error) {
	files, err := inputFiles(paths, ".jsonl")
	if err != nil {
		return nil, fmt.Errorf("records: %w", err)
	}
	var records []schemas.DecisionDispositionRecord
	for _, path := range files {
		loaded, err := loadFile(path)
		if err != nil {
			return nil, err
		}
		records = append(records, loaded...)
	}
	return records, nil
}

// inputFiles lists the files and the direct children of directories with one
// of the suffixes, sorted, each path once.
func inputFiles(paths []string, suffixes ...string) ([]string, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("at least one path is required")
	}
	var files []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", path, err)
		}
		if !info.IsDir() {
			files = append(files, path)
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, fmt.Errorf("directory %q: %w", path, err)
		}
		for _, entry := range entries {
			for _, suffix := range suffixes {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), suffix) {
					files = append(files, filepath.Join(path, entry.Name()))
					break
				}
			}
		}
	}
	sort.Strings(files)
	seen := map[string]bool{}
	var out []string
	for _, path := range files {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if !seen[abs] {
			seen[abs] = true
			out = append(out, path)
		}
	}
	return out, nil
}

func loadFile(path string) ([]schemas.DecisionDispositionRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open records %q: %w", path, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var records []schemas.DecisionDispositionRecord
	line := 0
	for scanner.Scan() {
		line++
		if len(strings.TrimSpace(scanner.Text())) == 0 {
			continue
		}
		var record schemas.DecisionDispositionRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, fmt.Errorf("records %q line %d: %w", path, line, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read records %q line %d: %w", path, line+1, err)
	}
	return records, nil
}

func eligible(snap schemas.SemanticSnapshot) bool {
	switch snap.QuestionPack {
	case splice.QuestionPackTrajActionV1, splice.QuestionPackTrajActionV2:
		return snap.Trajectory != nil
	case splice.QuestionPackTrajV1:
		if snap.Trajectory != nil {
			return true
		}
		previous, latest := false, false
		for _, candidate := range snap.CandidateRefs {
			previous = previous || candidate.Ref == "previous" && candidate.Claim != ""
			latest = latest || candidate.Ref == "latest" && candidate.Claim != ""
		}
		return previous && latest
	default:
		return false
	}
}

// notBoundaryReason names why a record is not a replayable trajectory
// boundary. Stage-input selector records (dependency_relation_v1) share the
// records file with the boundary records.
func notBoundaryReason(snap schemas.SemanticSnapshot) string {
	switch snap.QuestionPack {
	case splice.QuestionPackTrajActionV1, splice.QuestionPackTrajActionV2, splice.QuestionPackTrajV1:
		return "trajectory record without boundary evidence"
	case "":
		return fmt.Sprintf("not a boundary: record with no question pack (stage %q)", snap.Stage)
	default:
		return fmt.Sprintf("not a boundary: %s record (stage %q)", snap.QuestionPack, snap.Stage)
	}
}

type evaluation struct {
	row      Row
	boundary splice.TrajectoryBoundary
	err      error
}

// Replay processes each run in order. Different runs can execute concurrently.
// Transport failures produce error rows and do not stop the replay.
func Replay(ctx context.Context, records []schemas.DecisionDispositionRecord, evaluator zeroruntime.DecisionEvaluator, options Options, out io.Writer) (Summary, error) {
	rows, summary, err := ReplayRows(ctx, records, evaluator, options)
	if err != nil {
		return summary, err
	}
	encoder := json.NewEncoder(out)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return summary, fmt.Errorf("write replay boundary %s/%d/%d: %w", row.RunID, row.Iteration, row.Repair, err)
		}
	}
	return summary, nil
}

// ReplayRows evaluates the records and returns the rows in run order, without
// writing them.
func ReplayRows(ctx context.Context, records []schemas.DecisionDispositionRecord, evaluator zeroruntime.DecisionEvaluator, options Options) ([]Row, Summary, error) {
	summary := Summary{PolicyActions: map[schemas.TrajectoryAction]int{}}
	if evaluator == nil {
		return nil, summary, fmt.Errorf("replay: evaluator is required")
	}
	if options.Concurrency == 0 {
		options.Concurrency = 2
	}
	if options.Concurrency < 1 {
		return nil, summary, fmt.Errorf("concurrency must be positive")
	}
	if options.Pack != "" && options.Pack != splice.QuestionPackTrajActionV1 && options.Pack != splice.QuestionPackTrajActionV2 && options.Pack != splice.QuestionPackTrajV1 {
		return nil, summary, fmt.Errorf("unknown pack %q", options.Pack)
	}
	switch options.Evidence {
	case "":
		options.Evidence = EvidenceRecorded
	case EvidenceRecorded, EvidenceSwap:
	default:
		return nil, summary, fmt.Errorf("unknown evidence source %q (want recorded or swap)", options.Evidence)
	}
	if err := (config.TrajectoryConfig{Thresholds: options.Thresholds}).Validate(); err != nil {
		return nil, summary, err
	}
	cfg := options.BackendConfig.WithDefaults()
	cfg.Mode = "active_experimental"
	if err := cfg.Validate(); err != nil {
		return nil, summary, err
	}
	var snapshots []schemas.SemanticSnapshot
	type boundaryKey struct {
		runID                 string
		iteration, invocation int
	}
	seen := map[boundaryKey]bool{}
	for _, record := range records {
		snap := record.Snapshot
		if record.Disposition == schemas.DispositionProviderFailure && snap.Trajectory == nil && snap.RunID == "" {
			summary.Skipped++
			summary.SkippedItems = append(summary.SkippedItems, Skip{Reason: "projection failure: " + record.Reason})
			continue
		}
		if !eligible(snap) {
			summary.Skipped++
			summary.SkippedItems = append(summary.SkippedItems, Skip{RunID: snap.RunID, Iteration: snap.Iteration, Reason: notBoundaryReason(snap)})
			continue
		}
		// The first eligible record of a boundary is its record in both
		// evidence modes. A swap that cannot use it skips the boundary; it
		// does not fall through to a later record.
		key := boundaryKey{snap.RunID, snap.Iteration, snap.StageInvocation}
		if seen[key] {
			summary.Skipped++
			summary.SkippedItems = append(summary.SkippedItems, Skip{RunID: snap.RunID, Iteration: snap.Iteration, Repair: snap.StageInvocation, Reason: "later record of a boundary: the first record is replayed"})
			continue
		}
		seen[key] = true
		if options.Evidence == EvidenceSwap {
			swapped, reason, degraded := swapEvidence(record)
			if reason != "" {
				summary.Skipped++
				if degraded {
					summary.DegradedShadows++
				}
				summary.SkippedItems = append(summary.SkippedItems, Skip{RunID: snap.RunID, Iteration: snap.Iteration, Repair: snap.StageInvocation, Reason: reason})
				continue
			}
			snap = swapped
		}
		snapshots = append(snapshots, snap)
	}
	sort.SliceStable(snapshots, func(i, j int) bool {
		a, b := snapshots[i], snapshots[j]
		if a.RunID != b.RunID {
			return a.RunID < b.RunID
		}
		if a.Iteration != b.Iteration {
			return a.Iteration < b.Iteration
		}
		return a.StageInvocation < b.StageInvocation
	})
	results := make([]evaluation, len(snapshots))
	var runs [][]int
	for i, snap := range snapshots {
		if i == 0 || snap.RunID != snapshots[i-1].RunID {
			runs = append(runs, nil)
		}
		runs[len(runs)-1] = append(runs[len(runs)-1], i)
	}
	jobs := make(chan []int)
	var workers sync.WaitGroup
	count := min(options.Concurrency, len(runs))
	for w := 0; w < count; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for run := range jobs {
				policy := splice.NewTrajectoryReplayPolicy(options.Thresholds)
				for _, i := range run {
					rulesWriter := ""
					if options.Evidence == EvidenceSwap {
						rulesWriter = snapshots[i].Stage
					}
					result := evaluate(ctx, snapshots[i], evaluator, cfg, options.Pack, policy, rulesWriter)
					result.row.Evidence = options.Evidence
					var decision schemas.TrajectoryDecision
					if result.err == nil {
						policy.UsePack(result.row.Pack)
						decision, result.err = policy.Apply(result.boundary, splice.TrajectoryClassification{
							Probabilities: result.row.Probabilities, Backend: result.row.Backend, Model: result.row.ResolvedModel,
						})
					}
					if result.err != nil {
						result.row.Error = result.err.Error()
						decision = policy.Fallback(result.boundary)
					}
					result.row.PolicyAction, result.row.PolicyReason = decision.Action, decision.Reason
					results[i] = result
				}
			}
		}()
	}
	for _, run := range runs {
		jobs <- run
	}
	close(jobs)
	workers.Wait()
	rows := make([]Row, 0, len(results))
	for _, result := range results {
		row := result.row
		if result.err != nil {
			summary.Errors++
		} else {
			summary.Answered++
		}
		summary.Boundaries++
		summary.PolicyActions[row.PolicyAction]++
		rows = append(rows, row)
	}
	return rows, summary, nil
}

// swapEvidence returns the record's snapshot with each History entry's
// workspace values replaced by its EvidenceShadow entry. It returns a reason
// instead when the record cannot be swapped. The degraded result is true when
// the reason is a degraded pass builder.
func swapEvidence(record schemas.DecisionDispositionRecord) (schemas.SemanticSnapshot, string, bool) {
	snap := record.Snapshot
	if snap.Trajectory == nil {
		return snap, "no trajectory snapshot to swap", false
	}
	history := snap.Trajectory.Boundary.History
	switch {
	case len(record.EvidenceShadow) == 0:
		return snap, "no evidence shadow", false
	case len(record.EvidenceShadow) != len(history):
		return snap, fmt.Sprintf("evidence shadow has %d entries for a %d-entry history", len(record.EvidenceShadow), len(history)), false
	}
	// A degraded pass builder has no measured change. Swapping its empty
	// values in would read as "no change", so the record is skipped instead.
	for i, shadow := range record.EvidenceShadow {
		if shadow.DegradedReason != "" {
			return snap, fmt.Sprintf("pass builder degraded at entry %d: %s", i, shadow.DegradedReason), true
		}
	}
	swapped := append([]schemas.IterationState(nil), history...)
	for i, shadow := range record.EvidenceShadow {
		if shadow.Builder != "pass" {
			return snap, fmt.Sprintf("evidence shadow %d is the %q builder, not the pass builder", i, shadow.Builder), false
		}
		swapped[i].FilesChanged = shadow.FilesChanged
		swapped[i].LinesAdded = shadow.LinesAdded
		swapped[i].LinesRemoved = shadow.LinesRemoved
		swapped[i].PassChanged = shadow.PassChanged
	}
	trajectory := *snap.Trajectory
	trajectory.Boundary.History = swapped
	snap.Trajectory = &trajectory
	return snap, "", false
}

func evaluate(ctx context.Context, snap schemas.SemanticSnapshot, evaluator zeroruntime.DecisionEvaluator, cfg config.DecisionAdvisorConfig, pack string, policy *splice.TrajectoryReplayPolicy, rulesWriter string) evaluation {
	if pack == "" {
		pack = snap.QuestionPack
	}
	e := evaluation{row: Row{RunID: snap.RunID, Iteration: snap.Iteration, Repair: snap.StageInvocation, Pack: pack, Backend: cfg.Backend}, boundary: splice.TrajectoryBoundary{Rules: schemas.TrajectoryDecision{Action: schemas.ActionContinue, Reason: "No recorded rules decision."}}}
	e.boundary, e.err = splice.TrajectorySnapshotBoundary(snap)
	if e.err != nil {
		e.boundary.Rules = schemas.TrajectoryDecision{Action: schemas.ActionContinue, Reason: "Invalid recorded boundary evidence."}
		return e
	}
	if e.boundary.Rules.Action == "" {
		e.boundary.Rules = schemas.TrajectoryDecision{Action: schemas.ActionContinue, Reason: "No recorded rules decision."}
	}
	e.boundary.RunID, e.boundary.Iteration, e.boundary.Repair = snap.RunID, snap.Iteration, snap.StageInvocation
	// The rules come from the History being replayed, so the swapped evidence
	// reaches the classifier input. The recorded decision is not reused.
	e.boundary.Rules = splice.RulesDecisionForReplay(e.boundary.History, rulesWriter)
	e.row.RulesAction = e.boundary.Rules.Action
	e.boundary = policy.Observe(e.boundary)
	e.row.FailureIsNew = e.boundary.FailureHistory[len(e.boundary.FailureHistory)-1].IsNew
	e.row.BoundariesSinceNewFailure = e.boundary.BoundariesSinceNewFailure
	complete := snap.Trajectory != nil
	for _, event := range snap.RecentEvents {
		complete = complete || event.EventID == "boundary"
	}
	if complete {
		var err error
		snap, err = splice.TrajectorySemanticSnapshot(e.boundary, snap.QuestionPack)
		if err != nil {
			e.err = err
			return e
		}
	}
	req, err := splice.TrajectoryDecisionRequest(snap, pack, cfg.Profile, cfg.Limits.MaxRequestBytes)
	if err != nil {
		e.err = err
		return e
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Limits.TimeoutMS)*time.Millisecond)
	defer cancel()
	result, err := evaluator.Evaluate(callCtx, req)
	if err == nil {
		err = zeroruntime.ValidateDecisionResult(req, result)
	}
	if err != nil {
		var usage zeroruntime.DecisionUsageError
		if errors.As(err, &usage) && usage.DecisionUsage() != nil {
			result = *usage.DecisionUsage()
		}
	} else {
		e.row.Probabilities, err = splice.TrajectoryResultProbabilities(pack, result)
	}
	e.row.ResolvedModel = result.ResolvedModel
	e.row.InputTokens, e.row.OutputTokens, e.row.CostUSD = result.InputTokens, result.OutputTokens, result.ReportedCostUSD
	e.err = err
	if err == nil {
		e.row.TopLabel = splice.TopTrajectoryLabel(e.row.Probabilities)
	}
	return e
}
