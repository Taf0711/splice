package tui

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type herdrCommand struct {
	bin  string
	args []string
}

type recordingHerdrRunner struct {
	mu       sync.Mutex
	commands []herdrCommand
	err      error
}

func (runner *recordingHerdrRunner) run(_ context.Context, bin string, args ...string) error {
	runner.mu.Lock()
	runner.commands = append(runner.commands, herdrCommand{bin: bin, args: append([]string(nil), args...)})
	runner.mu.Unlock()
	return runner.err
}

func (runner *recordingHerdrRunner) snapshot() []herdrCommand {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return append([]herdrCommand(nil), runner.commands...)
}

func herdrEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestHerdrReporterRequiresCompleteEnvironment(t *testing.T) {
	cases := []map[string]string{
		{},
		{"HERDR_ENV": "0", "HERDR_PANE_ID": "w1:p1", "HERDR_BIN_PATH": "/bin/herdr"},
		{"HERDR_ENV": "1", "HERDR_BIN_PATH": "/bin/herdr"},
		{"HERDR_ENV": "1", "HERDR_PANE_ID": "w1:p1"},
	}
	missing := func(string) (string, error) { return "", errors.New("not found") }
	for index, env := range cases {
		if reporter := newHerdrReporter(herdrEnv(env), missing, nil); reporter != nil {
			reporter.Close()
			t.Fatalf("case %d unexpectedly enabled reporter", index)
		}
	}
}

func TestHerdrReporterFindsAbsoluteBinaryWhenEnvPathIsAbsent(t *testing.T) {
	runner := &recordingHerdrRunner{}
	lookup := func(name string) (string, error) {
		if name != "herdr" {
			t.Fatalf("lookup name = %q", name)
		}
		return "/usr/local/bin/herdr", nil
	}
	reporter := newHerdrReporter(herdrEnv(map[string]string{
		"HERDR_ENV":     "1",
		"HERDR_PANE_ID": "pane",
	}), lookup, runner.run)
	if reporter == nil {
		t.Fatal("expected PATH fallback")
	}
	reporter.Report(herdrIdle)
	reporter.Close()
	for _, command := range runner.snapshot() {
		if command.bin != "/usr/local/bin/herdr" {
			t.Fatalf("binary = %q", command.bin)
		}
	}
}

func TestHerdrReporterOrdersStatesAndRelease(t *testing.T) {
	runner := &recordingHerdrRunner{}
	reporter := newHerdrReporter(herdrEnv(map[string]string{
		"HERDR_ENV":      "1",
		"HERDR_PANE_ID":  "w1D:p1",
		"HERDR_BIN_PATH": "/opt/herdr",
	}), nil, runner.run)
	if reporter == nil {
		t.Fatal("expected reporter")
	}
	for _, state := range []herdrState{herdrIdle, herdrWorking, herdrBlocked, herdrWorking, herdrIdle} {
		reporter.Report(state)
	}
	reporter.Close()

	commands := runner.snapshot()
	if len(commands) != 6 {
		t.Fatalf("commands = %#v, want five reports and one release", commands)
	}
	for index, command := range commands {
		if command.bin != "/opt/herdr" || command.args[len(command.args)-1] != "w1D:p1" {
			t.Fatalf("command %d used wrong binary or pane: %#v", index, command)
		}
		if valueAfter(command.args, "--source") != herdrSource || valueAfter(command.args, "--agent") != herdrAgent {
			t.Fatalf("command %d missing stable labels: %#v", index, command.args)
		}
		seq, err := strconv.Atoi(valueAfter(command.args, "--seq"))
		if err != nil || seq != index+1 {
			t.Fatalf("command %d seq = %q, want %d", index, valueAfter(command.args, "--seq"), index+1)
		}
		if valueAfter(command.args, "--message") != "" {
			t.Fatalf("command %d exposed a message: %#v", index, command.args)
		}
	}
	if got := commands[len(commands)-1].args[1]; got != "release-agent" {
		t.Fatalf("last command = %q, want release-agent", got)
	}
	wantStates := []string{"idle", "working", "blocked", "working", "idle"}
	var gotStates []string
	for _, command := range commands[:len(commands)-1] {
		gotStates = append(gotStates, valueAfter(command.args, "--state"))
	}
	if !reflect.DeepEqual(gotStates, wantStates) {
		t.Fatalf("states = %v, want %v", gotStates, wantStates)
	}
}

func TestHerdrReporterBoundsHungCommands(t *testing.T) {
	calls := 0
	runner := func(ctx context.Context, _ string, _ ...string) error {
		calls++
		<-ctx.Done()
		return ctx.Err()
	}
	reporter := newHerdrReporter(herdrEnv(map[string]string{
		"HERDR_ENV":      "1",
		"HERDR_PANE_ID":  "pane",
		"HERDR_BIN_PATH": "/hung/herdr",
	}), nil, runner)
	start := time.Now()
	reporter.Report(herdrIdle)
	reporter.Close()
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("hung commands delayed close for %s", elapsed)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want report and release", calls)
	}
}

func TestHerdrReporterIgnoresCommandErrors(t *testing.T) {
	runner := &recordingHerdrRunner{err: errors.New("unavailable")}
	reporter := newHerdrReporter(herdrEnv(map[string]string{
		"HERDR_ENV":      "1",
		"HERDR_PANE_ID":  "pane",
		"HERDR_BIN_PATH": "/missing/herdr",
	}), nil, runner.run)
	reporter.Report(herdrIdle)
	reporter.Report(herdrWorking)
	reporter.Close()
	commands := runner.snapshot()
	if len(commands) != 3 || commands[2].args[1] != "release-agent" {
		t.Fatalf("errors disrupted lifecycle order: %#v", commands)
	}
}

func valueAfter(args []string, key string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == key {
			return args[index+1]
		}
	}
	return ""
}

type recordingLifecycle struct{ states []herdrState }

func (reporter *recordingLifecycle) Report(state herdrState) {
	reporter.states = append(reporter.states, state)
}

func TestAgentLifecycleRunAndCancel(t *testing.T) {
	reporter := &recordingLifecycle{}
	m := newModel(context.Background(), Options{})
	m.herdr = reporter
	m = m.beginRun(func() {})
	m.cancelRun()
	if want := []herdrState{herdrWorking, herdrIdle}; !reflect.DeepEqual(reporter.states, want) {
		t.Fatalf("states = %v, want %v", reporter.states, want)
	}
}

func TestAgentLifecycleCompletion(t *testing.T) {
	reporter := &recordingLifecycle{}
	m := newModel(context.Background(), Options{})
	m.herdr = reporter
	m = m.beginRun(func() {})
	updated, _ := m.Update(agentResponseMsg{runID: m.activeRunID})
	m = updated.(model)
	if want := []herdrState{herdrWorking, herdrIdle}; !reflect.DeepEqual(reporter.states, want) {
		t.Fatalf("states = %v, want %v", reporter.states, want)
	}
}

func TestAgentLifecyclePermissionAndAskUser(t *testing.T) {
	reporter := &recordingLifecycle{}
	m := newModel(context.Background(), Options{})
	m.herdr = reporter
	m.pending = true
	m.activeRunID = 7

	updated, _ := m.Update(permissionRequestMsg{
		runID: 7,
		request: agent.PermissionRequest{
			ToolCallID: "permission_1",
			ToolName:   "bash",
			Action:     agent.PermissionActionPrompt,
		},
	})
	m = updated.(model)
	updated, _ = m.resolvePermission(permissionDecisionAllow)
	m = updated.(model)

	updated, _ = m.Update(askUserRequestMsg{
		runID: 7,
		request: agent.AskUserRequest{
			ToolCallID: "ask_1",
			Questions:  []agent.AskUserQuestion{{Question: "Proceed?"}},
		},
	})
	m = updated.(model)
	m.input.SetValue("yes")
	updated, _ = m.confirmAskUser()
	m = updated.(model)

	want := []herdrState{herdrBlocked, herdrWorking, herdrBlocked, herdrWorking}
	if !reflect.DeepEqual(reporter.states, want) {
		t.Fatalf("states = %v, want %v", reporter.states, want)
	}
}

func TestAgentLifecycleSpecReview(t *testing.T) {
	reporter := &recordingLifecycle{}
	store := testSessionStore(t)
	provider := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{
		submitSpecScript("call-1", "Review Flow", "# Goal\n\nAdd review flow."),
	}}
	m := newSpecModeTestModel(t.TempDir(), provider, store)
	m.herdr = reporter
	m.input.SetValue("/spec add review flow")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	updated, _ = m.Update(execCmd(cmd))
	m = updated.(model)
	if m.pendingSpecReview == nil {
		t.Fatal("expected spec review")
	}
	updated, _ = m.cancelSpecReview()
	m = updated.(model)

	want := []herdrState{herdrWorking, herdrBlocked, herdrIdle}
	if !reflect.DeepEqual(reporter.states, want) {
		t.Fatalf("states = %v, want %v", reporter.states, want)
	}
}
