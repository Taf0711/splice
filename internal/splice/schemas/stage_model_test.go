package schemas

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStageModelConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     StageModelConfig
		wantErr string
	}{
		{"valid", StageModelConfig{ProviderProfile: "anthropic", Model: "claude-sonnet-4", ReasoningEffort: "high"}, ""},
		{"valid no effort", StageModelConfig{ProviderProfile: "anthropic", Model: "claude-sonnet-4"}, ""},
		{"missing profile", StageModelConfig{Model: "claude-sonnet-4"}, "provider_profile is required"},
		{"missing model", StageModelConfig{ProviderProfile: "anthropic"}, "model is required"},
		{"bad effort", StageModelConfig{ProviderProfile: "anthropic", Model: "x", ReasoningEffort: "extreme"}, "reasoning_effort must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
		})
	}
}

func TestStageModelConfigFileValidate(t *testing.T) {
	cfg := StageModelConfigFile{
		Default: StageModelConfig{ProviderProfile: "anthropic", Model: "claude-sonnet-4"},
		Stages: map[string]StageModelConfig{
			"code_writer": {ProviderProfile: "anthropic", Model: "claude-sonnet-4", ReasoningEffort: "high"},
			"plan_critic": {ProviderProfile: "openai", Model: "o3", ReasoningEffort: "high"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}

	cfg.Stages[""] = StageModelConfig{ProviderProfile: "x", Model: "y"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty stage name")
	}
	delete(cfg.Stages, "")

	cfg.Default = StageModelConfig{}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty default")
	}
}

func TestStageModelConfigFileResolve(t *testing.T) {
	cfg := StageModelConfigFile{
		Default: StageModelConfig{ProviderProfile: "default", Model: "default-model"},
		Stages: map[string]StageModelConfig{
			"code_writer": {ProviderProfile: "anthropic", Model: "claude-sonnet-4"},
		},
	}

	resolved, ok := cfg.Resolve("code_writer")
	if !ok {
		t.Fatal("expected per-stage override for code_writer")
	}
	if resolved.Model != "claude-sonnet-4" {
		t.Fatalf("got model %q, want claude-sonnet-4", resolved.Model)
	}

	resolved, ok = cfg.Resolve("test_runner")
	if ok {
		t.Fatal("expected no per-stage override for test_runner")
	}
	if resolved.Model != "default-model" {
		t.Fatalf("got default model %q, want default-model", resolved.Model)
	}
}

func TestLoadStageModelConfigFileNotExist(t *testing.T) {
	cfg, err := LoadStageModelConfig(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("missing file should be a no-op, got error: %v", err)
	}
	if cfg.Default.ProviderProfile != "" {
		t.Fatalf("expected zero-value config, got default profile %q", cfg.Default.ProviderProfile)
	}
}

func TestLoadStageModelConfigValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stage-models.json")
	content := `{
		"default": {"provider_profile": "anthropic", "model": "claude-sonnet-4", "reasoning_effort": "high"},
		"stages": {
			"plan_critic": {"provider_profile": "openai", "model": "o3", "reasoning_effort": "high"}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadStageModelConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Default.Model != "claude-sonnet-4" {
		t.Fatalf("default model = %q, want claude-sonnet-4", cfg.Default.Model)
	}
	if cfg.Stages["plan_critic"].Model != "o3" {
		t.Fatalf("plan_critic model = %q, want o3", cfg.Stages["plan_critic"].Model)
	}
}

func TestLoadStageModelConfigInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stage-models.json")
	content := `{"default": {"provider_profile": ""}}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStageModelConfig(path); err == nil {
		t.Fatal("expected validation error for empty provider_profile")
	}
}
