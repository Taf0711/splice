package agenteval

import (
	"bufio"
	"encoding/json"
	"io"
	"runtime"
)

// AttemptsRolloutDefault is the rollout count used when BenchmarkInput
// does not set one. Section 34 of the evaluation handoff requires at
// least three rollouts per task for statistical claims; the default of
// one keeps the existing single-shot behavior until the causal runs
// need more, and the count is always visible in the row.
const AttemptsRolloutDefault = 1

// WriteAttemptsJSONL folds every task attempt in the benchmark report
// into ReportRow records and writes them as one JSON object per line.
// This is the section-30 unified per-attempt log: aggregations derive
// from these rows downstream, never from the human report. Rows whose
// facts the runner could not prove stay absent (omitempty), so unknown
// cost and unknown provenance remain unknown.
func WriteAttemptsJSONL(w io.Writer, report BenchmarkReport) error {
	writer := bufio.NewWriter(w)
	defer writer.Flush()
	for _, task := range report.Tasks {
		row := RowFromBenchmarkTask(report, task)
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if _, err := writer.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return writer.Flush()
}

// RowFromBenchmarkTask folds one BenchmarkTaskReport into a ReportRow.
func RowFromBenchmarkTask(report BenchmarkReport, task BenchmarkTaskReport) ReportRow {
	meta := RowMeta{
		ModelID:       task.Model,
		ProviderRoute: task.RunnerKind,
		OSArch:        runtime.GOOS + "/" + runtime.GOARCH,
		UsageSamples:  task.Agent.UsageSamples,
	}
	row := RowFromReport(task.Report, meta)
	// Attempt identity comes from the benchmark task record: the per-task
	// Report may carry a stale or empty task id (blocked runs), and the
	// benchmark loop is the authority on which (task, model) pair ran.
	row.TaskID = task.TaskID
	if row.SuiteID == "" {
		row.SuiteID = report.SuiteID
	}
	// Attempt numbers start at 1; the benchmark runner currently runs one
	// attempt per (task, model) pair, so the attempt is always 1 here.
	// Rollout looping (section 34) increments this per repetition.
	row.Attempt = 1
	return row
}
