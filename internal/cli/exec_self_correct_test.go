package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestParseExecSelfCorrectFlag(t *testing.T) {
	t.Run("absent defaults to false", func(t *testing.T) {
		options, help, err := parseExecArgs([]string{"hello"})
		if err != nil {
			t.Fatalf("parseExecArgs returned error: %v", err)
		}
		if help {
			t.Fatal("help = true, want false")
		}
		if options.selfCorrect {
			t.Fatal("selfCorrect = true, want false by default")
		}
	})

	t.Run("flag sets true", func(t *testing.T) {
		options, _, err := parseExecArgs([]string{"--self-correct", "hello"})
		if err != nil {
			t.Fatalf("parseExecArgs returned error: %v", err)
		}
		if !options.selfCorrect {
			t.Fatal("selfCorrect = false, want true")
		}
		if strings.Join(options.promptParts, " ") != "hello" {
			t.Fatalf("promptParts = %#v, want [hello]", options.promptParts)
		}
	})

	t.Run("flag rejects an inline value", func(t *testing.T) {
		_, _, err := parseExecArgs([]string{"--self-correct=yes", "hello"})
		if err == nil {
			t.Fatal("expected an error for --self-correct=yes, got nil")
		}
	})

	t.Run("rejects combination with --use-spec", func(t *testing.T) {
		// --use-spec routes through the spec-draft (planning) path, which never wires
		// the post-edit self-correct loop. Accepting --self-correct there would
		// silently ignore it, so the combination is rejected at parse time.
		_, _, err := parseExecArgs([]string{"--use-spec", "--self-correct", "hello"})
		if err == nil {
			t.Fatal("expected an error for --use-spec with --self-correct, got nil")
		}
		if !strings.Contains(err.Error(), "--self-correct") || !strings.Contains(err.Error(), "--use-spec") {
			t.Fatalf("error should name both flags, got %q", err.Error())
		}
	})
}

func TestRunExecHelpDocumentsSelfCorrect(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run([]string{"exec", "--help"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout.String(), "--self-correct") {
		t.Fatalf("expected exec help to document --self-correct, got %q", stdout.String())
	}
}

// TestRunExecSelfCorrectSkippedWhenUntrusted pins the trust gate: the project
// test plan runs outside the sandbox, so --self-correct is a no-op with a
// notice in an untrusted workspace.
func TestRunExecSelfCorrectSkippedWhenUntrusted(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(fmt.Sprintf("trusted=%v", trusted), func(t *testing.T) {
			root := hostileProjectWorkspace(t, "", `{}`)
			args := []string{"exec", "--cwd", root, "--loop", "--self-correct", "--output-format", "stream-json", "finish the task"}
			if trusted {
				args = append([]string{"--trust"}, args...)
			}
			var stdout, stderr strings.Builder
			exitCode := runWithDeps(args, &stdout, &stderr, appDeps{
				getwd:          func() (string, error) { return root, nil },
				userConfigPath: func() (string, error) { return root + "/config.json", nil },
				resolveConfig: func(_ string, overrides config.Overrides) (config.ResolvedConfig, error) {
					resolved := execStageResolvedConfig(overrides.Provider.Model, 2)
					resolved.DefaultProjectTrust = "ask"
					return resolved, nil
				},
				newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
					return loopFinalProvider{}, nil
				},
			})
			if exitCode != exitSuccess {
				t.Fatalf("exit = %d, stderr=%s", exitCode, stderr.String())
			}
			notice := strings.Contains(stderr.String(), "--self-correct skipped: the workspace is not trusted")
			if notice == trusted {
				t.Fatalf("trusted=%v: self-correct skip notice = %v, stderr=%s", trusted, notice, stderr.String())
			}
		})
	}
}

// TestExecSelfCorrectorStartsLanguageServerOnlyWhenTrusted pins the trust
// argument of the exec language-server manager. A fake gopls on PATH records
// its start. An untrusted workspace must never start it, even for a diagnostics
// check on an edited Go file. A trusted workspace starts it, so the test can
// fail in both directions.
func TestExecSelfCorrectorStartsLanguageServerOnlyWhenTrusted(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(fmt.Sprintf("trusted=%v", trusted), func(t *testing.T) {
			binDir := t.TempDir()
			marker := filepath.Join(t.TempDir(), "gopls-started")
			script := "#!/bin/sh\ntouch " + marker + "\nexit 1\n"
			if err := os.WriteFile(filepath.Join(binDir, "gopls"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			workspace := t.TempDir()
			file := filepath.Join(workspace, "main.go")
			if err := os.WriteFile(file, []byte("package main\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			_, fileDiagnostics, cleanup := newExecSelfCorrector(true, workspace, "auto", trusted)
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			fileDiagnostics(ctx, file)

			_, statErr := os.Stat(marker)
			if started := statErr == nil; started != trusted {
				t.Fatalf("language server started = %v, want %v", started, trusted)
			}
		})
	}
}
