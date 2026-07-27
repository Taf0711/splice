package agenteval

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/modelregistry"
)

func TestHarnessRunsSelectedTaskFromFixtureAndScoresResult(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(_ context.Context, input AgentRunInput) AgentRunResult {
			if input.TaskID != "document-stream-json-verify-events" {
				t.Fatalf("agent TaskID = %q", input.TaskID)
			}
			if !strings.Contains(input.Prompt, "stream-json protocol docs") {
				t.Fatalf("agent Prompt = %q", input.Prompt)
			}
			target := filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			if err := os.WriteFile(target, []byte("updated"), 0o644); err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{ExitCode: 0}
		}),
		Runner: Runner{
			RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
				return CommandResult{ID: command.ID, ExitCode: 0}
			},
		},
	}

	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
	})

	if !report.OK {
		t.Fatalf("OK = false; report=%#v", report)
	}
	if report.Contract != BenchmarkContractVersion {
		t.Fatalf("Contract = %q", report.Contract)
	}
	if report.SuiteID != suite.ID {
		t.Fatalf("SuiteID = %q", report.SuiteID)
	}
	if report.Summary != (BenchmarkSummary{TotalTasks: 1, PassedTasks: 1}) {
		t.Fatalf("Summary = %#v", report.Summary)
	}
	if len(report.Tasks) != 1 {
		t.Fatalf("Tasks len = %d", len(report.Tasks))
	}
	taskReport := report.Tasks[0]
	if taskReport.TaskID != "document-stream-json-verify-events" {
		t.Fatalf("TaskID = %q", taskReport.TaskID)
	}
	if taskReport.WorkspacePath == "" || taskReport.FixturePath == "" {
		t.Fatalf("workspace fields were not populated: %#v", taskReport)
	}
	if taskReport.Agent.ExitCode != 0 || taskReport.Agent.Error != "" {
		t.Fatalf("Agent = %#v", taskReport.Agent)
	}
	if taskReport.Report.Status != StatusPass || !taskReport.Report.OK {
		t.Fatalf("Report = %#v", taskReport.Report)
	}
	if taskReport.Report.Contract != ReportContractVersion {
		t.Fatalf("task score contract = %q, want %q", taskReport.Report.Contract, ReportContractVersion)
	}
}

func TestHarnessRunsAllTasksWhenTaskIDIsEmpty(t *testing.T) {
	suite := Suite{
		ID:   "suite-a",
		Name: "Suite A",
		Tasks: []Task{
			{
				ID:                   "task-a",
				Name:                 "Task A",
				Prompt:               "change a",
				WorkspaceFixture:     "fixtures/splice-mini",
				ExpectedChangedFiles: []string{"docs/STREAM_JSON_PROTOCOL.md"},
				VerificationCommands: []Command{{ID: "verify-a", Name: "Verify A", Command: []string{"go", "test", "./..."}}},
			},
			{
				ID:                   "task-b",
				Name:                 "Task B",
				Prompt:               "change b",
				WorkspaceFixture:     "fixtures/splice-mini",
				ExpectedChangedFiles: []string{"docs/NPM_WRAPPER_SMOKE.md"},
				VerificationCommands: []Command{{ID: "verify-b", Name: "Verify B", Command: []string{"go", "test", "./..."}}},
			},
		},
	}
	calls := []string{}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(_ context.Context, input AgentRunInput) AgentRunResult {
			calls = append(calls, input.TaskID)
			var target string
			switch input.TaskID {
			case "task-a":
				target = filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			case "task-b":
				target = filepath.Join(input.WorkspacePath, "docs", "NPM_WRAPPER_SMOKE.md")
			default:
				t.Fatalf("unexpected task %q", input.TaskID)
			}
			if err := os.WriteFile(target, []byte(input.TaskID), 0o644); err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{ExitCode: 0}
		}),
		Runner: Runner{
			RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
				return CommandResult{ID: command.ID, ExitCode: 0}
			},
		},
	}

	report := harness.Run(context.Background(), filepath.Join("testdata", "sample_suite.json"), suite, BenchmarkInput{
		WorkRoot: t.TempDir(),
	})

	if !report.OK {
		t.Fatalf("OK = false; report=%#v", report)
	}
	if report.Summary != (BenchmarkSummary{TotalTasks: 2, PassedTasks: 2}) {
		t.Fatalf("Summary = %#v", report.Summary)
	}
	if strings.Join(calls, ",") != "task-a,task-b" {
		t.Fatalf("agent calls = %#v", calls)
	}
}

func TestHarnessRunsEachSelectedTaskForEachModel(t *testing.T) {
	suite := Suite{
		ID:   "suite-a",
		Name: "Suite A",
		Tasks: []Task{{
			ID:                   "task-a",
			Name:                 "Task A",
			Prompt:               "change a",
			WorkspaceFixture:     "fixtures/splice-mini",
			ExpectedChangedFiles: []string{"docs/STREAM_JSON_PROTOCOL.md"},
			VerificationCommands: []Command{{ID: "verify-a", Name: "Verify A", Command: []string{"go", "test", "./..."}}},
		}},
	}
	calls := []string{}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(_ context.Context, input AgentRunInput) AgentRunResult {
			calls = append(calls, input.Model)
			if input.Model == "" {
				t.Fatal("agent model was empty")
			}
			target := filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			if err := os.WriteFile(target, []byte(input.Model), 0o644); err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{ExitCode: 0}
		}),
		Runner: Runner{
			RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
				return CommandResult{ID: command.ID, ExitCode: 0}
			},
		},
	}

	report := harness.Run(context.Background(), filepath.Join("testdata", "sample_suite.json"), suite, BenchmarkInput{
		WorkRoot: t.TempDir(),
		Models:   []string{"model-a", "model-b"},
	})

	if !report.OK {
		t.Fatalf("OK = false; report=%#v", report)
	}
	if report.Summary != (BenchmarkSummary{TotalTasks: 2, PassedTasks: 2}) {
		t.Fatalf("Summary = %#v", report.Summary)
	}
	if strings.Join(calls, ",") != "model-a,model-b" {
		t.Fatalf("agent model calls = %#v", calls)
	}
	if report.Tasks[0].Model != "model-a" || report.Tasks[1].Model != "model-b" {
		t.Fatalf("task report models = %#v, %#v", report.Tasks[0].Model, report.Tasks[1].Model)
	}
}

func TestHarnessScoresTraceAndContextChecks(t *testing.T) {
	suite := Suite{
		ID:   "suite-a",
		Name: "Suite A",
		Tasks: []Task{{
			ID:                   "task-a",
			Name:                 "Task A",
			Prompt:               "change a",
			WorkspaceFixture:     "fixtures/splice-mini",
			ExpectedChangedFiles: []string{"docs/STREAM_JSON_PROTOCOL.md"},
			RequiredTraceEvents:  []string{"tool:apply_patch", "tool:read_file"},
			ContextChecks: ContextChecks{
				RequiredFiles:  []string{"docs/STREAM_JSON_PROTOCOL.md"},
				ForbiddenFiles: []string{"tmp/leak.txt"},
			},
			VerificationCommands: []Command{{ID: "verify-a", Name: "Verify A", Command: []string{"go", "test", "./..."}}},
		}},
	}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(_ context.Context, input AgentRunInput) AgentRunResult {
			target := filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			if err := os.WriteFile(target, []byte("updated"), 0o644); err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{ExitCode: 0, Stdout: "{\"type\":\"tool\",\"name\":\"read_file\"}\n"}
		}),
		Runner: Runner{
			RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
				return CommandResult{ID: command.ID, ExitCode: 0}
			},
		},
	}

	report := harness.Run(context.Background(), filepath.Join("testdata", "sample_suite.json"), suite, BenchmarkInput{
		WorkRoot: t.TempDir(),
	})

	if report.OK || report.Summary.FailedTasks != 1 {
		t.Fatalf("expected trace failure, got %#v", report)
	}
	trace := findResultByID(t, report.Tasks[0].Report.Results, "trace_events")
	if trace.Status != StatusFail || !reflect.DeepEqual(trace.MissingEvents, []string{"tool:apply_patch"}) {
		t.Fatalf("trace result = %#v", trace)
	}
	context := findResultByID(t, report.Tasks[0].Report.Results, "context_checks")
	if context.Status != StatusPass {
		t.Fatalf("context result = %#v", context)
	}
}

func TestHarnessAccumulatesUsageEventsAndCost(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(_ context.Context, input AgentRunInput) AgentRunResult {
			target := filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			if err := os.WriteFile(target, []byte("updated"), 0o644); err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{
				ExitCode: 0,
				Stdout: "{\"type\":\"usage\",\"model\":\"gpt-4.1\",\"promptTokens\":100,\"completionTokens\":50,\"totalTokens\":150,\"cachedInputTokens\":80}\n" +
					"{\"type\":\"usage\",\"model\":\"gpt-4.1\",\"promptTokens\":100,\"completionTokens\":50,\"totalTokens\":150,\"cacheWriteTokens\":10,\"reasoningTokens\":20}\n",
				LatencyMs: 5,
			}
		}),
		Runner: Runner{
			RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
				return CommandResult{ID: command.ID, ExitCode: 0}
			},
		},
	}

	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
		Models:   []string{"gpt-4.1"},
		Registry: &registry,
	})

	if !report.OK {
		t.Fatalf("OK = false; report=%#v", report)
	}
	if len(report.Tasks) != 1 {
		t.Fatalf("Tasks len = %d", len(report.Tasks))
	}
	agent := report.Tasks[0].Agent
	if agent.InputTokens != 200 || agent.OutputTokens != 100 || agent.CachedInputTokens != 80 || agent.CacheWriteTokens != 10 || agent.ReasoningTokens != 20 {
		t.Fatalf("Agent usage = %#v", agent)
	}
	if agent.CostUSD <= 0 {
		t.Fatalf("CostUSD = %v, want > 0", agent.CostUSD)
	}
	if agent.LatencyMs == 0 {
		t.Fatalf("LatencyMs = %d, want non-zero", agent.LatencyMs)
	}
	task := report.Tasks[0]
	if task.InputTokens != agent.InputTokens || task.OutputTokens != agent.OutputTokens || task.CachedInputTokens != agent.CachedInputTokens || task.CostUSD != agent.CostUSD || task.LatencyMs != agent.LatencyMs {
		t.Fatalf("task metrics were not copied from agent: task=%#v agent=%#v", task, agent)
	}
}

func TestHarnessLeavesUsageAndCostZeroWhenStdoutHasNoUsageEvents(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(_ context.Context, input AgentRunInput) AgentRunResult {
			target := filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			if err := os.WriteFile(target, []byte("updated"), 0o644); err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{ExitCode: 0}
		}),
		Runner: Runner{
			RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
				return CommandResult{ID: command.ID, ExitCode: 0}
			},
		},
	}

	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
		Models:   []string{"gpt-4.1"},
	})

	if !report.OK {
		t.Fatalf("OK = false; report=%#v", report)
	}
	agent := report.Tasks[0].Agent
	if agent.InputTokens != 0 || agent.OutputTokens != 0 || agent.CachedInputTokens != 0 || agent.CacheWriteTokens != 0 || agent.ReasoningTokens != 0 || agent.CostUSD != 0 {
		t.Fatalf("Agent usage/cost should remain zero: %#v", agent)
	}
	if report.Summary.TotalInputTokens != 0 || report.Summary.TotalOutputTokens != 0 || report.Summary.TotalCostUSD != 0 {
		t.Fatalf("Summary usage/cost should remain zero: %#v", report.Summary)
	}
}

func TestAccountAgentRunUsagePricesEachSampleModel(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	result := AgentRunResult{UsageSamples: []UsageSample{
		{Model: "gpt-4.1", InputTokens: 1_000_000, UsageReported: boolPointer(true)},
		{Model: "gpt-4.1-mini", InputTokens: 1_000_000, UsageReported: boolPointer(true)},
	}}
	populateAgentRunUsage(&result)
	accountAgentRunUsage(&result, &registry)
	if result.CostCoverage != CostCoverageComplete {
		t.Fatalf("coverage = %q, want complete", result.CostCoverage)
	}
	if result.CostUSD != 2.4 {
		t.Fatalf("cost = %v, want 2.4", result.CostUSD)
	}
	for i, sample := range result.UsageSamples {
		if sample.CostStatus != CostStatusPriced || sample.CostProvenance != "reconstructed_estimate" || sample.CostUSD == nil {
			t.Fatalf("sample[%d] = %#v, want reconstructed priced sample", i, sample)
		}
	}
}

func TestAccountAgentRunUsagePreservesPricedZeroAndUnpriced(t *testing.T) {
	zero := 0.0
	reported := true
	result := AgentRunResult{UsageSamples: []UsageSample{
		{Model: "gpt-4.1", UsageReported: &reported, CostStatus: CostStatusPriced, CostUSD: &zero, CostProvenance: "reported"},
		{Model: "gpt-4.1", InputTokens: 1, UsageReported: &reported, CostStatus: CostStatusUnpriced, UnpricedReason: "provider omitted price"},
	}}
	populateAgentRunUsage(&result)
	accountAgentRunUsage(&result, nil)
	if result.CostCoverage != CostCoveragePartial || result.CostUSD != 0 {
		t.Fatalf("coverage/cost = %q/%v, want partial/0", result.CostCoverage, result.CostUSD)
	}
	if result.UsageSamples[0].CostUSD == nil || *result.UsageSamples[0].CostUSD != 0 {
		t.Fatalf("priced zero sample = %#v, want non-nil zero", result.UsageSamples[0])
	}
	if result.UsageSamples[1].CostUSD != nil || result.UsageSamples[1].CostStatus != CostStatusUnpriced {
		t.Fatalf("unpriced sample = %#v, want nil unpriced cost", result.UsageSamples[1])
	}
}

func TestAccountAgentRunUsageMissingModelIsUnpriced(t *testing.T) {
	result := AgentRunResult{UsageSamples: []UsageSample{{InputTokens: 10, OutputTokens: 5}}}
	populateAgentRunUsage(&result)
	accountAgentRunUsage(&result, nil)
	if result.CostUSD != 0 || result.CostCoverage != CostCoverageUnavailable {
		t.Fatalf("cost/coverage = %v/%q, want 0/unavailable", result.CostUSD, result.CostCoverage)
	}
	if result.UsageSamples[0].CostStatus != CostStatusUnpriced || result.UsageSamples[0].CostUSD != nil {
		t.Fatalf("sample = %#v, want unpriced with nil cost", result.UsageSamples[0])
	}
}

func TestAccountAgentRunUsagePartialAndCoverageStates(t *testing.T) {
	reported := true
	one := 0.25
	tests := []struct {
		name     string
		result   AgentRunResult
		registry *modelregistry.Registry
		coverage string
		cost     float64
	}{
		{name: "partial", result: AgentRunResult{UsageSamples: []UsageSample{
			{Model: "gpt-4.1", UsageReported: &reported, CostStatus: CostStatusPriced, CostUSD: &one},
			{Model: "gpt-4.1", InputTokens: 1, UsageReported: &reported, CostStatus: CostStatusUnpriced, UnpricedReason: "missing"},
		}}, coverage: CostCoveragePartial, cost: one},
		{name: "not applicable", result: AgentRunResult{}, coverage: CostCoverageNotApplicable},
		{name: "unavailable", result: AgentRunResult{UsageSamples: []UsageSample{{Model: "gpt-4.1", UsageReported: boolPointer(false)}}}, coverage: CostCoverageUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			populateAgentRunUsage(&test.result)
			accountAgentRunUsage(&test.result, test.registry)
			if test.result.CostCoverage != test.coverage || test.result.CostUSD != test.cost {
				t.Fatalf("cost/coverage = %v/%q, want %v/%q", test.result.CostUSD, test.result.CostCoverage, test.cost, test.coverage)
			}
		})
	}
}

func TestAccountAgentRunUsageAuxiliarySamplesAffectTotalsNotStages(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	result := AgentRunResult{
		Stdout: `{"type":"final","text":"{\"stages\":[{\"name\":\"code_writer\",\"tokens_input\":999,\"cost_usd\":99}]}"}`,
		UsageSamples: []UsageSample{
			{Model: "gpt-4.1", Stage: "code_writer", InputTokens: 10, UsageReported: boolPointer(true)},
			{Model: "gpt-4.1", Stage: "step_back", InputTokens: 20, UsageReported: boolPointer(true)},
		},
	}
	populateAgentRunUsage(&result)
	accountAgentRunUsage(&result, &registry)
	if result.InputTokens != 30 {
		t.Fatalf("input tokens = %d, want 30 including auxiliary sample", result.InputTokens)
	}
	if len(result.Stages) != 1 || result.Stages[0].TokensInput != 999 || result.CostUSD == 99 {
		t.Fatalf("stages/cost = %#v/%v, final stage diagnostics must not drive accounting", result.Stages, result.CostUSD)
	}
}

func TestWriteBenchmarkCSV(t *testing.T) {
	report := BenchmarkReport{
		Tasks: []BenchmarkTaskReport{
			{
				TaskID:            "task-a",
				Model:             "gpt-4.1",
				InputTokens:       100,
				OutputTokens:      50,
				CachedInputTokens: 25,
				CostUSD:           0.125,
				LatencyMs:         1200,
				Report:            Report{Status: StatusPass},
			},
			{
				TaskID:       "task-b",
				Model:        "claude-sonnet-4.5",
				InputTokens:  10,
				OutputTokens: 5,
				CostUSD:      0.75,
				LatencyMs:    300,
				Report:       Report{Status: StatusFail},
			},
		},
	}
	var buf bytes.Buffer
	if err := WriteBenchmarkCSV(&buf, report); err != nil {
		t.Fatalf("WriteBenchmarkCSV returned error: %v", err)
	}
	want := "taskId,model,status,pass,inputTokens,outputTokens,cachedInputTokens,costUSD,latencyMs,stageBreakdown\n" +
		"task-a,gpt-4.1,pass,true,100,50,25,0.125000,1200,\n" +
		"task-b,claude-sonnet-4.5,fail,false,10,5,0,0.750000,300,\n"
	if buf.String() != want {
		t.Fatalf("CSV = %q, want %q", buf.String(), want)
	}
}

func TestBenchmarkSummaryAggregatesUsageCostAndLatency(t *testing.T) {
	report := BenchmarkReport{
		Tasks: []BenchmarkTaskReport{
			{InputTokens: 100, OutputTokens: 50, CachedInputTokens: 10, CacheWriteTokens: 5, ReasoningTokens: 6, CostUSD: 1.5, LatencyMs: 100, Report: Report{Status: StatusPass, OK: true}},
			{InputTokens: 200, OutputTokens: 100, CachedInputTokens: 20, CacheWriteTokens: 7, ReasoningTokens: 8, CostUSD: 2.5, LatencyMs: 200, Report: Report{Status: StatusPass, OK: true}},
			{InputTokens: 300, OutputTokens: 150, CachedInputTokens: 30, CacheWriteTokens: 9, ReasoningTokens: 10, CostUSD: 3.0, LatencyMs: 300, Report: Report{Status: StatusFail}},
		},
	}

	report.finishSummary()

	if report.Summary.TotalTasks != 3 || report.Summary.PassedTasks != 2 || report.Summary.FailedTasks != 1 {
		t.Fatalf("task counts = %#v", report.Summary)
	}
	if report.Summary.TotalCostUSD != 7 || report.Summary.EstimatedCostUSD != 7 || report.Summary.TotalInputTokens != 600 || report.Summary.TotalOutputTokens != 300 || report.Summary.TotalCachedInputTokens != 60 || report.Summary.TotalCacheWriteTokens != 21 || report.Summary.TotalReasoningTokens != 24 {
		t.Fatalf("aggregate usage/cost = %#v", report.Summary)
	}
	if report.Summary.MeanCostPerTask != 7.0/3.0 || report.Summary.MeanCostPerPassedTask != 7.0/2.0 || report.Summary.MeanEstimatedCostPerTask != 7.0/3.0 || report.Summary.MeanEstimatedCostOfPassedTasks != 4.0/2.0 || report.Summary.CampaignEstimatedCostPerPass != 7.0/2.0 || report.Summary.MeanLatencyMs != 200 {
		t.Fatalf("aggregate means = %#v", report.Summary)
	}
}

func TestBenchmarkSummaryCountsRequestCostStatuses(t *testing.T) {
	zero := 0.0
	report := BenchmarkReport{Tasks: []BenchmarkTaskReport{
		{Agent: AgentRunResult{UsageSamples: []UsageSample{
			{CostStatus: CostStatusPriced, CostUSD: &zero},
			{CostStatus: CostStatusUnpriced},
			{CostStatus: CostStatusError},
		}}, Report: Report{Status: StatusPass, OK: true}},
	}}

	report.finishSummary()
	if report.Summary.PricedRequestCount != 1 || report.Summary.UnpricedRequestCount != 1 || report.Summary.ErrorRequestCount != 1 {
		t.Fatalf("request cost statuses = %#v", report.Summary)
	}
	if report.Summary.CostCoverage != CostCoveragePartial {
		t.Fatalf("coverage = %q, want %q", report.Summary.CostCoverage, CostCoveragePartial)
	}
}

func TestHarnessBlocksSelectedTasksWhenAgentIsNil(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	harness := Harness{Materializer: Materializer{}, Runner: Runner{}}

	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
	})

	if report.OK {
		t.Fatalf("OK = true; report=%#v", report)
	}
	if report.Summary != (BenchmarkSummary{TotalTasks: 1, BlockedTasks: 1}) {
		t.Fatalf("Summary = %#v", report.Summary)
	}
	if report.Tasks[0].Agent.Error != "agent command is required" {
		t.Fatalf("Agent.Error = %q", report.Tasks[0].Agent.Error)
	}
	if report.Tasks[0].Agent.ExitCode != -1 {
		t.Fatalf("Agent.ExitCode = %d, want -1", report.Tasks[0].Agent.ExitCode)
	}
	if report.Tasks[0].Report.Status != StatusBlocked {
		t.Fatalf("Report.Status = %q", report.Tasks[0].Report.Status)
	}
}

func TestHarnessBlocksWhenAgentRunFails(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(context.Context, AgentRunInput) AgentRunResult {
			return AgentRunResult{ExitCode: 2, Stderr: "nope"}
		}),
		Runner: Runner{
			RunCommand: func(context.Context, string, Command) CommandResult {
				t.Fatal("runner should not score after a failed agent run")
				return CommandResult{}
			},
		},
	}

	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
	})

	if report.OK {
		t.Fatalf("OK = true; report=%#v", report)
	}
	if report.Summary != (BenchmarkSummary{TotalTasks: 1, BlockedTasks: 1}) {
		t.Fatalf("Summary = %#v", report.Summary)
	}
	if report.Tasks[0].Report.Status != StatusBlocked {
		t.Fatalf("Report.Status = %q", report.Tasks[0].Report.Status)
	}
}

func TestHarnessReportsErrorForUnknownTaskID(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(context.Context, AgentRunInput) AgentRunResult {
			t.Fatal("agent should not run for an unknown task id")
			return AgentRunResult{}
		}),
		Runner: Runner{},
	}

	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "no-such-task",
		WorkRoot: t.TempDir(),
	})

	if report.OK {
		t.Fatalf("OK = true; report=%#v", report)
	}
	if report.Summary != (BenchmarkSummary{TotalTasks: 1, ErrorTasks: 1}) {
		t.Fatalf("Summary = %#v", report.Summary)
	}
	if len(report.Tasks) != 1 || report.Tasks[0].TaskID != "no-such-task" {
		t.Fatalf("Tasks = %#v", report.Tasks)
	}
	if report.Tasks[0].Report.Status != StatusError {
		t.Fatalf("Report.Status = %q", report.Tasks[0].Report.Status)
	}
	if report.Tasks[0].Agent.ExitCode != -1 || !strings.Contains(report.Tasks[0].Agent.Error, "no-such-task") {
		t.Fatalf("Agent should record non-run selection error, got %#v", report.Tasks[0].Agent)
	}
}

func TestHarnessReportsErrorWhenMaterializationFails(t *testing.T) {
	suite := Suite{
		ID:   "suite-mat",
		Name: "Suite Mat",
		Tasks: []Task{{
			ID:                   "missing-fixture",
			Name:                 "Missing fixture",
			Prompt:               "do work",
			WorkspaceFixture:     "fixtures/does-not-exist",
			ExpectedChangedFiles: []string{"x.txt"},
			VerificationCommands: []Command{{ID: "v", Name: "V", Command: []string{"go", "version"}}},
		}},
	}
	agentCalled := false
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(context.Context, AgentRunInput) AgentRunResult {
			agentCalled = true
			return AgentRunResult{ExitCode: 0}
		}),
		Runner: Runner{RunCommand: func(context.Context, string, Command) CommandResult {
			t.Fatal("runner should not score when materialization fails")
			return CommandResult{}
		}},
	}

	report := harness.Run(context.Background(), filepath.Join("testdata", "sample_suite.json"), suite, BenchmarkInput{
		WorkRoot: t.TempDir(),
	})

	if report.OK {
		t.Fatalf("OK = true; report=%#v", report)
	}
	if report.Summary != (BenchmarkSummary{TotalTasks: 1, ErrorTasks: 1}) {
		t.Fatalf("Summary = %#v", report.Summary)
	}
	if report.Tasks[0].Report.Status != StatusError {
		t.Fatalf("Report.Status = %q", report.Tasks[0].Report.Status)
	}
	if !strings.Contains(report.Tasks[0].Report.Error, "materialization failed") {
		t.Fatalf("Report.Error = %q", report.Tasks[0].Report.Error)
	}
	if report.Tasks[0].Agent.ExitCode != -1 || report.Tasks[0].Agent.Error == "" {
		t.Fatalf("Agent should record non-run materialization error, got %#v", report.Tasks[0].Agent)
	}
	if agentCalled {
		t.Fatal("agent should not run when materialization fails")
	}
}

func TestHarnessAppliesPerTaskTimeout(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	var hadDeadline bool
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(ctx context.Context, input AgentRunInput) AgentRunResult {
			_, hadDeadline = ctx.Deadline()
			target := filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			if err := os.WriteFile(target, []byte("updated"), 0o644); err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{ExitCode: 0}
		}),
		Runner: Runner{
			RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
				return CommandResult{ID: command.ID, ExitCode: 0}
			},
		},
	}

	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
		Timeout:  30 * time.Second,
	})

	if !report.OK {
		t.Fatalf("OK = false; report=%#v", report)
	}
	if !hadDeadline {
		t.Fatal("expected agent context to carry a deadline when Timeout is set")
	}
}

func TestHarnessTimeoutCancelsBlockedAgent(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	agentReached := false
	sawCancel := false
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(ctx context.Context, _ AgentRunInput) AgentRunResult {
			agentReached = true
			<-ctx.Done()
			sawCancel = ctx.Err() != nil
			return AgentRunResult{ExitCode: -1, Error: ctx.Err().Error()}
		}),
		Runner: Runner{RunCommand: func(context.Context, string, Command) CommandResult {
			t.Fatal("runner should not score after a timed-out agent run")
			return CommandResult{}
		}},
	}

	// The timeout is longer than materialization of the small fixture even on
	// slower Windows CI runners, so the agent is reached before it blocks until
	// the deadline fires. 10s wasn't generous enough in practice — it failed
	// on an otherwise-unrelated PR merge (materialization alone consumed the
	// full budget on a contended Windows runner, so the agent was never
	// reached); widened for headroom, since the assertions below still bound
	// worst-case test time via that same deadline, not this constant.
	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
		Timeout:  30 * time.Second,
	})

	if !agentReached {
		t.Fatal("agent was never reached; the timeout fired before the agent ran")
	}
	if !sawCancel {
		t.Fatal("agent ran but never observed context cancellation")
	}
	if report.OK {
		t.Fatalf("OK = true; expected the timeout to fail the task; report=%#v", report)
	}
	if report.Tasks[0].Report.Status != StatusBlocked {
		t.Fatalf("Report.Status = %q, want blocked after agent timeout", report.Tasks[0].Report.Status)
	}
}

func TestHarnessRemovesWorkspacesByDefaultAndKeepsWhenRequested(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(_ context.Context, input AgentRunInput) AgentRunResult {
			target := filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			if err := os.WriteFile(target, []byte("updated"), 0o644); err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{ExitCode: 0}
		}),
		Runner: Runner{
			RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
				return CommandResult{ID: command.ID, ExitCode: 0}
			},
		},
	}

	removed := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
	})
	if !removed.OK {
		t.Fatalf("expected passing report, got %#v", removed)
	}
	if _, err := os.Stat(removed.Tasks[0].WorkspacePath); !os.IsNotExist(err) {
		t.Fatalf("default run should remove workspace, stat err=%v", err)
	}

	kept := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:         "document-stream-json-verify-events",
		WorkRoot:       t.TempDir(),
		KeepWorkspaces: true,
	})
	if !kept.OK {
		t.Fatalf("expected passing report, got %#v", kept)
	}
	if _, err := os.Stat(kept.Tasks[0].WorkspacePath); err != nil {
		t.Fatalf("keep-workspaces should preserve workspace: %v", err)
	}
}
