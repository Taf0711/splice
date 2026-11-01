package cli

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/worktrees"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type fakeExecController struct {
	calls atomic.Int32
}

func (f *fakeExecController) Name() string             { return tools.ExecCommandToolName }
func (f *fakeExecController) Description() string      { return "fake exec controller" }
func (f *fakeExecController) Parameters() tools.Schema { return tools.Schema{} }
func (f *fakeExecController) Safety() tools.Safety     { return tools.Safety{} }
func (f *fakeExecController) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{}
}
func (f *fakeExecController) ExecSessions() []tools.ExecSessionSnapshot { return nil }
func (f *fakeExecController) StopExecSession(int) bool                  { return false }
func (f *fakeExecController) StopAllExecSessions() []int {
	f.calls.Add(1)
	return []int{1}
}

func TestExecSessionStopperCallsController(t *testing.T) {
	registry := tools.NewRegistry()
	fake := &fakeExecController{}
	registry.Register(fake)
	execSessionStopper(lookupExecSessionController(registry))()
	if fake.calls.Load() != 1 {
		t.Fatalf("StopAllExecSessions calls = %d, want 1", fake.calls.Load())
	}
}

func TestExecSessionStopperIgnoresMissingTool(t *testing.T) {
	execSessionStopper(lookupExecSessionController(nil))()
	execSessionStopper(lookupExecSessionController(tools.NewRegistry()))()
}

// P2-4 (a), P3-2, P3-3: runExec looks up the exec session controller once,
// stops the background sessions as soon as the run returns (before
// merge-back), and stops them again on return as a safety net.
func TestRunExecStopsBackgroundSessionsBeforeMergeBack(t *testing.T) {
	fake := &fakeExecController{}
	var lookups atomic.Int32
	original := lookupExecSessionController
	lookupExecSessionController = func(*tools.Registry) tools.ExecSessionController {
		lookups.Add(1)
		return fake
	}
	t.Cleanup(func() { lookupExecSessionController = original })

	root := t.TempDir()
	worktreeDir := t.TempDir()
	initTestGitWorktree(t, root, worktreeDir)
	var stdout, stderr bytes.Buffer
	stopsBeforeMerge := int32(-1)
	exitCode := runWithDeps([]string{"exec", "--worktree", "task-a", "--merge-back", "--allow-unverified-merge", "hello"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return root, nil },
		prepareWorktree: func(context.Context, worktrees.Options) (worktrees.Result, error) {
			return worktrees.Result{Name: "task-a", Path: worktreeDir, RepoRoot: root}, nil
		},
		mergeBackWorktree: func(context.Context, worktrees.MergeBackOptions) (worktrees.MergeBackResult, error) {
			stopsBeforeMerge = fake.calls.Load()
			return worktrees.MergeBackResult{Status: worktrees.MergeBackMerged, Message: "merged"}, nil
		},
		unlockWorktree: func(context.Context, worktrees.UnlockOptions) error { return nil },
		removeWorktree: func(context.Context, worktrees.RemoveOptions) error { return nil },
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return execResolvedConfig(), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return newExecStageAwareProvider(execStageProviderOptions{
				Files:     []schemas.FileChange{{Path: "hello.txt", Content: "hi\n", ChangeType: "create"}},
				TestFiles: []schemas.FileChange{{Path: "hello-check.txt", Content: "ok\n", ChangeType: "create"}},
			}), nil
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	if lookups.Load() != 1 {
		t.Fatalf("controller lookups = %d, want 1", lookups.Load())
	}
	if stopsBeforeMerge < 1 {
		t.Fatalf("stops before merge-back = %d, want the sessions stopped when the run returned", stopsBeforeMerge)
	}
	if fake.calls.Load() < 2 {
		t.Fatalf("stops = %d, want the return safety net too", fake.calls.Load())
	}
}
