package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	splicerun "github.com/Taf0711/splice/internal/splice"
)

// SPLICE_TUI_DEMO=worktree-reject is a demo-only seam for scripts/tui-worktree-reject.tape.
// Unset or any other value calls splicerun.Run unchanged. The tape types /exec; this
// replay only replaces the pipeline runner. Worktree prepare, lock, and the TW2
// review stay on the real path.
const tuiDemoWorktreeReject = "worktree-reject"

func tuiDemoReplayActive() bool {
	return strings.TrimSpace(os.Getenv("SPLICE_TUI_DEMO")) == tuiDemoWorktreeReject
}

func tuiSpliceRunOrDemo(ctx context.Context, prompt string, provider agent.Provider, options agent.Options, mem splicerun.MemoryStore, rec splicerun.WorkspaceRecovery) (agent.Result, error) {
	if !tuiDemoReplayActive() {
		return splicerun.Run(ctx, prompt, provider, options, mem, rec)
	}
	return replayWorktreeRejectDemo(ctx, options)
}

var demoStepPause = 400 * time.Millisecond

func replayWorktreeRejectDemo(ctx context.Context, options agent.Options) (agent.Result, error) {
	emit := func(name, status, detail string, progress int, files []string) {
		if options.OnStageEvent == nil {
			return
		}
		options.OnStageEvent(agent.StageEvent{Name: name, Status: status, Detail: detail, Progress: progress, ChangedFiles: files})
	}
	steps := []struct {
		name, status, detail string
		progress             int
		files                []string
	}{
		{"code_writer", "running", "writing add.go", 40, []string{"add.go"}},
		{"code_writer", "completed", "wrote add.go", 100, []string{"add.go"}},
		{"test_runner", "running", "go test ./...", 30, nil},
		{"test_runner", "failed", "TestAdd failed", 100, nil},
		{"step-back", "skipped", "no progress; retry from last good snapshot", 0, nil},
		{"code_writer", "running", "revising add.go", 20, []string{"add.go"}},
		{"code_writer", "completed", "revised add.go", 100, []string{"add.go"}},
		{"test_runner retry", "completed", "go test ./...", 100, nil},
	}
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return agent.Result{}, err
		}
		emit(step.name, step.status, step.detail, step.progress, step.files)
		if demoStepPause > 0 {
			select {
			case <-ctx.Done():
				return agent.Result{}, ctx.Err()
			case <-time.After(demoStepPause):
			}
		}
	}
	if dir := strings.TrimSpace(options.Cwd); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "add.go"), []byte("package add\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644)
	}
	return agent.Result{FinalAnswer: "Fixed the planted failing test in the worktree."}, nil
}
