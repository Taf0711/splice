package warmcost

// Reasoning-effort capability guard for the paid campaign launcher.
//
// The standing pins reasoningEffort=medium in the eval config. The provider is
// the authority on which efforts a model actually supports: OpenRouter's model
// catalog advertises reasoning.supported_efforts, and a model that does not
// list the requested effort may silently substitute another. That happened on
// the fam-05 automatic arm: `z-ai/glm-5.3-flash` advertises
// ["max","high","low"], the pinned medium was rejected with
//
//	reasoning effort "medium" is not supported ...; using high instead
//
// and the arm then burned the per-attempt bound in a reasoning loop with no
// submission. This guard turns that silent substitution into a pre-spend abort,
// the same shape as the resolved-stage-model assertion.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// DefaultOpenRouterModelsURL is OpenRouter's public model catalog. It needs no
// credential for the metadata read.
const DefaultOpenRouterModelsURL = "https://openrouter.ai/api/v1/models"

// ReasoningEffortResolution is the effective reasoning effort for a stage and
// where it came from: a stage-models.json entry (per-stage, then Default), else
// the active provider's config.json reasoningEffort.
type ReasoningEffortResolution struct {
	Stage  string `json:"stage"`
	Effort string `json:"effort"`
	Source string `json:"source"`
}

// ResolveStageReasoningEffort mirrors ResolveStageModel's precedence for the
// reasoning effort. A missing or empty effort resolves to the empty string
// (the provider default), which the caller reports as unchecked.
func ResolveStageReasoningEffort(spliceDir, stage string) ReasoningEffortResolution {
	cfg, err := schemas.LoadStageModelConfig(filepath.Join(spliceDir, "stage-models.json"))
	if err == nil {
		entry, specific := cfg.Resolve(stage)
		if effort := strings.TrimSpace(entry.ReasoningEffort); effort != "" {
			source := "stage-models.json:default"
			if specific {
				source = "stage-models.json:" + stage
			}
			return ReasoningEffortResolution{Stage: stage, Effort: effort, Source: source}
		}
	}
	if effort := primaryReasoningEffort(spliceDir); effort != "" {
		return ReasoningEffortResolution{Stage: stage, Effort: effort, Source: "config.json:activeProvider"}
	}
	return ReasoningEffortResolution{Stage: stage, Source: "unresolved"}
}

// primaryReasoningEffort reads config.json's active provider reasoningEffort.
func primaryReasoningEffort(spliceDir string) string {
	data, err := os.ReadFile(filepath.Join(spliceDir, "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		ActiveProvider string `json:"activeProvider"`
		Providers      []struct {
			Name            string `json:"name"`
			ReasoningEffort string `json:"reasoningEffort"`
			Active          bool   `json:"active"`
		} `json:"providers"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return ""
	}
	for _, p := range cfg.Providers {
		if p.Name == cfg.ActiveProvider || (cfg.ActiveProvider == "" && p.Active) {
			return strings.TrimSpace(p.ReasoningEffort)
		}
	}
	return ""
}

// ModelReasoningInfo is the provider's advertised reasoning capability.
type ModelReasoningInfo struct {
	SupportedEfforts []string `json:"supported_efforts"`
	DefaultEffort    string   `json:"default_effort"`
}

// ParseModelReasoningInfo extracts the reasoning block for one model id from
// an OpenRouter /api/v1/models body. A missing model or a missing reasoning
// block is a loud error, not a silent pass: the guard must not claim a check
// it did not perform.
func ParseModelReasoningInfo(body []byte, modelID string) (ModelReasoningInfo, error) {
	var payload struct {
		Data []struct {
			ID        string              `json:"id"`
			Reasoning *ModelReasoningInfo `json:"reasoning"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ModelReasoningInfo{}, fmt.Errorf("decode model catalog: %w", err)
	}
	modelID = strings.TrimSpace(modelID)
	for _, m := range payload.Data {
		if m.ID != modelID {
			continue
		}
		if m.Reasoning == nil {
			return ModelReasoningInfo{}, fmt.Errorf("model %s advertises no reasoning metadata", modelID)
		}
		return *m.Reasoning, nil
	}
	return ModelReasoningInfo{}, fmt.Errorf("model %s not present in the catalog", modelID)
}

// EffortSupported reports whether effort is one the provider advertises. An
// empty effort (provider default) or an empty advertised list (no advertised
// restriction) counts as supported; the caller decides whether that is a
// verified result.
func EffortSupported(effort string, supported []string) bool {
	effort = strings.TrimSpace(effort)
	if effort == "" || len(supported) == 0 {
		return true
	}
	for _, s := range supported {
		if strings.EqualFold(strings.TrimSpace(s), effort) {
			return true
		}
	}
	return false
}

// FetchModelReasoningInfo reads and parses the provider catalog for one model.
func FetchModelReasoningInfo(ctx context.Context, endpoint, modelID string, client *http.Client) (ModelReasoningInfo, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = DefaultOpenRouterModelsURL
	}
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ModelReasoningInfo{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return ModelReasoningInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ModelReasoningInfo{}, fmt.Errorf("model catalog %s: status %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return ModelReasoningInfo{}, err
	}
	return ParseModelReasoningInfo(body, modelID)
}
