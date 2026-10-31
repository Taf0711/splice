package swebenchpro

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WritePredictions serializes preds to path as the exact JSON array shape the
// official evaluator consumes:
//
//	[{"instance_id":"...","patch":"diff --git ...","prefix":"..."}]
//
// It rejects entries with empty instance_id so a silent mislabel cannot reach
// the evaluator and be scored against the wrong instance. An empty Patch is
// allowed: the official evaluator scores it as a failure for that instance,
// which is the honest result when Splice produced no diff.
func WritePredictions(path string, prefix string, preds []Prediction) error {
	if path == "" {
		return fmt.Errorf("swebenchpro.WritePredictions: path is empty")
	}
	out := make([]Prediction, 0, len(preds))
	for i, p := range preds {
		if p.InstanceID == "" {
			return fmt.Errorf("swebenchpro.WritePredictions: preds[%d].instance_id is empty", i)
		}
		if p.Prefix == "" {
			p.Prefix = prefix
		}
		out = append(out, p)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("swebenchpro.WritePredictions: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("swebenchpro.WritePredictions: mkdir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("swebenchpro.WritePredictions: write %s: %w", path, err)
	}
	return nil
}

// LoadPredictions reads back a predictions file. Used by the loader side of
// benchmarks/report and by tests (golden fixture round-trip).
func LoadPredictions(path string) ([]Prediction, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("swebenchpro.LoadPredictions: read %s: %w", path, err)
	}
	var preds []Prediction
	if err := json.Unmarshal(data, &preds); err != nil {
		return nil, fmt.Errorf("swebenchpro.LoadPredictions: parse %s: %w", path, err)
	}
	return preds, nil
}
