package schemas

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// StageModelConfig defines which provider profile, model, and reasoning effort
// a single pipeline stage should use. It is the per-stage entry in a
// StageModelConfigFile, resolved by the orchestrator right before stage.Run.
type StageModelConfig struct {
	ProviderProfile string `json:"provider_profile"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// Validate checks the stage model config.
func (c StageModelConfig) Validate() error {
	if c.ProviderProfile == "" {
		return errors.New("provider_profile is required")
	}
	if c.Model == "" {
		return errors.New("model is required")
	}
	switch c.ReasoningEffort {
	case "", "minimal", "low", "medium", "high":
	default:
		return fmt.Errorf("reasoning_effort must be minimal, low, medium, or high, got %q", c.ReasoningEffort)
	}
	return nil
}

// StageModelConfigFile is the JSON file that maps stage names to their model
// configurations. The Default entry is used when a stage has no specific
// override. Escalation is an optional model config used when the trajectory
// monitor fires cycle or oscillation actions (AR10c). File location:
// ~/.config/splice/stage-models.json (or the path passed to
// LoadStageModelConfig).
type StageModelConfigFile struct {
	Default    StageModelConfig            `json:"default"`
	Escalation *StageModelConfig           `json:"escalation,omitempty"`
	Stages     map[string]StageModelConfig `json:"stages,omitempty"`
}

// Validate checks the config file.
func (f StageModelConfigFile) Validate() error {
	if err := f.Default.Validate(); err != nil {
		return fmt.Errorf("default: %w", err)
	}
	if f.Escalation != nil {
		if err := f.Escalation.Validate(); err != nil {
			return fmt.Errorf("escalation: %w", err)
		}
	}
	for name, cfg := range f.Stages {
		if name == "" {
			return errors.New("stage name must not be empty")
		}
		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("stages[%s]: %w", name, err)
		}
	}
	return nil
}

// Resolve returns the model config for a stage, falling back to Default when
// no per-stage override exists. The second return is true when a per-stage
// override was found.
func (f StageModelConfigFile) Resolve(stageName string) (StageModelConfig, bool) {
	if cfg, ok := f.Stages[stageName]; ok {
		return cfg, true
	}
	return f.Default, false
}

// LoadStageModelConfig reads and validates a stage model config JSON file.
// Returns a zero-value config (empty Default and no Stages) and a nil error
// when the file does not exist, so absence is a graceful no-op: the
// orchestrator uses the default provider.
func LoadStageModelConfig(path string) (StageModelConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return StageModelConfigFile{}, nil
		}
		return StageModelConfigFile{}, fmt.Errorf("read stage model config %s: %w", path, err)
	}
	var cfg StageModelConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return StageModelConfigFile{}, fmt.Errorf("parse stage model config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return StageModelConfigFile{}, fmt.Errorf("validate stage model config %s: %w", path, err)
	}
	return cfg, nil
}
