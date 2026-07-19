package stages

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

const defaultListMaxResults = 100
const maxDefaultReadQueries = 3

var candidatePathPattern = regexp.MustCompile(`[A-Za-z0-9_./-]+\.(?:py|pyi|ts|tsx|js|jsx|go|rs|java|rb|md|toml|cfg|ini|json|yaml|yml|txt|sh)\b`)

func (o StageOptions) language(defaultLanguage string) string {
	if o.Language != "" {
		return o.Language
	}
	return defaultLanguage
}

func (o StageOptions) contextRequest(intent string) *schemas.ContextRequest {
	if o.OverrideContextRequest != nil {
		return o.OverrideContextRequest
	}
	if o.PullContext {
		req := defaultContextRequest(intent)
		return &req
	}
	return nil
}

func defaultContextRequest(intent string) schemas.ContextRequest {
	queries := []schemas.ContextQuery{
		{QueryType: schemas.ContextListFiles, MaxResults: defaultListMaxResults, MaxChars: 10000},
	}
	for _, path := range candidatePaths(intent) {
		queries = append(queries, schemas.ContextQuery{
			QueryType:  schemas.ContextReadFile,
			Path:       &path,
			MaxResults: 10,
			MaxChars:   5000,
		})
	}
	return schemas.ContextRequest{
		Reason: ("Inspect existing project files before writing so edits modify real code " +
			"instead of overwriting it."),
		Queries: queries,
	}
}

func candidatePaths(intent string) []string {
	seen := []string{}
	for _, match := range candidatePathPattern.FindAllString(intent, -1) {
		path := strings.TrimSpace(match)
		if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
			continue
		}
		if !slices.Contains(seen, path) {
			seen = append(seen, path)
		}
		if len(seen) >= maxDefaultReadQueries {
			break
		}
	}
	return seen
}

func selectRelevantContext(static []string, prior map[string]string, context *schemas.ContextBundle) []string {
	selected := append([]string(nil), static...)
	for _, stage := range []string{"requirements", "spec_writer"} {
		if summary, ok := prior[stage]; ok && summary != "" {
			selected = append(selected, fmt.Sprintf("%s: %s", stage, summary))
		}
	}
	if context != nil {
		selected = append(selected, formatContextBundle(context)...)
	}
	return selected
}

func formatContextBundle(bundle *schemas.ContextBundle) []string {
	formatted := []string{}
	for _, item := range bundle.Items {
		payload, _ := json.Marshal(item.Payload)
		errStr := ""
		if item.Error != nil {
			errStr = fmt.Sprintf(" error=%s", *item.Error)
		}
		suffix := ""
		if item.Truncated {
			suffix = " truncated"
		}
		formatted = append(formatted, fmt.Sprintf(
			"context %s%s: %s\n%s%s",
			item.Query.QueryType, suffix, item.Summary, string(payload), errStr,
		))
	}
	return formatted
}

// selectMemory maps a MemoryBundle into a bounded list of SelectedMemory for stage
// input. It returns nil when bundle is nil or empty, so omitempty keeps the JSON
// field absent when no memory was retrieved.
func selectMemory(bundle *schemas.MemoryBundle) []schemas.SelectedMemory {
	if bundle == nil || len(bundle.Observations) == 0 {
		return nil
	}
	const maxObservations = 5
	const maxRunes = 500
	selected := make([]schemas.SelectedMemory, 0, min(len(bundle.Observations), maxObservations))
	for i, obs := range bundle.Observations {
		if i >= maxObservations {
			break
		}
		content := obs.Content
		runes := []rune(content)
		if len(runes) > maxRunes {
			content = string(runes[:maxRunes]) + "..."
		}
		selected = append(selected, schemas.SelectedMemory{
			Title:      obs.Title,
			Content:    content,
			MemoryType: obs.MemoryType,
			Scope:      obs.Scope,
		})
	}
	return selected
}

func formatPathList(paths []string, max int) string {
	if len(paths) == 0 {
		return "none"
	}
	if len(paths) <= max {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s, ... and %d more", strings.Join(paths[:max], ", "), len(paths)-max)
}

// fileChangeArraySchema is the shared JSON schema for a stage's `files` array
// (used by submit_code and submit_tests). Both tools accept the same file
// change shape, so the schema is defined once.
func fileChangeArraySchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string"},
				"content":     map[string]any{"type": "string"},
				"change_type": map[string]any{"type": "string", "enum": []string{"create", "modify", "delete"}},
			},
			"required": []string{"path", "content", "change_type"},
		},
	}
}
