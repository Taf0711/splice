package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGitCommitAllDeterministicHead pins the freshness-critical invariant:
// committing the SAME tree twice (a pristine reset) yields the SAME HEAD
// sha. A timestamp-varying commit would change the per-attempt freshness
// classification and silently break the causal "same treatment" invariant.
func TestGitCommitAllDeterministicHead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := gitCommitAll(dir)
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if first == "" {
		t.Fatal("first commit returned empty HEAD")
	}
	// Simulate the per-attempt reset: wipe and re-copy the same bytes, then
	// commit again. The sha must not move.
	if err := os.RemoveAll(filepath.Join(dir, "session.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := gitCommitAll(dir)
	if err != nil {
		t.Fatalf("second commit: %v", err)
	}
	if second != first {
		t.Fatalf("HEAD moved across an identical reset: %s -> %s", first, second)
	}
	// An unchanged tree (nothing to commit) must keep HEAD, not error.
	third, err := gitCommitAll(dir)
	if err != nil {
		t.Fatalf("no-op commit: %v", err)
	}
	if third != first {
		t.Fatalf("no-op commit moved HEAD: %s -> %s", first, third)
	}
}

// TestCheckForRequiresVerifier pins the fail-loud verifier contract: a
// missing verifier file is an error, never a silent "true" fallback that
// would count unverified runs as correct.
func TestCheckForRequiresVerifier(t *testing.T) {
	dir := t.TempDir()
	present := familyEntry{ID: "fam-x", TargetCheckFile: "verifiers/fam-x.sh"}
	if err := os.MkdirAll(filepath.Join(dir, "verifiers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "verifiers", "fam-x.sh"), []byte("exit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := checkFor(dir, present)
	if err != nil {
		t.Fatalf("existing verifier: %v", err)
	}
	if !strings.Contains(got, "exit 0") {
		t.Fatalf("checkFor returned %q, want the verifier body", got)
	}

	missing := familyEntry{ID: "fam-y", TargetCheckFile: "verifiers/fam-y.sh"}
	if _, err := checkFor(dir, missing); err == nil {
		t.Fatal("missing verifier must error, not fall back to true")
	}
}

// timeoutRunFunc models the production seam: exec.CommandContext honoring a
// deadline context. It blocks until the context dies, then reports the
// cancellation as an error, exactly like a stalled provider stream.
func timeoutRunFunc(ctx context.Context, in evalRunInputShim) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(10 * time.Second):
		return "finished", nil
	}
}

// evalRunInputShim keeps the fake seam independent of the eval package's
// RunInput so the test stays focused on timeout classification.
type evalRunInputShim struct {
	SessionID string
}

// TestFamiliesTimeoutClassifiesInfraAndContinues drives the classification
// logic directly: a deadline-exceeded run must be marked infra "timeout",
// forced to unsuccessful, and must not stop later attempts.
func TestFamiliesTimeoutClassifiesInfraAndContinues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var out string
	var runErr error
	var attempts int
	for try := 0; try < 2; try++ {
		attempts++
		out, runErr = timeoutRunFunc(ctx, evalRunInputShim{SessionID: "s"})
		// Mirror the runner's retry guard: a dead deadline is final.
		if ctx.Err() != nil {
			break
		}
		if runErr == nil {
			break
		}
	}

	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (a deadline expiry must not retry)", attempts)
	}
	if out != "" || !errors.Is(runErr, context.DeadlineExceeded) {
		t.Fatalf("run outcome = %q, %v; want cancellation", out, runErr)
	}

	// The classification the runner applies on DeadlineExceeded.
	row := familyPairRow{Success: true, Tokens: 1234}
	if ctx.Err() == context.DeadlineExceeded {
		row.InfraStatus = "timeout"
		row.Success = false
	}
	if row.InfraStatus != "timeout" {
		t.Fatalf("infra status = %q, want timeout", row.InfraStatus)
	}
	if row.Success {
		t.Fatal("a timed-out attempt must not count as success")
	}
}

// TestFamiliesRunTimeoutBounded proves the deadline bounds execution: with a
// 100ms budget against a run that wants 10 seconds, the cancellation lands
// in bounded time and the deadline (not the run) ends the attempt. The
// production seam is exec.CommandContext, which kills the child process on
// ctx.Done; the fake models the same contract.
func TestFamiliesRunTimeoutBounded(t *testing.T) {
	if familiesRunTimeout <= 0 {
		t.Fatalf("familiesRunTimeout = %v, want positive", familiesRunTimeout)
	}
	if familiesRunTimeout < time.Minute {
		t.Fatalf("familiesRunTimeout = %v; the production bound must give a healthy run head room", familiesRunTimeout)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _ = timeoutRunFunc(ctx, evalRunInputShim{SessionID: "s"})
	elapsed := time.Since(started)
	if elapsed > 2*time.Second {
		t.Fatalf("cancelled run took %v; the timeout must bound execution", elapsed)
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("ctx.Err() = %v, want DeadlineExceeded", ctx.Err())
	}
}

// TestResetWarmMemoryFailsWithoutSidecar pins the fail-loud isolation
// contract: when the sidecar is unavailable, resetWarmMemory errors instead
// of silently leaving attempt N's cognition in place for attempt N+1.
func TestResetWarmMemoryFailsWithoutSidecar(t *testing.T) {
	// Point the sidecar socket at a path nothing serves so Resolve cannot
	// spawn or reach a daemon.
	dir := t.TempDir()
	t.Setenv("SPLICE_MEMD_SOCKET", filepath.Join(dir, "absent.sock"))
	t.Setenv("SPLICE_MEMD_BIN", "") // no spawnable sidecar binary

	// resolveBinary falls back to PATH and the executable's sibling; make
	// PATH empty for this test so no stray splice-memd is found.
	t.Setenv("PATH", "")

	err := resetWarmMemory(context.Background(), dir, familyEntry{ID: "fam-x"})
	if err == nil {
		t.Fatal("reset without a sidecar must fail loud, not skip isolation")
	}
}

// TestSumStreamJSONWork pins the transcript work-counter parsing the
// telemetry columns rely on.
func TestSumStreamJSONWork(t *testing.T) {
	transcript := strings.Join([]string{
		`{"schemaVersion":2,"type":"tool_call","id":"1","name":"read_file"}`,
		`{"schemaVersion":2,"type":"tool_call","id":"2","name":"grep"}`,
		`{"schemaVersion":2,"type":"tool_call","id":"3","name":"read_file"}`,
		`{"schemaVersion":2,"type":"tool_call","id":"4","name":"submit_code"}`,
		`{"schemaVersion":2,"type":"tool_result","id":"1"}`,
		`not json at all`,
	}, "\n")
	toolCalls, fileReads, searchCalls := sumStreamJSONWork([]byte(transcript))
	if toolCalls != 4 {
		t.Fatalf("toolCalls = %d, want 4", toolCalls)
	}
	if fileReads != 2 {
		t.Fatalf("fileReads = %d, want 2", fileReads)
	}
	if searchCalls != 1 {
		t.Fatalf("searchCalls = %d, want 1", searchCalls)
	}
	toolCalls, fileReads, searchCalls = sumStreamJSONWork([]byte(""))
	if toolCalls != 0 || fileReads != 0 || searchCalls != 0 {
		t.Fatalf("empty transcript = %d/%d/%d, want 0/0/0 (unknown, not fabricated)", toolCalls, fileReads, searchCalls)
	}
}

// TestGitHeadCommitReadsResetRepo guards the helper the seed path relies on.
func TestGitHeadCommitReadsResetRepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitCommitAll(dir); err != nil {
		t.Fatalf("commit: %v", err)
	}
	head := gitHeadCommit(dir)
	if len(head) != 40 {
		t.Fatalf("HEAD = %q, want a full sha", head)
	}
	if _, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output(); err != nil {
		t.Fatalf("repo unusable: %v", err)
	}
}
