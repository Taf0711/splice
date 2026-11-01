package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestPRCommandRunnerForcesFsmonitorOff proves the hardened env reaches git:
// a repository that sets core.fsmonitor to a command still reads "false".
func TestPRCommandRunnerForcesFsmonitorOff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "core.fsmonitor", "/bin/echo hostile")

	out, err := defaultPRCommandRunner(context.Background(), dir, "git", "config", "--get", "core.fsmonitor")
	if err != nil {
		t.Fatalf("runner error = %v", err)
	}
	if strings.TrimSpace(out) != "false" {
		t.Fatalf("core.fsmonitor = %q, want the hardened value false", strings.TrimSpace(out))
	}
}

func TestUntrustedWorkspaceStartsNoGitOrPRWatcher(t *testing.T) {
	dir := t.TempDir()
	sink := func(tea.Msg) {}
	untrusted := model{cwd: dir, trusted: false, prService: NewPrService(dir), runtimeMessageSink: sink}
	if cmd := untrusted.prWatcherCmd(); cmd != nil {
		t.Fatal("PR watcher must not start in an untrusted workspace")
	}
	untrusted.gitFileBaseline = map[string]bool{}
	if _, cmd := untrusted.maybeGitSweep(); cmd != nil {
		t.Fatal("git sweep must not run in an untrusted workspace")
	}

	trusted := untrusted
	trusted.trusted = true
	if cmd := trusted.prWatcherCmd(); cmd == nil {
		t.Fatal("PR watcher should start in a trusted workspace")
	}
	if _, cmd := trusted.maybeGitSweep(); cmd == nil {
		t.Fatal("git sweep should run in a trusted workspace")
	}
}

// TestInitGitSweepIsGatedOnTrust pins the trust argument of the Init git sweep.
// Init returns a batch. The sweep is the one command that runs git status, and
// it answers with gitSweepMsg. An untrusted model must not produce that message.
// A trusted model must, so the test fails in both directions.
func TestInitGitSweepIsGatedOnTrust(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.invalid"}, {"config", "user.name", "t"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, trusted := range []bool{false, true} {
		m := model{ctx: context.Background(), cwd: repo, trusted: trusted, runtimeMessageSink: func(tea.Msg) {}}
		got := initMessagesWithin(m.Init(), 3*time.Second)
		sawSweep := false
		for _, msg := range got {
			if _, ok := msg.(gitSweepMsg); ok {
				sawSweep = true
			}
		}
		if sawSweep != trusted {
			t.Fatalf("trusted=%v: git sweep ran = %v, want %v", trusted, sawSweep, trusted)
		}
	}
}

// initMessagesWithin runs an Init command and the commands of its batch, and
// returns the messages that arrive within the deadline. Each command runs in
// its own goroutine, so a blocking timer never holds up the others.
func initMessagesWithin(cmd tea.Cmd, deadline time.Duration) []tea.Msg {
	if cmd == nil {
		return nil
	}
	timeout := time.After(deadline)
	first := make(chan tea.Msg, 1)
	go func() { first <- cmd() }()
	var top tea.Msg
	select {
	case top = <-first:
	case <-timeout:
		return nil
	}
	batch, ok := top.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{top}
	}
	out := make(chan tea.Msg, len(batch))
	pending := 0
	for _, sub := range batch {
		if sub == nil {
			continue
		}
		pending++
		go func(c tea.Cmd) { out <- c() }(sub)
	}
	var msgs []tea.Msg
	for i := 0; i < pending; i++ {
		select {
		case msg := <-out:
			msgs = append(msgs, msg)
		case <-timeout:
			return msgs
		}
	}
	return msgs
}
