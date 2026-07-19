package splice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestTopologicalOrderDiamondAndTieBreak(t *testing.T) {
	tasks := []schemas.Task{
		designTask("root", "Root", nil),
		designTask("right", "Right", []string{"root"}),
		designTask("left", "Left", []string{"root"}),
		designTask("done", "Done", []string{"left", "right"}),
	}

	ordered, err := topologicalOrder(tasks)
	if err != nil {
		t.Fatalf("topologicalOrder returned error: %v", err)
	}
	got := taskIDs(ordered)
	want := []string{"root", "right", "left", "done"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestRunDesignPlanCycleReturnsInBandFailure(t *testing.T) {
	plan := designPlan([]schemas.Task{
		designTask("t1", "First", []string{"t2"}),
		designTask("t2", "Second", []string{"t1"}),
	})

	result, err := RunDesignPlan(context.Background(), plan, runFakeProvider{}, agent.Options{SessionID: "plan-cycle"}, nil, nil)
	if err != nil {
		t.Fatalf("RunDesignPlan returned Go error: %v", err)
	}
	if !result.Incomplete {
		t.Fatal("expected in-band incomplete result")
	}
	var decoded schemas.DesignPlanResult
	if err := json.Unmarshal([]byte(result.FinalAnswer), &decoded); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if decoded.PlanID != "plan-cycle" || decoded.Status != "failed" {
		t.Fatalf("unexpected result: %#v", decoded)
	}
	if got := strings.Join(decoded.SkippedTaskIDs, ","); got != "t1,t2" {
		t.Fatalf("skipped ids = %v, want original task order", decoded.SkippedTaskIDs)
	}
	if !strings.Contains(result.IncompleteReason, "dependency cycle") {
		t.Fatalf("incomplete reason = %q, want dependency cycle", result.IncompleteReason)
	}
}

// designHappyProvider switches later code_writer tasks to "modify" so a
// design plan with multiple tasks against the same file does not trip the
// fail-loud create-no-overwrite contract introduced in AR1.
type designHappyProvider struct{ workDir string }

func (provider designHappyProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	toolName := ""
	if len(request.Tools) > 0 {
		toolName = request.Tools[0].Name
	}
	if toolName != "submit_code" {
		return runFakeProvider{}.StreamCompletion(ctx, request)
	}

	changeType := "create"
	if _, err := os.Stat(filepath.Join(provider.workDir, "main.go")); err == nil {
		changeType = "modify"
	}
	out := schemas.CodeWriterOutput{
		Files: []schemas.FileChange{
			{Path: "main.go", Content: "package main\n\nfunc Hello() string { return \"hello\" }\n", ChangeType: changeType},
		},
		Language:   "go",
		Intent:     "add Hello function",
		Confidence: 0.95,
	}
	b, _ := json.Marshal(out)
	args := string(b)
	ch := make(chan zeroruntime.StreamEvent, 5)
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "1", ToolName: toolName}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "1", ArgumentsFragment: args}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "1"}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 10, OutputTokens: 5}}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func TestRunDesignPlanHappyPath(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	plan := designPlan([]schemas.Task{
		designTask("t1", "First", nil),
		designTask("t2", "Second", []string{"t1"}),
		designTask("t3", "Third", []string{"t1"}),
	})
	var reasoning []string

	result, err := RunDesignPlan(context.Background(), plan, designHappyProvider{workDir: workDir}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		SessionID:      "plan-happy",
		MaxTurns:       1,
		OnReasoning:    func(text string) { reasoning = append(reasoning, text) },
	}, nil, nil)
	if err != nil {
		t.Fatalf("RunDesignPlan returned error: %v", err)
	}
	if result.Incomplete {
		t.Fatalf("expected completed plan, got incomplete: %s", result.IncompleteReason)
	}

	var decoded schemas.DesignPlanResult
	if err := json.Unmarshal([]byte(result.FinalAnswer), &decoded); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if decoded.Status != "completed" || len(decoded.CompletedTasks) != 3 {
		t.Fatalf("unexpected result: %#v", decoded)
	}
	for _, taskID := range []string{"t1", "t2", "t3"} {
		wantRunID := "plan-happy-" + taskID
		found := false
		for _, completed := range decoded.CompletedTasks {
			if completed.TaskID == taskID && completed.RunID == wantRunID && completed.Status == "completed" {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing completed task %s with run id %s in %#v", taskID, wantRunID, decoded.CompletedTasks)
		}
	}
	taskStarts := 0
	for _, line := range reasoning {
		if strings.HasPrefix(line, "Starting task ") {
			taskStarts++
		}
	}
	if taskStarts != 3 {
		t.Fatalf("task progress lines = %d, want 3; reasoning=%q", taskStarts, strings.Join(reasoning, ""))
	}
}

func TestRunDesignPlanFailFast(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	plan := designPlan([]schemas.Task{
		designTask("t1", "First", nil),
		designTask("t2", "Second", []string{"t1"}),
		designTask("t3", "Third", []string{"t2"}),
	})

	result, err := RunDesignPlan(context.Background(), plan, designFailingProvider{failIntent: "Implement Second"}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		SessionID:      "plan-fail",
		MaxTurns:       1,
	}, nil, nil)
	if err != nil {
		t.Fatalf("RunDesignPlan returned error: %v", err)
	}
	if !result.Incomplete {
		t.Fatal("expected incomplete plan")
	}
	var decoded schemas.DesignPlanResult
	if err := json.Unmarshal([]byte(result.FinalAnswer), &decoded); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if len(decoded.CompletedTasks) != 1 || decoded.CompletedTasks[0].TaskID != "t1" {
		t.Fatalf("completed tasks = %#v, want only t1", decoded.CompletedTasks)
	}
	if decoded.FailedTask == nil || decoded.FailedTask.TaskID != "t2" || decoded.FailedTask.Status != "failed" {
		t.Fatalf("failed task = %#v, want failed t2", decoded.FailedTask)
	}
	if got := strings.Join(decoded.SkippedTaskIDs, ","); got != "t3" {
		t.Fatalf("skipped ids = %v, want [t3]", decoded.SkippedTaskIDs)
	}
}

func TestRunDesignPlanGeneratedPlanID(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	plan := designPlan([]schemas.Task{designTask("t1", "First", nil)})

	result, err := RunDesignPlan(context.Background(), plan, runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		MaxTurns:       1,
	}, nil, nil)
	if err != nil {
		t.Fatalf("RunDesignPlan returned error: %v", err)
	}
	var decoded schemas.DesignPlanResult
	if err := json.Unmarshal([]byte(result.FinalAnswer), &decoded); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if !strings.HasPrefix(decoded.PlanID, "plan-") || len(decoded.PlanID) != len("plan-")+16 {
		t.Fatalf("generated plan id = %q, want plan- plus 16 hex chars", decoded.PlanID)
	}
	if len(decoded.CompletedTasks) != 1 || decoded.CompletedTasks[0].RunID != decoded.PlanID+"-t1" {
		t.Fatalf("completed tasks = %#v, want generated plan id prefix", decoded.CompletedTasks)
	}
}

func TestRunDesignPlanCancellationMidPlanReturnsCanceled(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	plan := designPlan([]schemas.Task{
		designTask("t1", "First", nil),
		designTask("t2", "Second", []string{"t1"}),
	})

	_, err := RunDesignPlan(ctx, plan, designCancellingProvider{cancel: cancel, cancelIntent: "Implement Second"}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		SessionID:      "plan-cancel",
		MaxTurns:       1,
	}, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

type designCancellingProvider struct {
	cancel       context.CancelFunc
	cancelIntent string
}

func (provider designCancellingProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	if designStageIntent(request) == provider.cancelIntent {
		provider.cancel()
		return nil, context.Canceled
	}
	return runFakeProvider{}.StreamCompletion(ctx, request)
}

type designFailingProvider struct {
	failIntent string
}

func (provider designFailingProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	toolName := ""
	if len(request.Tools) > 0 {
		toolName = request.Tools[0].Name
	}
	intent := designStageIntent(request)
	if intent == provider.failIntent && toolName == "submit_code" {
		ch := make(chan zeroruntime.StreamEvent, 4)
		ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "bad", ToolName: toolName}
		ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "bad", ArgumentsFragment: `{"files":`}
		ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "bad"}
		ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
		close(ch)
		return ch, nil
	}
	return runFakeProvider{}.StreamCompletion(ctx, request)
}

func designStageIntent(request zeroruntime.CompletionRequest) string {
	for index := len(request.Messages) - 1; index >= 0; index-- {
		message := request.Messages[index]
		if message.Role != zeroruntime.MessageRoleUser {
			continue
		}
		var input schemas.CodeWriterInput
		if err := json.Unmarshal([]byte(message.Content), &input); err == nil && input.Intent != "" {
			return input.Intent
		}
	}
	return ""
}

func designTask(id string, title string, dependsOn []string) schemas.Task {
	tier := schemas.TierTrivial
	return schemas.Task{
		ID:            id,
		Title:         title,
		Intent:        "Implement " + title,
		DependsOn:     append([]string(nil), dependsOn...),
		EstimatedTier: &tier,
	}
}

func TestRunDesignPlanWithResume_SkipsCompletedTasks(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	plan := designPlan([]schemas.Task{
		designTask("t1", "First", nil),
		designTask("t2", "Second", []string{"t1"}),
		designTask("t3", "Third", []string{"t1"}),
	})
	var reasoning []string

	result, err := RunDesignPlanWithResume(context.Background(), plan, designHappyProvider{workDir: workDir}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		SessionID:      "plan-resume",
		MaxTurns:       1,
		OnReasoning:    func(text string) { reasoning = append(reasoning, text) },
	}, nil, nil, RunDesignPlanOptions{
		PlanID:           "plan-resume",
		CompletedTaskIDs: []string{"t1"},
	})
	if err != nil {
		t.Fatalf("RunDesignPlanWithResume returned error: %v", err)
	}
	if result.Incomplete {
		t.Fatalf("expected completed plan, got incomplete: %s", result.IncompleteReason)
	}

	var decoded schemas.DesignPlanResult
	if err := json.Unmarshal([]byte(result.FinalAnswer), &decoded); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if decoded.Status != "completed" || len(decoded.CompletedTasks) != 3 {
		t.Fatalf("unexpected result: %#v", decoded)
	}
	if decoded.PlanID != "plan-resume" {
		t.Fatalf("plan id = %q, want plan-resume", decoded.PlanID)
	}

	taskStarts := 0
	for _, line := range reasoning {
		if strings.HasPrefix(line, "Starting task ") {
			taskStarts++
		}
	}
	if taskStarts != 2 {
		t.Fatalf("task progress lines = %d, want 2; reasoning=%q", taskStarts, strings.Join(reasoning, ""))
	}
}

func TestRunDesignPlanWithResume_CallbackFires(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	plan := designPlan([]schemas.Task{
		designTask("t1", "First", nil),
		designTask("t2", "Second", []string{"t1"}),
	})

	var callbackCalls []struct {
		TaskID string
		RunID  string
		Status string
	}
	callback := func(task schemas.Task, runID string, pipelineResult schemas.PipelineResult) {
		callbackCalls = append(callbackCalls, struct {
			TaskID string
			RunID  string
			Status string
		}{TaskID: task.ID, RunID: runID, Status: pipelineResult.Status})
	}

	result, err := RunDesignPlanWithResume(context.Background(), plan, designHappyProvider{workDir: workDir}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		SessionID:      "plan-callback",
		MaxTurns:       1,
	}, nil, nil, RunDesignPlanOptions{
		PlanID:          "plan-callback",
		OnTaskLifecycle: callback,
	})
	if err != nil {
		t.Fatalf("RunDesignPlanWithResume returned error: %v", err)
	}
	if result.Incomplete {
		t.Fatalf("expected completed plan, got incomplete: %s", result.IncompleteReason)
	}

	if len(callbackCalls) != 2 {
		t.Fatalf("callback calls = %d, want 2; calls=%#v", len(callbackCalls), callbackCalls)
	}
	for _, call := range callbackCalls {
		wantRunID := "plan-callback-" + call.TaskID
		if call.RunID != wantRunID {
			t.Fatalf("callback run id = %q, want %q", call.RunID, wantRunID)
		}
		if call.Status != "completed" {
			t.Fatalf("callback status = %q, want completed", call.Status)
		}
	}
}

type designCapturingProvider struct {
	runFakeProvider
	captured []zeroruntime.CompletionRequest
}

func (provider *designCapturingProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	provider.captured = append(provider.captured, request)
	return provider.runFakeProvider.StreamCompletion(ctx, request)
}

func TestRunDesignPlanWithResume_AcceptanceFactsInIntent(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	fact := schemas.AcceptanceFact{Statement: "it must return 42"}
	task := designTask("t1", "First", nil)
	task.AcceptanceFacts = []schemas.AcceptanceFact{fact}
	plan := designPlan([]schemas.Task{task})

	provider := &designCapturingProvider{}
	result, err := RunDesignPlanWithResume(context.Background(), plan, provider, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		SessionID:      "plan-facts",
		MaxTurns:       1,
	}, nil, nil, RunDesignPlanOptions{
		PlanID: "plan-facts",
	})
	if err != nil {
		t.Fatalf("RunDesignPlanWithResume returned error: %v", err)
	}
	if result.Incomplete {
		t.Fatalf("expected completed plan, got incomplete: %s", result.IncompleteReason)
	}

	found := false
	for _, request := range provider.captured {
		intent := designStageIntent(request)
		if strings.Contains(intent, "it must return 42") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("acceptance fact not found in any captured code writer intent; captured=%d", len(provider.captured))
	}
}

func TestRunDesignPlanWithResume_UniquePlanID(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	plan := designPlan([]schemas.Task{designTask("t1", "First", nil)})

	result1, err := RunDesignPlanWithResume(context.Background(), plan, runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		SessionID:      "plan-same-session",
		MaxTurns:       1,
	}, nil, nil, RunDesignPlanOptions{})
	if err != nil {
		t.Fatalf("first run returned error: %v", err)
	}
	result2, err := RunDesignPlanWithResume(context.Background(), plan, runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		SessionID:      "plan-same-session",
		MaxTurns:       1,
	}, nil, nil, RunDesignPlanOptions{})
	if err != nil {
		t.Fatalf("second run returned error: %v", err)
	}

	var decoded1, decoded2 schemas.DesignPlanResult
	if err := json.Unmarshal([]byte(result1.FinalAnswer), &decoded1); err != nil {
		t.Fatalf("parse first result: %v", err)
	}
	if err := json.Unmarshal([]byte(result2.FinalAnswer), &decoded2); err != nil {
		t.Fatalf("parse second result: %v", err)
	}
	if decoded1.PlanID == "" || decoded2.PlanID == "" {
		t.Fatalf("expected both plan ids to be non-empty")
	}
	if decoded1.PlanID == decoded2.PlanID {
		t.Fatalf("plan ids should be unique; both = %q", decoded1.PlanID)
	}
}

func designPlan(tasks []schemas.Task) schemas.DesignPlan {
	return schemas.DesignPlan{
		Epic:         "Execute a test design plan",
		Requirements: []string{"Run each task"},
		InScope:      []string{"Pipeline execution"},
		OutOfScope:   []string{"Worktrees"},
		SystemDesign: "Each task is independent.",
		Tasks:        tasks,
		Source:       "authored",
	}
}
