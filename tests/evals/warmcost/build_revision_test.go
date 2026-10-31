package warmcost

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mustGit runs a git command in dir and fails the test on error.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestBinaryRevisionHonorsExplicitOverride proves the --binary-revision value
// is authoritative even when the repo derivation would return a different value
// (here, an empty one because RepoDir is not a git repository).
func TestBinaryRevisionHonorsExplicitOverride(t *testing.T) {
	r, err := NewRunner(Config{
		Binary:         "/bin/true",
		OutDir:         t.TempDir(),
		RepoDir:        t.TempDir(), // not a git repo, so the fallback is ""
		BinaryRevision: "d9c52b3abcdef0",
		Repeats:        1,
		Tasks:          []Task{{ID: "t", Prompt: "p", Check: "true"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.binaryRevision(); got != "d9c52b3abcdef0" {
		t.Fatalf("binaryRevision() = %q, want the explicit override", got)
	}
}

// TestBinaryRevisionFallsBackToRepoHead proves the repo derivation is the
// fallback when the override is empty.
func TestBinaryRevisionFallsBackToRepoHead(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "-c", "user.email=e@example.invalid", "-c", "user.name=e",
		"commit", "-q", "-m", "seed")
	want := strings.TrimSpace(mustGit(t, dir, "rev-parse", "HEAD"))
	if want == "" {
		t.Fatal("temp repo has no HEAD")
	}
	r, err := NewRunner(Config{
		Binary:  "/bin/true",
		OutDir:  t.TempDir(),
		RepoDir: dir,
		Repeats: 1,
		Tasks:   []Task{{ID: "t", Prompt: "p", Check: "true"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.binaryRevision(); got != want {
		t.Fatalf("binaryRevision() = %q, want repo HEAD %q", got, want)
	}
}

// TestAttemptRecordsBinaryPathAndRevision proves the override lands on the
// attempt artifact next to the binary path, so a run is tied to the artifact
// that actually ran and not only to the harness repo's HEAD.
func TestAttemptRecordsBinaryPathAndRevision(t *testing.T) {
	outDir := t.TempDir()
	cfg := Config{
		Binary:         "/bin/true",
		OutDir:         outDir,
		BinaryRevision: "e3e97b1cafe",
		Repeats:        1,
		Arms:           []Arm{ArmCold},
		Tasks:          []Task{{ID: "t1", Prompt: "do", Check: "true"}},
	}
	seam := func(_ context.Context, _ ExecRequest) (ExecResult, error) {
		return telemetryFinal(t, "run-1", nil), nil
	}
	r, err := NewRunner(cfg, seam)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	runDirs, _ := filepath.Glob(filepath.Join(outDir, "*"))
	if len(runDirs) != 1 {
		t.Fatalf("run dirs = %v", runDirs)
	}
	raw, err := os.ReadFile(filepath.Join(runDirs[0], "attempt-t1-cold-r0.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got Attempt
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.BinaryRevision != "e3e97b1cafe" {
		t.Fatalf("attempt binary_revision = %q, want override", got.BinaryRevision)
	}
	if got.Binary != "/bin/true" {
		t.Fatalf("attempt binary = %q, want /bin/true", got.Binary)
	}
	// Provenance carries the same override.
	aggRaw, err := os.ReadFile(filepath.Join(runDirs[0], "aggregate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var agg Aggregate
	if err := json.Unmarshal(aggRaw, &agg); err != nil {
		t.Fatal(err)
	}
	if agg.Provenance.BinaryRevision != "e3e97b1cafe" {
		t.Fatalf("provenance binary_revision = %q, want override", agg.Provenance.BinaryRevision)
	}
}

// TestPaidRunBuildGuards runs the shell-level guards for paid-run.sh's
// BUILD_REV/BIN selection with BUILD_ONLY=1 (no provider, no sidecar).
func TestPaidRunBuildGuards(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	script := "paid_run_guards_test.sh"
	if _, err := os.Stat(script); err != nil {
		t.Skipf("guard script not found: %v", err)
	}
	cmd := exec.Command("bash", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("paid-run build guards failed: %v\n%s", err, out)
	}
	t.Logf("paid-run build guards:\n%s", out)
}
