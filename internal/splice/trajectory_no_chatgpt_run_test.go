package splice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/tools"
)

// With no ChatGPT sign-in profile, a run keeps the rules, says so once, and
// starts no classifier. This goes through splice.Run with the default flags and
// no decision_advisor section. The CLI is not used, because the CLI exec tests
// create worktrees in the real repository.
func TestRunWithoutChatGPTProfileSaysRulesOnly(t *testing.T) {
	t.Setenv(DecisionAdvisorModeEnvVar, "")
	t.Setenv(DecisionAdvisorModelEnvVar, "")
	t.Setenv(TrajectoryPackEnvVar, "")
	t.Setenv(DecisionRecordsDirEnvVar, "")
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.25\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "main_test.go"), []byte("package main\nimport \"testing\"\nfunc TestHello(t *testing.T) { if Hello() != \"hello\" { t.Fatal(\"wrong greeting\") } }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(workDir))
	registry.Register(tools.NewListDirectoryTool(workDir))
	registry.Register(tools.NewGrepTool(workDir))
	registry.Register(tools.NewWriteFileTool(workDir))
	registry.Register(tools.NewBashTool(workDir))
	var progress strings.Builder
	options := agent.Options{
		SessionID:      "no-chatgpt-run",
		Cwd:            workDir,
		Registry:       registry,
		FileTracker:    tools.NewFileTracker(),
		PermissionMode: agent.PermissionModeAuto,
		Flags:          flags.Set{},
		OnReasoning:    func(s string) { progress.WriteString(s) },
		// No decision_advisor section: the resolver found no ChatGPT profile, so
		// the section stays empty and no evaluator is given.
	}
	if _, err := Run(context.Background(), "add a Hello function and tests", &decisionRecordRunProvider{}, options, nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(progress.String(), "rules only (no ChatGPT sign-in)"); n != 1 {
		t.Fatalf("progress must say the rules decide exactly once, got %d:\n%s", n, progress.String())
	}
	if strings.Contains(progress.String(), "classifier=") {
		t.Fatalf("no classifier decision may run:\n%s", progress.String())
	}
}
