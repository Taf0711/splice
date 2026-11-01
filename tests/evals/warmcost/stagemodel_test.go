package warmcost

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, dir, config, stages string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if config != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if stages != "" {
		if err := os.WriteFile(filepath.Join(dir, "stage-models.json"), []byte(stages), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestResolveStageModelOverrideBeatsPrimary is the recorded defect: a
// stage-models.json code_writer override must be surfaced, not hidden behind
// the --model flag's value.
func TestResolveStageModelOverrideBeatsPrimary(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir,
		`{"activeProvider":"openrouter","providers":[{"name":"openrouter","model":"z-ai/glm-5.3-flash","active":true}]}`,
		`{"default":{"provider_profile":"openrouter","model":"z-ai/glm-5.3-flash"},"stages":{"code_writer":{"provider_profile":"chatgpt","model":"gpt-5.6-sol"}}}`)
	got, err := ResolveStageModel(dir, "code_writer")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-5.6-sol" || got.Source == "" || got.Source == "activeProvider" {
		t.Fatalf("resolved %+v, want the code_writer override surfaced", got)
	}
}

// TestResolveStageModelFallsBackToPrimary proves absence of stage-models.json
// is a graceful fall-through to the active provider model.
func TestResolveStageModelFallsBackToPrimary(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir,
		`{"activeProvider":"openrouter","providers":[{"name":"openrouter","model":"z-ai/glm-5.3-flash","active":true}]}`,
		"")
	got, err := ResolveStageModel(dir, "code_writer")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "z-ai/glm-5.3-flash" || got.Source != "activeProvider" {
		t.Fatalf("resolved %+v, want primary z-ai/glm-5.3-flash", got)
	}
}

// TestResolveStageModelDefaultBeatsPrimary proves the file's Default entry wins
// over the primary model even when the stage has no specific entry.
func TestResolveStageModelDefaultBeatsPrimary(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir,
		`{"activeProvider":"openrouter","providers":[{"name":"openrouter","model":"z-ai/glm-5.3-flash","active":true}]}`,
		`{"default":{"provider_profile":"chatgpt","model":"gpt-5.6-sol"}}`)
	got, err := ResolveStageModel(dir, "code_writer")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-5.6-sol" {
		t.Fatalf("resolved %+v, want the default override gpt-5.6-sol", got)
	}
}
