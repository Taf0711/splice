package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/sessions"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

const execDesignRequest = "Add a Hello function that returns a greeting."

// execDesignProvider answers the design stages with scripted typed output. It
// serves the plan for submit_design_plan and the critique for submit_critique.
// Each call takes the next scripted value; the last value repeats.
type execDesignProvider struct {
	mu        sync.Mutex
	plans     []schemas.DesignPlan
	critiques []schemas.PlanCritique
	calls     map[string]int
	requests  []string
	// reportedCost, when set, is the provider-reported charge of each request.
	reportedCost *float64
}

func newExecDesignProvider(plans []schemas.DesignPlan, critiques []schemas.PlanCritique) *execDesignProvider {
	return &execDesignProvider{plans: plans, critiques: critiques, calls: map[string]int{}}
}

func (provider *execDesignProvider) StreamCompletion(_ context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	toolName := ""
	if len(request.Tools) > 0 {
		toolName = request.Tools[0].Name
	}
	provider.mu.Lock()
	index := provider.calls[toolName]
	provider.calls[toolName]++
	var payload any
	switch toolName {
	case "submit_design_plan":
		provider.requests = append(provider.requests, lastUserContent(request))
		payload = provider.plans[min(index, len(provider.plans)-1)]
	case "submit_critique":
		payload = provider.critiques[min(index, len(provider.critiques)-1)]
	default:
		provider.mu.Unlock()
		return nil, fmt.Errorf("design provider got unexpected tool %q", toolName)
	}
	provider.mu.Unlock()

	args, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	ch := make(chan zeroruntime.StreamEvent, 8)
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 100, OutputTokens: 20}, ReportedCostUSD: provider.reportedCost}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "design_" + toolName, ToolName: toolName}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "design_" + toolName, ArgumentsFragment: string(args)}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "design_" + toolName}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func (provider *execDesignProvider) callCount(toolName string) int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.calls[toolName]
}

func (provider *execDesignProvider) seenRequests() []string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]string(nil), provider.requests...)
}

// toolCallRecorder counts the tool calls that reach a wrapped provider, so a
// test can prove which route served which stage.
type toolCallRecorder struct {
	inner zeroruntime.Provider
	mu    sync.Mutex
	calls map[string]int
}

func (recorder *toolCallRecorder) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	if len(request.Tools) > 0 {
		recorder.mu.Lock()
		recorder.calls[request.Tools[0].Name]++
		recorder.mu.Unlock()
	}
	return recorder.inner.StreamCompletion(ctx, request)
}

func (recorder *toolCallRecorder) callCount(toolName string) int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.calls[toolName]
}

func lastUserContent(request zeroruntime.CompletionRequest) string {
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role == zeroruntime.MessageRoleUser {
			return request.Messages[index].Content
		}
	}
	return ""
}

func execDesignCriticalCritique() schemas.PlanCritique {
	return schemas.PlanCritique{
		Critiques: []schemas.Critique{{
			Category: "correctness",
			Severity: schemas.SeverityHigh,
			Issue:    "the plan omits the error path",
		}},
		MustFixBeforeExecution: true,
		OverallAssessment:      "Add the error path before execution.",
	}
}

func execDesignPassingCritique() schemas.PlanCritique {
	return schemas.PlanCritique{
		Critiques: []schemas.Critique{{
			Category: "maintainability",
			Severity: schemas.SeverityLow,
			Issue:    "a name could be clearer",
		}},
		OverallAssessment: "The plan is ready.",
	}
}

// execDesignFixture wires one --design run. Each stage has its own route in
// stage-models.json, and each route reaches its own provider.
type execDesignFixture struct {
	cwd    string
	design *execDesignProvider
	critic *execDesignProvider
	writer *toolCallRecorder
}

func newExecDesignFixture(t *testing.T, plans []schemas.DesignPlan, critiques []schemas.PlanCritique) (execDesignFixture, string) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cwd := t.TempDir()
	userConfigPath := filepath.Join(t.TempDir(), "splice", "config.json")
	if err := os.MkdirAll(filepath.Dir(userConfigPath), 0o700); err != nil {
		t.Fatal(err)
	}
	stageModels := `{
  "default": {"provider_profile": "echo", "model": "echo-model"},
  "stages": {
    "design_crystallize": {"provider_profile": "design", "model": "design-model"},
    "plan_critic": {"provider_profile": "critic", "model": "critic-model"},
    "code_writer": {"provider_profile": "writer", "model": "writer-model"}
  }
}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(userConfigPath), "stage-models.json"), []byte(stageModels), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := execDesignFixture{
		cwd:    cwd,
		design: newExecDesignProvider(plans, nil),
		critic: newExecDesignProvider(nil, critiques),
		writer: &toolCallRecorder{inner: newExecStageAwareProvider(execStageProviderOptions{}), calls: map[string]int{}},
	}
	return fixture, userConfigPath
}

func (fixture execDesignFixture) run(t *testing.T, userConfigPath string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps(append([]string{"exec"}, args...), &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return fixture.cwd, nil },
		userConfigPath: func() (string, error) {
			return userConfigPath, nil
		},
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return execDesignResolvedConfig(), nil
		},
		newProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			switch profile.Model {
			case "design-model":
				return fixture.design, nil
			case "critic-model":
				return fixture.critic, nil
			case "writer-model":
				return fixture.writer, nil
			}
			return newExecStageAwareProvider(execStageProviderOptions{}), nil
		},
	})
	return exitCode, stdout.String(), stderr.String()
}

// execDesignResolvedConfig adds one provider profile per stage route.
func execDesignResolvedConfig() config.ResolvedConfig {
	cfg := execResolvedConfig()
	base := cfg.Provider
	cfg.Providers = []config.ProviderProfile{base}
	for _, name := range []string{"design", "critic", "writer"} {
		profile := base
		profile.Name = name
		profile.Model = name + "-model"
		cfg.Providers = append(cfg.Providers, profile)
	}
	return cfg
}

func designEvents(t *testing.T, stdout string, eventType string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, event := range decodeJSONLines(t, stdout) {
		if event["type"] == eventType {
			out = append(out, event)
		}
	}
	return out
}

// TestRunExecDesignRunsPlanAndRecordsStageUsage pins the happy path: the
// crystallizer, the critic, and the code writer each use their own route, the
// change is applied, the run verifies, and every design stage's usage carries
// its stage name.
func TestRunExecDesignRunsPlanAndRecordsStageUsage(t *testing.T) {
	plan := cliDesignPlan([]schemas.Task{
		cliDesignTask("t1", "First", nil),
		cliDesignTask("t2", "Second", []string{"t1"}),
	})
	fixture, userConfigPath := newExecDesignFixture(t, []schemas.DesignPlan{plan}, []schemas.PlanCritique{execDesignPassingCritique()})

	exitCode, stdout, stderr := fixture.run(t, userConfigPath, "--design", execDesignRequest, "--require-verified", "--output-format", "stream-json")
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d, stdout=%s stderr=%s", exitCode, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(fixture.cwd, "main.go")); err != nil {
		t.Fatalf("the plan did not apply its change: %v", err)
	}

	if got := fixture.design.callCount("submit_design_plan"); got != 1 {
		t.Fatalf("design route calls = %d, want 1", got)
	}
	if got := fixture.design.callCount("submit_critique"); got != 0 {
		t.Fatalf("design route received %d critique calls, want 0", got)
	}
	if got := fixture.critic.callCount("submit_critique"); got != 1 {
		t.Fatalf("critic route calls = %d, want 1", got)
	}
	if got := fixture.critic.callCount("submit_design_plan"); got != 0 {
		t.Fatalf("critic route received %d plan calls, want 0", got)
	}
	if got := fixture.writer.callCount("submit_code"); got < 2 {
		t.Fatalf("writer route submit_code calls = %d, want at least 2 (one per task)", got)
	}
	if requests := fixture.design.seenRequests(); len(requests) != 1 || !strings.Contains(requests[0], execDesignRequest) {
		t.Fatalf("design request does not carry the user request: %q", requests)
	}

	final := findJSONEvent(t, decodeJSONLines(t, stdout), "final")
	if !strings.Contains(final["text"].(string), `"status": "completed"`) {
		t.Fatalf("final event = %#v, want a completed plan result", final)
	}

	designPlan := designEvents(t, stdout, "design_plan")
	if len(designPlan) != 1 {
		t.Fatalf("design_plan events = %d, want 1: %s", len(designPlan), stdout)
	}
	tasks, _ := designPlan[0]["tasks"].([]any)
	if len(tasks) != 2 || tasks[0] != "First" || tasks[1] != "Second" {
		t.Fatalf("design_plan tasks = %#v, want the two task titles in order", tasks)
	}
	if designPlan[0]["revised"] != false || designPlan[0]["critiqueResolved"] != true {
		t.Fatalf("design_plan = %#v, want revised=false and critiqueResolved=true", designPlan[0])
	}

	stages := map[string]bool{}
	for _, event := range designEvents(t, stdout, "stage") {
		if event["status"] == "running" {
			stages[event["name"].(string)] = true
		}
	}
	if !stages["design_crystallize"] || !stages["plan_critic"] {
		t.Fatalf("stage events = %v, want design_crystallize and plan_critic running", stages)
	}

	usageStages := map[string]bool{}
	for _, event := range designEvents(t, stdout, "usage") {
		if stage, ok := event["stage"].(string); ok {
			usageStages[stage] = true
		}
	}
	if !usageStages["design_crystallize"] || !usageStages["plan_critic"] {
		t.Fatalf("usage stages = %v, want design_crystallize and plan_critic", usageStages)
	}

	// The session holds the same records that `splice usage` reads. The design
	// history opens with the marker, then the request.
	sessionID, _ := findJSONEvent(t, decodeJSONLines(t, stdout), "run_start")["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("run_start has no session id: %s", stdout)
	}
	sessionEvents, err := sessions.NewStore(sessions.StoreOptions{}).ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	var sawMarker bool
	sessionStages := map[string]bool{}
	for _, event := range sessionEvents {
		if event.Type == sessions.EventDesignModeEntered {
			sawMarker = true
		}
		if event.Type != sessions.EventUsage {
			continue
		}
		var payload map[string]any
		raw, _ := json.Marshal(event.Payload)
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode usage payload: %v", err)
		}
		if stage, ok := payload["stage"].(string); ok {
			sessionStages[stage] = true
		}
	}
	if !sawMarker {
		t.Fatal("the session has no design_mode_entered event")
	}
	if !sessionStages["design_crystallize"] || !sessionStages["plan_critic"] {
		t.Fatalf("session usage stages = %v, want design_crystallize and plan_critic", sessionStages)
	}
}

// TestRunExecDesignTextAndJSONCarryThePlan pins the non-stream formats: the
// final answer and the design summary reach their output.
func TestRunExecDesignTextAndJSONCarryThePlan(t *testing.T) {
	plan := cliDesignPlan([]schemas.Task{cliDesignTask("t1", "First", nil)})
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			fixture, userConfigPath := newExecDesignFixture(t, []schemas.DesignPlan{plan}, []schemas.PlanCritique{execDesignPassingCritique()})
			exitCode, stdout, stderr := fixture.run(t, userConfigPath, "--design", execDesignRequest, "--output-format", format)
			if exitCode != exitSuccess {
				t.Fatalf("exitCode = %d, stdout=%s stderr=%s", exitCode, stdout, stderr)
			}
			if format == "text" {
				if !strings.Contains(stdout, "Design plan completed") {
					t.Fatalf("text output = %q, want the plan completion", stdout)
				}
				if !strings.Contains(stderr, "[splice] design: 1 task") || !strings.Contains(stderr, "First") {
					t.Fatalf("stderr = %q, want the design summary with the task title", stderr)
				}
				return
			}
			events := designEvents(t, stdout, "design_plan")
			if len(events) != 1 {
				t.Fatalf("json design_plan events = %d: %s", len(events), stdout)
			}
		})
	}
}

// TestRunExecDesignRevisesOnceWhenCriticBlocks pins the single revision: a
// blocking critique runs one more crystallization with the critique in
// context, and a clean second critique lets the plan run.
func TestRunExecDesignRevisesOnceWhenCriticBlocks(t *testing.T) {
	first := cliDesignPlan([]schemas.Task{cliDesignTask("t1", "Draft", nil)})
	revised := cliDesignPlan([]schemas.Task{cliDesignTask("t1", "Revised", nil)})
	fixture, userConfigPath := newExecDesignFixture(t,
		[]schemas.DesignPlan{first, revised},
		[]schemas.PlanCritique{execDesignCriticalCritique(), execDesignPassingCritique()},
	)

	exitCode, stdout, stderr := fixture.run(t, userConfigPath, "--design", execDesignRequest, "--output-format", "stream-json")
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d, stdout=%s stderr=%s", exitCode, stdout, stderr)
	}
	if got := fixture.design.callCount("submit_design_plan"); got != 2 {
		t.Fatalf("crystallize calls = %d, want 2 (one revision)", got)
	}
	if got := fixture.critic.callCount("submit_critique"); got != 2 {
		t.Fatalf("critic calls = %d, want 2", got)
	}
	requests := fixture.design.seenRequests()
	if len(requests) != 2 || !strings.Contains(requests[1], "the plan omits the error path") {
		t.Fatalf("the revision did not see the blocking critique: %q", requests)
	}
	designPlan := designEvents(t, stdout, "design_plan")
	if len(designPlan) != 1 {
		t.Fatalf("design_plan events = %d", len(designPlan))
	}
	if designPlan[0]["revised"] != true || designPlan[0]["critiqueResolved"] != true {
		t.Fatalf("design_plan = %#v, want revised=true and critiqueResolved=true", designPlan[0])
	}
	tasks, _ := designPlan[0]["tasks"].([]any)
	if len(tasks) != 1 || tasks[0] != "Revised" {
		t.Fatalf("design_plan tasks = %#v, want the revised plan", tasks)
	}
}

// TestRunExecDesignProceedsWhenCriticStillBlocks pins the unresolved path: a
// critique that still blocks after one revision does not stop a plan that has
// tasks, but the output records that the critique was not resolved.
func TestRunExecDesignProceedsWhenCriticStillBlocks(t *testing.T) {
	plan := cliDesignPlan([]schemas.Task{cliDesignTask("t1", "First", nil)})
	fixture, userConfigPath := newExecDesignFixture(t,
		[]schemas.DesignPlan{plan},
		[]schemas.PlanCritique{execDesignCriticalCritique(), execDesignCriticalCritique()},
	)

	exitCode, stdout, stderr := fixture.run(t, userConfigPath, "--design", execDesignRequest, "--output-format", "stream-json")
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d, stdout=%s stderr=%s", exitCode, stdout, stderr)
	}
	if got := fixture.design.callCount("submit_design_plan"); got != 2 {
		t.Fatalf("crystallize calls = %d, want 2", got)
	}
	designPlan := designEvents(t, stdout, "design_plan")
	if len(designPlan) != 1 || designPlan[0]["critiqueResolved"] != false || designPlan[0]["revised"] != true {
		t.Fatalf("design_plan = %#v, want revised=true and critiqueResolved=false", designPlan)
	}
	warned := false
	for _, event := range designEvents(t, stdout, "warning") {
		if strings.Contains(event["message"].(string), "not resolved") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no warning names the unresolved critique: %s", stdout)
	}
	final := findJSONEvent(t, decodeJSONLines(t, stdout), "final")
	if !strings.Contains(final["text"].(string), `"status": "completed"`) {
		t.Fatalf("the plan did not run after the unresolved critique: %#v", final)
	}
}

// TestRunExecDesignStageFailureExitsProvider pins the fail-loud path: a design
// stage output that does not validate stops the run with the provider exit
// code and names the cause. The validator rejects a plan with no task, so the
// stage never reaches the plan runner with an empty plan.
func TestRunExecDesignStageFailureExitsProvider(t *testing.T) {
	empty := cliDesignPlan(nil)
	fixture, userConfigPath := newExecDesignFixture(t, []schemas.DesignPlan{empty}, []schemas.PlanCritique{execDesignPassingCritique()})

	exitCode, stdout, stderr := fixture.run(t, userConfigPath, "--design", execDesignRequest)
	if exitCode != exitProvider {
		t.Fatalf("exitCode = %d, want %d; stdout=%s stderr=%s", exitCode, exitProvider, stdout, stderr)
	}
	if !strings.Contains(stderr, "at least one task") {
		t.Fatalf("stderr = %q, want the task requirement", stderr)
	}
	if got := fixture.writer.callCount("submit_code"); got != 0 {
		t.Fatalf("the code writer ran %d time(s) after a failed design", got)
	}
}

// TestRunExecDesignUsageErrors pins the flag conflicts. Each one is a usage
// error (exit 2) before any provider request.
func TestRunExecDesignUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "plan", args: []string{"exec", "--design", "x", "--plan", "plan.json"}, want: "--design cannot be combined with --plan."},
		{name: "use spec", args: []string{"exec", "--design", "x", "--use-spec"}, want: "--design cannot be combined with --use-spec."},
		{name: "loop", args: []string{"exec", "--design", "x", "--loop"}, want: "--design cannot be combined with --loop."},
		{name: "pipeline", args: []string{"exec", "--design", "x", "--pipeline", "p.json"}, want: "--design cannot be combined with --pipeline."},
		{name: "prompt", args: []string{"exec", "--design", "x", "do it"}, want: "--design cannot be combined with a prompt argument."},
		{name: "file", args: []string{"exec", "--design", "x", "--file", "prompt.txt"}, want: "--design cannot be combined with --file."},
		{name: "stream input", args: []string{"exec", "--design", "x", "--input-format", "stream-json"}, want: "--design cannot be combined with --input-format stream-json."},
		{name: "resume", args: []string{"exec", "--design", "x", "--resume", "session-1"}, want: "--design cannot be combined with --resume or --fork."},
		{name: "fork", args: []string{"exec", "--design", "x", "--fork", "session-1"}, want: "--design cannot be combined with --resume or --fork."},
		{name: "no value", args: []string{"exec", "--design"}, want: "--design requires a value"},
		{name: "empty inline value", args: []string{"exec", "--design="}, want: "--design requires a value"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := Run(tc.args, &stdout, &stderr)
			if exitCode != exitUsage {
				t.Fatalf("exitCode = %d, want %d; stderr=%s", exitCode, exitUsage, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

// TestRunExecDesignHelpListsFlag keeps the help text in step with the flag.
func TestRunExecDesignHelpListsFlag(t *testing.T) {
	var out bytes.Buffer
	if err := writeExecHelp(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--design <request>") {
		t.Fatalf("exec help does not list --design:\n%s", out.String())
	}
}

// TestRunExecDesignPricesDesignUsageFromTheReportedCharge pins the cost rule
// for design stages. A provider-reported charge prices the request as a
// pipeline stage request is priced: a priced status with reported provenance,
// and no catalog lookup.
func TestRunExecDesignPricesDesignUsageFromTheReportedCharge(t *testing.T) {
	plan := cliDesignPlan([]schemas.Task{cliDesignTask("t1", "First", nil)})
	fixture, userConfigPath := newExecDesignFixture(t, []schemas.DesignPlan{plan}, []schemas.PlanCritique{execDesignPassingCritique()})
	charge := 0.5
	fixture.design.reportedCost = &charge
	fixture.critic.reportedCost = &charge

	exitCode, stdout, stderr := fixture.run(t, userConfigPath, "--design", execDesignRequest, "--output-format", "stream-json")
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d, stdout=%s stderr=%s", exitCode, stdout, stderr)
	}
	seen := map[string]bool{}
	for _, event := range designEvents(t, stdout, "usage") {
		stage, _ := event["stage"].(string)
		if stage != "design_crystallize" && stage != "plan_critic" {
			continue
		}
		seen[stage] = true
		if event["costStatus"] != "priced" || event["costProvenance"] != "reported" {
			t.Fatalf("%s usage = %#v, want costStatus priced and costProvenance reported", stage, event)
		}
		if got, _ := event["costUsd"].(float64); got != charge {
			t.Fatalf("%s costUsd = %v, want %v", stage, event["costUsd"], charge)
		}
	}
	if !seen["design_crystallize"] || !seen["plan_critic"] {
		t.Fatalf("design usage stages = %v, want both design stages", seen)
	}
}

// TestRunExecDesignRecordsPlanExecution pins the session state after a design
// run. The plan is approved and executed, so a later /resume reconstructs the
// design as completed, not as a plan that still awaits approval. The final
// result JSON is not stored as a design turn.
func TestRunExecDesignRecordsPlanExecution(t *testing.T) {
	plan := cliDesignPlan([]schemas.Task{
		cliDesignTask("t1", "First", nil),
		cliDesignTask("t2", "Second", []string{"t1"}),
	})
	fixture, userConfigPath := newExecDesignFixture(t, []schemas.DesignPlan{plan}, []schemas.PlanCritique{execDesignPassingCritique()})

	exitCode, stdout, stderr := fixture.run(t, userConfigPath, "--design", execDesignRequest, "--output-format", "stream-json")
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d, stdout=%s stderr=%s", exitCode, stdout, stderr)
	}
	sessionID, _ := findJSONEvent(t, decodeJSONLines(t, stdout), "run_start")["sessionId"].(string)
	events, err := sessions.NewStore(sessions.StoreOptions{}).ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	counts := map[sessions.EventType]int{}
	for _, event := range events {
		counts[event.Type]++
	}
	if counts[sessions.EventPlanApproved] != 1 {
		t.Fatalf("plan_approved events = %d, want 1", counts[sessions.EventPlanApproved])
	}
	if counts[sessions.EventTaskStarted] != 2 || counts[sessions.EventTaskCompleted] != 2 {
		t.Fatalf("task events started=%d completed=%d, want 2 and 2", counts[sessions.EventTaskStarted], counts[sessions.EventTaskCompleted])
	}
	state, err := splicerun.ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("reconstruct design state: %v", err)
	}
	if state.Phase != schemas.DesignPhaseCompleted {
		t.Fatalf("design phase = %q, want %q: a /resume would show the plan as pending", state.Phase, schemas.DesignPhaseCompleted)
	}
	for _, message := range splicerun.MapDesignHistory(events) {
		if strings.Contains(message.Content, `"plan_id"`) {
			t.Fatalf("the final result JSON was stored as a design turn: %q", message.Content)
		}
	}
}

// TestRunExecDesignRejectsBadRoutesBeforeAnyRequest pins the route preflight. A
// route that does not resolve is invalid input, so it exits 2 as it does for
// --plan. It stops the run before the first provider request, whichever stage
// has the bad route.
func TestRunExecDesignRejectsBadRoutesBeforeAnyRequest(t *testing.T) {
	plan := cliDesignPlan([]schemas.Task{cliDesignTask("t1", "First", nil)})
	for _, tc := range []struct {
		name   string
		routes string
	}{
		{name: "design_crystallize", routes: `{"default":{"provider_profile":"echo","model":"echo-model"},"stages":{"design_crystallize":{"provider_profile":"missing","model":"x"}}}`},
		{name: "code_writer", routes: `{"default":{"provider_profile":"echo","model":"echo-model"},"stages":{"code_writer":{"provider_profile":"missing","model":"x"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, userConfigPath := newExecDesignFixture(t, []schemas.DesignPlan{plan}, []schemas.PlanCritique{execDesignPassingCritique()})
			if err := os.WriteFile(filepath.Join(filepath.Dir(userConfigPath), "stage-models.json"), []byte(tc.routes), 0o600); err != nil {
				t.Fatal(err)
			}
			exitCode, stdout, stderr := fixture.run(t, userConfigPath, "--design", execDesignRequest)
			if exitCode != exitUsage {
				t.Fatalf("exitCode = %d, want %d; stdout=%s stderr=%s", exitCode, exitUsage, stdout, stderr)
			}
			if !strings.Contains(stderr, "stage "+tc.name+" model") {
				t.Fatalf("stderr = %q, want the route error to name stage %s", stderr, tc.name)
			}
			if n := fixture.design.callCount("submit_design_plan") + fixture.critic.callCount("submit_critique") + fixture.writer.callCount("submit_code"); n != 0 {
				t.Fatalf("%d provider request(s) sent before the route error, want 0", n)
			}
		})
	}
}

// TestRunExecDesignRevisionErrorExitsProvider pins the revision failure: when
// the revision cannot produce a valid plan, the run stops with the provider
// exit code and does not run a task.
func TestRunExecDesignRevisionErrorExitsProvider(t *testing.T) {
	first := cliDesignPlan([]schemas.Task{cliDesignTask("t1", "Draft", nil)})
	invalid := cliDesignPlan(nil)
	fixture, userConfigPath := newExecDesignFixture(t,
		[]schemas.DesignPlan{first, invalid},
		[]schemas.PlanCritique{execDesignCriticalCritique()},
	)
	exitCode, stdout, stderr := fixture.run(t, userConfigPath, "--design", execDesignRequest)
	if exitCode != exitProvider {
		t.Fatalf("exitCode = %d, want %d; stdout=%s stderr=%s", exitCode, exitProvider, stdout, stderr)
	}
	if got := fixture.design.callCount("submit_design_plan"); got < 2 {
		t.Fatalf("crystallize calls = %d, want the revision to run", got)
	}
	if got := fixture.writer.callCount("submit_code"); got != 0 {
		t.Fatalf("the code writer ran %d time(s) after a failed revision", got)
	}
}

// TestRunExecDesignRequiresASessionStore pins the session guard. When the
// session cannot be recorded, a design run stops before the first request and
// names the cause.
func TestRunExecDesignRequiresASessionStore(t *testing.T) {
	plan := cliDesignPlan([]schemas.Task{cliDesignTask("t1", "First", nil)})
	fixture, userConfigPath := newExecDesignFixture(t, []schemas.DesignPlan{plan}, []schemas.PlanCritique{execDesignPassingCritique()})
	// A regular file where the data directory belongs makes the session store fail.
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", blocked)
	exitCode, stdout, stderr := fixture.run(t, userConfigPath, "--design", execDesignRequest)
	if exitCode != exitCrash {
		t.Fatalf("exitCode = %d, want %d; stdout=%s stderr=%s", exitCode, exitCrash, stdout, stderr)
	}
	if !strings.Contains(stderr, "--design needs a session") {
		t.Fatalf("stderr = %q, want the session guard message", stderr)
	}
	if n := fixture.design.callCount("submit_design_plan"); n != 0 {
		t.Fatalf("%d crystallize request(s) sent without a session, want 0", n)
	}
}

// TestRunExecDesignReportsTheCritiqueIssueCount pins the issue count in the
// design_plan event: it counts every critique issue, not only the blocking ones.
func TestRunExecDesignReportsTheCritiqueIssueCount(t *testing.T) {
	plan := cliDesignPlan([]schemas.Task{cliDesignTask("t1", "First", nil)})
	critique := execDesignPassingCritique()
	critique.Critiques = append(critique.Critiques, schemas.Critique{
		Category: "operability",
		Severity: schemas.SeverityInfo,
		Issue:    "the log line has no id",
	})
	fixture, userConfigPath := newExecDesignFixture(t, []schemas.DesignPlan{plan}, []schemas.PlanCritique{critique})
	exitCode, stdout, stderr := fixture.run(t, userConfigPath, "--design", execDesignRequest, "--output-format", "stream-json")
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d, stdout=%s stderr=%s", exitCode, stdout, stderr)
	}
	events := designEvents(t, stdout, "design_plan")
	if len(events) != 1 || events[0]["critiqueIssues"] != float64(2) {
		t.Fatalf("design_plan = %#v, want critiqueIssues 2", events)
	}
}

// TestRunExecDesignFailedTaskIsRecordedAsFailed pins the task lifecycle for a
// failing task. The code writer fails, so the session records task_failed for
// that task, and the run does not report success.
func TestRunExecDesignFailedTaskIsRecordedAsFailed(t *testing.T) {
	plan := cliDesignPlan([]schemas.Task{cliDesignTask("t1", "First", nil)})
	fixture, userConfigPath := newExecDesignFixture(t, []schemas.DesignPlan{plan}, []schemas.PlanCritique{execDesignPassingCritique()})
	fixture.writer = &toolCallRecorder{inner: rejectingToolProvider{inner: newExecStageAwareProvider(execStageProviderOptions{}), tool: "submit_code"}, calls: map[string]int{}}

	exitCode, stdout, stderr := fixture.run(t, userConfigPath, "--design", execDesignRequest, "--output-format", "stream-json")
	if exitCode == exitSuccess {
		t.Fatalf("a failing task must not exit 0; stdout=%s stderr=%s", stdout, stderr)
	}
	sessionID, _ := findJSONEvent(t, decodeJSONLines(t, stdout), "run_start")["sessionId"].(string)
	if n := countStoredEvents(t, sessionID, sessions.EventTaskFailed); n != 1 {
		t.Fatalf("task_failed events = %d, want 1", n)
	}
	if n := countStoredEvents(t, sessionID, sessions.EventTaskCompleted); n != 0 {
		t.Fatalf("task_completed events = %d, want 0 for a failing task", n)
	}
}

// countStoredEvents counts the events of one type in a stored session.
func countStoredEvents(t *testing.T, sessionID string, eventType sessions.EventType) int {
	t.Helper()
	events, err := sessions.NewStore(sessions.StoreOptions{}).ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

// TestRunExecDesignWarnsOnTierFallback pins the fallback report. A design stage
// whose tier layer fails runs on the run model, and the run says so.
func TestRunExecDesignWarnsOnTierFallback(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cwd := t.TempDir()
	store := sessions.NewStore(sessions.StoreOptions{})
	meta, err := store.Create(sessions.CreateInput{Title: "design fallback", Cwd: cwd})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := store.AppendEvent(meta.SessionID, sessions.AppendEventInput{Type: sessions.EventDesignModeEntered}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(meta.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]any{"role": "user", "content": execDesignRequest}}); err != nil {
		t.Fatal(err)
	}

	plan := cliDesignPlan([]schemas.Task{cliDesignTask("t1", "First", nil)})
	design := newExecDesignProvider([]schemas.DesignPlan{plan}, nil)
	critic := newExecDesignProvider(nil, []schemas.PlanCritique{execDesignPassingCritique()})
	resolver := func(stage string) (agent.ModelSelection, error) {
		switch stage {
		case "design_crystallize":
			return agent.ModelSelection{ProviderName: "design", Model: "design-model", Provider: design}, nil
		case "plan_critic":
			return agent.ModelSelection{}, splicerun.NewTierFallbackError(errors.New("tier model unavailable"))
		}
		return agent.ModelSelection{}, nil
	}
	var stdout, stderr bytes.Buffer
	writer := &execEventWriter{stdout: &stdout, stderr: &stderr, format: execOutputText, runID: "run-fallback", streamedText: &strings.Builder{}}
	_, _ = runExecDesign(context.Background(), execDesignRun{
		now:           time.Now(),
		store:         store,
		sessionID:     meta.SessionID,
		workspaceRoot: cwd,
		provider:      critic,
		stageResolver: resolver,
		primaryName:   "echo",
		primaryModel:  "echo-model",
		options:       agent.Options{Cwd: cwd},
		writer:        writer,
		record: func(eventType sessions.EventType, payload any) {
			_, _ = store.AppendEvent(meta.SessionID, sessions.AppendEventInput{Type: eventType, Payload: payload})
		},
	})
	if !strings.Contains(stderr.String(), "design stage uses the run model") || !strings.Contains(stderr.String(), "plan_critic") {
		t.Fatalf("stderr = %q, want the tier fallback warning naming plan_critic", stderr.String())
	}
}

// rejectingToolProvider fails one tool with an ordinary upstream error. A
// cancelled request would read as an interrupt, not as a failed task.
type rejectingToolProvider struct {
	inner zeroruntime.Provider
	tool  string
}

func (provider rejectingToolProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	if len(request.Tools) > 0 && request.Tools[0].Name == provider.tool {
		return nil, errors.New("upstream rejected the request")
	}
	return provider.inner.StreamCompletion(ctx, request)
}
