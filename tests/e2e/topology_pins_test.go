package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestT06PinWithMissingProfile is spec T-06: a node pin that names a profile
// the user config does not have stops the run before any provider request.
func TestT06PinWithMissingProfile(t *testing.T) {
	t.Parallel()
	s := NewScenario(t)

	topo := exportDefault(t, s, filepath.Join(s.Root, "base.json"))
	topo["name"] = "pinned"
	for _, n := range topo["nodes"].([]any) {
		node := n.(map[string]any)
		if node["name"] == "code_writer" {
			node["model"] = map[string]any{"provider_profile": "ghost", "model": "m1"}
		}
	}
	file := filepath.Join(s.Root, "pinned.json")
	writeJSON(t, file, topo)

	r := s.Run("exec", "--pipeline", file, t01Prompt)
	// docs/COMMANDS.md: 2 = the command or input was invalid. The preflight
	// finds the unresolved pin before any provider request.
	r.WantExit(t, 2)
	for _, want := range []string{`node "code_writer"`, `unknown provider profile "ghost"`} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.Stderr)
		}
	}
	if reqs := s.Provider.Requests(); len(reqs) != 0 {
		t.Errorf("provider requests = %d, want 0: %v", len(reqs), reqs)
	}
}

// stageModelsWith is a valid stage-models.json whose default route is the
// fake profile. Stage routes are added by each scenario.
func stageModelsWith(stages map[string]any) map[string]any {
	return map[string]any{
		"default": map[string]any{"provider_profile": "fake", "model": FakeModel},
		"stages":  stages,
	}
}

// TestT07StageModelRoutes is spec T-07: a stage route that does not resolve,
// and a stage-models.json that does not validate, both fail the run before
// any provider request.
func TestT07StageModelRoutes(t *testing.T) {
	t.Parallel()

	t.Run("broken-route", func(t *testing.T) {
		t.Parallel()
		s := NewScenario(t)
		s.WriteStageModels(stageModelsWith(map[string]any{
			"code_writer": map[string]any{"provider_profile": "ghost", "model": "m1"},
		}))

		r := s.Run("exec", t01Prompt)
		// docs/COMMANDS.md: 2 = the command or input was invalid. The preflight
		// finds the broken route before any provider request.
		r.WantExit(t, 2)
		if !strings.Contains(r.Stdout+r.Stderr, `stage "code_writer" references unknown provider profile "ghost"`) {
			t.Errorf("output does not name the broken stage route:\n%s%s", r.Stdout, r.Stderr)
		}
		if reqs := s.Provider.Requests(); len(reqs) != 0 {
			t.Errorf("provider requests = %d, want 0 on the broken route: %v", len(reqs), reqs)
		}
	})

	t.Run("provider-setup-failure", func(t *testing.T) {
		t.Parallel()
		s := NewScenario(t)
		// A profile with no key falls through to the OAuth token store. A
		// corrupt store makes the provider fail to start. A missing key
		// variable does not fail here: that provider starts and the
		// request fails later.
		cfg := s.DefaultUserConfig()
		cfg["providers"] = append(cfg["providers"].([]any), map[string]any{
			"name":          "nokey",
			"provider_kind": "openai-compatible",
			"baseURL":       s.Provider.BaseURL,
			"model":         FakeModel,
		})
		s.WriteUserConfig(cfg)
		tokens := filepath.Join(s.Root, "oauth-tokens.json")
		writeFile(t, tokens, "not json")
		s.Env["SPLICE_OAUTH_STORAGE"] = "file"
		s.Env["SPLICE_OAUTH_TOKENS_PATH"] = tokens
		s.WriteStageModels(stageModelsWith(map[string]any{
			"code_writer": map[string]any{"provider_profile": "nokey", "model": FakeModel},
		}))

		r := s.Run("exec", t01Prompt)
		// docs/COMMANDS.md: 3 = a provider failure. The route is valid; its
		// provider cannot start, so this is not an input error (2).
		r.WantExit(t, 3)
		if !strings.Contains(r.Stdout+r.Stderr, `build provider for stage "code_writer"`) {
			t.Errorf("output does not name the provider setup failure:\n%s%s", r.Stdout, r.Stderr)
		}
		if reqs := s.Provider.Requests(); len(reqs) != 0 {
			t.Errorf("provider requests = %d, want 0: %v", len(reqs), reqs)
		}
	})

	t.Run("invalid-file", func(t *testing.T) {
		t.Parallel()
		s := NewScenario(t)
		// A default route with no model does not validate.
		s.WriteStageModels(map[string]any{
			"default": map[string]any{"provider_profile": "fake"},
		})

		r := s.Run("exec", t01Prompt)
		// docs/COMMANDS.md: 2 = the command or input was invalid. The preflight
		// finds the broken route before any provider request.
		r.WantExit(t, 2)
		out := r.Stdout + r.Stderr
		if !strings.Contains(out, "stage-models.json") || !strings.Contains(out, "default: model is required") {
			t.Errorf("output does not name the invalid file and cause:\n%s", out)
		}
		if reqs := s.Provider.Requests(); len(reqs) != 0 {
			t.Errorf("provider requests = %d, want 0: %v", len(reqs), reqs)
		}
	})
}
