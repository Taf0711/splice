package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/mcp"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/tui"
	"github.com/Taf0711/splice/internal/update"
)

type noticeTestWriter struct {
	mu   sync.Mutex
	data bytes.Buffer
	done chan struct{}
}

func (w *noticeTestWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done != nil {
		select {
		case <-w.done:
		default:
			close(w.done)
		}
	}
	return w.data.Write(data)
}

func (w *noticeTestWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.String()
}

func TestBackgroundUpdateNoticeUsesStderrAndKeepsExecStdoutClean(t *testing.T) {
	// splice exec streams structured JSON on stdout, so a notice there corrupts it.
	t.Setenv("SPLICE_DISABLE_UPDATE_NOTICE", "")
	t.Setenv("SPLICE_DISABLE_UPDATES", "")
	t.Setenv("SPLICE_UPDATE_CACHE_PATH", filepath.Join(t.TempDir(), "update-check.json"))
	var stdout bytes.Buffer
	stderr := &noticeTestWriter{done: make(chan struct{})}
	task := startUpdateNotice(fillAppDeps(appDeps{
		resolveExecutable: func() (string, error) { return filepath.Join(t.TempDir(), "splice"), nil },
		checkUpdate: func(context.Context, update.Options) (update.Result, error) {
			return update.Result{CurrentVersion: "1.0.0", LatestVersion: "1.1.0", UpdateAvailable: true}, nil
		},
	}))
	finishUpdateNotice(task, stderr)
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want unchanged", got)
	}
	if got := stderr.String(); !strings.Contains(got, "splice update --apply") {
		t.Fatalf("stderr = %q, want standalone update command", got)
	}
}

func TestExecProtocolNoticeSuppressesNonTTYStderr(t *testing.T) {
	// splice exec protocol mode contracts for empty stderr, so a notice must never enter a machine-readable session.
	t.Setenv("SPLICE_DISABLE_UPDATE_NOTICE", "")
	t.Setenv("SPLICE_DISABLE_UPDATES", "")
	t.Setenv("SPLICE_UPDATE_CACHE_PATH", filepath.Join(t.TempDir(), "update-check.json"))
	var stdout bytes.Buffer
	stderrFile, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("create stderr file: %v", err)
	}
	defer stderrFile.Close()
	called := false
	deps := fillAppDeps(appDeps{
		getwd:             func() (string, error) { return t.TempDir(), nil },
		resolveConfig:     func(string, config.Overrides) (config.ResolvedConfig, error) { return execResolvedConfig(), nil },
		resolveExecutable: func() (string, error) { return filepath.Join(t.TempDir(), "splice"), nil },
		checkUpdate: func(context.Context, update.Options) (update.Result, error) {
			called = true
			return update.Result{CurrentVersion: "0.0.0", LatestVersion: "0.1.3", UpdateAvailable: true}, nil
		},
	})
	if exitCode := runExec([]string{"--list-tools", "--input-format", "stream-json", "--output-format", "stream-json"}, &stdout, stderrFile, deps); exitCode != exitSuccess {
		t.Fatalf("runExec exit code = %d", exitCode)
	}
	if err := stderrFile.Close(); err != nil {
		t.Fatalf("close stderr file: %v", err)
	}
	stderr, err := os.ReadFile(stderrFile.Name())
	if err != nil {
		t.Fatalf("read stderr file: %v", err)
	}
	if called {
		t.Fatal("update check ran for non-TTY protocol stderr")
	}
	if strings.Contains(string(stderr), "Update available") {
		t.Fatalf("stderr = %q, contains an update notice", stderr)
	}
}

func TestUpdateNoticeSwitchScopes(t *testing.T) {
	t.Setenv("SPLICE_UPDATE_CACHE_PATH", filepath.Join(t.TempDir(), "update-check.json"))
	t.Setenv("SPLICE_DISABLE_UPDATE_NOTICE", "1")
	t.Setenv("SPLICE_DISABLE_UPDATES", "")
	called := false
	var stdout, stderr bytes.Buffer
	task := startUpdateNotice(fillAppDeps(appDeps{
		resolveExecutable: func() (string, error) { return "splice", nil },
		checkUpdate: func(context.Context, update.Options) (update.Result, error) {
			called = true
			return update.Result{UpdateAvailable: true}, nil
		},
	}))
	finishUpdateNotice(task, &stderr)
	time.Sleep(20 * time.Millisecond)
	if called || stderr.Len() != 0 {
		t.Fatalf("notice-only switch did not suppress background notice: called=%v stderr=%q", called, stderr.String())
	}
	exitCode := runWithDeps([]string{"--update"}, &stdout, &stderr, appDeps{
		checkUpdate: func(context.Context, update.Options) (update.Result, error) {
			return update.Result{CurrentVersion: "1.0.0", LatestVersion: "1.1.0", UpdateAvailable: true}, nil
		},
	})
	if exitCode == exitSuccess && !strings.Contains(stdout.String(), "Update available") {
		t.Fatalf("--update did not use manual check path: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	t.Setenv("SPLICE_DISABLE_UPDATES", "1")
	stdout.Reset()
	stderr.Reset()
	called = false
	exitCode = runWithDeps([]string{"--update"}, &stdout, &stderr, appDeps{
		checkUpdate: func(context.Context, update.Options) (update.Result, error) {
			called = true
			return update.Result{}, nil
		},
	})
	if exitCode != exitSuccess || called || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("all-updates switch did not suppress manual update: exit=%d called=%v stdout=%q stderr=%q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRootUpdateHelpExplainsCheckMapping(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exitCode := runWithDeps([]string{"--update", "-h"}, &stdout, &stderr, appDeps{}); exitCode != exitSuccess {
		t.Fatalf("exit code = %d, stderr=%q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "splice --update") || !strings.Contains(stdout.String(), "splice update --check") {
		t.Fatalf("help = %q", stdout.String())
	}
}

func TestRootUpdateReachesUpdateCheckPath(t *testing.T) {
	t.Setenv("SPLICE_DISABLE_UPDATES", "")
	var calls int
	var got []update.Options
	deps := appDeps{
		checkUpdate: func(_ context.Context, options update.Options) (update.Result, error) {
			calls++
			got = append(got, options)
			return update.Result{CurrentVersion: "1.0.0", LatestVersion: "1.0.0"}, nil
		},
	}
	var firstOut, firstErr bytes.Buffer
	if exitCode := runWithDeps([]string{"update", "--check"}, &firstOut, &firstErr, deps); exitCode != exitSuccess {
		t.Fatalf("splice update --check exit = %d, stderr=%q", exitCode, firstErr.String())
	}
	var secondOut, secondErr bytes.Buffer
	if exitCode := runWithDeps([]string{"--update"}, &secondOut, &secondErr, deps); exitCode != exitSuccess {
		t.Fatalf("splice --update exit = %d, stderr=%q", exitCode, secondErr.String())
	}
	if calls != 2 || len(got) != 2 || got[0].CurrentVersion != got[1].CurrentVersion || got[0].Repository != got[1].Repository || got[0].Endpoint != got[1].Endpoint || got[0].Timeout != got[1].Timeout || firstOut.String() != secondOut.String() {
		t.Fatalf("--update did not match update check path: calls=%d options=%#v first=%q second=%q", calls, got, firstOut.String(), secondOut.String())
	}
}

func TestInteractiveTUIDoesNotStartUpdateNotice(t *testing.T) {
	cwd := t.TempDir()
	setCLIUserConfigRoot(t)
	userConfigPath := filepath.Join(t.TempDir(), "splice", "config.json")
	stderr := &noticeTestWriter{done: make(chan struct{})}
	checkStarted := make(chan struct{})
	var checkOnce sync.Once
	var tuiRunning bool

	exitCode := runWithDeps([]string{}, io.Discard, stderr, appDeps{
		getwd: func() (string, error) { return cwd, nil },
		userConfigPath: func() (string, error) {
			return userConfigPath, nil
		},
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{MaxTurns: 12, DefaultProjectTrust: "always"}, nil
		},
		checkUpdate: func(context.Context, update.Options) (update.Result, error) {
			checkOnce.Do(func() { close(checkStarted) })
			return update.Result{CurrentVersion: "1.0.0", LatestVersion: "1.1.0", UpdateAvailable: true}, nil
		},
		resolveExecutable: func() (string, error) { return filepath.Join(t.TempDir(), "splice"), nil },
		registerMCPTools: func(context.Context, *tools.Registry, config.MCPConfig, mcp.RegisterOptions) (mcpToolRuntime, error) {
			return noopMCPRuntime{}, nil
		},
		runTUI: func(context.Context, tui.Options) int {
			tuiRunning = true
			select {
			case <-checkStarted:
				select {
				case <-stderr.done:
					t.Fatalf("update notice wrote to stderr while the TUI was running: %q", stderr.String())
				case <-time.After(100 * time.Millisecond):
				}
			case <-time.After(100 * time.Millisecond):
			}
			if got := stderr.String(); got != "" {
				t.Fatalf("stderr during TUI startup = %q, want no update notice", got)
			}
			return 0
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, stderr=%q", exitCode, stderr.String())
	}
	if !tuiRunning {
		t.Fatal("TUI did not start")
	}
}

type commandNoticeWriter struct {
	mu               sync.Mutex
	data             bytes.Buffer
	returned         bool
	wroteAfterReturn bool
}

func (w *commandNoticeWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.returned {
		w.wroteAfterReturn = true
	}
	return w.data.Write(data)
}

func (w *commandNoticeWriter) markReturned() {
	w.mu.Lock()
	w.returned = true
	w.mu.Unlock()
}

func (w *commandNoticeWriter) snapshot() (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.String(), w.wroteAfterReturn
}

func TestUpdateNoticeDeadlineDoesNotWriteAfterCommandReturns(t *testing.T) {
	t.Setenv("SPLICE_DISABLE_UPDATE_NOTICE", "")
	t.Setenv("SPLICE_DISABLE_UPDATES", "")
	t.Setenv("SPLICE_UPDATE_CACHE_PATH", filepath.Join(t.TempDir(), "update-check.json"))
	release := make(chan struct{})
	checkStarted := make(chan struct{})
	checkDone := make(chan struct{})
	writer := &commandNoticeWriter{}
	deps := fillAppDeps(appDeps{
		resolveExecutable: func() (string, error) { return filepath.Join(t.TempDir(), "splice"), nil },
		checkUpdate: func(context.Context, update.Options) (update.Result, error) {
			close(checkStarted)
			<-release
			close(checkDone)
			return update.Result{CurrentVersion: "1.0.0", LatestVersion: "1.1.0", UpdateAvailable: true}, nil
		},
	})

	started := time.Now()
	task := startUpdateNotice(deps)
	select {
	case <-checkStarted:
	case <-time.After(time.Second):
		t.Fatal("notice check did not start")
	}
	finishUpdateNotice(task, writer)
	writer.markReturned()
	close(release)
	select {
	case <-checkDone:
	case <-time.After(time.Second):
		t.Fatal("notice check did not finish after release")
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("command waited too long for courtesy notice: %s", elapsed)
	}
	data, wroteAfterReturn := writer.snapshot()
	if wroteAfterReturn || strings.Contains(data, "Update available") {
		t.Fatalf("notice writer after command return = %v, data = %q", wroteAfterReturn, data)
	}
}

var _ io.Writer = (*noticeTestWriter)(nil)
