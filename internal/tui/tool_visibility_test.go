package tui

// Checkpoint A of the "make tool activity visible" workstream: prove and
// repair the existing tool-row flow — a tool call changes the visible view
// before it completes, the working line names the active tool, running shell
// cards show the exact wrapped command plus a per-call elapsed clock (no
// second timer), long generic results preview 3 useful lines with an accurate
// hidden count and both disclosure routes (click + Ctrl+O), explore output
// stays compact, failures stay visible, and the active-tool state clears on
// result, cancellation, and run end.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// gatedTool is a registry tool whose execution blocks until the test releases
// it, so a test can observe the TUI mid-call (after OnToolCall, before
// OnToolResult).
type gatedTool struct {
	name    string
	release chan struct{}
}

func (tool *gatedTool) Name() string        { return tool.name }
func (tool *gatedTool) Description() string { return "gated test tool" }
func (tool *gatedTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", Properties: map[string]tools.PropertySchema{}, AdditionalProperties: true}
}
func (tool *gatedTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectNone, Permission: tools.PermissionAllow}
}
func (tool *gatedTool) Run(ctx context.Context, args map[string]any) tools.Result {
	return tool.run(ctx)
}

func (tool *gatedTool) RunWithOptions(ctx context.Context, _ map[string]any, options tools.RunOptions) tools.Result {
	if options.OnToolOutput != nil {
		options.OnToolOutput(tools.OutputSnapshot{ToolCallID: options.ToolCallID, Output: "live output before completion\n"})
	}
	return tool.run(ctx)
}

func (tool *gatedTool) run(ctx context.Context) tools.Result {
	select {
	case <-tool.release:
		return tools.Result{Status: tools.StatusOK, Output: "gated done"}
	case <-ctx.Done():
		return tools.Result{Status: tools.StatusError, Output: "gated cancelled"}
	}
}

// TestToolCallChangesVisibleViewBeforeCompletion is the fake-provider proof:
// a provider pauses after OnToolCall (the tool blocks in Run), and the live
// model already shows the running card and names the tool on the working line
// before the result exists. Releasing the tool completes the turn.
func TestToolCallChangesVisibleViewBeforeCompletion(t *testing.T) {
	release := make(chan struct{})
	tool := &gatedTool{name: "bash", release: release}
	registry := tools.NewRegistry()
	registry.Register(tool)
	provider := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{
		{
			{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call_1", ToolName: "bash"},
			{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call_1", ArgumentsFragment: `{"command":"sleep"}`},
			{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call_1"},
			{Type: zeroruntime.StreamEventDone},
		},
		{{Type: zeroruntime.StreamEventDone}},
	}}
	runtimeMessageCh := make(chan tea.Msg, 16)
	m := newModel(context.Background(), Options{
		Cwd:          t.TempDir(),
		ProviderName: "openai",
		ModelName:    "gpt-test",
		Provider:     provider,
		Registry:     registry,
		RuntimeMessageSink: func(msg tea.Msg) {
			runtimeMessageCh <- msg
		},
	})
	m.designMode = false
	m.width = 80
	m.height = 30
	m.input.SetValue("run the gated tool")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}
	finalCh := make(chan tea.Msg, 1)
	go func() { finalCh <- execCmd(cmd) }()

	// Drain runtime messages until the tool-call row lands. The tool is still
	// blocked in Run, so no result row can exist yet.
	deadline := time.After(10 * time.Second)
	callRowSeen := false
	for !callRowSeen {
		select {
		case msg := <-runtimeMessageCh:
			updated, _ = next.Update(msg)
			next = updated.(model)
			if row, ok := msg.(agentRowMsg); ok && row.row.kind == rowToolCall {
				callRowSeen = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for the tool call row")
		}
	}

	// The next runtime event is the ephemeral output snapshot. It must update the
	// running card without adding a transcript or session row.
	transcriptRows := len(next.transcript)
	select {
	case msg := <-runtimeMessageCh:
		updated, _ = next.Update(msg)
		next = updated.(model)
		if _, ok := msg.(toolOutputSnapshotMsg); !ok {
			t.Fatalf("message after tool call = %T, want toolOutputSnapshotMsg", msg)
		}
	case <-deadline:
		t.Fatal("timed out waiting for live tool output")
	}
	if len(next.transcript) != transcriptRows {
		t.Fatal("live output must remain ephemeral")
	}

	// The tool has NOT completed: the working line names it, and the running
	// card already includes the live output.
	if got := next.workingActivity(); got != "running bash" {
		t.Fatalf("working activity before completion = %q, want %q", got, "running bash")
	}
	runningCard := ""
	for _, row := range next.transcript {
		if row.kind == rowToolCall {
			runningCard = plainRender(t, next.renderRow(row, 80, buildRowContext(next.transcript)))
		}
	}
	if runningCard == "" {
		t.Fatal("expected a running tool card in the live transcript")
	}
	for _, want := range []string{"Running", "live output before completion"} {
		if !strings.Contains(runningCard, want) {
			t.Fatalf("running card must contain %q, got:\n%s", want, runningCard)
		}
	}

	// Release the tool; the turn completes and the tool state clears.
	close(release)
	finalMsg := receiveFinalMessage(t, finalCh)
	updated, _ = next.Update(finalMsg)
	next = updated.(model)
	// The gated tool produced a result row; the active state must be gone.
	resultCard := ""
	for _, row := range next.transcript {
		if row.kind == rowToolResult {
			resultCard = plainRender(t, next.renderRow(row, 80, buildRowContext(next.transcript)))
		}
	}
	if resultCard == "" || !strings.Contains(resultCard, "gated done") {
		t.Fatalf("expected a completed result card, got:\n%s", resultCard)
	}
	if next.workingActivity() == "running bash" {
		t.Fatal("working line must not name the tool after its result landed")
	}
	// A result row for the active call must clear the model's active-tool state.
	if next.activeToolID != "" || next.activeToolName != "" {
		t.Fatalf("active tool state leaked after result: id=%q name=%q", next.activeToolID, next.activeToolName)
	}
}

func TestWorkingActivityNamesActiveTool(t *testing.T) {
	cases := []struct {
		name       string
		activeName string
		activeID   string
		pending    bool
		streaming  string
		want       string
	}{
		{"shell tool names the tool", "bash", "c1", true, "", "running bash"},
		{"mcp tool uses clean label", "mcp_exa_web_search_exa", "c2", true, "", "running web search"},
		{"write_file names the tool", "write_file", "c3", true, "", "running write_file"},
		{"no active tool falls back to thinking", "", "", true, "", "thinking"},
		{"writing wins when text streams", "bash", "c1", true, "answer text", "writing"},
		{"idle is thinking", "bash", "c1", false, "", "thinking"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := limeTestModel()
			m.pending = tc.pending
			m.activeRunID = 7
			m.activeToolID = tc.activeID
			m.activeToolName = tc.activeName
			m.activeToolStart = time.Now()
			m.streamingText = []byte(tc.streaming)
			if got := m.workingActivity(); got != tc.want {
				t.Fatalf("workingActivity() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWorkingStatusLineShowsRunningTool(t *testing.T) {
	m := limeTestModel()
	m.pending = true
	m.activeRunID = 7
	m.activeToolID = "c1"
	m.activeToolName = "bash"
	m.activeToolStart = time.Now()
	m.turnStartedAt = m.now()
	m.reducedMotion = true // deterministic glyph

	line := plainRender(t, m.workingStatusLine())
	for _, want := range []string{"Working", "running bash"} {
		if !strings.Contains(line, want) {
			t.Fatalf("working line = %q, missing %q", line, want)
		}
	}
	if strings.Contains(line, "thinking") {
		t.Fatalf("working line must not say thinking while a tool runs: %q", line)
	}
}

// longShellCommand is a command long enough to wrap at 60/80/120 columns.
const longShellCommand = "go test -race -count=1 ./internal/cli ./internal/tui ./internal/providers ./internal/splice ./internal/worktrees"

func TestRunningShellCardWrapsExactCommand(t *testing.T) {
	for _, width := range []int{60, 80, 120} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			base := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
			m := limeTestModel()
			m.width = width
			m.pending = true
			m.activeRunID = 7
			m.activeToolID = "call_1"
			m.activeToolName = "bash"
			m.activeToolStart = base
			m.now = func() time.Time { return base.Add(12 * time.Second) }
			row := transcriptRow{kind: rowToolCall, id: "call_1", tool: "bash", detail: longShellCommand, runID: 7}

			out := plainRender(t, m.renderRow(row, width, buildRowContext(nil)))
			lines := strings.Split(out, "\n")
			for index, line := range lines {
				if got := lipgloss.Width(line); got > width {
					t.Fatalf("line %d width %d exceeds card width %d: %q", index, got, width, line)
				}
			}
			// The exact command is fully present on wrapped "$ " lines — no
			// ellipsis truncation, no middle-truncated one-line target.
			if !strings.Contains(out, "$ go test -race") || !strings.Contains(out, "./internal/worktrees") {
				t.Fatalf("exact command must appear wrapped, got:\n%s", out)
			}
			if strings.Contains(out, "…") {
				t.Fatalf("a running command must not be truncated, got:\n%s", out)
			}
			// The live card shows the per-call elapsed clock (from the existing
			// spinner tick; the test fakes the clock).
			if !strings.Contains(out, "12s") {
				t.Fatalf("live running card must show elapsed time, got:\n%s", out)
			}
		})
	}
}

func TestRunningShellCardPreservesCommandWhitespace(t *testing.T) {
	command := "printf  '%s'   value"
	m := limeTestModel()
	m.pending = true
	m.activeRunID = 7
	m.activeToolID = "call_1"
	m.activeToolName = "bash"
	m.activeToolStart = time.Now()
	row := transcriptRow{kind: rowToolCall, id: "call_1", tool: "bash", detail: command, runID: 7}
	out := plainRender(t, m.renderRow(row, 120, buildRowContext(nil)))
	if !strings.Contains(out, command) {
		t.Fatalf("command whitespace changed: %q", out)
	}
}

func TestRunningShellCardStaticForOrphan(t *testing.T) {
	m := limeTestModel()
	m.pending = true
	m.activeRunID = 2
	orphan := transcriptRow{kind: rowToolCall, id: "old", tool: "bash", detail: longShellCommand, runID: 1}
	out := plainRender(t, m.renderRow(orphan, 80, buildRowContext(nil)))
	if strings.Contains(out, "12s") {
		t.Fatalf("an orphaned call card must not tick elapsed, got:\n%s", out)
	}
	// The exact command is still readable on orphaned (rehydrated) call rows.
	if !strings.Contains(out, "$ go test -race") {
		t.Fatalf("orphaned shell call must keep its exact command, got:\n%s", out)
	}
}

func TestCompletedShellCardShowsExactCommand(t *testing.T) {
	m := limeTestModel()
	detail := "stdout:\nok   github.com/Taf0711/splice/internal/tui\nexit_code: 0"
	row := transcriptRow{kind: rowToolResult, id: "call_1", tool: "bash", status: tools.StatusOK, detail: detail}
	rc := buildRowContext([]transcriptRow{{kind: rowToolCall, id: "call_1", tool: "bash", detail: longShellCommand}})
	out := plainRender(t, m.renderRow(row, 80, rc))
	if !strings.Contains(out, "$ "+longShellCommand) && !strings.Contains(out, "$ go test -race") {
		t.Fatalf("completed shell card must show the exact command, got:\n%s", out)
	}
	if !strings.Contains(out, "ok   github.com/Taf0711/splice/internal/tui") {
		t.Fatalf("completed shell card must show output, got:\n%s", out)
	}
	// The head must not middle-truncate the command into the target slot.
	if strings.Contains(out, "…") {
		t.Fatalf("completed shell card must not truncate the command, got:\n%s", out)
	}
}

func TestCompletedShellCardKeepsNewestFiveOutputLines(t *testing.T) {
	m := transcriptViewTestModel()
	detail := "stdout:\n" + numberedLines(20) + "\nexit_code: 0"
	row := transcriptRow{kind: rowToolResult, id: "call_1", tool: "bash", status: tools.StatusOK, detail: detail}
	rc := buildRowContext([]transcriptRow{{kind: rowToolCall, id: "call_1", tool: "bash", detail: "go test ./..."}})
	out := plainRender(t, m.renderRow(row, m.width, rc))
	for _, want := range []string{"$ go test ./...", "line-016", "line-020", "… 15 earlier lines", "Ctrl+O full output"} {
		if !strings.Contains(out, want) {
			t.Fatalf("completed shell preview missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "line-015") {
		t.Fatalf("completed shell preview must keep only the newest five lines:\n%s", out)
	}
}

func TestFailedShellCardKeepsCommandAndErrorTogether(t *testing.T) {
	m := limeTestModel()
	detail := "stderr:\nboom: cannot find package\nexit_code: 1"
	row := transcriptRow{kind: rowToolResult, id: "call_1", tool: "bash", status: tools.StatusError, detail: detail}
	rc := buildRowContext([]transcriptRow{{kind: rowToolCall, id: "call_1", tool: "bash", detail: "go build ./..."}})
	out := plainRender(t, m.renderRow(row, 80, rc))
	for _, want := range []string{"$ go build ./...", "boom: cannot find package", "exit 1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("failed shell card = %q, missing %q", out, want)
		}
	}
	if !strings.Contains(out, "Ran") {
		t.Fatalf("failed shell card must keep its action label, got:\n%s", out)
	}
}

func TestLongGenericResultPreviewsThreeLines(t *testing.T) {
	m := transcriptViewTestModel() // realistic 96-col viewport; the footer must fit
	total := cardBodyMaxLines + 10 // 26 raw output lines
	long := numberedLines(total)
	row := transcriptRow{kind: rowToolResult, id: "t1", tool: "mcp_exa_web_search_exa", status: tools.StatusOK, detail: long}

	collapsed := plainRender(t, m.renderRow(row, m.width, buildRowContext(nil)))
	// The first 3 useful lines are previewed...
	for _, want := range []string{"line-001", "line-002", "line-003"} {
		if !strings.Contains(collapsed, want) {
			t.Fatalf("collapsed preview = %q, missing %q", collapsed, want)
		}
	}
	// ...and everything after stays hidden behind an ACCURATE count: one body
	// line per fixture line, minus the 3 previewed ones. Deriving the expected
	// value from the fixture (not a hand-written constant) also catches any
	// body renderer that silently drops lines.
	if strings.Contains(collapsed, "line-004") {
		t.Fatalf("collapsed preview must not show line-004, got:\n%s", collapsed)
	}
	wantHidden := fmt.Sprintf("… %d more lines", total-3)
	if !strings.Contains(collapsed, wantHidden) {
		t.Fatalf("collapsed card must show the accurate hidden count (%d), got:\n%s", total-3, collapsed)
	}
	// Both disclosure routes are advertised: mouse click and Ctrl+O.
	for _, want := range []string{"click to expand", "Ctrl+O full output"} {
		if !strings.Contains(collapsed, want) {
			t.Fatalf("collapsed footer = %q, missing %q", collapsed, want)
		}
	}

	// Expanding shows the capped body and a collapse hint.
	row.expanded = true
	expanded := plainRender(t, m.renderRow(row, m.width, buildRowContext(nil)))
	if !strings.Contains(expanded, "line-005") {
		t.Fatalf("expanded card must show the body, got:\n%s", expanded)
	}
	if !strings.Contains(expanded, "▾ collapse") {
		t.Fatalf("expanded card must offer collapse, got:\n%s", expanded)
	}
}

func TestShortGenericResultStaysInline(t *testing.T) {
	m := limeTestModel()
	row := transcriptRow{kind: rowToolResult, id: "t2", tool: "custom_tool", status: tools.StatusOK, detail: numberedLines(3)}
	out := plainRender(t, m.renderRow(row, m.width, buildRowContext(nil)))
	for _, want := range []string{"line-001", "line-003"} {
		if !strings.Contains(out, want) {
			t.Fatalf("short result = %q, missing %q", out, want)
		}
	}
	if strings.Contains(out, "more lines") {
		t.Fatalf("short result must not show a hidden-count footer, got:\n%s", out)
	}
}

func TestFailedLongResultNeverCollapses(t *testing.T) {
	m := limeTestModel()
	long := numberedLines(cardBodyMaxLines + 10)
	row := transcriptRow{kind: rowToolResult, id: "e1", tool: "custom_tool", status: tools.StatusError, detail: long}
	out := plainRender(t, m.renderRow(row, m.width, buildRowContext(nil)))
	if strings.Contains(out, "click to expand") {
		t.Fatalf("a failed result must never hide its error behind a click, got:\n%s", out)
	}
	if !strings.Contains(out, "line-001") {
		t.Fatalf("a failed result must show its body, got:\n%s", out)
	}
}

func TestExploreResultStaysCompact(t *testing.T) {
	m := transcriptViewTestModel() // realistic 96-col viewport (limeTestModel has width 0)
	body := "line-001\nline-002\nline-003\nline-004"
	row := transcriptRow{kind: rowToolResult, id: "r1", tool: "read_file", status: tools.StatusOK, detail: body}
	rc := buildRowContext([]transcriptRow{{kind: rowToolCall, id: "r1", tool: "read_file", detail: "internal/agent/loop.go"}})
	out := plainRender(t, m.renderRow(row, m.width, rc))
	// Compact semantics: the summary names the path but never dumps the body,
	// and explore cards skip the collapse/expand footer entirely.
	for _, want := range []string{"Explored", "internal/agent/loop.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("read card = %q, missing %q", out, want)
		}
	}
	if strings.Contains(out, "line-002") {
		t.Fatalf("a successful read must not dump its body, got:\n%s", out)
	}
	if strings.Contains(out, "click to expand") {
		t.Fatalf("explore cards must not advertise expansion, got:\n%s", out)
	}
}

func TestActiveToolClearsOnResultRow(t *testing.T) {
	m := limeTestModel()
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	m.pending = true
	m.activeRunID = 7
	m.width = 80
	m.height = 30

	updated, _ := m.Update(agentRowMsg{runID: 7, row: transcriptRow{
		kind: rowToolCall, id: "call_1", tool: "bash", detail: "go test ./...", runID: 7,
	}})
	next := updated.(model)
	if got := next.workingActivity(); got != "running bash" {
		t.Fatalf("after call row: workingActivity = %q, want %q", got, "running bash")
	}
	if next.activeToolID != "call_1" || next.activeToolStart.IsZero() {
		t.Fatalf("active tool state not stamped: id=%q start=%v", next.activeToolID, next.activeToolStart)
	}

	now = now.Add(12 * time.Second)
	updated, _ = next.Update(agentRowMsg{runID: 7, row: transcriptRow{
		kind: rowToolResult, id: "call_1", tool: "bash", status: tools.StatusOK, detail: "stdout:\nok\nexit_code: 0", runID: 7,
	}})
	next = updated.(model)
	if next.activeToolID != "" || next.activeToolName != "" || !next.activeToolStart.IsZero() {
		t.Fatalf("active tool state must clear on result: id=%q name=%q start=%v", next.activeToolID, next.activeToolName, next.activeToolStart)
	}
	if got := next.workingActivity(); got != "thinking" {
		t.Fatalf("after result row: workingActivity = %q, want thinking", got)
	}
	result := next.transcript[len(next.transcript)-1]
	if result.toolElapsed != 12*time.Second {
		t.Fatalf("completed tool elapsed = %s, want 12s", result.toolElapsed)
	}
	if out := plainRender(t, next.renderRow(result, 80, buildRowContext(next.transcript))); !strings.Contains(out, "12s") {
		t.Fatalf("completed card must retain call duration: %s", out)
	}
}

func TestActiveToolClearsOnCancellation(t *testing.T) {
	m := limeTestModel()
	m.pending = true
	m.activeRunID = 3
	m.activeToolID = "call_9"
	m.activeToolName = "bash"
	m.activeToolStart = time.Now()
	m.cancelRun()
	if m.activeToolID != "" || m.activeToolName != "" || !m.activeToolStart.IsZero() {
		t.Fatalf("cancelRun must clear the active tool, got id=%q name=%q", m.activeToolID, m.activeToolName)
	}
}

func TestLiveShellOutputKeepsNewestFiveLinesAndClears(t *testing.T) {
	m := limeTestModel()
	m.pending = true
	m.activeRunID = 7
	m.width = 80
	m.height = 30

	updated, _ := m.Update(agentRowMsg{runID: 7, row: transcriptRow{
		kind: rowToolCall, id: "call_1", tool: "bash", detail: "go test ./...", runID: 7,
	}})
	next := updated.(model)
	rowsBefore := len(next.transcript)
	updated, _ = next.Update(toolOutputSnapshotMsg{runID: 7, id: "call_1", snapshot: numberedLines(7)})
	next = updated.(model)
	if len(next.transcript) != rowsBefore {
		t.Fatal("a live snapshot must not create a transcript row")
	}
	call := next.transcript[len(next.transcript)-1]
	out := plainRender(t, next.renderRow(call, 80, buildRowContext(next.transcript)))
	for _, hidden := range []string{"line-001", "line-002"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("live preview must keep only the newest five lines, got:\n%s", out)
		}
	}
	for _, visible := range []string{"line-003", "line-007"} {
		if !strings.Contains(out, visible) {
			t.Fatalf("live preview missing %q: \n%s", visible, out)
		}
	}

	updated, _ = next.Update(agentRowMsg{runID: 7, row: transcriptRow{
		kind: rowToolResult, id: "call_1", tool: "bash", status: tools.StatusOK, detail: "done", runID: 7,
	}})
	next = updated.(model)
	if next.liveToolCallID != "" || next.liveToolOutput != "" {
		t.Fatalf("final result must clear live output: id=%q output=%q", next.liveToolCallID, next.liveToolOutput)
	}
	for _, row := range next.transcript {
		if strings.Contains(row.text+row.detail, "line-007") {
			t.Fatal("live output leaked into retained transcript")
		}
	}
}

func TestActiveToolStartsEmptyPerRun(t *testing.T) {
	m := limeTestModel()
	m.liveToolCallID = "old"
	m.liveToolOutput = "old output"
	next := m.beginRun(func() {})
	if next.activeToolID != "" || next.activeToolName != "" || !next.activeToolStart.IsZero() || next.liveToolCallID != "" || next.liveToolOutput != "" {
		t.Fatalf("a fresh run must not inherit tool state, got id=%q name=%q output=%q", next.activeToolID, next.activeToolName, next.liveToolOutput)
	}
}
