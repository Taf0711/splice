package stages

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
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

// contextRequest builds the stage's initial context request. evidence is
// the revision context of a repair re-entry (empty on the first pass): the
// files its failures name join the initial reads.
func (o StageOptions) contextRequest(intent, evidence string) *schemas.ContextRequest {
	if o.OverrideContextRequest != nil {
		return o.OverrideContextRequest
	}
	if o.PullContext {
		req := defaultContextRequestWithEvidence(intent, evidence, o.WorkDir, o.Language)
		return &req
	}
	return nil
}

// DefaultContextRequestFor is the exported counterfactual: the default
// context request the cold path would issue for this stage. evidence is the
// repair revision context, or empty. The cognition scope planner uses it
// for structural suppression accounting, and the prefetch paths extend it.
func DefaultContextRequestFor(intent, evidence, workDir, language string) schemas.ContextRequest {
	return defaultContextRequestPreferring(intent, evidence, workDir, language, nil)
}

// DefaultContextRequestPreferring is the default request with memory's
// files: preferred paths (files a related verified run needed) are read
// after the named and evidence paths, and in a large workspace they replace
// the arbitrary production-source slice. A small workspace still reads all
// of its sources.
func DefaultContextRequestPreferring(intent, evidence, workDir, language string, preferred []string) schemas.ContextRequest {
	return defaultContextRequestPreferring(intent, evidence, workDir, language, preferred)
}

func defaultContextRequest(intent string, workDir string, language string) schemas.ContextRequest {
	return defaultContextRequestPreferring(intent, "", workDir, language, nil)
}

func defaultContextRequestWithEvidence(intent, evidence, workDir, language string) schemas.ContextRequest {
	return defaultContextRequestPreferring(intent, evidence, workDir, language, nil)
}

// languageManifests are the small project files that name the module or
// package. The writer needs the module name to write correct imports.
var languageManifests = map[string][]string{
	"go":         {"go.mod"},
	"python":     {"pyproject.toml"},
	"typescript": {"package.json"},
	"javascript": {"package.json"},
}

// testMentionPattern finds a request that talks about tests. Only then do
// the workspace's test files join the initial reads.
var testMentionPattern = regexp.MustCompile(`(?i)\btests?\b|_test\.|table-driven|\bspec\b`)

// defaultContextRequestWithEvidence is the initial context rule (D0 of
// plans/SPEC_JEV_DECISION_LAYER_2026-09-25.md). On the recorded campaigns,
// 116 of 124 first context expansions asked for an existing file that the
// old rule did not deliver: a request that named one path read only that
// path, even when the path was the file to create.
//
//   - Named paths that exist are read first. Named paths that do not exist
//     are files to create: the reason names them and nothing reads them.
//   - Files that the repair evidence names are read next.
//   - The production sources are read when nothing was named, or when the
//     workspace is small enough that all of them fit the fallback bound. A
//     large workspace does not get an arbitrary slice of its sources.
//   - The module manifest is always read when it exists.
//   - Test files are read only when the request talks about tests. Otherwise
//     they stay out: a repair-loop fixture can plant a trap test.
func defaultContextRequestPreferring(intent, evidence, workDir, language string, preferred []string) schemas.ContextRequest {
	queries := []schemas.ContextQuery{
		{QueryType: schemas.ContextListFiles, MaxResults: defaultListMaxResults, MaxChars: 10000},
	}
	var readPaths, createTargets []string
	add := func(path string) {
		if !slices.Contains(readPaths, path) {
			readPaths = append(readPaths, path)
		}
	}
	// Without a workspace, a named path cannot be proven missing: keep it.
	unknownWorkspace := strings.TrimSpace(workDir) == ""
	for _, path := range candidatePaths(intent) {
		if unknownWorkspace || workspaceFileExists(workDir, path) {
			add(path)
		} else if !slices.Contains(createTargets, path) {
			createTargets = append(createTargets, path)
		}
	}
	// An evidence path takes a read slot only when it exists: the failure
	// text names test files bare ("catalog_test.go:16:"), and those took
	// the slots of the real paths after them (P2d cpl-01).
	evidenceReads := 0
	for _, match := range candidatePathPattern.FindAllString(evidence, -1) {
		if evidenceReads == maxDefaultReadQueries {
			break
		}
		path := strings.TrimPrefix(match, "./")
		if strings.HasPrefix(path, "/") || strings.Contains(path, "..") || slices.Contains(readPaths, path) || !workspaceFileExists(workDir, path) {
			continue
		}
		add(path)
		evidenceReads++
	}
	// The sources the request points to (its package, its identifiers)
	// come before memory's files: memory adds to what the request names and
	// never displaces it. P1e: memory's files replaced this ranking, and
	// warm reads needed 0.3 to 0.4 more expansion rounds than cold.
	if len(readPaths) == 0 {
		for _, path := range rankedSourceMatches(workDir, language, intent, maxFallbackSourceFiles) {
			add(path)
		}
	}
	for _, path := range preferred {
		if path = strings.TrimPrefix(path, "./"); workspaceFileExists(workDir, path) {
			add(path)
		}
	}
	if len(readPaths) == 0 {
		for _, path := range productionSourcePaths(workDir, language) {
			add(path)
		}
	} else if sources := sourcePaths(workDir, language, false, maxFallbackSourceFiles+1); len(sources) <= maxFallbackSourceFiles {
		for _, path := range sources {
			add(path)
		}
	}
	for _, manifest := range languageManifests[strings.ToLower(strings.TrimSpace(language))] {
		if workspaceFileExists(workDir, manifest) {
			add(manifest)
		}
	}
	if testMentionPattern.MatchString(intent) {
		for _, path := range sourcePaths(workDir, language, true, maxFallbackSourceFiles) {
			add(path)
		}
	}
	for _, path := range readPaths {
		queries = append(queries, schemas.ContextQuery{
			QueryType:  schemas.ContextReadFile,
			Path:       &path,
			MaxResults: 10,
			MaxChars:   fallbackSourceMaxChars,
		})
	}
	reason := "Inspect existing project files before writing so edits modify real code instead of overwriting it."
	if len(createTargets) > 0 {
		reason += " These named files do not exist yet, so the task creates them: " + strings.Join(createTargets, ", ") + "."
	}
	req := schemas.ContextRequest{Reason: reason, Queries: queries}
	req.FitAggregate()
	return req
}

// workspaceFileExists reports whether a workspace-relative path names a
// regular file inside workDir.
func workspaceFileExists(workDir, path string) bool {
	if strings.TrimSpace(workDir) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(workDir, path))
	return err == nil && info.Mode().IsRegular()
}

// productionSourcePaths lists up to maxFallbackSourceFiles workspace-relative
// production source paths for the detected language. Test files are excluded,
// hidden and dependency directories are skipped, and the result is sorted for
// deterministic context bundles.
func productionSourcePaths(workDir string, language string) []string {
	return sourcePaths(workDir, language, false, maxFallbackSourceFiles)
}

// Bounds for ranking the fallback sources: a large workspace is not scanned
// in full, and one large file is read only in part.
const (
	maxRankedSourceFiles = 2000
	maxRankedFileBytes   = 64 << 10
)

var (
	intentIdentifierPattern = regexp.MustCompile(`\b[A-Z][A-Za-z0-9]*[a-z][A-Za-z0-9]*\b`)
	intentWordPattern       = regexp.MustCompile(`\b[a-z][a-z0-9_]{2,}\b`)
	// A sentence's first word ("Add", "Reuse") is capitalized, not an
	// identifier, and common method names would match it.
	sentenceStartPattern = regexp.MustCompile(`(^|[.!?:]\s+)[A-Z][a-z]+\b`)
)

// rankedSourcePaths is the fallback when the request names no file. The
// request usually names its package ("in the billing package") and the
// identifiers it uses ("DunningLevel"), so the sources in that package and
// the sources that mention those identifiers are read first. An
// alphabetical slice read cmd/, admin/, audit/, and auth/ for a billing
// task, and the model spent its expansion rounds finding billing.
//
// A directory in the path that matches a word of the request scores 3. Each
// identifier of the request that the file mentions as a word scores 1.
// Ties keep path order. When nothing scores, the result is the alphabetical
// slice, as before.
func rankedSourcePaths(workDir, language, intent string, limit int) []string {
	if matches := rankedSourceMatches(workDir, language, intent, limit); len(matches) > 0 {
		return matches
	}
	return productionSourcePaths(workDir, language)
}

// rankedSourceMatches is rankedSourcePaths without the alphabetical
// fallback: only the sources that match the request, best first.
func rankedSourceMatches(workDir, language, intent string, limit int) []string {
	all := sourcePaths(workDir, language, false, maxRankedSourceFiles)
	words := map[string]bool{}
	for _, w := range intentWordPattern.FindAllString(strings.ToLower(intent), -1) {
		words[w] = true
	}
	var idents []*regexp.Regexp
	seen := map[string]bool{}
	for _, id := range intentIdentifierPattern.FindAllString(sentenceStartPattern.ReplaceAllString(intent, "$1"), -1) {
		if !seen[id] {
			seen[id] = true
			idents = append(idents, regexp.MustCompile(`\b`+regexp.QuoteMeta(id)+`\b`))
		}
	}
	type scored struct {
		path  string
		score int
	}
	var ranked []scored
	for _, path := range all {
		score := 0
		for _, dir := range strings.Split(filepath.ToSlash(filepath.Dir(path)), "/") {
			if words[strings.ToLower(dir)] {
				score += 3
				break
			}
		}
		if len(idents) > 0 {
			if body := readHead(filepath.Join(workDir, path), maxRankedFileBytes); body != nil {
				for _, id := range idents {
					if id.Match(body) {
						score++
					}
				}
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{path, score})
		}
	}
	if len(ranked) == 0 {
		return nil
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	out := make([]string, 0, limit)
	for _, r := range ranked {
		if len(out) == limit {
			break
		}
		out = append(out, r.path)
	}
	return out
}

// readHead returns up to max bytes from the start of the file, or nil.
func readHead(path string, max int) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, max)
	n, _ := io.ReadFull(f, buf)
	return buf[:n]
}

// sourcePaths lists up to limit workspace-relative source paths for the
// language: test files when tests is true, production files otherwise.
func sourcePaths(workDir string, language string, tests bool, limit int) []string {
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
		if isTestFileName(entry.Name()) != tests {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil || relative == "." || relative == ".." {
			return nil
		}
		if !slices.Contains(paths, relative) {
			paths = append(paths, relative)
		}
		if len(paths) >= limit {
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
	for _, note := range bundle.HostNotes {
		formatted = append(formatted, fmt.Sprintf("host note: %s", note))
	}
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
				"base_ref":    map[string]any{"type": "string", "description": "For modify/delete: the path of the delivered view you are editing, exactly as named in your context views (for example \"clock_test.go\"). The host resolves it to the delivered base text; you never compute hashes."},
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
