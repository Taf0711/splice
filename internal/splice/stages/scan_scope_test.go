package stages

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// recordingCheck is a VerificationCheck that records the paths it was
// given and reports a clean pass. It never runs a tool.
type recordingCheck struct {
	paths []string
	calls int
}

func (c *recordingCheck) Name() string     { return "recording" }
func (c *recordingCheck) Category() string { return "recording" }

func (c *recordingCheck) Run(_ context.Context, req VerificationCheckRequest) (VerificationCheckResult, error) {
	c.calls++
	c.paths = append([]string(nil), req.Paths...)
	return VerificationCheckResult{ToolRun: schemas.VerificationToolRun{
		Tool:     "recording",
		Required: true,
		Scope:    req.Scope,
		Status:   schemas.VerificationPassed,
		Summary:  "clean",
	}}, nil
}

// recordGitCommand runs recorded shell-like commands with the real git binary.
// It bypasses PrepareStageCommand, which refuses a nil sandbox engine, so the
// tests exercise the discovery logic and git behavior, not the sandbox.
func recordGitCommand(ctx context.Context, name string, args map[string]any, _ func(context.Context) (ToolResult, error)) (ToolResult, error) {
	command, _ := args["command"].([]string)
	cwd, _ := args["cwd"].(string)
	if name != "splice.shell" || len(command) == 0 {
		return ToolResult{}, fmt.Errorf("test: unexpected recorded command %q", name)
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	return ToolResult{OK: err == nil, Output: string(out)}, err
}

// gitSubcommand returns the git subcommand of a recorded argv. It skips the
// "-c name=value" pairs that the stage puts before the subcommand.
func gitSubcommand(command []string) string {
	i := 1
	for i+1 < len(command) && command[i] == "-c" {
		i += 2
	}
	if i >= len(command) {
		return ""
	}
	return command[i]
}

func scopeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setup: git %v: %v: %s", args, err, out)
	}
}

// scopeGitRepo returns a fresh git repo with auto-maintenance disabled. The
// path is symlink-resolved, so a stage that reports the git toplevel (on
// macOS /private/var/...) compares equal to the fixture path.
func scopeGitRepo(t *testing.T) string {
	t.Helper()
	dir := scopeResolve(t.TempDir())
	scopeGit(t, dir, "init", "-q")
	scopeGit(t, dir, "config", "maintenance.auto", "false")
	scopeGit(t, dir, "config", "gc.auto", "0")
	return dir
}

func scopeWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("setup: mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("setup: write %s: %v", path, err)
	}
}

// scopeResolve evaluates symlinks when it can. A path that does not resolve is
// returned unchanged.
func scopeResolve(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// scopeHasPath reports whether paths contains want, comparing symlink-resolved
// paths on both sides.
func scopeHasPath(paths []string, want string) bool {
	resolvedWant := scopeResolve(want)
	for _, p := range paths {
		if scopeResolve(p) == resolvedWant {
			return true
		}
	}
	return false
}

// TestSecurityAuditorScansUntrackedFiles: an untracked new source file must be scanned even when a tracked file also changed.
func TestSecurityAuditorScansUntrackedFiles(t *testing.T) {
	dir := scopeGitRepo(t)
	scopeWrite(t, filepath.Join(dir, "a.py"), "x = 1\n")
	scopeGit(t, dir, "add", "a.py")
	scopeGit(t, dir, "commit", "-qm", "base")
	scopeWrite(t, filepath.Join(dir, "a.py"), "eval(input())\n")
	scopeWrite(t, filepath.Join(dir, "bad.py"), "eval(input())\n")

	check := &recordingCheck{}
	stage, err := NewSecurityAuditor(check)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	result, err := stage.Run(context.Background(), newHarnessInput("audit security"), panickingProvider{}, StageOptions{
		WorkDir:       dir,
		Language:      "python",
		RecordCommand: recordGitCommand,
	})
	if err != nil {
		t.Fatalf("stage returned error: %v", err)
	}
	if want := filepath.Join(dir, "bad.py"); !scopeHasPath(check.paths, want) {
		t.Fatalf("untracked new file bad.py was not scanned; check received %v, analyzed %v", check.paths, result.Data["analyzed_paths"])
	}
}

// TestSecurityAuditorTruncatedWalkIsIncomplete: a fallback walk that stops at maxScanFiles must not produce a complete pass.
func TestSecurityAuditorTruncatedWalkIsIncomplete(t *testing.T) {
	dir := scopeGitRepo(t)
	total := maxScanFiles + 10
	for i := 0; i < total; i++ {
		scopeWrite(t, filepath.Join(dir, fmt.Sprintf("m%03d.py", i)), "x = 1\n")
	}
	scopeGit(t, dir, "add", "-A")
	scopeGit(t, dir, "commit", "-qm", "base")

	stage, err := NewSecurityAuditor(&recordingCheck{})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	result, err := stage.Run(context.Background(), newHarnessInput("audit security"), panickingProvider{}, StageOptions{
		WorkDir:       dir,
		Language:      "python",
		RecordCommand: recordGitCommand,
	})
	if err != nil {
		t.Fatalf("stage returned error: %v", err)
	}
	report, ok := result.Data["security_auditor_output"].(schemas.VerificationReport)
	if !ok {
		t.Fatalf("setup: security_auditor_output missing or wrong type: %T", result.Data["security_auditor_output"])
	}
	scanned, ok := result.Data["analyzed_paths"].([]string)
	if !ok {
		t.Fatalf("setup: analyzed_paths missing or wrong type: %T", result.Data["analyzed_paths"])
	}
	if report.Status == schemas.VerificationPassed && report.Complete && len(scanned) < total {
		t.Fatalf("%d files, scanned %d, report claims complete pass (status %s, complete %v)", total, len(scanned), report.Status, report.Complete)
	}
	if truncated, _ := result.Data["truncated"].(bool); !truncated {
		t.Fatalf("%d files, scanned %d, but Data[truncated] is not true", total, len(scanned))
	}
	if report.Complete || report.Status != schemas.VerificationIncomplete {
		t.Fatalf("truncated walk must be incomplete; got status %s, complete %v", report.Status, report.Complete)
	}
}

// TestChangedFileDiscoveryInSubdirectory: git paths are repo-root relative, so discovery must still find changed files when WorkDir is a subdirectory.
func TestChangedFileDiscoveryInSubdirectory(t *testing.T) {
	t.Run("security_auditor", func(t *testing.T) {
		dir := scopeGitRepo(t)
		scopeWrite(t, filepath.Join(dir, "sub", "a.py"), "x = 1\n")
		scopeWrite(t, filepath.Join(dir, "sub", "b.py"), "y = 1\n")
		scopeGit(t, dir, "add", "-A")
		scopeGit(t, dir, "commit", "-qm", "base")
		scopeWrite(t, filepath.Join(dir, "sub", "a.py"), "eval(input())\n")

		workDir := filepath.Join(dir, "sub")
		check := &recordingCheck{}
		stage, err := NewSecurityAuditor(check)
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		result, err := stage.Run(context.Background(), newHarnessInput("audit security"), panickingProvider{}, StageOptions{
			WorkDir:       workDir,
			Language:      "python",
			RecordCommand: recordGitCommand,
		})
		if err != nil {
			t.Fatalf("security auditor returned error: %v", err)
		}
		if scoped, _ := result.Data["scoped_to_changes"].(bool); !scoped {
			t.Fatalf("changed file sub/a.py was dropped when WorkDir is a subdirectory; scope fell back to a full walk, check received %v", check.paths)
		}
		if want := filepath.Join(workDir, "a.py"); !scopeHasPath(check.paths, want) || len(check.paths) != 1 {
			t.Fatalf("security check received %v, want only %s", check.paths, want)
		}
	})

	t.Run("static_analyzer", func(t *testing.T) {
		dir := scopeGitRepo(t)
		scopeWrite(t, filepath.Join(dir, "sub", "a.go"), "package sub\n")
		scopeWrite(t, filepath.Join(dir, "sub", "b.go"), "package sub\n")
		scopeGit(t, dir, "add", "-A")
		scopeGit(t, dir, "commit", "-qm", "base")
		scopeWrite(t, filepath.Join(dir, "sub", "a.go"), "package sub\n\nvar X = 1\n")

		workDir := filepath.Join(dir, "sub")
		check := &recordingCheck{}
		stage, err := NewStaticAnalyzer(check)
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		result, err := stage.Run(context.Background(), newHarnessInput("check quality"), panickingProvider{}, StageOptions{
			WorkDir:       workDir,
			Language:      "go",
			RecordCommand: recordGitCommand,
		})
		if err != nil {
			t.Fatalf("static analyzer returned error: %v", err)
		}
		if scoped, _ := result.Data["scoped_to_changes"].(bool); !scoped {
			t.Fatalf("changed file sub/a.go was dropped when WorkDir is a subdirectory; scope fell back to a full walk, check received %v", check.paths)
		}
		// analyzed_paths are relative to WorkDir, so sub/a.go is "a.go".
		analyzed, _ := result.Data["analyzed_paths"].([]string)
		if want := filepath.Join(workDir, "a.go"); !scopeHasPath(check.paths, want) || len(check.paths) != 1 || len(analyzed) != 1 || analyzed[0] != "a.go" {
			t.Fatalf("static check received %v (analyzed %v), want only %s", check.paths, analyzed, want)
		}
	})
}

// TestStaticAnalyzerUntrackedListingErrorIsNotIgnored: a failed untracked-file listing must not be treated as an empty list.
func TestStaticAnalyzerUntrackedListingErrorIsNotIgnored(t *testing.T) {
	dir := t.TempDir()
	scopeWrite(t, filepath.Join(dir, "a.go"), "package main\n")
	scopeWrite(t, filepath.Join(dir, "b.go"), "package main\n")

	record := func(ctx context.Context, name string, args map[string]any, _ func(context.Context) (ToolResult, error)) (ToolResult, error) {
		command, _ := args["command"].([]string)
		if name != "splice.shell" || len(command) < 2 {
			return ToolResult{}, fmt.Errorf("test: unexpected recorded command %q", name)
		}
		switch gitSubcommand(command) {
		case "diff":
			return ToolResult{OK: true, Output: "a.go\n"}, nil
		case "ls-files":
			return ToolResult{}, errors.New("test: git ls-files failed")
		}
		return ToolResult{}, fmt.Errorf("test: unexpected git subcommand %q", gitSubcommand(command))
	}

	stage, err := NewStaticAnalyzer(&recordingCheck{})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	result, err := stage.Run(context.Background(), newHarnessInput("check quality"), panickingProvider{}, StageOptions{
		WorkDir:       dir,
		Language:      "go",
		RecordCommand: record,
	})
	// PASS when any of these holds: the stage refuses with an error that names
	// the untracked listing, the report is not complete, or the analyzed paths
	// include the untracked b.go. Anything else is the defect.
	if err != nil {
		if msg := strings.ToLower(err.Error()); strings.Contains(msg, "ls-files") || strings.Contains(msg, "untracked") {
			return
		}
		t.Fatalf("untracked listing failed but stage error does not name ls-files or untracked: %v", err)
	}
	report, ok := result.Data["static_analyzer_output"].(schemas.VerificationReport)
	if !ok {
		t.Fatalf("setup: static_analyzer_output missing or wrong type: %T", result.Data["static_analyzer_output"])
	}
	if !report.Complete {
		return
	}
	analyzed, ok := result.Data["analyzed_paths"].([]string)
	if !ok {
		t.Fatalf("setup: analyzed_paths missing or wrong type: %T", result.Data["analyzed_paths"])
	}
	for _, p := range analyzed {
		if p == "b.go" {
			return
		}
	}
	t.Fatalf("untracked listing failed (git ls-files) but report says complete (status %s) and analyzed %v without b.go; stage must return an error, mark the report incomplete, or include b.go", report.Status, analyzed)
}

// TestStaticAnalyzerTruncatedWalkIsIncomplete: a fallback walk that stops at defaultStaticMaxFiles must not produce a complete pass.
func TestStaticAnalyzerTruncatedWalkIsIncomplete(t *testing.T) {
	dir := scopeGitRepo(t)
	total := defaultStaticMaxFiles + 10
	for i := 0; i < total; i++ {
		scopeWrite(t, filepath.Join(dir, fmt.Sprintf("m%03d.go", i)), "package main\n")
	}
	scopeGit(t, dir, "add", "-A")
	scopeGit(t, dir, "commit", "-qm", "base")

	stage, err := NewStaticAnalyzer(&recordingCheck{})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	result, err := stage.Run(context.Background(), newHarnessInput("check quality"), panickingProvider{}, StageOptions{
		WorkDir:       dir,
		Language:      "go",
		RecordCommand: recordGitCommand,
	})
	if err != nil {
		t.Fatalf("stage returned error: %v", err)
	}
	report, ok := result.Data["static_analyzer_output"].(schemas.VerificationReport)
	if !ok {
		t.Fatalf("setup: static_analyzer_output missing or wrong type: %T", result.Data["static_analyzer_output"])
	}
	analyzed, ok := result.Data["analyzed_paths"].([]string)
	if !ok {
		t.Fatalf("setup: analyzed_paths missing or wrong type: %T", result.Data["analyzed_paths"])
	}
	if report.Status == schemas.VerificationPassed && report.Complete && len(analyzed) < total {
		t.Fatalf("%d go files, analyzed %d, report claims complete pass (status %s, complete %v)", total, len(analyzed), report.Status, report.Complete)
	}
	if truncated, _ := result.Data["truncated"].(bool); !truncated {
		t.Fatalf("%d go files, analyzed %d, but Data[truncated] is not true", total, len(analyzed))
	}
	if report.Complete || report.Status != schemas.VerificationIncomplete {
		t.Fatalf("truncated walk must be incomplete; got status %s, complete %v", report.Status, report.Complete)
	}
}

// TestSecurityAuditorCleanTreeBelowCapIsComplete: a walk that finds fewer files
// than the cap is complete and not truncated.
func TestSecurityAuditorCleanTreeBelowCapIsComplete(t *testing.T) {
	dir := scopeGitRepo(t)
	for i := 0; i < 3; i++ {
		scopeWrite(t, filepath.Join(dir, fmt.Sprintf("m%d.py", i)), "x = 1\n")
	}
	scopeGit(t, dir, "add", "-A")
	scopeGit(t, dir, "commit", "-qm", "base")

	check := &recordingCheck{}
	stage, err := NewSecurityAuditor(check)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	result, err := stage.Run(context.Background(), newHarnessInput("audit security"), panickingProvider{}, StageOptions{
		WorkDir:       dir,
		Language:      "python",
		RecordCommand: recordGitCommand,
	})
	if err != nil {
		t.Fatalf("stage returned error: %v", err)
	}
	report, ok := result.Data["security_auditor_output"].(schemas.VerificationReport)
	if !ok {
		t.Fatalf("setup: security_auditor_output missing or wrong type: %T", result.Data["security_auditor_output"])
	}
	if !report.Complete || report.Status != schemas.VerificationPassed {
		t.Fatalf("clean tree below cap: got status %s, complete %v; want passed and complete", report.Status, report.Complete)
	}
	if truncated, ok := result.Data["truncated"].(bool); !ok || truncated {
		t.Fatalf("clean tree below cap: Data[truncated] = %v (ok %v), want false", result.Data["truncated"], ok)
	}
	if len(check.paths) != 3 {
		t.Fatalf("check received %d paths, want all 3 files: %v", len(check.paths), check.paths)
	}
}

// TestSecurityAuditorExactlyCapIsNotTruncated: a walk that finds exactly the
// cap is complete. The stage must not report truncation without one more file.
func TestSecurityAuditorExactlyCapIsNotTruncated(t *testing.T) {
	dir := scopeGitRepo(t)
	for i := 0; i < maxScanFiles; i++ {
		scopeWrite(t, filepath.Join(dir, fmt.Sprintf("m%03d.py", i)), "x = 1\n")
	}
	scopeGit(t, dir, "add", "-A")
	scopeGit(t, dir, "commit", "-qm", "base")

	check := &recordingCheck{}
	stage, err := NewSecurityAuditor(check)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	result, err := stage.Run(context.Background(), newHarnessInput("audit security"), panickingProvider{}, StageOptions{
		WorkDir:       dir,
		Language:      "python",
		RecordCommand: recordGitCommand,
	})
	if err != nil {
		t.Fatalf("stage returned error: %v", err)
	}
	report, ok := result.Data["security_auditor_output"].(schemas.VerificationReport)
	if !ok {
		t.Fatalf("setup: security_auditor_output missing or wrong type: %T", result.Data["security_auditor_output"])
	}
	if truncated, ok := result.Data["truncated"].(bool); !ok || truncated {
		t.Fatalf("exactly %d files: Data[truncated] = %v (ok %v), want false", maxScanFiles, result.Data["truncated"], ok)
	}
	if !report.Complete || report.Status != schemas.VerificationPassed {
		t.Fatalf("exactly %d files: got status %s, complete %v; want passed and complete", maxScanFiles, report.Status, report.Complete)
	}
	if len(check.paths) != maxScanFiles {
		t.Fatalf("check received %d paths, want %d", len(check.paths), maxScanFiles)
	}
}

// TestSecurityAuditorUntrackedListingFailureFallsBackToWalk: when the untracked
// listing fails, the stage uses the fallback walk and still analyzes the
// untracked file.
func TestSecurityAuditorUntrackedListingFailureFallsBackToWalk(t *testing.T) {
	dir := t.TempDir()
	scopeWrite(t, filepath.Join(dir, "a.py"), "x = 1\n")
	scopeWrite(t, filepath.Join(dir, "bad.py"), "eval(input())\n")

	record := func(ctx context.Context, name string, args map[string]any, _ func(context.Context) (ToolResult, error)) (ToolResult, error) {
		command, _ := args["command"].([]string)
		if name != "splice.shell" || len(command) < 2 {
			return ToolResult{}, fmt.Errorf("test: unexpected recorded command %q", name)
		}
		switch gitSubcommand(command) {
		case "diff":
			return ToolResult{OK: true, Output: "a.py\n"}, nil
		case "ls-files":
			return ToolResult{}, errors.New("test: git ls-files failed")
		}
		return ToolResult{}, fmt.Errorf("test: unexpected git subcommand %q", gitSubcommand(command))
	}

	check := &recordingCheck{}
	stage, err := NewSecurityAuditor(check)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	result, err := stage.Run(context.Background(), newHarnessInput("audit security"), panickingProvider{}, StageOptions{
		WorkDir:       dir,
		Language:      "python",
		RecordCommand: record,
	})
	if err != nil {
		t.Fatalf("stage returned error after untracked listing failure: %v", err)
	}
	if scoped, _ := result.Data["scoped_to_changes"].(bool); scoped {
		t.Fatalf("untracked listing failed but scope claims changes only; check received %v", check.paths)
	}
	if want := filepath.Join(dir, "bad.py"); !scopeHasPath(check.paths, want) {
		t.Fatalf("fallback walk did not analyze untracked file %s; check received %v", want, check.paths)
	}
}

// TestSecurityAuditorUntrackedOnlyChangeIsScopedToIt: an empty diff with one
// untracked file scopes the scan to that file alone.
func TestSecurityAuditorUntrackedOnlyChangeIsScopedToIt(t *testing.T) {
	dir := scopeGitRepo(t)
	scopeWrite(t, filepath.Join(dir, "a.py"), "x = 1\n")
	scopeGit(t, dir, "add", "-A")
	scopeGit(t, dir, "commit", "-qm", "base")
	scopeWrite(t, filepath.Join(dir, "bad.py"), "eval(input())\n")

	check := &recordingCheck{}
	stage, err := NewSecurityAuditor(check)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	result, err := stage.Run(context.Background(), newHarnessInput("audit security"), panickingProvider{}, StageOptions{
		WorkDir:       dir,
		Language:      "python",
		RecordCommand: recordGitCommand,
	})
	if err != nil {
		t.Fatalf("stage returned error: %v", err)
	}
	if scoped, _ := result.Data["scoped_to_changes"].(bool); !scoped {
		t.Fatalf("untracked-only change was not scoped to changes; check received %v", check.paths)
	}
	if want := filepath.Join(dir, "bad.py"); len(check.paths) != 1 || !scopeHasPath(check.paths, want) {
		t.Fatalf("check received %v, want only %s", check.paths, want)
	}
	analyzed, _ := result.Data["analyzed_paths"].([]string)
	if len(analyzed) != 1 || analyzed[0] != "bad.py" {
		t.Fatalf("analyzed_paths = %v, want [bad.py]", analyzed)
	}
}

// TestStaticAnalyzerUnrelatedChangeIsNotApplicable: a change that touches no
// file in the analyzed language is not applicable. The stage must not fall back
// to a capped walk of unrelated files.
func TestStaticAnalyzerUnrelatedChangeIsNotApplicable(t *testing.T) {
	dir := scopeGitRepo(t)
	for i := 0; i < defaultStaticMaxFiles+10; i++ {
		scopeWrite(t, filepath.Join(dir, fmt.Sprintf("m%03d.go", i)), "package main\n")
	}
	scopeWrite(t, filepath.Join(dir, "README.md"), "# v1\n")
	scopeGit(t, dir, "add", "-A")
	scopeGit(t, dir, "commit", "-qm", "base")
	scopeWrite(t, filepath.Join(dir, "README.md"), "# v2\n")

	check := &recordingCheck{}
	stage, err := NewStaticAnalyzer(check)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	result, err := stage.Run(context.Background(), newHarnessInput("check quality"), panickingProvider{}, StageOptions{
		WorkDir:       dir,
		Language:      "go",
		RecordCommand: recordGitCommand,
	})
	if err != nil {
		t.Fatalf("stage returned error: %v", err)
	}
	report, ok := result.Data["static_analyzer_output"].(schemas.VerificationReport)
	if !ok {
		t.Fatalf("setup: static_analyzer_output missing or wrong type: %T", result.Data["static_analyzer_output"])
	}
	if report.Status != schemas.VerificationNotApplicable || !report.Complete {
		t.Fatalf("unrelated change: got status %s, complete %v; want not applicable and complete", report.Status, report.Complete)
	}
	if truncated, _ := result.Data["truncated"].(bool); truncated {
		t.Fatalf("unrelated change: Data[truncated] is true, want false")
	}
	if scoped, _ := result.Data["scoped_to_changes"].(bool); !scoped {
		t.Fatalf("unrelated change: scoped_to_changes is false, want true")
	}
	if check.calls != 0 {
		t.Fatalf("unrelated change: a check ran %d time(s) on no files, want none", check.calls)
	}
	if analyzed, _ := result.Data["analyzed_paths"].([]string); len(analyzed) != 0 {
		t.Fatalf("unrelated change: analyzed %v, want no paths", analyzed)
	}
}

// TestStaticAnalyzerCleanTreeStillWalks: a clean tree has no changes, so the
// stage falls back to the capped walk and reports the truncation.
func TestStaticAnalyzerCleanTreeStillWalks(t *testing.T) {
	dir := scopeGitRepo(t)
	for i := 0; i < defaultStaticMaxFiles+10; i++ {
		scopeWrite(t, filepath.Join(dir, fmt.Sprintf("m%03d.go", i)), "package main\n")
	}
	scopeGit(t, dir, "add", "-A")
	scopeGit(t, dir, "commit", "-qm", "base")

	check := &recordingCheck{}
	stage, err := NewStaticAnalyzer(check)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	result, err := stage.Run(context.Background(), newHarnessInput("check quality"), panickingProvider{}, StageOptions{
		WorkDir:       dir,
		Language:      "go",
		RecordCommand: recordGitCommand,
	})
	if err != nil {
		t.Fatalf("stage returned error: %v", err)
	}
	report, ok := result.Data["static_analyzer_output"].(schemas.VerificationReport)
	if !ok {
		t.Fatalf("setup: static_analyzer_output missing or wrong type: %T", result.Data["static_analyzer_output"])
	}
	if report.Status != schemas.VerificationIncomplete || report.Complete {
		t.Fatalf("clean tree over cap: got status %s, complete %v; want incomplete", report.Status, report.Complete)
	}
	if truncated, _ := result.Data["truncated"].(bool); !truncated {
		t.Fatalf("clean tree over cap: Data[truncated] is not true")
	}
	if scoped, _ := result.Data["scoped_to_changes"].(bool); scoped {
		t.Fatalf("clean tree: scoped_to_changes is true, want walk")
	}
	if len(check.paths) != defaultStaticMaxFiles {
		t.Fatalf("clean tree: check received %d paths, want the capped walk of %d", len(check.paths), defaultStaticMaxFiles)
	}
}

// TestSecurityAuditorReadmeOnlyChangeIsNotTruncated: the security auditor passes
// every changed file to its checks and never walks when changes exist. A
// README-only change therefore analyzes README.md alone, with no truncation.
func TestSecurityAuditorReadmeOnlyChangeIsNotTruncated(t *testing.T) {
	dir := scopeGitRepo(t)
	for i := 0; i < maxScanFiles+10; i++ {
		scopeWrite(t, filepath.Join(dir, fmt.Sprintf("m%03d.py", i)), "x = 1\n")
	}
	scopeWrite(t, filepath.Join(dir, "README.md"), "# v1\n")
	scopeGit(t, dir, "add", "-A")
	scopeGit(t, dir, "commit", "-qm", "base")
	scopeWrite(t, filepath.Join(dir, "README.md"), "# v2\n")

	check := &recordingCheck{}
	stage, err := NewSecurityAuditor(check)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	result, err := stage.Run(context.Background(), newHarnessInput("audit security"), panickingProvider{}, StageOptions{
		WorkDir:       dir,
		Language:      "python",
		RecordCommand: recordGitCommand,
	})
	if err != nil {
		t.Fatalf("stage returned error: %v", err)
	}
	report, ok := result.Data["security_auditor_output"].(schemas.VerificationReport)
	if !ok {
		t.Fatalf("setup: security_auditor_output missing or wrong type: %T", result.Data["security_auditor_output"])
	}
	if report.Status == schemas.VerificationIncomplete || !report.Complete {
		t.Fatalf("README-only change: got status %s, complete %v; want not incomplete", report.Status, report.Complete)
	}
	if truncated, _ := result.Data["truncated"].(bool); truncated {
		t.Fatalf("README-only change: Data[truncated] is true, want false")
	}
	analyzed, _ := result.Data["analyzed_paths"].([]string)
	if len(analyzed) != 1 || analyzed[0] != "README.md" {
		t.Fatalf("README-only change: analyzed_paths = %v, want [README.md]", analyzed)
	}
}

// Git quotes non-ASCII path names unless core.quotepath is off. A quoted name
// fails the extension match and the stat, so the file was dropped. The static
// analyzer and the security auditor must both receive the file.
func TestChangedNonASCIIFileNamesReachAnalyzers(t *testing.T) {
	dir := scopeGitRepo(t)
	scopeWrite(t, filepath.Join(dir, "café.py"), "x = 1\n")
	scopeGit(t, dir, "add", "café.py")
	scopeGit(t, dir, "commit", "-qm", "base")
	scopeWrite(t, filepath.Join(dir, "café.py"), "x = 2\n")
	scopeWrite(t, filepath.Join(dir, "naïve.py"), "y = 1\n")

	options := StageOptions{RecordCommand: recordGitCommand}
	analyzed, _, err := gitScopedFiles(context.Background(), dir, "python", options)
	if err != nil {
		t.Fatalf("gitScopedFiles: %v", err)
	}
	audited, err := gitChangedSourceFiles(context.Background(), dir, options)
	if err != nil {
		t.Fatalf("gitChangedSourceFiles: %v", err)
	}
	for label, got := range map[string][]string{"static analyzer": analyzed, "security auditor": audited} {
		for _, want := range []string{filepath.Join(dir, "café.py"), filepath.Join(dir, "naïve.py")} {
			if !slices.Contains(got, want) {
				t.Errorf("%s files = %v, want %q with its non-ASCII name", label, got, want)
			}
		}
	}
}
