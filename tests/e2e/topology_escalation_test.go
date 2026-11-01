package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// alwaysFailTest makes the test runner fail on every pass, so the trajectory
// monitor sees the same failing state each iteration.
const alwaysFailTest = "package main\n\nimport \"testing\"\n\nfunc TestAlwaysFails(t *testing.T) { t.Fatal(\"always fails\") }\n"

// TestT08EscalationOverridesPin is spec T-08: a cycle escalates the run, the
// escalated model replaces the code writer's pin for every later request, and
// the switch notice names the pinned node.
func TestT08EscalationOverridesPin(t *testing.T) {
	t.Parallel()
	s := NewScenario(t)

	topo := exportDefault(t, s, filepath.Join(s.Root, "base.json"))
	topo["name"] = "esc"
	for _, n := range topo["nodes"].([]any) {
		node := n.(map[string]any)
		if node["name"] == "code_writer" {
			node["model"] = map[string]any{"provider_profile": "fake", "model": "pinned-model"}
		}
	}
	file := filepath.Join(s.Root, "esc.json")
	writeJSON(t, file, topo)
	s.WriteStageModels(map[string]any{
		"default":    map[string]any{"provider_profile": "fake", "model": FakeModel},
		"escalation": map[string]any{"provider_profile": "fake", "model": "escalated-model"},
	})

	// Identical output on every call. The run makes four code writer requests
	// before the switch and six after it, then stops at the iteration limit.
	const codeWriterReplies = 10
	for i := 0; i < codeWriterReplies; i++ {
		s.Provider.Script(StageCodeWriter, SubmitCode(t01Prompt,
			File("main.go", "modify", helloMain),
			File("main_test.go", "create", alwaysFailTest),
		))
	}

	r := s.Run("exec", "-o", "stream-json", "--pipeline", file, t01Prompt)
	// docs/COMMANDS.md: 4 = the run ended without a complete result.
	r.WantExit(t, 4)

	// The switch notice names the pinned node, once.
	const switchNotice = "overrides the model pin of node(s) code_writer"
	if n := countReasoning(r, switchNotice); n != 1 {
		t.Errorf("switch notice seen %d time(s), want 1", n)
	}

	reqs := s.Provider.Requests()
	var models []string
	for _, q := range reqs {
		if q.Stage == StageCodeWriter {
			models = append(models, q.Model)
		}
	}
	if len(models) != codeWriterReplies {
		t.Fatalf("code writer requests = %d, want %d", len(models), codeWriterReplies)
	}
	if unused := s.Provider.Unused(); len(unused) > 0 {
		t.Errorf("unused scripted replies: %v", unused)
	}
	// Four pinned requests, then the escalated model for every later request.
	for i, model := range models {
		want := "escalated-model"
		if i < 4 {
			want = "pinned-model"
		}
		if model != want {
			t.Errorf("code writer request %d used model %q, want %q", i+1, model, want)
		}
	}
	if !strings.Contains(r.Reasoning(), "[escalation] switched to model escalated-model") {
		t.Errorf("no escalation switch notice in the stream")
	}
}
