package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Taf0711/splice/internal/secrets"
)

type herdrState string

const (
	herdrIdle    herdrState = "idle"
	herdrWorking herdrState = "working"
	herdrBlocked herdrState = "blocked"

	herdrSource         = "custom:splice"
	herdrAgent          = "splice"
	herdrQueueSize      = 8
	herdrCommandTimeout = 200 * time.Millisecond
	herdrCloseTimeout   = 2 * time.Second
)

type agentLifecycleReporter interface {
	Report(herdrState)
}

type herdrCommandRunner func(context.Context, string, ...string) error
type herdrLookPath func(string) (string, error)

type herdrReport struct {
	state herdrState
	seq   uint64
}

type herdrReporter struct {
	bin    string
	paneID string
	run    herdrCommandRunner
	queue  chan herdrReport
	done   chan struct{}

	mu         sync.Mutex
	seq        uint64
	last       herdrState
	closed     bool
	releaseSeq uint64
}

func newHerdrReporter(getenv func(string) string, lookPath herdrLookPath, runner herdrCommandRunner) *herdrReporter {
	if getenv == nil || strings.TrimSpace(getenv("HERDR_ENV")) != "1" {
		return nil
	}
	paneID := strings.TrimSpace(getenv("HERDR_PANE_ID"))
	if paneID == "" {
		return nil
	}
	bin := strings.TrimSpace(getenv("HERDR_BIN_PATH"))
	if bin == "" {
		if lookPath == nil {
			lookPath = exec.LookPath
		}
		resolved, err := lookPath("herdr")
		if err != nil {
			return nil
		}
		bin = resolved
	}
	if !filepath.IsAbs(bin) {
		return nil
	}
	if runner == nil {
		runner = runHerdrCommand
	}
	reporter := &herdrReporter{
		bin:    bin,
		paneID: paneID,
		run:    runner,
		queue:  make(chan herdrReport, herdrQueueSize),
		done:   make(chan struct{}),
	}
	go reporter.work()
	return reporter
}

func runHerdrCommand(ctx context.Context, bin string, args ...string) error {
	command := exec.CommandContext(ctx, bin, args...)
	command.Env = secrets.ScrubChildEnv(os.Environ())
	return command.Run()
}

func (reporter *herdrReporter) Report(state herdrState) {
	if reporter == nil || (state != herdrIdle && state != herdrWorking && state != herdrBlocked) {
		return
	}
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	if reporter.closed || reporter.last == state {
		return
	}
	reporter.seq++
	report := herdrReport{state: state, seq: reporter.seq}
	select {
	case reporter.queue <- report:
		reporter.last = state
	default:
		// Lifecycle changes are sparse. If Herdr is too slow, keep Splice responsive
		// and let a later state or the final release restore authority.
	}
}

func (reporter *herdrReporter) Close() {
	if reporter == nil {
		return
	}
	reporter.mu.Lock()
	if reporter.closed {
		reporter.mu.Unlock()
		return
	}
	reporter.closed = true
	reporter.seq++
	reporter.releaseSeq = reporter.seq
	close(reporter.queue)
	reporter.mu.Unlock()

	select {
	case <-reporter.done:
	case <-time.After(herdrCloseTimeout):
	}
}

func (reporter *herdrReporter) work() {
	defer close(reporter.done)
	for report := range reporter.queue {
		reporter.runReport(report)
	}
	reporter.runRelease(reporter.releaseSeq)
}

func (reporter *herdrReporter) runReport(report herdrReport) {
	ctx, cancel := context.WithTimeout(context.Background(), herdrCommandTimeout)
	defer cancel()
	_ = reporter.run(ctx, reporter.bin,
		"pane", "report-agent",
		"--source", herdrSource,
		"--agent", herdrAgent,
		"--state", string(report.state),
		"--seq", strconv.FormatUint(report.seq, 10),
		reporter.paneID,
	)
}

func (reporter *herdrReporter) runRelease(seq uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), herdrCommandTimeout)
	defer cancel()
	_ = reporter.run(ctx, reporter.bin,
		"pane", "release-agent",
		"--source", herdrSource,
		"--agent", herdrAgent,
		"--seq", strconv.FormatUint(seq, 10),
		reporter.paneID,
	)
}

func (m model) reportAgentLifecycle(state herdrState) {
	if m.herdr != nil {
		m.herdr.Report(state)
	}
}
