//go:build unix

package procrun

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/sandbox"
)

// sleep37Pids returns the pids of live processes whose command line is
// exactly "sleep 37". The unusual duration keeps other processes from matching.
func sleep37Pids(t *testing.T) []int {
	t.Helper()
	out, err := exec.Command("ps", "-eo", "pid=,args=").Output()
	if err != nil {
		t.Fatalf("setup: ps: %v", err)
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.Join(fields[1:], " ") != "sleep 37" {
			continue
		}
		pid, convErr := strconv.Atoi(fields[0])
		if convErr != nil {
			t.Fatalf("setup: parse ps pid %q: %v", fields[0], convErr)
		}
		pids = append(pids, pid)
	}
	return pids
}

// killPids sends SIGKILL to each pid. Errors are ignored: a pid that already
// exited is the goal.
func killPids(pids []int) {
	for _, pid := range pids {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Signal(syscall.SIGKILL)
		}
	}
}

// runHungSpawn runs a prepared spawn whose shell or interpreter starts a
// grandchild that holds the stdout pipe. The 1s deadline in the prepared
// context must end the whole spawn. Leaked grandchildren are killed before the
// final wait so a failing run cannot block the test.
func runHungSpawn(t *testing.T, prepared Prepared) {
	t.Helper()
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, runErr := prepared.Cmd.CombinedOutput()
		done <- runErr
	}()

	returned := false
	var runErr error
	select {
	case runErr = <-done:
		returned = true
	case <-time.After(5 * time.Second):
		t.Errorf("stage spawn outlived its 1s deadline by more than 4s (CombinedOutput still blocked at 5s)")
	}
	if returned && time.Since(start) < 500*time.Millisecond {
		t.Skipf("spawn returned before the deadline (err=%v); the grandchild pattern did not run", runErr)
	}

	if leaked := sleep37Pids(t); len(leaked) > 0 {
		t.Errorf("grandchild \"sleep 37\" survived the spawn deadline (pids %v)", leaked)
		killPids(leaked)
	}

	if !returned {
		select {
		case <-done:
		case <-time.After(45 * time.Second):
			t.Errorf("CombinedOutput did not return within 45s guard")
		}
	}
}

// TestPrepareDirectSpawnKillsGrandchildOnDeadline runs a hooks-profile spawn
// (direct path, no engine) whose shell starts a grandchild that holds the
// stdout pipe. The 1s deadline must end the whole spawn, not only the direct
// child.
func TestPrepareDirectSpawnKillsGrandchildOnDeadline(t *testing.T) {
	t.Cleanup(func() { killPids(sleep37Pids(t)) })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)

	prepared, err := Prepare(ctx, Request{
		ProfileID: ProfileHooksDispatch,
		Spec: sandbox.CommandSpec{
			Name: "/bin/sh",
			Args: []string{"-c", "sleep 37 & wait"},
		},
	})
	if err != nil {
		t.Fatalf("setup: prepare: %v", err)
	}
	runHungSpawn(t, prepared)
}

// TestPrepareStageSpawnKillsGrandchildOnDeadline is the same grandchild
// pattern under the deterministic splice.stage profile with the real stage
// sandbox engine. python3 is the allowlisted interpreter that can start the
// grandchild. The case skips when the stage profile refuses the spawn.
func TestPrepareStageSpawnKillsGrandchildOnDeadline(t *testing.T) {
	t.Cleanup(func() { killPids(sleep37Pids(t)) })

	requireNativeBackend(t)
	workspace := resolvedTempDir(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)

	prepared, err := Prepare(ctx, Request{
		ProfileID:       ProfileSpliceStage,
		Engine:          NewStageEngine(workspace),
		AllowedBinaries: StageBinaries,
		Spec: sandbox.CommandSpec{
			Name: "python3",
			Args: []string{"-c", "import subprocess,time; subprocess.Popen(['sleep','37']); time.sleep(37)"},
			Dir:  workspace,
		},
	})
	if err != nil {
		t.Fatalf("setup: prepare splice.stage spawn: %v", err)
	}
	runHungSpawn(t, prepared)
}

// requireNativeBackend skips the test when the host has no native
// sandbox backend. Without one, Prepare refuses every splice.stage spawn, so
// the stage profile cannot be observed at all.
func requireNativeBackend(t *testing.T) {
	t.Helper()
	if backend := sandbox.SelectBackend(sandbox.BackendOptions{}); !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
}

// resolvedTempDir returns a fresh temp directory with symlinks resolved, so
// sandbox policy paths match the real paths.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("setup: resolve temp dir: %v", err)
	}
	return resolved
}

// TestStageEngineDeniesSSHKeyRead pins that a splice.stage subprocess cannot
// read ~/.ssh. cat is not in StageBinaries, so the reader is python3, which is.
func TestStageEngineDeniesSSHKeyRead(t *testing.T) {
	requireNativeBackend(t)
	home := resolvedTempDir(t)
	// HOME is set before the engine is built, so the engine sees the fake home.
	t.Setenv("HOME", home)
	workspace := resolvedTempDir(t)

	keyDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatalf("setup: mkdir %s: %v", keyDir, err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "id_test"), []byte("SSHSECRET"), 0o600); err != nil {
		t.Fatalf("setup: write ssh key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "ok.txt"), []byte("WSOK"), 0o644); err != nil {
		t.Fatalf("setup: write workspace file: %v", err)
	}

	engine := NewStageEngine(workspace)
	read := func(path string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		prepared, err := Prepare(ctx, Request{
			ProfileID:       ProfileSpliceStage,
			Engine:          engine,
			AllowedBinaries: StageBinaries,
			Spec: sandbox.CommandSpec{
				Name: "python3",
				Args: []string{"-c", "import sys; sys.stdout.write(open(sys.argv[1]).read())", path},
				Dir:  workspace,
			},
		})
		if err != nil {
			return "", err
		}
		out, runErr := prepared.Cmd.CombinedOutput()
		return string(out), runErr
	}

	// Positive control: the same reader, profile, and engine read a workspace
	// file. A reader that cannot read anything (no python3, a refused spawn)
	// fails here, so the ssh check below cannot pass vacuously.
	if out, err := read(filepath.Join(workspace, "ok.txt")); err != nil || !strings.Contains(out, "WSOK") {
		t.Fatalf("setup: reader control failed under splice.stage: err=%v output=%q", err, out)
	}

	// A non-nil error here is the denial (python3 exits 1 on PermissionError)
	// or a spawn refused for this path. The control above proved the reader,
	// so neither can hide a successful read.
	out, err := read(filepath.Join(keyDir, "id_test"))
	if strings.Contains(out, "SSHSECRET") {
		t.Errorf("splice.stage subprocess read ~/.ssh/id_test (output contains SSHSECRET; err=%v)", err)
	}
}
