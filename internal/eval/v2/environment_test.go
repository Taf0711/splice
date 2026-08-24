package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWorkspaceIsIdempotentAndResetPreservesJournal(t *testing.T) {
	manifest := validManifest()
	sourceDir, spliceBinary, sidecarBinary := cleanBinarySource(t)
	manifest.BinarySHA256 = fileHashForTest(t, spliceBinary)
	manifest.Sidecar.BinarySHA256 = fileHashForTest(t, sidecarBinary)
	root := t.TempDir()
	options := WorkspaceOptions{Root: root, SourceDir: sourceDir, SpliceBinary: spliceBinary, SidecarBinary: sidecarBinary}

	workspace, err := BuildWorkspace(manifest, options)
	if err != nil {
		t.Fatalf("BuildWorkspace: %v", err)
	}
	first := manifest.Schedule.Trials[0].Key
	second := manifest.Schedule.Trials[1].Key
	firstPath, err := workspace.TrialPath(first)
	if err != nil {
		t.Fatalf("first TrialPath: %v", err)
	}
	secondPath, err := workspace.TrialPath(second)
	if err != nil {
		t.Fatalf("second TrialPath: %v", err)
	}
	writeFileForTest(t, firstPath, "marker.txt", "keep")
	writeFileForTest(t, workspace.TrialsDir, "journal.json", "journal")

	rebuilt, err := BuildWorkspace(manifest, options)
	if err != nil {
		t.Fatalf("idempotent BuildWorkspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rebuilt.ExperimentRoot, "bin", "splice")); err != nil {
		t.Fatalf("staged splice binary missing: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(rebuilt.TrialPathMust(t, first), "marker.txt")); err != nil || string(got) != "keep" {
		t.Fatalf("rebuild changed first trial marker: %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(rebuilt.TrialsDir, "journal.json")); err != nil || string(got) != "journal" {
		t.Fatalf("rebuild changed journal: %q, %v", got, err)
	}

	if err := ResetWorkspace(rebuilt, first); err != nil {
		t.Fatalf("ResetWorkspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rebuilt.TrialPathMust(t, first), "marker.txt")); !os.IsNotExist(err) {
		t.Fatalf("first trial marker still exists after reset: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(secondPath, "marker.txt")); err == nil || !os.IsNotExist(err) {
		t.Fatalf("unexpected sibling marker state: %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(rebuilt.TrialsDir, "journal.json")); err != nil || string(got) != "journal" {
		t.Fatalf("reset changed journal: %q, %v", got, err)
	}

	if err := os.WriteFile(rebuilt.SpliceBinaryPath, []byte("corrupt"), 0o755); err != nil {
		t.Fatalf("corrupt staged binary: %v", err)
	}
	if _, err := BuildWorkspace(manifest, options); err == nil || !strings.Contains(err.Error(), "staged hash") || !strings.Contains(err.Error(), manifest.BinarySHA256) {
		t.Fatalf("corrupt staged binary error = %v", err)
	}
}

func TestBuildWorkspaceRejectsBinaryMismatchAndDirtySource(t *testing.T) {
	manifest := validManifest()
	sourceDir, spliceBinary, sidecarBinary := cleanBinarySource(t)
	manifest.BinarySHA256 = strings.Repeat("a", 64)
	manifest.Sidecar.BinarySHA256 = fileHashForTest(t, sidecarBinary)
	_, err := BuildWorkspace(manifest, WorkspaceOptions{Root: t.TempDir(), SourceDir: sourceDir, SpliceBinary: spliceBinary, SidecarBinary: sidecarBinary})
	if err == nil || !strings.Contains(err.Error(), "source hash") || !strings.Contains(err.Error(), manifest.BinarySHA256) {
		t.Fatalf("binary mismatch error = %v", err)
	}

	writeFileForTest(t, sourceDir, "dirty.txt", "dirty")
	if err := RequireCleanSource(sourceDir); err == nil || !strings.Contains(err.Error(), "dirty.txt") {
		t.Fatalf("dirty source error = %v", err)
	}
}

func TestChildEnvironmentIsSortedAllowlistedAndRootBound(t *testing.T) {
	manifest := validProtocol()
	_ = manifest
	root := t.TempDir()
	environ := []string{
		"Z_SECRET=omit",
		"TERM=xterm",
		"PATH=/usr/bin",
		"SPLICE_EVAL_CONFIG=" + filepath.Join(root, "config", "run.json"),
		"SPLICE_EVAL_ROOT=" + root,
		"HOME=/Users/tester",
		"SPLICE_EVAL_SESSION_ROOT=" + filepath.Join(root, "sessions"),
	}
	got, err := ChildEnvironment(validManifest(), environ)
	if err != nil {
		t.Fatalf("ChildEnvironment: %v", err)
	}
	want := []string{
		"HOME=/Users/tester",
		"PATH=/usr/bin",
		"SPLICE_EVAL_CONFIG=" + filepath.Join(root, "config", "run.json"),
		"SPLICE_EVAL_ROOT=" + root,
		"SPLICE_EVAL_SESSION_ROOT=" + filepath.Join(root, "sessions"),
		"TERM=xterm",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("environment = %v, want %v", got, want)
	}
	bad := append([]string{}, environ...)
	bad = append(bad, "SPLICE_EVAL_CONFIG="+filepath.Join(root, "reference", "secret.json"))
	if _, err := ChildEnvironment(validManifest(), bad); err == nil || !strings.Contains(err.Error(), "hidden root") {
		t.Fatalf("hidden environment path error = %v", err)
	}
}

func TestDenyRulesCoverAccessShapesAndCanonicalContainment(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"checks", "reference", "manifests", "private_corpus", "allowed"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	rules, err := NewDenyRuleSet(validManifest(), root, []string{"checks", "reference", "manifests", "private_corpus"})
	if err != nil {
		t.Fatalf("NewDenyRuleSet: %v", err)
	}
	for _, hidden := range []string{"checks", "reference", "manifests", "private_corpus"} {
		for _, shape := range []string{"direct", "shell", "search", "symlink"} {
			attempt := filepath.Join(root, hidden, shape, "secret.txt")
			resolution := []string{}
			if shape == "symlink" {
				resolution = []string{attempt}
			}
			if err := rules.Check(attempt, resolution); err == nil || !strings.Contains(err.Error(), "hidden_root") || !strings.Contains(err.Error(), shapeToolClass(shape)) {
				t.Fatalf("%s/%s denial error = %v", hidden, shape, err)
			}
		}
	}
	if err := rules.Check(filepath.Join(root, "allowed", "file.txt"), nil); err != nil {
		t.Fatalf("allowed path denied: %v", err)
	}
	if err := rules.Check(filepath.Join(root, "allowed", "outside.txt"), []string{"/tmp/outside.txt"}); err == nil || !strings.Contains(err.Error(), "workspace_escape") {
		t.Fatalf("symlink escape accepted: %v", err)
	}
}

func TestPreflightReportRequiresCompleteDeniedMatrix(t *testing.T) {
	roots := []string{"/run/checks", "/run/reference"}
	report := PreflightReport{HiddenRoots: roots}
	for _, root := range roots {
		for _, class := range preflightToolClasses {
			report.Results = append(report.Results, PreflightResult{
				Probe:  PreflightProbe{Name: class + "-" + filepath.Base(root), ToolClass: class, AttemptPath: filepath.Join(root, "secret"), HiddenRoot: root},
				Denied: true, Detail: "denied by policy",
			})
		}
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("complete preflight rejected: %v", err)
	}
	missing := report
	missing.Results = missing.Results[:len(missing.Results)-1]
	if err := missing.Validate(); err == nil || !strings.Contains(err.Error(), "missing denied preflight probe") {
		t.Fatalf("missing probe accepted: %v", err)
	}
	passing := report
	passing.Results = append([]PreflightResult{}, report.Results...)
	passing.Results[0].Denied = false
	if err := passing.Validate(); err == nil || !strings.Contains(err.Error(), "was not denied") {
		t.Fatalf("passing probe accepted: %v", err)
	}
	stale := report
	stale.SidecarChecks = []SidecarCheck{{SocketPath: "/run/sidecar.sock", SocketExists: true, ProcessAlive: false}}
	if err := stale.Validate(); err == nil || !strings.Contains(err.Error(), "stale sidecar") {
		t.Fatalf("stale sidecar accepted: %v", err)
	}
}

func shapeToolClass(shape string) string {
	if shape == "symlink" {
		return "symlink_escape"
	}
	return map[string]string{"direct": "direct_read", "shell": "shell_read", "search": "search_glob"}[shape]
}

func (w Workspace) TrialPathMust(t *testing.T, key TrialKey) string {
	t.Helper()
	path, err := w.TrialPath(key)
	if err != nil {
		t.Fatalf("TrialPath: %v", err)
	}
	return path
}

func cleanBinarySource(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	splice := filepath.Join(root, "splice")
	sidecar := filepath.Join(root, "splice-memd")
	writeFileForTest(t, root, "splice", "splice-binary")
	writeFileForTest(t, root, "splice-memd", "sidecar-binary")
	runGitTest(t, root, "init", "-q")
	runGitTest(t, root, "add", "splice", "splice-memd")
	runGitTest(t, root, "-c", "user.name=Eval Test", "-c", "user.email=eval@example.invalid", "commit", "-qm", "seed")
	return root, splice, sidecar
}

func fileHashForTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read hash input: %v", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeFileForTest(t *testing.T, root, relative, content string) {
	t.Helper()
	path := relative
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, relative)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
