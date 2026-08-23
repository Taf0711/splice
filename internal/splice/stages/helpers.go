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
