package warmcost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// StageModelResolution is the effective model a pipeline stage will use, and
// where it came from. It mirrors the pipeline's own precedence: a per-stage
// entry in stage-models.json wins; otherwise the file's Default entry; only
// when neither is set does the primary provider's model apply. This is the
// check that the operator's stage-models.json can silently override --model -
// the defect that spent $0.1648 running gpt-5.6-sol under an intended
// z-ai/glm-5.3-flash run.
type StageModelResolution struct {
	Stage  string `json:"stage"`
	Model  string `json:"model"`
	Source string `json:"source"`
}

// ConfigDir resolves the splice config directory the pipeline reads: an
// explicit dir, else $XDG_CONFIG_HOME/splice, else ~/.config/splice.
func ConfigDir(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return explicit, nil
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "splice"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "splice"), nil
}

// primaryModel reads config.json's activeProvider entry and returns its model.
func primaryModel(spliceDir string) string {
	data, err := os.ReadFile(filepath.Join(spliceDir, "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		ActiveProvider string `json:"activeProvider"`
		Providers      []struct {
			Name   string `json:"name"`
			Model  string `json:"model"`
			Active bool   `json:"active"`
		} `json:"providers"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return ""
	}
	for _, p := range cfg.Providers {
		if p.Name == cfg.ActiveProvider || (cfg.ActiveProvider == "" && p.Active) {
			return p.Model
		}
	}
	return ""
}

// ResolveStageModel resolves the effective model for one stage exactly as the
// pipeline does. A missing stage-models.json is a graceful no-op and falls
// through to the primary provider's model.
func ResolveStageModel(spliceDir, stage string) (StageModelResolution, error) {
	cfg, err := schemas.LoadStageModelConfig(filepath.Join(spliceDir, "stage-models.json"))
	if err != nil {
		return StageModelResolution{}, err
	}
	entry, specific := cfg.Resolve(stage)
	if specific || (entry.ProviderProfile != "" && entry.Model != "") {
		source := "stage-models.json:default"
		if specific {
			source = "stage-models.json:" + stage
		}
		return StageModelResolution{Stage: stage, Model: entry.Model, Source: source}, nil
	}
	model := primaryModel(spliceDir)
	if model == "" {
		return StageModelResolution{Stage: stage, Model: "", Source: "unresolved"}, fmt.Errorf("no stage override and no active provider model in %s", spliceDir)
	}
	return StageModelResolution{Stage: stage, Model: model, Source: "activeProvider"}, nil
}
