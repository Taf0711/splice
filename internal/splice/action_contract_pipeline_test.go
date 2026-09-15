package splice

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// The P1 acceptance pair for the two-tool action contract: a valid
// expansion is fulfilled by the host and the FOLLOWING model request
// carries the fetched source, then a valid submission applies and the
// pass completes with ZERO format retries. The companion negative proves
// a both-tools response triggers no side effect.

// actionSequenceProvider plays one scripted response per provider request
// and records every request for assertions.
type actionSequenceProvider struct {
	mu       sync.Mutex
	requests []zeroruntime.CompletionRequest
	steps    [][]zeroruntime.StreamEvent
}

func (p *actionSequenceProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.mu.Lock()
	index := len(p.requests)
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	var events []zeroruntime.StreamEvent
	if index < len(p.steps) {
		events = p.steps[index]
	} else {
		events = []zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventDone}}
	}
	ch := make(chan zeroruntime.StreamEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func contextToolCallEvents(args string) []zeroruntime.StreamEvent {
	return []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "c1", ToolName: "request_codebase_context"},
		{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "c1", ArgumentsFragment: args},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "c1"},
		{Type: zeroruntime.StreamEventDone},
	}
}

func submitToolCallEvents(args string) []zeroruntime.StreamEvent {
	return []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "s1", ToolName: "submit_code"},
		{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "s1", ArgumentsFragment: args},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "s1"},
		{Type: zeroruntime.StreamEventDone},
	}
}

// actionRecoveryRunner fulfills the bounded queries both the initial
// handshake and the expansion produce, through the same guarded tool
// names the registry exposes.
type actionRecoveryRunner struct {
	workDir string
	reads   []string
}

func (r *actionRecoveryRunner) RunTool(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	if name == "write_file" {
		path, _ := args["path"].(string)
		content, _ := args["content"].(string)
		if path == "" {
			return ToolResult{OK: false, Output: "write_file: path required"}, nil
		}
		target := filepath.Join(r.workDir, path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return ToolResult{OK: false, Output: err.Error()}, nil
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return ToolResult{OK: false, Output: err.Error()}, nil
		}
		return ToolResult{OK: true, Output: "written"}, nil
	}
	switch name {
	case "read_file":
		if p, ok := args["path"].(string); ok {
			r.reads = append(r.reads, p)
		}
		return ToolResult{OK: true, Output: "package main\n\nfunc runClockTable(t *testing.T) {}\n"}, nil
	case "list_directory":
		return ToolResult{OK: true, Output: "clock.go\n  clock_test.go\n"}, nil
	case "grep":
		return ToolResult{OK: true, Output: "clock_test.go:4:func runClockTable(t *testing.T) {}\n"}, nil
	default:
		return ToolResult{OK: true, Output: ""}, nil
	}
}

func actionRecoveryPlan() schemas.ExecutionPlan {
	return schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: readTaskIntent,
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}
}

// TestActionContractExpansionThenSubmissionAppliesWithZeroFormatRetries is
// the P1 acceptance: valid expansion, host fulfillment into the next
// request, valid submission, application, pass completion, and zero
// format retries.
func TestActionContractExpansionThenSubmissionAppliesWithZeroFormatRetries(t *testing.T) {
	t.Setenv(EvidenceSubstitutionEnvVar, "on")
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "clock.go"), []byte(prodSiblingBody), 0o644); err != nil {
		t.Fatal(err)
	}

	contextArgs := `{"reason":"the intent reuses runClockTable but its signature was not delivered","queries":[{"query_type":"read_file","path":"clock_test.go","max_results":5,"max_chars":4000}]}`
	submission := schemas.CodeWriterOutput{
		Files:      []schemas.FileChange{{Path: "expiry_test.go", ChangeType: "create", Content: "package main\n\nimport \"testing\"\n\nfunc TestStoreExpiryBoundary(t *testing.T) { runClockTable(t) }\n"}},
		Language:   "go",
		Intent:     "add the expiry boundary test",
		Confidence: 0.9,
	}
	submitArgs, err := json.Marshal(submission)
	if err != nil {
		t.Fatal(err)
	}
	provider := &actionSequenceProvider{steps: [][]zeroruntime.StreamEvent{
		contextToolCallEvents(contextArgs),
		submitToolCallEvents(string(submitArgs)),
	}}
	options := PipelineConfigFromAgentOptions(agent.Options{})
	// A format retry is another provider request: the request count is the
	// observable, so zero retries means exactly one request per action.
	runner := &actionRecoveryRunner{workDir: workDir}

	tr := newRunTraceAccumulator(nil, "action-recovery", "session", workDir, actionRecoveryPlan(), "active", nil)
	records, _, completed, err := runPass(context.Background(), "action-recovery", 1, actionRecoveryPlan(),
		stageRegistry{"code_writer": stages.CodeWriter{}}, provider, options, workDir, runner, time.Time{}, nil, nil, tr, NewStageExecutionBudget(0))
	if err != nil || !completed {
		t.Fatalf("pass: completed=%v err=%v records=%+v", completed, err, records)
	}

	// Exactly two provider requests: one per action, zero format retries.
	if got := len(provider.requests); got != 2 {
		t.Fatalf("provider requests = %d, want 2 (expansion plus submission; more means the contract retried)", got)
	}

	// Provider seam: both typed tools offered, and any-tool forcing on the
	// forced attempt.
	first := provider.requests[0]
	toolNames := make([]string, 0, len(first.Tools))
	for _, tool := range first.Tools {
		toolNames = append(toolNames, tool.Name)
	}
	if len(toolNames) != 2 || toolNames[0] != "submit_code" || toolNames[1] != "request_codebase_context" {
		t.Fatalf("first request tools = %v, want both typed tools", toolNames)
	}
	if !first.ToolChoiceRequired {
		t.Fatalf("first request must require some tool call; ToolChoiceRequired = false")
	}

	// The fetched source reaches the second request's user payload, so the
	// model can use the declaration without another expansion.
	second := provider.requests[1]
	payload := ""
	for _, m := range second.Messages {
		if m.Role == zeroruntime.MessageRoleUser {
			payload += m.Content
		}
	}
	if !strings.Contains(payload, "runClockTable") {
		t.Fatalf("the fulfilled read did not reach the submission request's payload")
	}

	// The submitted file is applied to the workspace.
	written, err := os.ReadFile(filepath.Join(workDir, "expiry_test.go"))
	if err != nil {
		t.Fatalf("applied file missing: %v", err)
	}
	if !strings.Contains(string(written), "runClockTable(t)") {
		t.Fatalf("applied file lost its content: %q", written)
	}

	// The delivered bundle reached the stage: the second invocation's
	// stage record carries the merged context items. The per-round trace
	// map is recorded only for rounds the budget counts, and the ledger's
	// context_round attribution is the wired round signal.
}

// TestActionContractBothToolCallsRejectedWithoutSideEffect is the negative:
// a response that calls both tools is a conflict. It must exhaust the typed
// retries, apply nothing, and leave the workspace untouched.
func TestActionContractBothToolCallsRejectedWithoutSideEffect(t *testing.T) {
	t.Setenv(EvidenceSubstitutionEnvVar, "on")
	workDir := t.TempDir()
	contextArgs := `{"reason":"need the helper signature","queries":[{"query_type":"read_file","path":"clock_test.go","max_results":5,"max_chars":4000}]}`
	submission := schemas.CodeWriterOutput{
		Files:      []schemas.FileChange{{Path: "expiry_test.go", ChangeType: "create", Content: "package main\n"}},
		Language:   "go",
		Intent:     "add the expiry boundary test",
		Confidence: 0.9,
	}
	submitArgs, _ := json.Marshal(submission)

	// One stream carries BOTH tool calls: the exact-one-action violation.
	mixed := append(append([]zeroruntime.StreamEvent{}, contextToolCallEvents(contextArgs)[:3]...), submitToolCallEvents(string(submitArgs))...)
	provider := &actionSequenceProvider{steps: [][]zeroruntime.StreamEvent{mixed, mixed, mixed, mixed}}
	options := PipelineConfigFromAgentOptions(agent.Options{})
	runner := &actionRecoveryRunner{workDir: workDir}

	records, _, completed, err := runPass(context.Background(), "action-conflict", 1, actionRecoveryPlan(),
		stageRegistry{"code_writer": stages.CodeWriter{}}, provider, options, workDir, runner, time.Time{}, nil, nil, nil, NewStageExecutionBudget(0))
	if err != nil {
		t.Fatalf("runPass: %v", err)
	}
	if completed {
		t.Fatal("a both-tool conflict must not complete the pass")
	}
	failed := false
	for _, rec := range records {
		if rec.Status == schemas.StageFailed {
			failed = true
			if rec.OutputSummary != nil && !strings.Contains(*rec.OutputSummary, "exactly one action") {
				t.Fatalf("failed record does not name the conflict: %q", *rec.OutputSummary)
			}
		}
	}
	if !failed {
		t.Fatalf("no failed stage record for the conflict: %+v", records)
	}
	// The conflict consumed the typed retries: three attempts, each a
	// billed provider request.
	if got := len(provider.requests); got != 3 {
		t.Fatalf("provider requests = %d, want 3 (maxTypedToolAttempts exhausted on the conflict)", got)
	}
	if _, statErr := os.Stat(filepath.Join(workDir, "expiry_test.go")); !os.IsNotExist(statErr) {
		t.Fatalf("conflicted response applied a file; statErr=%v", statErr)
	}
	if len(records) == 0 {
		t.Fatal("the failed attempt produced no stage record; spend must stay visible")
	}
}
