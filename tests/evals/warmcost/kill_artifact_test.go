package warmcost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunExecStreamsOutputToPath proves the child's stdout is written to
// OutputPath incrementally, so a killed or timed-out attempt still leaves a
// partial transcript on disk.
func TestRunExecStreamsOutputToPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raw.jsonl")
	res, err := runExec(context.Background(), ExecRequest{
		Binary:     "/bin/sh",
		Args:       []string{"-c", "echo partial-line; exit 3"},
		OutputPath: path,
	})
	if err != nil {
		t.Fatalf("runExec: %v", err)
	}
	if res.ExitCode != 3 {
		t.Fatalf("exit = %d, want 3", res.ExitCode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read streamed output: %v", err)
	}
	if !strings.Contains(string(data), "partial-line") {
		t.Fatalf("streamed output %q missing partial-line", data)
	}
}

// TestTimeoutAttemptKeepsPartialRawStream is the recorded defect: two killed
// glm attempts left no artifact because the harness wrote the raw stream only
// after a clean child exit. A per-attempt timeout must keep the partial stream,
// mark the row as a timeout (an infrastructure outcome, never a model
// failure), and record the kill reason.
func TestTimeoutAttemptKeepsPartialRawStream(t *testing.T) {
	scriptDir := t.TempDir()
	script := filepath.Join(scriptDir, "slow.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho '{\"type\":\"run_start\"}'\nsleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	cfg := Config{
		Binary:  script,
		OutDir:  outDir,
		Repeats: 1,
		Arms:    []Arm{ArmCold},
		Timeout: 1 * time.Second,
		Tasks:   []Task{{ID: "t1", Prompt: "do", Check: "true"}},
	}
	r, err := NewRunner(cfg, nil) // production runExec seam
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
	if got.RunStatus != "timeout" {
		t.Fatalf("run_status = %q, want timeout (ledger_error=%q)", got.RunStatus, got.LedgerError)
	}
	if !strings.Contains(got.LedgerError, "timeout") {
		t.Fatalf("ledger_error = %q, want a timeout reason", got.LedgerError)
	}
	if got.RawStream == "" {
		t.Fatal("raw_stream is empty; the partial transcript was dropped")
	}
	partial, err := os.ReadFile(filepath.Join(runDirs[0], got.RawStream))
	if err != nil {
		t.Fatalf("read partial raw stream: %v", err)
	}
	if !strings.Contains(string(partial), "run_start") {
		t.Fatalf("partial raw stream %q missing the pre-timeout output", partial)
	}
}
