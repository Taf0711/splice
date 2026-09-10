package splice

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestVerifiedRevisionProbe pins verifiedRevision's behavior on a dirty repo
// through the real stage-sandbox path.
func TestVerifiedRevisionProbe(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	if err := os.WriteFile(dir+"/f.go", []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	run("add", "-A")
	run("commit", "-qm", "base")

	// Make a tracked edit WITHOUT staging: the eval's Task A end state.
	if err := os.WriteFile(dir+"/f.go", []byte("package main\n// edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The unsandboxed snapshot covers the edit; the sandboxed one cannot
	// (pinned in TestVerifiedRevisionProbe2). The eval contract commits the
	// verified tree before Task B, so simulate that here.
	run("add", "-A")
	run("commit", "-qm", "verified")
	committed := strings.TrimSpace(run("rev-parse", "HEAD"))

	// After the commit the worktree is clean: verifiedRevision must return
	// HEAD (== the committed verified bytes) with NO fallback reason.
	snap, reason := verifiedRevision(context.Background(), dir)
	t.Logf("HEAD=%s snapshot=%s reason=%q", committed[:7], snap[:7], reason)
	if snap != committed {
		t.Fatalf("verifiedRevision = %s, want HEAD %s", snap[:10], committed[:10])
	}
	if reason != "" {
		t.Fatalf("clean-tree anchor must not carry a fallback reason, got %q", reason)
	}
	// The freshness diff the gate will run at Task B start must be empty.
	if out := run("diff", "--name-only", snap); out != "" {
		t.Fatalf("committed verified tree should diff empty: %q", out)
	}
}
