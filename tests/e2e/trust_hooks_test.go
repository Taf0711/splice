package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// h03Prompt has no classifier keyword, so the run is light and the static
// analyzer and test runner (which the preflight checks) are in the plan.
const h03Prompt = "add a Hello function in main.go"

// h03Fixture writes a project hook that touches a marker before every tool.
func h03Fixture(t *testing.T) *Scenario {
	t.Helper()
	s := NewScenario(t)
	cmd, args := touchCommand(s, "hook-h03")
	writeProjectFile(s, ".splice/hooks.json", map[string]any{"enabled": true, "hooks": []any{map[string]any{
		"id": "h1", "name": "h1", "event": "beforeTool", "matcher": "*",
		"command": cmd, "args": args, "enabled": true,
	}}})
	return s
}

// TestH03ProjectHookExecUntrusted is spec H-03 for exec: an untrusted
// workspace never runs its project hook, even with tool calls.
func TestH03ProjectHookExecUntrusted(t *testing.T) {
	t.Parallel()
	s := h03Fixture(t)
	s.Provider.Script(StageCodeWriter, SubmitCode(h03Prompt, File("main.go", "modify", helloMain)))
	r := s.Run("exec", "-o", "stream-json", h03Prompt)
	r.WantExit(t, 0)
	if s.MarkerExists("hook-h03") {
		t.Errorf("project beforeTool hook ran in an untrusted workspace")
	}
	if strings.Contains(r.Reasoning(), "may intercept") {
		t.Errorf("untrusted run warns about a project hook:\n%s", r.Reasoning())
	}
}

// TestH03ProjectHookExecUseSpecUntrusted is spec H-03 for exec --use-spec.
func TestH03ProjectHookExecUseSpecUntrusted(t *testing.T) {
	t.Parallel()
	s := h03Fixture(t)
	s.Provider.Script(StageSpecDraft, h03SpecReplies()...)
	r := s.Run("exec", "--use-spec", "-o", "stream-json", h03Prompt)
	r.WantExit(t, 0)
	if s.MarkerExists("hook-h03") {
		t.Errorf("project beforeTool hook ran in an untrusted --use-spec draft")
	}
}

// TestH03ProjectHookExecTrusted is the H-03 control: trusted, the hook runs
// on the tool calls and the preflight warns that it may intercept them.
func TestH03ProjectHookExecTrusted(t *testing.T) {
	t.Parallel()
	s := h03Fixture(t)
	s.Provider.Script(StageCodeWriter, SubmitCode(h03Prompt, File("main.go", "modify", helloMain)))
	r := s.Run("--trust", "exec", "-o", "stream-json", h03Prompt)
	r.WantExit(t, 0)
	if !s.MarkerExists("hook-h03") {
		t.Errorf("trusted project beforeTool hook did not run")
	}
	for _, tool := range []string{"splice.shell", "splice.test"} {
		want := `hook "h1" may intercept ` + tool
		if !strings.Contains(r.Reasoning(), want) {
			t.Errorf("preflight warning %q missing:\n%s", want, r.Reasoning())
		}
	}
}

// TestH03ProjectHookUseSpecTrusted is the H-03 use-spec control.
func TestH03ProjectHookUseSpecTrusted(t *testing.T) {
	t.Parallel()
	s := h03Fixture(t)
	s.Provider.Script(StageSpecDraft, h03SpecReplies()...)
	r := s.Run("--trust", "exec", "--use-spec", "-o", "stream-json", h03Prompt)
	r.WantExit(t, 0)
	if !s.MarkerExists("hook-h03") {
		t.Errorf("trusted project beforeTool hook did not run in --use-spec")
	}
}

// StageSpecDraft is the forced tool of the first spec-draft request.
const StageSpecDraft = "ask_user"

// h03SpecReplies makes a spec draft call one host tool (list_directory) and
// then submit the spec.
func h03SpecReplies() []Reply {
	return []Reply{
		ToolCallReply("list_directory", map[string]any{"path": "."}),
		ToolCallReply("submit_spec", map[string]any{"title": "Hello func", "plan": "# Plan\n- add Hello"}),
	}
}

// TestH10FormatOnWriteUntrusted is spec H-10 for an untrusted workspace: the
// user enables format-on-write, but a project-owned formatter never runs.
func TestH10FormatOnWriteUntrusted(t *testing.T) {
	t.Parallel()
	s := h10Fixture(t)
	s.Provider.Script(StageCodeWriter, SubmitCode(h03Prompt, File("main.go", "modify", helloMain)))
	r := s.Run("exec", "-o", "stream-json", h03Prompt)
	r.WantExit(t, 0)
	if s.MarkerExists("gofmt-h10") {
		t.Errorf("fake gofmt ran in an untrusted workspace")
	}
}

// TestH10FormatOnWriteTrusted is the H-10 control: trusted, the formatter the
// user enabled runs on the written .go file and the marker is created.
func TestH10FormatOnWriteTrusted(t *testing.T) {
	t.Parallel()
	s := h10Fixture(t)
	s.Provider.Script(StageCodeWriter, SubmitCode(h03Prompt, File("main.go", "modify", helloMain)))
	r := s.Run("--trust", "exec", "-o", "stream-json", h03Prompt)
	r.WantExit(t, 0)
	if !s.MarkerExists("gofmt-h10") {
		t.Errorf("trusted write did not run the enabled formatter")
	}
}

// h10Fixture enables tools.format_on_write in the user config and puts a fake
// gofmt first on PATH. The fake only touches a marker.
func h10Fixture(t *testing.T) *Scenario {
	t.Helper()
	s := NewScenario(t)
	cfg := s.DefaultUserConfig()
	cfg["flags"] = map[string]any{"tools.format_on_write": true}
	s.WriteUserConfig(cfg)

	bin := filepath.Join(s.Root, "fakebin")
	writeFile(t, filepath.Join(bin, "gofmt"), "#!/bin/sh\ntouch "+s.Marker("gofmt-h10")+"\n")
	if err := os.Chmod(filepath.Join(bin, "gofmt"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.Env["PATH"] = bin + string(os.PathListSeparator) + os.Getenv("PATH")
	return s
}
