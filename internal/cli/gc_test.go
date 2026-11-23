package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunGCDryRunOnEmptyTempRoot(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"gc", "--dry-run"}, &stdout, &stderr, appDeps{})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "Would remove") {
		t.Fatalf("expected dry-run output to start with %q, got %q", "Would remove", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Nothing was deleted.") {
		t.Fatalf("expected dry-run output to say nothing was deleted, got %q", stdout.String())
	}
}
