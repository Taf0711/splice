package agenteval

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type BenchmarkInput struct {
	TaskID         string
	WorkRoot       string
	Models         []string
	KeepWorkspaces bool
	Registry       *modelregistry.Registry
	// Timeout bounds each task's materialization, agent run, and scoring. A
	// non-positive value leaves the task unbounded.
	Timeout time.Duration
}

type BenchmarkReport struct {
	Contract string                `json:"contract"`
	SuiteID  string                `json:"suiteId"`
	OK       bool                  `json:"ok"`
	Summary  BenchmarkSummary      `json:"summary"`
	Tasks    []BenchmarkTaskReport `json:"tasks"`
}

type BenchmarkTaskReport struct {
	TaskID            string           `json:"taskId"`
	Model             string           `json:"model,omitempty"`
	WorkspacePath     string           `json:"workspacePath"`
	FixturePath       string           `json:"fixturePath"`
	InputTokens       int              `json:"inputTokens,omitempty"`
	OutputTokens      int              `json:"outputTokens,omitempty"`
	CachedInputTokens int              `json:"cachedInputTokens,omitempty"`
	CacheWriteTokens  int              `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens   int              `json:"reasoningTokens,omitempty"`
	CostUSD           float64          `json:"costUsd,omitempty"`
	LatencyMs         int64            `json:"latencyMs,omitempty"`
	Stages            []StageBreakdown `json:"stages,omitempty"`
	Agent             AgentRunResult   `json:"agent"`
	Report            Report           `json:"report"`
}

type BenchmarkSummary struct {
	TotalTasks             int     `json:"totalTasks"`
	PassedTasks            int     `json:"passedTasks"`
	FailedTasks            int     `json:"failedTasks"`
	BlockedTasks           int     `json:"blockedTasks"`
	ErrorTasks             int     `json:"errorTasks"`
	TotalCostUSD           float64 `json:"totalCostUsd,omitempty"`
	TotalInputTokens       int     `json:"totalInputTokens,omitempty"`
	TotalOutputTokens      int     `json:"totalOutputTokens,omitempty"`
	TotalCachedInputTokens int     `json:"totalCachedInputTokens,omitempty"`
	MeanCostPerTask        float64 `json:"meanCostPerTask,omitempty"`
	MeanCostPerPassedTask  float64 `json:"meanCostPerPassedTask,omitempty"`
	MeanLatencyMs          int64   `json:"meanLatencyMs,omitempty"`
}

type Harness struct {
	Materializer Materializer
	Agent        AgentRunner
	Runner       Runner
}

func (harness Harness) Run(ctx context.Context, suitePath string, suite Suite, input BenchmarkInput) BenchmarkReport {
	if ctx == nil {
		ctx = context.Background()
	}
	report := BenchmarkReport{
		Contract: ReportContractVersion,
		SuiteID:  suite.ID,
	}
	tasks, err := selectBenchmarkTasks(suite, input.TaskID)
	if err != nil {
		taskID := input.TaskID
		report.Tasks = append(report.Tasks, BenchmarkTaskReport{
			TaskID: taskID,
			Agent:  AgentRunResult{ExitCode: -1, Error: err.Error()},
			Report: Report{
				Contract: ReportContractVersion,
				SuiteID:  suite.ID,
				TaskID:   taskID,
				Status:   StatusError,
				OK:       false,
				Summary:  Summary{Total: 1, Errors: 1},
				Error:    err.Error(),
				Results: []Result{{
					ID:      "task",
					Name:    "Task selection",
					Kind:    ResultChangedFiles,
					Status:  StatusError,
					Message: err.Error(),
				}},
			},
		})
		report.finishSummary()
		return report
	}

	for _, task := range tasks {
		for _, model := range benchmarkModels(input.Models) {
			report.Tasks = append(report.Tasks, harness.runTask(ctx, suitePath, suite, task, model, input))
		}
	}
	report.finishSummary()
	return report
}

func (harness Harness) runTask(ctx context.Context, suitePath string, suite Suite, task Task, model string, input BenchmarkInput) BenchmarkTaskReport {
	if input.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, input.Timeout)
		defer cancel()
	}
	taskReport := BenchmarkTaskReport{
		TaskID: task.ID,
		Model:  model,
		Agent:  AgentRunResult{ExitCode: -1},
	}
	if harness.Agent == nil {
		taskReport.Agent = AgentRunResult{ExitCode: -1, Error: "agent command is required"}
		taskReport.Report = Score(suite, ScoreInput{
			TaskID:      task.ID,
			Blocked:     true,
			BlockReason: taskReport.Agent.Error,
		})
		return taskReport
	}

	workspace, err := harness.Materializer.MaterializeTask(ctx, suitePath, task, MaterializeInput{
		WorkRoot: input.WorkRoot,
	})
	if err != nil {
		taskReport.Agent.Error = err.Error()
		taskReport.Report = errorReport(suite.ID, task.ID, fmt.Sprintf("workspace materialization failed: %v", err))
		return taskReport
	}
	taskReport.WorkspacePath = workspace.Path
	taskReport.FixturePath = workspace.FixturePath
	if !input.KeepWorkspaces {
		defer func() { _ = os.RemoveAll(workspace.Path) }()
	}

	agentResult := harness.Agent.Run(ctx, AgentRunInput{
		TaskID:        task.ID,
		Model:         model,
		Prompt:        task.Prompt,
		WorkspacePath: workspace.Path,
	})
	if !agentRunHasUsageTotals(agentResult) {
		populateAgentRunUsage(&agentResult)
	}
	estimateAgentRunCost(&agentResult, model, input.Registry)
	taskReport.Agent = agentResult
	copyAgentMetrics(&taskReport, agentResult)
	if agentResult.Error != "" || agentResult.ExitCode != 0 {
		reason := firstNonEmpty(agentResult.Error, strings.TrimSpace(agentResult.Stderr), fmt.Sprintf("agent exited with code %d", agentResult.ExitCode))
		taskReport.Report = Score(suite, ScoreInput{
			TaskID:      task.ID,
			Blocked:     true,
			BlockReason: reason,
		})
		return taskReport
	}

	taskReport.Report = harness.Runner.Run(ctx, suite, RunInput{
		TaskID:        task.ID,
		WorkspacePath: workspace.Path,
		TraceStdout:   agentResult.Stdout,
	})
	return taskReport
}

func (report *BenchmarkReport) finishSummary() {
	report.Summary = BenchmarkSummary{TotalTasks: len(report.Tasks)}
	var totalLatencyMs int64
	for _, task := range report.Tasks {
		report.Summary.TotalCostUSD += task.CostUSD
		report.Summary.TotalInputTokens += task.InputTokens
		report.Summary.TotalOutputTokens += task.OutputTokens
		report.Summary.TotalCachedInputTokens += task.CachedInputTokens
		totalLatencyMs += task.LatencyMs
		switch {
		case task.Report.OK:
			report.Summary.PassedTasks++
		case task.Report.Status == StatusBlocked:
			report.Summary.BlockedTasks++
		case task.Report.Status == StatusError:
			report.Summary.ErrorTasks++
		default:
			report.Summary.FailedTasks++
		}
	}
	if report.Summary.TotalTasks > 0 {
		report.Summary.MeanCostPerTask = report.Summary.TotalCostUSD / float64(report.Summary.TotalTasks)
		report.Summary.MeanLatencyMs = totalLatencyMs / int64(report.Summary.TotalTasks)
	}
	if report.Summary.PassedTasks > 0 {
		report.Summary.MeanCostPerPassedTask = report.Summary.TotalCostUSD / float64(report.Summary.PassedTasks)
	}
	report.OK = report.Summary.TotalTasks > 0 &&
		report.Summary.FailedTasks == 0 &&
		report.Summary.BlockedTasks == 0 &&
		report.Summary.ErrorTasks == 0
}

func copyAgentMetrics(taskReport *BenchmarkTaskReport, agentResult AgentRunResult) {
	if taskReport == nil {
		return
	}
	taskReport.InputTokens = agentResult.InputTokens
	taskReport.OutputTokens = agentResult.OutputTokens
	taskReport.CachedInputTokens = agentResult.CachedInputTokens
	taskReport.CacheWriteTokens = agentResult.CacheWriteTokens
	taskReport.ReasoningTokens = agentResult.ReasoningTokens
	taskReport.CostUSD = agentResult.CostUSD
	taskReport.LatencyMs = agentResult.LatencyMs
	taskReport.Stages = agentResult.Stages
}

func agentRunHasUsageTotals(result AgentRunResult) bool {
	return result.InputTokens != 0 ||
		result.OutputTokens != 0 ||
		result.CachedInputTokens != 0 ||
		result.CacheWriteTokens != 0 ||
		result.ReasoningTokens != 0
}

func estimateAgentRunCost(result *AgentRunResult, model string, registry *modelregistry.Registry) {
	if result == nil {
		return
	}
	result.CostUSD = 0
	if registry == nil || strings.TrimSpace(model) == "" || !agentRunHasUsageTotals(*result) {
		return
	}
	usage := zeroruntime.Usage{
		InputTokens:       result.InputTokens,
		OutputTokens:      result.OutputTokens,
		CachedInputTokens: result.CachedInputTokens,
		CacheWriteTokens:  result.CacheWriteTokens,
		ReasoningTokens:   result.ReasoningTokens,
	}
	cost, err := registry.EstimateCost(model, usage)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent eval cost estimate failed for model %q: %v\n", model, err)
		return
	}
	result.CostUSD = cost.TotalCost
	// Estimate per-stage cost using each stage's own model (or the task model
	// as fallback). Overwrites the zero cost the pipeline records (StageUsage
	// carries tokens, not cost). When stages use different models, the sum of
	// per-stage costs is more accurate than the single-model estimate above.
	var stageCostSum float64
	for i := range result.Stages {
		stageModel := result.Stages[i].Model
		if stageModel == "" {
			stageModel = model
		}
		stageUsage := zeroruntime.Usage{
			InputTokens:       result.Stages[i].TokensInput,
			OutputTokens:      result.Stages[i].TokensOutput,
			CachedInputTokens: result.Stages[i].TokensCached,
		}
		if stageCost, err := registry.EstimateCost(stageModel, stageUsage); err == nil {
			result.Stages[i].CostUSD = stageCost.TotalCost
			stageCostSum += stageCost.TotalCost
		}
	}
	if len(result.Stages) > 0 && stageCostSum > 0 {
		result.CostUSD = stageCostSum
	}
}

func WriteBenchmarkCSV(w io.Writer, report BenchmarkReport) error {
	writer := csv.NewWriter(w)
	if err := writer.Write([]string{"taskId", "model", "status", "pass", "inputTokens", "outputTokens", "cachedInputTokens", "costUSD", "latencyMs", "stageBreakdown"}); err != nil {
		return err
	}
	for _, task := range report.Tasks {
		if err := writer.Write([]string{
			task.TaskID,
			task.Model,
			string(task.Report.Status),
			fmt.Sprintf("%t", task.Report.Status == StatusPass),
			fmt.Sprintf("%d", task.InputTokens),
			fmt.Sprintf("%d", task.OutputTokens),
			fmt.Sprintf("%d", task.CachedInputTokens),
			fmt.Sprintf("%f", task.CostUSD),
			fmt.Sprintf("%d", task.LatencyMs),
			formatStageBreakdown(task.Stages),
		}); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

// formatStageBreakdown renders per-stage token/cost data as a compact
// semicolon-delimited string: "name:in=N,out=N,cost=F;name:...". Empty for
// non-pipeline agents.
func formatStageBreakdown(stages []StageBreakdown) string {
	if len(stages) == 0 {
		return ""
	}
	parts := make([]string, 0, len(stages))
	for _, s := range stages {
		parts = append(parts, fmt.Sprintf("%s:in=%d,out=%d,cost=%.4f", s.Name, s.TokensInput, s.TokensOutput, s.CostUSD))
	}
	return strings.Join(parts, ";")
}

func selectBenchmarkTasks(suite Suite, taskID string) ([]Task, error) {
	if taskID == "" {
		tasks := make([]Task, 0, len(suite.Tasks))
		for _, task := range suite.Tasks {
			tasks = append(tasks, normalizeTask(task))
		}
		return tasks, nil
	}
	task, err := selectTask(suite, taskID)
	if err != nil {
		return nil, err
	}
	return []Task{task}, nil
}

func benchmarkModels(models []string) []string {
	normalized := make([]string, 0, len(models))
	seen := map[string]bool{}
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		normalized = append(normalized, model)
	}
	if len(normalized) == 0 {
		return []string{""}
	}
	return normalized
}

func errorReport(suiteID string, taskID string, message string) Report {
	return Report{
		Contract: ReportContractVersion,
		SuiteID:  suiteID,
		TaskID:   taskID,
		Status:   StatusError,
		OK:       false,
		Summary:  Summary{Total: 1, Errors: 1},
		Error:    message,
		Results: []Result{{
			ID:      "benchmark",
			Name:    "Benchmark harness",
			Kind:    ResultChangedFiles,
			Status:  StatusError,
			Message: message,
		}},
	}
}
