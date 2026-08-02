package stages

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// A typecheck exit code was previously being reported as a test result.
func TestTestCommandPrefersPackageTestOverTypecheck(t *testing.T) {
	workDir := t.TempDir()
	writeTestRunnerFixture(t, filepath.Join(workDir, "package.json"), `{"scripts":{"typecheck":"tsc --noEmit","test":"npm test"}}`)

	got, err := testCommand(workDir, "javascript")
	if err != nil {
		t.Fatalf("testCommand returned error: %v", err)
	}
	want := []string{"npm", "run", "test"}
	if !equalTestRunnerStrings(got, want) {
		t.Fatalf("test command = %#v, want %#v", got, want)
	}
}

// Non-test checks must not be selected as a test command.
func TestTestCommandReturnsNoneForNonTestChecks(t *testing.T) {
	workDir := t.TempDir()
	writeTestRunnerFixture(t, filepath.Join(workDir, "package.json"), `{"scripts":{"typecheck":"tsc --noEmit","build":"npm run compile","lint":"eslint ."}}`)

	got, err := testCommand(workDir, "javascript")
	if err != nil {
		t.Fatalf("testCommand returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("test command = %#v, want no command", got)
	}
}

// A non-Go workspace with no checks must not inherit the Go test default.
func TestTestCommandDoesNotDefaultNonGoWorkspaceToGo(t *testing.T) {
	workDir := t.TempDir()

	got, err := testCommand(workDir, "javascript")
	if err != nil {
		t.Fatalf("testCommand returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("test command = %#v, want no command", got)
	}
}

// An undetectable non-Go workspace must report skipped verification, not failure.
func TestTestRunnerReportsSkippedWhenNoCommandDetected(t *testing.T) {
	workDir := t.TempDir()
	output, err := (TestRunner{}).Run(context.Background(), newHarnessInput("verify workspace"), &fakeProvider{}, StageOptions{
		WorkDir:  workDir,
		Language: "javascript",
	})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if !strings.Contains(output.Detail, "verification could not run") {
		t.Fatalf("detail = %q, want honest verification limitation", output.Detail)
	}
	results, ok := output.Data["test_results"].(schemas.TestRunResults)
	if !ok {
		t.Fatalf("test_results = %#v, want schemas.TestRunResults", output.Data["test_results"])
	}
	if len(results.Tests) != 1 {
		t.Fatalf("test results = %#v, want one suite result", results.Tests)
	}
	if results.Tests[0].Status != "skipped" {
		t.Fatalf("suite status = %q, want skipped", results.Tests[0].Status)
	}
}

// A Go workspace with go.mod must keep resolving the Go test command.
func TestTestCommandUsesGoTestsForGoModule(t *testing.T) {
	workDir := t.TempDir()
	writeTestRunnerFixture(t, filepath.Join(workDir, "go.mod"), "module example.com/testfixture\n\ngo 1.25\n")

	got, err := testCommand(workDir, "go")
	if err != nil {
		t.Fatalf("testCommand returned error: %v", err)
	}
	want := []string{"go", "test", "./..."}
	if !equalTestRunnerStrings(got, want) {
		t.Fatalf("test command = %#v, want %#v", got, want)
	}
}

// A Go language hint without go.mod must not resolve the Go test command.
func TestTestCommandDoesNotUseGoTestsWithoutGoModule(t *testing.T) {
	workDir := t.TempDir()

	got, err := testCommand(workDir, "go")
	if err != nil {
		t.Fatalf("testCommand returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("test command = %#v, want no command", got)
	}
}

func writeTestRunnerFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func equalTestRunnerStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
