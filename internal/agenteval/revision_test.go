package agenteval

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Stamp a committed workspace: HEAD lands in the stamp.
func TestStampRevisionCommittedWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.name", "t")
	git("config", "user.email", "t@local")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-m", "base")

	runner := Runner{}
	stamp := runner.stampRevision(context.Background(), root, "suite-rev-7")
	if stamp.WorkspaceCommit == "" {
		t.Fatal("committed workspace must stamp a HEAD commit")
	}
	if len(stamp.WorkspaceDirty) != 0 {
		t.Fatalf("clean workspace stamped dirty: %v", stamp.WorkspaceDirty)
	}
	if stamp.SuiteRevision != "suite-rev-7" {
		t.Fatalf("suite revision = %q", stamp.SuiteRevision)
	}
}

// A dirty workspace records which paths differ (section 31: the evidence
// inputs must be provable, and dirtiness is part of that proof).
func TestStampRevisionDirtyWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.name", "t")
	git("config", "user.email", "t@local")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n// dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := Runner{}
	stamp := runner.stampRevision(context.Background(), root, "")
	if stamp.WorkspaceCommit == "" {
		t.Fatal("missing HEAD commit")
	}
	if len(stamp.WorkspaceDirty) != 1 || stamp.WorkspaceDirty[0] != "main.go" {
		t.Fatalf("dirty paths = %v, want [main.go]", stamp.WorkspaceDirty)
	}
}

// A non-repo workspace yields an EMPTY stamp (unproven provenance stays
// visibly absent, never fabricated).
func TestStampRevisionNonRepoStaysEmpty(t *testing.T) {
	root := t.TempDir()
	runner := Runner{}
	stamp := runner.stampRevision(context.Background(), root, "suite-rev-1")
	if stamp.WorkspaceCommit != "" || len(stamp.WorkspaceDirty) != 0 {
		t.Fatalf("non-repo must stamp empty, got %+v", stamp)
	}
	if stamp.SuiteRevision != "suite-rev-1" {
		t.Fatalf("caller suite revision must survive: %q", stamp.SuiteRevision)
	}
}

// Score copies the revision stamp onto the report.
func TestScoreCarriesRevisionStamp(t *testing.T) {
	suite := Suite{ID: "s", Tasks: []Task{{
		ID:                   "t1",
		Prompt:               "p",
		VerificationCommands: []Command{{ID: "v1", Name: "pass", Command: []string{"true"}}},
	}}}
	report := Score(suite, ScoreInput{
		TaskID:         "t1",
		CommandResults: []CommandResult{{ID: "v1", ExitCode: 0}},
		Revision:       &RevisionStamp{WorkspaceCommit: "abc", SuiteRevision: "rev-1"},
	})
	if report.Revision.WorkspaceCommit != "abc" || report.Revision.SuiteRevision != "rev-1" {
		t.Fatalf("report revision = %+v", report.Revision)
	}
}
