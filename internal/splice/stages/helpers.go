package stages

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/splice/memoryreason"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

const defaultListMaxResults = 100
const maxDefaultReadQueries = 3

// maxFallbackSourceFiles bounds the production-source fallback so a large
// workspace cannot flood the context bundle. Each file still honors the same
// per-read MaxChars budget the explicit-path queries use.
const maxFallbackSourceFiles = 8
const fallbackSourceMaxChars = 5000

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
		req := defaultContextRequest(intent, o.WorkDir, o.Language)
		return &req
	}
	return nil
}

// DefaultContextRequestFor is the exported counterfactual: the default
// context request the cold path would issue for this stage. The cognition
// scope planner uses it for structural suppression accounting only.
func DefaultContextRequestFor(intent, workDir, language string) schemas.ContextRequest {
	return defaultContextRequest(intent, workDir, language)
}

func defaultContextRequest(intent string, workDir string, language string) schemas.ContextRequest {
	queries := []schemas.ContextQuery{
		{QueryType: schemas.ContextListFiles, MaxResults: defaultListMaxResults, MaxChars: 10000},
	}
	readPaths := candidatePaths(intent)
	if len(readPaths) == 0 {
		// The task text names no path. Fall back to the workspace's production
		// source files for the detected language so the writer sees real
		// contents instead of inventing them from a directory listing alone.
		// Test files stay excluded: verification owns them, and a repair-loop
		// fixture may plant a trap test the writer must not see.
		readPaths = productionSourcePaths(workDir, language)
	}
	for _, path := range readPaths {
		queries = append(queries, schemas.ContextQuery{
			QueryType:  schemas.ContextReadFile,
			Path:       &path,
			MaxResults: 10,
			MaxChars:   fallbackSourceMaxChars,
		})
	}
	return schemas.ContextRequest{
		Reason: ("Inspect existing project files before writing so edits modify real code " +
			"instead of overwriting it."),
		Queries: queries,
	}
}

// productionSourcePaths lists up to maxFallbackSourceFiles workspace-relative
// production source paths for the detected language. Test files are excluded,
// hidden and dependency directories are skipped, and the result is sorted for
// deterministic context bundles.
func productionSourcePaths(workDir string, language string) []string {
	extensions := languageExtensions[strings.ToLower(strings.TrimSpace(language))]
	if len(extensions) == 0 || strings.TrimSpace(workDir) == "" {
		return nil
	}
	root, err := filepath.Abs(workDir)
	if err != nil {
		return nil
	}
	var paths []string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // best effort: an unreadable entry never blocks context
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (defaultIgnoreDirs[name] || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !slices.Contains(extensions, filepath.Ext(path)) {
			return nil
		}
		if isTestFileName(entry.Name()) {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil || relative == "." || relative == ".." {
			return nil
		}
		if !slices.Contains(paths, relative) {
			paths = append(paths, relative)
		}
		if len(paths) >= maxFallbackSourceFiles {
			return fs.SkipAll
		}
		return nil
	})
	sort.Strings(paths)
	return paths
}

// isTestFileName reports whether name is a test file for the languages Splice
// supports. The patterns cover Go, Python, and JavaScript/TypeScript naming
// conventions plus spec files.
func isTestFileName(name string) bool {
	base := strings.ToLower(name)
	patterns := []string{
		"*_test.go", "test_*.py", "*_test.py",
		"*.test.ts", "*.test.tsx", "*.test.js", "*.test.jsx",
		"*.spec.ts", "*.spec.tsx", "*.spec.js", "*.spec.jsx",
	}
	for _, pattern := range patterns {
		if matched, _ := filepath.Match(pattern, base); matched {
			return true
		}
	}
	return false
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

// selectRelevantContext composes the model-visible context entries for one
// stage request. Package F3 (warm-cost handoff Section 10): after assembly,
// the composition passes through the shared dedup filter so static
// instructions and prior-stage summaries repeated across requests are
// delivered once. Required evidence, acceptance constraints, and active
// failure evidence are protected by the filter and never removed.
func selectRelevantContext(static []string, prior map[string]string, context *schemas.ContextBundle, roster []string) []string {
	selected := append([]string(nil), static...)
	keys := make([]string, 0, len(prior))
	for stage, summary := range prior {
		if summary != "" {
			keys = append(keys, stage)
		}
	}
	if len(roster) > 0 {
		rosterIndex := make(map[string]int, len(roster))
		for i, stage := range roster {
			if _, exists := rosterIndex[stage]; !exists {
				rosterIndex[stage] = i
			}
		}
		sort.SliceStable(keys, func(i, j int) bool {
			a, aok := rosterIndex[keys[i]]
			b, bok := rosterIndex[keys[j]]
			switch {
			case aok && bok:
				return a < b
			case aok:
				return true
			case bok:
				return false
			default:
				return keys[i] < keys[j]
			}
		})
	} else {
		sort.Strings(keys)
	}
	for _, stage := range keys {
		selected = append(selected, fmt.Sprintf("%s: %s", stage, prior[stage]))
	}
	if context != nil {
		selected = append(selected, formatContextBundle(context)...)
	}
	return dedupeRepeatedContext(selected)
}

// dedupeRepeatedContext removes exact-duplicate entries and static
// instructions repeated across request compositions (F3). The filter is
// content-addressed: two entries survive identically only when they are
// byte-equal after trimming surrounding whitespace. Protected classes
// (required evidence, acceptance constraints, active failure evidence) are
// exempt: a repeated protected entry is delivered every time it appears.
// Order is otherwise preserved, so composition stays deterministic and
// byte-comparable.
func dedupeRepeatedContext(entries []string) []string {
	out := make([]string, 0, len(entries))
	seen := map[string]int{}
	for _, entry := range entries {
		key := strings.TrimSpace(entry)
		if key == "" {
			continue
		}
		if isProtectedContextEntry(key) {
			// Protected evidence survives every occurrence.
			out = append(out, entry)
			continue
		}
		if first, dup := seen[key]; dup {
			// Byte-identical repeat: keep the first occurrence's position.
			_ = first
			continue
		}
		seen[key] = len(out)
		out = append(out, entry)
	}
	return out
}

// isProtectedContextEntry reports whether an entry carries required
// evidence, acceptance constraints, or active failure evidence. These
// classes survive repeated-content removal (handoff Section 4: required
// intent, acceptance constraints, active failure evidence, and source edit
// preconditions survive prompt compaction).
func isProtectedContextEntry(entry string) bool {
	for _, marker := range protectedContextMarkers {
		if strings.Contains(entry, marker) {
			return true
		}
	}
	return false
}

// protectedContextMarkers are the stable content markers of protected entry
// classes. They match the composition formats in this file and the repair
// evidence formats in repair.go.
var protectedContextMarkers = []string{
	"acceptance",
	"failing",
	"failure",
	"error=",
	"fingerprint",
	"revision",
}

// formatContextBundle renders a fulfilled context bundle into prompt text
// (B3). Source text is serialized ONCE: the item payload's text rides as a
// plain fenced block, not JSON-embedded inside another JSON string. Views
// are deduped by (path, version, range) identity so the same source bytes
// never reach the prompt twice.
func formatContextBundle(bundle *schemas.ContextBundle) []string {
	formatted := []string{}
	seen := map[string]bool{}
	for _, item := range bundle.Items {
		errStr := ""
		if item.Error != nil {
			errStr = fmt.Sprintf(" error=%s", *item.Error)
		}
		suffix := ""
		if item.Truncated {
			suffix = " truncated"
		}
		// Dedupe by source identity when the item carries one (B1 views
		// embed path/version/range); fall back to the query signature.
		identity := contextItemIdentity(item)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		// The payload's text field is plain source/context text: deliver
		// it as a fenced block, serialized exactly once. Structured
		// metadata (path/version) stays on the summary line, not nested
		// in a JSON string inside a string.
		text := ""
		if raw, ok := item.Payload["text"].(string); ok {
			text = raw
		} else if item.Payload != nil {
			payload, _ := json.Marshal(item.Payload)
			text = string(payload)
		}
		formatted = append(formatted, fmt.Sprintf(
			"context %s%s: %s\n%s%s",
			item.Query.QueryType, suffix, item.Summary, text, errStr,
		))
	}
	return formatted
}

// contextItemIdentity builds the dedupe key for one fulfilled item:
// source identity (path+version+range) when present, else the query
// shape. Two items with the same identity deliver the same bytes twice.
func contextItemIdentity(item schemas.ContextItem) string {
	path, _ := item.Payload["path"].(string)
	version, _ := item.Payload["version"].(string)
	start, _ := item.Payload["start"].(int)
	end, _ := item.Payload["end"].(int)
	if path != "" && version != "" {
		return fmt.Sprintf("%s@%s#%d-%d", path, version, start, end)
	}
	q := item.Query
	key, _ := json.Marshal(struct {
		Type    string
		Path    string
		Pattern string
		Symbol  string
	}{
		Type:    string(q.QueryType),
		Path:    derefString(q.Path),
		Pattern: derefString(q.Pattern),
		Symbol:  derefString(q.Symbol),
	})
	return string(key)
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// selectMemory maps an admitted MemoryBundle into bounded SelectedMemory
// items carrying stable audit ids. It returns nil when bundle is nil or
// empty, so omitempty keeps the JSON field absent when no memory was
// delivered. Admission policy lives in the memoryreason module; this wrapper
// only converts types.
func selectMemory(bundle *schemas.MemoryBundle) []schemas.SelectedMemory {
	return memoryreason.Select(bundle)
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

// proposalArraySchema is the shared JSON schema for a stage's `files`
// array under the compact/1 edit protocol (C3). Both writer and test
// generator advertise the SAME versioned schema so both arms of a
// comparison send the same shape. A file entry is exactly one of:
// create (content only), modify (base_ref + edits), delete (base_ref).
func proposalArraySchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string"},
				"change_type": map[string]any{"type": "string", "enum": []string{"create", "modify", "delete"}},
				"base_ref":    map[string]any{"type": "string", "description": "For modify/delete: the source handle of the base content you received in context views. The host resolves it to exact bytes; you never compute hashes."},
				"edits": map[string]any{
					"type":        "array",
					"description": "For modify: exact text replacements matched against the base content. old must appear exactly once; no fuzzy matching.",
					"items": map[string]any{
						"type":       "object",
						"properties": map[string]any{"old": map[string]any{"type": "string"}, "new": map[string]any{"type": "string", "description": "Replacement text; empty deletes the matched span."}},
						"required":   []string{"old", "new"},
					},
				},
				"content": map[string]any{"type": "string", "description": "For create only: the full file content."},
			},
			"required": []string{"path", "change_type"},
		},
	}
}

// fileChangeArraySchema is the legacy full/1 schema, kept for stored
// artifacts and legacy routes. The live comparison uses proposalArraySchema
// for BOTH arms; this function remains the pairing reference for the
// legacy protocol version.
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
