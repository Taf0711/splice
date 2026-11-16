//go:build darwin

// This file is an external test package. It needs procrun.NewStageEngine (the
// real stage profile), and procrun imports sandbox, so package sandbox cannot
// import it. The credential list comes from the real credentialFileDenyPaths
// through export_test.go, so no copy of the list exists here.
package sandbox_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
)

// TestStageEngineDeniesCredentialFiles reads every credential entry
// through the deterministic stage profile (procrun.NewStageEngine,
// splice.stage). Owner decision 2026-11-11: stages that run repo code must not
// read the credential files. Interactive agent bash keeps them readable
// (TestCredentialFilesDeniedToReadFile pins that), so this test does not use
// the agent policy.
func TestStageEngineDeniesCredentialFiles(t *testing.T) {
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	if !backend.Available || backend.Name != sandbox.BackendMacOSSeatbelt {
		t.Skipf("host sandbox backend is not sandbox-exec: %s", backend.Message)
	}

	home := resolvedTempDir(t)
	// HOME is set before the engine is built, so "~" entries resolve to it.
	t.Setenv("HOME", home)
	workspace := resolvedTempDir(t)
	writeSecretFile(t, filepath.Join(workspace, "ok.txt"), "WSOK")

	entries := sandbox.CredentialFileDenyPaths()
	if len(entries) == 0 {
		t.Fatalf("setup: credentialFileDenyPaths is empty")
	}
	// One secret per entry. A file entry gets the secret as its content. A
	// directory entry (for example ~/.pip) gets a file inside it.
	targets := map[string]string{}
	var order []string
	for _, entry := range entries {
		rel := strings.TrimPrefix(filepath.ToSlash(entry), "~/")
		if rel == filepath.ToSlash(entry) {
			t.Fatalf("setup: credential entry %q is not under ~/", entry)
		}
		path := filepath.Join(home, filepath.FromSlash(rel))
		if credentialDirEntries[rel] {
			path = filepath.Join(path, "secret")
		}
		writeSecretFile(t, path, "SECRET123")
		targets[rel] = path
		order = append(order, rel)
	}

	engine := procrun.NewStageEngine(workspace)
	read := func(t *testing.T, path string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// cat is not in StageBinaries. python3 is.
		prepared, err := procrun.Prepare(ctx, procrun.Request{
			ProfileID:       procrun.ProfileSpliceStage,
			Engine:          engine,
			AllowedBinaries: procrun.StageBinaries,
			Spec: sandbox.CommandSpec{
				Name: "python3",
				Args: []string{"-c", "import sys; sys.stdout.write(open(sys.argv[1]).read())", path},
				Dir:  workspace,
			},
		})
		if err != nil {
			t.Fatalf("setup: prepare stage read of %s: %v", path, err)
		}
		if !prepared.Plan.Wrapped || prepared.Plan.Backend.Name != sandbox.BackendMacOSSeatbelt {
			t.Fatalf("setup: plan = %#v, want wrapped sandbox-exec", prepared.Plan)
		}
		output, runErr := prepared.Cmd.CombinedOutput()
		return string(output), runErr
	}

	// Positive control: the same reader, profile, and engine read a workspace
	// file. A reader that cannot read anything fails here, not as a pass.
	if out, err := read(t, filepath.Join(workspace, "ok.txt")); err != nil || !strings.Contains(out, "WSOK") {
		t.Fatalf("setup: control workspace read failed under splice.stage: err=%v output=%q", err, out)
	}

	for _, rel := range order {
		t.Run(rel, func(t *testing.T) {
			out, err := read(t, targets[rel])
			if strings.Contains(out, "SECRET123") {
				t.Errorf("splice.stage subprocess read ~/%s (output contains SECRET123; err=%v)", rel, err)
			}
		})
	}
}

// credentialDirEntries are credentialFileDenyPaths entries that are
// directories. The test puts its secret in a file inside each one.
var credentialDirEntries = map[string]bool{
	".pip":              true,
	".config/pip":       true,
	"Library/Keychains": true,
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

func writeSecretFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("setup: mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("setup: write %s: %v", path, err)
	}
}
