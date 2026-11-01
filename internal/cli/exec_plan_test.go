package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/worktrees"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestRunExecPlanHappyPathTextAndStreamJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		args func(string) []string
	}{
		{name: "text", args: func(path string) []string { return []string{"exec", "--plan", path} }},
		{name: "stream-json", args: func(path string) []string {
			return []string{"exec", "--plan", path, "--output-format", "stream-json"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			cwd := t.TempDir()
			planPath := writeExecPlanFile(t, cwd, cliDesignPlan([]schemas.Task{
				cliDesignTask("t1", "First", nil),
				cliDesignTask("t2", "Second", []string{"t1"}),
			}))
			args := tc.args(planPath)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runWithDeps(args, &stdout, &stderr, appDeps{
				getwd: func() (string, error) {
					return cwd, nil
				},
				resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
					return execResolvedConfig(), nil
				},
				newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
					return newExecStageAwareProvider(execStageProviderOptions{}), nil
				},
			})

			if exitCode != exitSuccess {
				t.Fatalf("exitCode = %d, stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
			}
			if tc.name == "stream-json" && stderr.Len() != 0 {
				t.Fatalf("expected empty stderr, got %q", stderr.String())
			}
			if tc.name == "stream-json" {
				events := decodeJSONLines(t, stdout.String())
				final := findJSONEvent(t, events, "final")
				if !strings.Contains(final["text"].(string), `"status": "completed"`) ||
					!strings.Contains(final["text"].(string), `"completed_tasks"`) {
					t.Fatalf("unexpected final event: %#v", final)
				}
				reasoning := false
				for _, event := range events {
					if event["type"] == "reasoning" && strings.Contains(event["delta"].(string), "Starting task 1/2 t1: First") {
						reasoning = true
					}
				}
				if !reasoning {
					t.Fatalf("expected per-task reasoning event in %#v", events)
				}
				return
			}
			if !strings.Contains(stdout.String(), "Design plan completed") {
				t.Fatalf("unexpected text output: %s", stdout.String())
			}
		})
	}
}

func TestRunExecPlanWorktreeResolvesRelativePlanAgainstSourceWorkspace(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cwd := t.TempDir()
	worktreeDir := t.TempDir()
	initTestGitWorktree(t, cwd, worktreeDir)
	// The plan file exists only in the source workspace, not in the worktree.
	writeExecPlanFile(t, cwd, cliDesignPlan([]schemas.Task{cliDesignTask("t1", "First", nil)}))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "--plan", "plan.json", "--worktree"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		prepareWorktree: func(_ context.Context, options worktrees.Options) (worktrees.Result, error) {
			if options.Cwd != cwd {
				t.Fatalf("worktree cwd = %q, want %q", options.Cwd, cwd)
			}
			return worktrees.Result{Name: "plan-run", Path: worktreeDir, RepoRoot: cwd}, nil
		},
		resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
			return execResolvedConfig(), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return newExecStageAwareProvider(execStageProviderOptions{}), nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d, stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Design plan completed") {
		t.Fatalf("unexpected text output: %s", stdout.String())
	}
}

func TestRunExecPlanUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "use spec", args: []string{"exec", "--plan", "plan.json", "--use-spec"}, want: "--plan cannot be combined with --use-spec."},
		{name: "file", args: []string{"exec", "--plan", "plan.json", "--file", "prompt.txt"}, want: "--plan cannot be combined with --file."},
		{name: "prompt", args: []string{"exec", "--plan", "plan.json", "do it"}, want: "--plan cannot be combined with a prompt argument."},
		{name: "stream input", args: []string{"exec", "--plan", "plan.json", "--input-format", "stream-json"}, want: "--plan cannot be combined with --input-format stream-json."},
		{name: "resume id", args: []string{"exec", "--plan", "plan.json", "--resume", "session-1"}, want: "--plan cannot be combined with --resume or --fork."},
		{name: "resume latest", args: []string{"exec", "--plan", "plan.json", "--resume"}, want: "--plan cannot be combined with --resume or --fork."},
		{name: "fork", args: []string{"exec", "--plan", "plan.json", "--fork", "session-1"}, want: "--plan cannot be combined with --resume or --fork."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := Run(tc.args, &stdout, &stderr)

			if exitCode != exitUsage {
				t.Fatalf("exitCode = %d, want %d", exitCode, exitUsage)
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

func TestRunExecPlanFileErrors(t *testing.T) {
	tests := []struct {
		name  string
		write func(t *testing.T, cwd string) string
		want  string
	}{
		{
			name: "missing file",
			write: func(t *testing.T, cwd string) string {
				t.Helper()
				return filepath.Join(cwd, "missing.json")
			},
			want: "design plan file not found:",
		},
		{
			name: "invalid json",
			write: func(t *testing.T, cwd string) string {
				t.Helper()
				path := filepath.Join(cwd, "bad.json")
				if err := os.WriteFile(path, []byte(`{"epic":`), 0644); err != nil {
					t.Fatalf("write plan: %v", err)
				}
				return path
			},
			want: "failed to decode design plan",
		},
		{
			name: "unknown field",
			write: func(t *testing.T, cwd string) string {
				t.Helper()
				plan := cliDesignPlan([]schemas.Task{cliDesignTask("t1", "First", nil)})
				data, err := json.Marshal(plan)
				if err != nil {
					t.Fatalf("marshal plan: %v", err)
				}
				raw := strings.TrimSuffix(string(data), "}") + `,"unexpected":true}`
				path := filepath.Join(cwd, "unknown.json")
				if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
					t.Fatalf("write plan: %v", err)
				}
				return path
			},
			want: `json: unknown field "unexpected"`,
		},
		{
			name: "validate failure",
			write: func(t *testing.T, cwd string) string {
				t.Helper()
				plan := cliDesignPlan([]schemas.Task{cliDesignTask("t1", "First", nil)})
				plan.Epic = ""
				return writeExecPlanFile(t, cwd, plan)
			},
			want: "invalid design plan",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			planPath := tc.write(t, cwd)
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := runWithDeps([]string{"exec", "--plan", planPath}, &stdout, &stderr, appDeps{
				getwd: func() (string, error) {
					return cwd, nil
				},
			})

			if exitCode != exitUsage {
				t.Fatalf("exitCode = %d, want %d", exitCode, exitUsage)
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

func writeExecPlanFile(t *testing.T, cwd string, plan schemas.DesignPlan) string {
	t.Helper()
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	path := filepath.Join(cwd, "plan.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	return path
}

func cliDesignTask(id string, title string, dependsOn []string) schemas.Task {
	return schemas.Task{
		ID:        id,
		Title:     title,
		Intent:    "Implement " + title,
		DependsOn: append([]string(nil), dependsOn...),
	}
}

func cliDesignPlan(tasks []schemas.Task) schemas.DesignPlan {
	return schemas.DesignPlan{
		Epic:         "Execute CLI design plan",
		Requirements: []string{"Run plan tasks"},
		InScope:      []string{"Plan runner"},
		OutOfScope:   []string{"Worktrees"},
		SystemDesign: "Tasks are independent.",
		Tasks:        tasks,
		Source:       "authored",
	}
}

// TestRunExecWorktreeRefusesLaunchDirMissingFromWorktree pins the H-08
// wiring: a -w run from a repository subdirectory works in that subdirectory
// of the worktree, so a subdirectory that the worktree does not hold fails
// loud instead of falling back to the worktree root.
func TestRunExecWorktreeRefusesLaunchDirMissingFromWorktree(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	repo := t.TempDir()
	launch := filepath.Join(repo, "untracked-sub")
	if err := os.MkdirAll(launch, 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeDir := t.TempDir()
	for _, tc := range []struct {
		name        string
		reused      bool
		removeErr   error
		wantRemoved bool
		wantText    string
	}{
		{name: "fresh worktree is removed", wantRemoved: true},
		{name: "reused worktree stays", reused: true},
		{name: "failed removal names the worktree", removeErr: errors.New("git refused"), wantRemoved: true, wantText: "the worktree remains at " + worktreeDir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var unlocked bool
			var removed []worktrees.RemoveOptions
			var stdout, stderr bytes.Buffer
			exitCode := runWithDeps([]string{"exec", "--worktree=sub-run", "do it"}, &stdout, &stderr, appDeps{
				getwd: func() (string, error) { return launch, nil },
				prepareWorktree: func(_ context.Context, options worktrees.Options) (worktrees.Result, error) {
					return worktrees.Result{Name: "sub-run", Path: worktreeDir, RepoRoot: repo, Locked: true, Reused: tc.reused}, nil
				},
				unlockWorktree: func(context.Context, worktrees.UnlockOptions) error {
					unlocked = true
					return nil
				},
				removeWorktree: func(_ context.Context, options worktrees.RemoveOptions) error {
					if !unlocked {
						t.Fatal("removal before unlock")
					}
					removed = append(removed, options)
					return tc.removeErr
				},
				resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
					return execResolvedConfig(), nil
				},
				newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
					t.Fatal("the run must not reach the provider")
					return nil, nil
				},
			})
			if exitCode != exitUsage {
				t.Fatalf("exitCode = %d, want %d; stderr=%s", exitCode, exitUsage, stderr.String())
			}
			for _, want := range []string{"launch directory untracked-sub is not in the worktree", "untracked or ignored", "Commit the directory, or run from the repository root", tc.wantText} {
				if !strings.Contains(stderr.String(), want) {
					t.Fatalf("stderr = %q, want %q", stderr.String(), want)
				}
			}
			if !unlocked {
				t.Fatal("the failed launch must release the worktree lock")
			}
			if got := len(removed) == 1 && removed[0].Path == worktreeDir && removed[0].Force; got != tc.wantRemoved || (!tc.wantRemoved && len(removed) != 0) {
				t.Fatalf("removed = %+v, want removed=%v", removed, tc.wantRemoved)
			}
		})
	}
}

// P1-1: a design plan is verified only when every task verified. A plan whose
// tasks verify passes --require-verified; an unverified task exits 4 and the
// reason names the task.
func TestRunExecPlanRequireVerified(t *testing.T) {
	for _, tc := range []struct {
		name     string
		options  execStageProviderOptions
		wantExit int
	}{
		{name: "verified", options: execStageProviderOptions{}, wantExit: exitSuccess},
		{name: "unverified", options: execStageProviderOptions{
			Files:     []schemas.FileChange{{Path: "hello.txt", Content: "hi\n", ChangeType: "create"}},
			TestFiles: []schemas.FileChange{{Path: "hello-check.txt", Content: "ok\n", ChangeType: "create"}},
		}, wantExit: exitIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			cwd := t.TempDir()
			planPath := writeExecPlanFile(t, cwd, cliDesignPlan([]schemas.Task{cliDesignTask("t1", "First", nil)}))
			var stdout, stderr bytes.Buffer
			exitCode := runWithDeps([]string{"exec", "--plan", planPath, "--require-verified"}, &stdout, &stderr, appDeps{
				getwd: func() (string, error) { return cwd, nil },
				resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
					return execResolvedConfig(), nil
				},
				newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
					return newExecStageAwareProvider(tc.options), nil
				},
			})
			if exitCode != tc.wantExit {
				t.Fatalf("exit = %d, want %d; stdout=%s stderr=%s", exitCode, tc.wantExit, stdout.String(), stderr.String())
			}
			if tc.wantExit == exitIncomplete && !strings.Contains(stderr.String(), "task t1: ") {
				t.Fatalf("stderr = %q, want the unverified reason to name task t1", stderr.String())
			}
		})
	}
}
