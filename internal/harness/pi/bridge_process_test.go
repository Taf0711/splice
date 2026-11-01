package pi

// Process-boundary integration tests for the Pi bridge. These tests spawn the
// real splice-pi-bridge binary in fixture mode (no live model or service) and
// assert the stream-json contract the Pi extension consumes: event ordering,
// malformed and unknown control input, terminal state, cleanup, and the
// shipped cancellation control.
//
// The tests are optional and local-first. They skip when the bridge binary is
// not built; run `go build -o splice-pi-bridge ./cmd/splice-pi-bridge` first,
// or point SPLICE_PI_BRIDGE_BIN at a prebuilt path.

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/streamjson"
)

const bridgeBinaryEnv = "SPLICE_PI_BRIDGE_BIN"

const bridgeWatchdog = 60 * time.Second

type bridgeProcess struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Scanner
	stderr   *strings.Builder
	waitOnce sync.Once
	waitErr  error
	waitDone chan struct{}
	kill     func()
}

// wait reaps the child exactly once. The test body and the cleanup handler
// may both call it; the second caller observes the first result instead of
// blocking on an already-consumed channel.
func (p *bridgeProcess) wait() error {
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
		close(p.waitDone)
	})
	<-p.waitDone
	return p.waitErr
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test package")
		}
		dir = parent
	}
}

func findBridgeBinary(t *testing.T) string {
	t.Helper()
	if env := strings.TrimSpace(os.Getenv(bridgeBinaryEnv)); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env
		}
		t.Skipf("%s points at %q which does not exist", bridgeBinaryEnv, env)
	}
	candidate := filepath.Join(findRepoRoot(t), "splice-pi-bridge")
	if _, err := os.Stat(candidate); err != nil {
		t.Skipf("bridge binary %q is not built; run go build -o splice-pi-bridge ./cmd/splice-pi-bridge", candidate)
	}
	return candidate
}

func startBridge(t *testing.T, env map[string]string, args ...string) *bridgeProcess {
	t.Helper()
	bin := findBridgeBinary(t)
	cmd := exec.Command(bin, args...)
	if waitCancel := env["SPLICE_PI_FIXTURE_WAIT_CANCEL"]; waitCancel != "" {
		cmd.Env = append(os.Environ(), "SPLICE_PI_FIXTURE_WAIT_CANCEL="+waitCancel)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrBuilder := &strings.Builder{}
	cmd.Stderr = stderrBuilder
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bridge: %v", err)
	}
	process := &bridgeProcess{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   bufio.NewScanner(stdout),
		stderr:   stderrBuilder,
		waitDone: make(chan struct{}),
	}
	// Watchdog: a wedged bridge must fail the test with a readable stream
	// truncation instead of hanging the suite.
	timer := time.AfterFunc(bridgeWatchdog, func() { _ = cmd.Process.Kill() })
	process.kill = func() { _ = cmd.Process.Kill() }
	t.Cleanup(func() {
		timer.Stop()
		process.kill()
		_ = process.stdin.Close()
		_ = process.wait()
	})
	return process
}

func (p *bridgeProcess) nextEvent(t *testing.T) (streamjson.Event, bool) {
	t.Helper()
	for p.stdout.Scan() {
		line := strings.TrimSpace(p.stdout.Text())
		if line == "" {
			continue
		}
		var event streamjson.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("bridge emitted a non-JSON line: %q (%v)", line, err)
		}
		if event.RunID == "" {
			t.Fatalf("stream-json contract requires a runId on every event, got %q", line)
		}
		return event, true
	}
	return streamjson.Event{}, false
}

func (p *bridgeProcess) sendControl(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(p.stdin, line+"\n"); err != nil {
		t.Fatalf("write control %q: %v", line, err)
	}
}

// TestBridgeProcessFixtureStreamOrdering pins the full wire sequence at the
// real process boundary: pipeline_plan first, stage running before completed,
// tool calls inside the running window, usage present, then final and a
// terminal run_end whose exit code is authoritative.
func TestBridgeProcessFixtureStreamOrdering(t *testing.T) {
	workDir := t.TempDir()
	process := startBridge(t, nil, "-fixture", "-prompt", "fix the spelling typo", "-cwd", workDir)

	var (
		sawPlan      bool
		sawRunning   bool
		sawCompleted bool
		sawToolCall  bool
		sawUsage     bool
		sawFinal     bool
		runEnd       *streamjson.Event
	)

	for {
		event, ok := process.nextEvent(t)
		if !ok {
			break
		}
		switch event.Type {
		case streamjson.EventPipelinePlan:
			if sawPlan {
				t.Fatal("pipeline_plan must appear exactly once")
			}
			sawPlan = true
			if len(event.Stages) == 0 || event.Stages[0] != "code_writer" {
				t.Fatalf("plan stages = %v, want code_writer first", event.Stages)
			}
		case streamjson.EventStage:
			if !sawPlan {
				t.Fatalf("stage event before pipeline_plan: %+v", event)
			}
			switch event.Status {
			case "running":
				if sawCompleted {
					t.Fatal("stage running after completed breaks lifecycle order")
				}
				sawRunning = true
			case "completed":
				sawCompleted = true
			}
		case streamjson.EventToolCall:
			if !sawRunning {
				t.Fatalf("tool_call before any running stage: %+v", event)
			}
			sawToolCall = true
		case streamjson.EventUsage:
			sawUsage = true
		case streamjson.EventFinal:
			sawFinal = true
			if strings.TrimSpace(event.Text) == "" {
				t.Fatal("final event must carry the answer text")
			}
		case streamjson.EventRunEnd:
			copied := event
			runEnd = &copied
		}
	}

	if err := process.wait(); err != nil {
		t.Fatalf("bridge exit: %v (stderr: %s)", err, process.stderr.String())
	}
	checks := map[string]bool{
		"pipeline_plan":   sawPlan,
		"stage running":   sawRunning,
		"stage completed": sawCompleted,
		"tool_call":       sawToolCall,
		"usage":           sawUsage,
		"final":           sawFinal,
	}
	for name, ok := range checks {
		if !ok {
			t.Fatalf("fixture stream missing %s event", name)
		}
	}
	if runEnd == nil {
		t.Fatal("fixture stream ended without a run_end terminal event")
	}
	if runEnd.Status != "success" || runEnd.ExitCode == nil || *runEnd.ExitCode != 0 {
		t.Fatalf("terminal state = %+v, want success exit 0", runEnd)
	}
	// Cleanup: the fixture wrote its output into the run cwd.
	if _, err := os.Stat(filepath.Join(workDir, "hello.go")); err != nil {
		t.Fatalf("fixture output missing after clean run: %v", err)
	}
}

// TestBridgeProcessSurvivesMalformedAndUnknownControl pins the control seam at
// the boundary: malformed lines, unknown kinds, and undeclared capabilities are
// warned about and never kill the run.
func TestBridgeProcessSurvivesMalformedAndUnknownControl(t *testing.T) {
	workDir := t.TempDir()
	process := startBridge(t, nil, "-fixture", "-prompt", "fix the spelling typo", "-cwd", workDir)

	process.sendControl(t, "not json")
	process.sendControl(t, `{"kind":"make_coffee"}`)
	process.sendControl(t, `{"kind":"grant_permission","permId":"call_1"}`)
	process.sendControl(t, `{"kind":"set_model","model":"gpt-4.1"}`)

	var runEnd *streamjson.Event
	for {
		event, ok := process.nextEvent(t)
		if !ok {
			break
		}
		if event.Type == streamjson.EventRunEnd {
			copied := event
			runEnd = &copied
		}
	}
	if err := process.wait(); err != nil {
		t.Fatalf("bad control input killed the run: %v (stderr: %s)", err, process.stderr.String())
	}
	if runEnd == nil || runEnd.Status != "success" || runEnd.ExitCode == nil || *runEnd.ExitCode != 0 {
		t.Fatalf("terminal state = %+v, want success exit 0 despite malformed control input", runEnd)
	}
	warnings := process.stderr.String()
	if !strings.Contains(warnings, "malformed") {
		t.Fatalf("stderr must warn about the malformed line, got %q", warnings)
	}
	if !strings.Contains(warnings, "capability") || !strings.Contains(warnings, "unknown control command") {
		t.Fatalf("stderr must reject undeclared and unknown commands, got %q", warnings)
	}
}

// TestBridgeProcessCancelControl pins the one shipped control: cancel_run on
// stdin aborts an in-flight run, and the terminal state reports interrupted
// with the conventional SIGINT exit code. The fixture gate env holds the
// provider stream open until the cancel command is routed, so the cancel is
// guaranteed to land while the run is in flight. No timing assumptions.
func TestBridgeProcessCancelControl(t *testing.T) {
	workDir := t.TempDir()
	process := startBridge(t,
		map[string]string{"SPLICE_PI_FIXTURE_WAIT_CANCEL": "1"},
		"-fixture", "-prompt", "fix the spelling typo", "-cwd", workDir)

	// The stage running event is emitted before the gated provider blocks, so
	// observing it proves the run is in flight.
	sawRunning := false
	for {
		event, ok := process.nextEvent(t)
		if !ok {
			t.Fatal("stream ended before the stage started")
		}
		if event.Type == streamjson.EventStage && event.Status == "running" {
			sawRunning = true
			break
		}
	}
	if !sawRunning {
		t.Fatal("run never entered the running state")
	}
	process.sendControl(t, `{"kind":"cancel_run"}`)

	var runEnd *streamjson.Event
	runEndCount := 0
	for {
		event, ok := process.nextEvent(t)
		if !ok {
			break
		}
		if event.Type == streamjson.EventRunEnd {
			runEndCount++
			copied := event
			runEnd = &copied
		}
	}
	if runEndCount != 1 {
		t.Fatalf("run_end events = %d, want exactly 1", runEndCount)
	}
	if runEnd.Status != "interrupted" || runEnd.ExitCode == nil || *runEnd.ExitCode != 130 {
		t.Fatalf("canceled terminal state = %+v, want interrupted exit 130", runEnd)
	}
	if err := process.wait(); err == nil {
		t.Fatal("interrupted run must exit nonzero")
	}
}

// TestBridgeProcessCancelAfterCompletionStaysSuccess pins the honest no-op
// rule: a cancel command that arrives after the run completed must not flip a
// finished run to interrupted. The stream is drained to its success terminal
// first, so completion is a fact before the cancel write happens.
func TestBridgeProcessCancelAfterCompletionStaysSuccess(t *testing.T) {
	workDir := t.TempDir()
	process := startBridge(t, nil, "-fixture", "-prompt", "fix the spelling typo", "-cwd", workDir)

	var runEnd *streamjson.Event
	runEndCount := 0
	for {
		event, ok := process.nextEvent(t)
		if !ok {
			break
		}
		if event.Type == streamjson.EventRunEnd {
			runEndCount++
			copied := event
			runEnd = &copied
		}
	}
	if runEndCount != 1 {
		t.Fatalf("run_end events = %d, want exactly 1", runEndCount)
	}
	if runEnd.Status != "success" || runEnd.ExitCode == nil || *runEnd.ExitCode != 0 {
		t.Fatalf("terminal state = %+v, want success exit 0", runEnd)
	}
	// The child has exited by now, so this write may fail with EPIPE; a failed
	// write is fine, a successful one must also be a no-op.
	_, _ = io.WriteString(process.stdin, `{"kind":"cancel_run"}`+"\n")
	if err := process.wait(); err != nil {
		t.Fatalf("completed run must exit 0, got %v", err)
	}
}
