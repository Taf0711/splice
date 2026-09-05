package splice

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/splice/stages"
)

func TestVerifiedRevisionProbe2(t *testing.T) {
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
	if err := os.WriteFile(dir+"/f.go", []byte("package main\n// edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Direct stash create (no sandbox): what does plain git give?
	direct := run("stash", "create")
	t.Logf("direct stash create: %s", direct)

	// Sandboxed stash create via the same path verifiedRevision uses.
	// Sandboxed stash create via the same path verifiedRevision uses: the
	// stage sandbox is write-scoped and REFUSES the object-database write.
	// This pin documents the substrate decision: widening the stage profile
	// for stash create would be a reviewed sandbox change, not an accident.
	engine := procrun.NewStageEngine(dir)
	revCmd, plan, cerr := stages.PrepareStageCommand(context.Background(), engine, dir, []string{"git", "-C", dir, "stash", "create"})
	if cerr != nil {
		t.Fatalf("PrepareStageCommand: %v", cerr)
	}
	defer plan.Cleanup()
	var stderrSink bytes.Buffer
	revCmd.Stderr = &stderrSink
	out, err := revCmd.Output()
	t.Logf("sandboxed stash create: out=%q err=%v stderr=%q", string(out), err, stderrSink.String())
	if err == nil && strings.TrimSpace(string(out)) != "" {
		t.Fatalf("sandboxed stash create unexpectedly succeeded (%s): the stage sandbox now allows object writes; re-verify the anchoring story", strings.TrimSpace(string(out)))
	}
	// The direct snapshot covers the edit (freshness would pass with it);
	// the anchor story relies on the caller committing the verified tree.
	_ = direct
}
