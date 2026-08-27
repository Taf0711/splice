package cognition

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/worktrees"
)

// freshnessGit runs git in dir and fails the test on error, mirroring the
// real-git fixture pattern in internal/worktrees/worktrees_test.go.
func freshnessGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	result, err := worktrees.DiffQuiet(context.Background(), nil, dir, args...)
	if err != nil || result < 0 {
		t.Fatalf("git %s: err=%v exit=%d", strings.Join(args, " "), err, result)
	}
	return ""
}

// newFreshnessRepo builds a real git repo with one commit containing the
// given files, returning the repo root and the first commit SHA.
func newFreshnessRepo(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	freshnessGit(t, root, "init", "-b", "main")
	freshnessGit(t, root, "config", "user.name", "splice-test")
	freshnessGit(t, root, "config", "user.email", "splice-test@local")
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	freshnessGit(t, root, "add", "-A")
	// Capture the commit SHA via rev-parse after committing.
	if _, err := exec.Command("git", "-C", root, "commit", "-m", "base").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return root, strings.TrimSpace(string(out))
}

func TestClassifyFreshness_Fresh(t *testing.T) {
	root, commit := newFreshnessRepo(t, map[string]string{
		"internal/auth/session.go": "package auth\n",
	})
	if got := ClassifyFreshness(context.Background(), root, commit, "internal/auth/session.go"); got != FreshnessFresh {
		t.Fatalf("freshness = %q, want fresh", got)
	}
}

func TestClassifyFreshness_Stale(t *testing.T) {
	root, commit := newFreshnessRepo(t, map[string]string{
		"internal/auth/session.go": "package auth\n",
	})
	// Change the file after the observed commit.
	if err := os.WriteFile(filepath.Join(root, "internal/auth/session.go"), []byte("package auth\n// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ClassifyFreshness(context.Background(), root, commit, "internal/auth/session.go"); got != FreshnessStale {
		t.Fatalf("freshness = %q, want stale", got)
	}
}

func TestClassifyFreshness_PackageDirStale(t *testing.T) {
	root, commit := newFreshnessRepo(t, map[string]string{
		"internal/auth/session.go": "package auth\n",
	})
	// A new file under the package dir changes the dir vs the commit.
	if err := os.WriteFile(filepath.Join(root, "internal/auth/token.go"), []byte("package auth\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Untracked files are not seen by git diff; track the change to make the
	// directory differ from the observed commit.
	freshnessGit(t, root, "add", "-A")
	if got := ClassifyFreshness(context.Background(), root, commit, "internal/auth"); got != FreshnessStale {
		t.Fatalf("freshness = %q, want stale (package dir changed)", got)
	}
}

func TestClassifyFreshness_UnknownOnBadCommit(t *testing.T) {
	root, _ := newFreshnessRepo(t, map[string]string{
		"internal/auth/session.go": "package auth\n",
	})
	if got := ClassifyFreshness(context.Background(), root, "deadbeef00000000000000000000000000000000", "internal/auth/session.go"); got != FreshnessUnknown {
		t.Fatalf("freshness = %q, want unknown (missing commit)", got)
	}
}

func TestClassifyFreshness_UnknownOnEmptyInput(t *testing.T) {
	root, _ := newFreshnessRepo(t, map[string]string{
		"internal/auth/session.go": "package auth\n",
	})
	if got := ClassifyFreshness(context.Background(), root, "", "internal/auth/session.go"); got != FreshnessUnknown {
		t.Fatalf("empty commit = %q, want unknown", got)
	}
	if got := ClassifyFreshness(context.Background(), root, "HEAD", ""); got != FreshnessUnknown {
		t.Fatalf("empty anchor = %q, want unknown", got)
	}
	if got := ClassifyFreshness(context.Background(), "", "HEAD", "internal/auth/session.go"); got != FreshnessUnknown {
		t.Fatalf("empty repo root = %q, want unknown", got)
	}
}
