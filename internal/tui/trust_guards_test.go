package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	splicerun "github.com/Taf0711/splice/internal/splice"
)

// writeFakeGopls puts a gopls on PATH that records its start in a marker file.
func writeFakeGopls(t *testing.T) (marker string) {
	t.Helper()
	binDir := t.TempDir()
	marker = filepath.Join(t.TempDir(), "gopls-started")
	script := "#!/bin/sh\ntouch " + marker + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "gopls"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

// TestTUILanguageServerManagerIsTrustGated pins the trust argument of the
// session-long LSP manager. An untrusted model must not start a language server.
func TestTUILanguageServerManagerIsTrustGated(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, trusted := range []bool{false, true} {
		t.Run(fmt.Sprintf("trusted=%v", trusted), func(t *testing.T) {
			marker := writeFakeGopls(t)
			workspace := t.TempDir()
			file := filepath.Join(workspace, "main.go")
			if err := os.WriteFile(file, []byte("package main\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			m := newModel(context.Background(), Options{Cwd: workspace, Trusted: trusted})
			if m.lspManager == nil {
				t.Fatal("model has no LSP manager for its workspace")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = m.lspManager.Check(ctx, file, "package main\n")

			_, statErr := os.Stat(marker)
			if started := statErr == nil; started != trusted {
				t.Fatalf("language server started = %v, want %v", started, trusted)
			}
		})
	}
}

// TestTUISelfCorrectTestsAreTrustGated pins the IncludeTests gate of the TUI
// self-correct. The project test plan runs outside the sandbox, so it runs only
// in a trusted workspace, even when the user turned /selfcorrect tests on.
func TestTUISelfCorrectTestsAreTrustGated(t *testing.T) {
	originalRun := tuiSpliceRun
	defer func() { tuiSpliceRun = originalRun }()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	for _, trusted := range []bool{false, true} {
		t.Run(fmt.Sprintf("trusted=%v", trusted), func(t *testing.T) {
			workspace := t.TempDir()
			// A failing Go test: a run that executes the project plan reports a
			// failure, and a run that skips it reports none (the notes file has
			// no language server).
			writeGuardFile(t, filepath.Join(workspace, "go.mod"), "module example.invalid/guard\n\ngo 1.21\n")
			writeGuardFile(t, filepath.Join(workspace, "guard_test.go"), "package guard\n\nimport \"testing\"\n\nfunc TestFail(t *testing.T) { t.Fatal(\"boom\") }\n")
			writeGuardFile(t, filepath.Join(workspace, "notes.txt"), "x\n")

			var outcome agent.Outcome
			tuiSpliceRun = func(ctx context.Context, _ string, _ agent.Provider, options agent.Options, _ splicerun.MemoryStore, _ splicerun.WorkspaceRecovery) (agent.Result, error) {
				if options.SelfCorrect == nil {
					t.Fatal("TUI pipeline run has no self-corrector")
				}
				_, outcome = options.SelfCorrect.AfterEdit(ctx, []string{filepath.Join(workspace, "notes.txt")})
				return agent.Result{FinalAnswer: "done"}, nil
			}

			m := newModel(context.Background(), Options{Cwd: workspace, Trusted: trusted})
			m.selfCorrectTests = true
			if msg := m.runAgentWithOptions(1, context.Background(), "edit", nil, tuiAgentRunOptions{})(); msg.(agentResponseMsg).err != nil {
				t.Fatalf("run: %v", msg.(agentResponseMsg).err)
			}
			ran := outcome != agent.OutcomePassed
			if ran != trusted {
				t.Fatalf("project test plan ran = %v (outcome %s), want %v", ran, outcome, trusted)
			}
		})
	}
}

func writeGuardFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
