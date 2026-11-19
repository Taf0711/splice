package stages

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
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
		req := defaultContextRequestCarried(intent, evidence, o.WorkDir, o.Language, o.CarriedQueries, nil)
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

// defaultContextRequestPreferring is the initial context rule (D0 of
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
	return defaultContextRequestCarried(intent, evidence, workDir, language, nil, preferred)
}

// defaultContextRequestCarried is the default request with the files a
// previous invocation of the stage fetched (carried). Carried files come
// after the named and evidence paths and before the ranked fallback. They
// never turn the fallback off: it still runs when nothing is named.
func defaultContextRequestCarried(intent, evidence, workDir, language string, carried []schemas.ContextQuery, preferred []string) schemas.ContextRequest {
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
	// The fallback runs only when the request names no path. Carried paths
	// are not named paths, so they do not turn it off.
	fallback := len(readPaths) == 0
	carriedReads := map[string]schemas.ContextQuery{}
	for _, q := range carried {
		if q.Path == nil {
			continue
		}
		path := strings.TrimPrefix(*q.Path, "./")
		if !workspaceFileExists(workDir, path) {
			continue
		}
		if _, seen := carriedReads[path]; !seen {
			q.Path = &path
			carriedReads[path] = q
		}
		add(path)
	}
	// The sources the request points to (its package, its identifiers)
	// come before memory's files: memory adds to what the request names and
	// never displaces it. P1e: memory's files replaced this ranking, and
	// warm reads needed 0.3 to 0.4 more expansion rounds than cold.
	if fallback {
		for _, path := range rankedSourceMatches(workDir, language, intent, maxFallbackSourceFiles) {
			add(path)
		}
	}
	for _, path := range preferred {
		if path = strings.TrimPrefix(path, "./"); workspaceFileExists(workDir, path) {
			add(path)
		}
	}
	for _, path := range allSourcesIfFew(workDir, language) {
		add(path)
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
		if granted, ok := carriedReads[path]; ok {
			queries = append(queries, granted)
			continue
		}
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

// allSourcesIfFew returns every production source when they fit the fallback
// bound, and nil otherwise. A large workspace gets no arbitrary slice of its
// sources: a request that matches no file reads none of them.
func allSourcesIfFew(workDir string, language string) []string {
	sources := sourcePaths(workDir, language, false, maxFallbackSourceFiles+1)
	if len(sources) > maxFallbackSourceFiles {
		return nil
	}
	return sources
}

// Bounds for ranking the fallback sources: a large workspace is not scanned
// in full, and one large file is read only in part.
const (
	maxRankedSourceFiles = 2000
	maxRankedFileBytes   = 512 << 10
	// A token counts only when at most this share of candidate files holds
	// it, and never when fewer than rankedMinDocFreq files would be allowed.
	rankedMaxDocShare = 0.05
	rankedMinDocFreq  = 3
	// A directory word adds this much, divided by the document-frequency
	// limit. That is always below one distinctive hit (1/df), so a directory
	// alone never outranks a file that holds a request identifier.
	rankedDirWeight = 0.5
)

var (
	intentWordPattern       = regexp.MustCompile(`\b[a-z][a-z0-9_]{2,}\b`)
	intentIdentifierPattern = regexp.MustCompile(`\b[A-Z][A-Za-z0-9]*[a-z][A-Za-z0-9]*\b`)
	// Code spans: fenced blocks and inline backtick text.
	codeSpanPattern = regexp.MustCompile("(?s)```.*?```|`[^`\n]+`")
	// identWordPattern matches a dotted or plain identifier.
	identWordPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_.]*`)
	// callNamePattern matches a name followed by an opening parenthesis.
	callNamePattern = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.]*)\(`)
	// camelWordPattern matches a CamelCase word with an inner capital.
	camelWordPattern = regexp.MustCompile(`^(?:[a-z][A-Za-z0-9]*[A-Z]|[A-Z][a-z0-9]*[A-Z])[A-Za-z0-9]*$`)
)

// rankedKeywords are Python and Go keywords and built-ins that never name
// a project symbol.
var rankedKeywords = map[string]bool{
	"def": true, "class": true, "return": true, "import": true, "from": true,
	"elif": true, "else": true, "for": true, "while": true, "not": true,
	"and": true, "with": true, "try": true, "except": true, "finally": true,
	"lambda": true, "pass": true, "yield": true, "raise": true, "assert": true,
	"global": true, "nonlocal": true, "None": true, "True": true, "False": true,
	"async": true, "await": true, "break": true, "continue": true, "self": true,
	"func": true, "package": true, "var": true, "const": true, "type": true,
	"struct": true, "interface": true, "map": true, "chan": true, "range": true,
	"select": true, "case": true, "default": true, "defer": true, "switch": true,
	"fallthrough": true, "goto": true, "make": true, "nil": true, "the": true,
}

// wordTokens returns the identifier-like words of text. A sentence-final
// period is not part of the word, so "sympy.core.function." is one name.
func wordTokens(text string) []string {
	var out []string
	for _, word := range identWordPattern.FindAllString(text, -1) {
		if word = strings.TrimRight(word, "."); word != "" {
			out = append(out, word)
		}
	}
	return out
}

// rankedTokens returns the distinctive identifiers of a request. Code form
// is a word in a code span, a name followed by "(", a snake_case word, a
// dotted name (kept whole and split), or a CamelCase word with an inner
// capital. A plain capitalized word counts only when code form also names
// it. Tokens shorter than three characters and keywords are dropped.
func rankedTokens(intent string) []string {
	codeForm := map[string]bool{}
	var candidates []string
	add := func(word string) {
		if len(word) < 3 || rankedKeywords[word] || slices.Contains(candidates, word) {
			return
		}
		candidates = append(candidates, word)
	}
	addDotted := func(word string) {
		add(word)
		for _, part := range strings.Split(word, ".") {
			add(part)
		}
	}
	for _, span := range codeSpanPattern.FindAllString(intent, -1) {
		for _, word := range wordTokens(span) {
			codeForm[word] = true
			if strings.Contains(word, ".") {
				addDotted(word)
			} else {
				add(word)
			}
		}
	}
	prose := codeSpanPattern.ReplaceAllString(intent, " ")
	for _, match := range callNamePattern.FindAllStringSubmatch(prose, -1) {
		codeForm[match[1]] = true
		addDotted(match[1])
	}
	for _, word := range wordTokens(prose) {
		switch {
		case strings.Contains(word, "."):
			addDotted(word)
		case strings.Contains(word, "_"), camelWordPattern.MatchString(word):
			add(word)
		}
	}
	for _, word := range intentIdentifierPattern.FindAllString(prose, -1) {
		if codeForm[word] {
			add(word)
		}
	}
	return candidates
}

// isIdentByte reports whether b can continue an identifier.
func isIdentByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// containsIdent reports whether body holds tok as a whole identifier.
func containsIdent(body []byte, tok string) bool {
	needle := []byte(tok)
	for off := 0; off <= len(body); {
		i := bytes.Index(body[off:], needle)
		if i < 0 {
			return false
		}
		start := off + i
		end := start + len(needle)
		if (start == 0 || !isIdentByte(body[start-1])) && (end == len(body) || !isIdentByte(body[end])) {
			return true
		}
		off = start + 1
	}
	return false
}

// rankedSourcePaths is the fallback when the request names no file: the
// ranked matches, or every source when the workspace is small. A large
// workspace with no match gets nothing.
func rankedSourcePaths(workDir, language, intent string, limit int) []string {
	if matches := rankedSourceMatches(workDir, language, intent, limit); len(matches) > 0 {
		return matches
	}
	return allSourcesIfFew(workDir, language)
}

// rankedSourceMatches is rankedSourcePaths without the alphabetical
// fallback: only the sources that match the request, best first.
//
// Tier one ranks files by request identifiers. Each candidate is read up to
// maxRankedFileBytes. A token's weight is 1/df, where df counts the
// candidate files that hold it. A token that no file holds, or that more
// than rankedMaxDocShare of the files hold (never fewer than
// rankedMinDocFreq), is dropped as generic. A file that defines a token
// (def, class, func, type) scores 1 for it regardless of df. A file needs at
// least one scoring token to rank. A directory word adds rankedDirWeight over
// that limit. Ties go to the shorter path, then to lexical order.
//
// Tier two runs only when tier one ranks no file. It returns the sources
// under a directory the request names by word, in path order. It never
// mixes with tier one.
func rankedSourceMatches(workDir, language, intent string, limit int) []string {
	words := map[string]bool{}
	for _, w := range intentWordPattern.FindAllString(strings.ToLower(intent), -1) {
		words[w] = true
	}
	all := sourcePaths(workDir, language, false, maxRankedSourceFiles)
	tokens := rankedTokens(intent)
	ranked := rankByTokens(workDir, all, tokens, words)
	if len(ranked) == 0 {
		var out []string
		for _, path := range all {
			if inWordDir(path, words) {
				out = append(out, path)
			}
		}
		ranked = out
	}
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked
}

// inWordDir reports whether a directory of path is named by one of words.
func inWordDir(path string, words map[string]bool) bool {
	for _, d := range strings.Split(filepath.ToSlash(filepath.Dir(path)), "/") {
		if words[strings.ToLower(d)] {
			return true
		}
	}
	return false
}

// definitionNamePattern captures the name a definition declares: a Python def
// or class, or a Go func (with an optional receiver) or type.
var definitionNamePattern = regexp.MustCompile(`(?m)^\s*(?:def|class|type|func(?:\s+\([^)]*\))?)\s+([A-Za-z_][A-Za-z0-9_]*)`)

// definedNames returns the set of names a file body defines. One pass per
// file serves every request token (F2: a regex per token cost seconds).
func definedNames(body []byte) map[string]bool {
	names := map[string]bool{}
	for _, m := range definitionNamePattern.FindAllSubmatch(body, -1) {
		names[string(m[1])] = true
	}
	return names
}

// rankByTokens is tier one of rankedSourceMatches: the candidate files
// ranked by weighted token hits, best first. It returns nil when no token
// is in code form or no file scores.
func rankByTokens(workDir string, all []string, tokens []string, words map[string]bool) []string {
	if len(tokens) == 0 {
		return nil
	}
	type fileHits struct {
		path    string
		hits    []int
		defined map[int]bool
		dir     bool
	}
	df := make([]int, len(tokens))
	files := make([]fileHits, 0, len(all))
	// One buffer serves every file: allocating a 512 KiB head per file cost
	// more than the scan itself.
	buf := make([]byte, maxRankedFileBytes)
	for _, path := range all {
		body := readHeadInto(filepath.Join(workDir, path), buf)
		f := fileHits{path: path, defined: map[int]bool{}, dir: inWordDir(path, words)}
		var defined map[string]bool
		for i, tok := range tokens {
			if !containsIdent(body, tok) {
				continue
			}
			f.hits = append(f.hits, i)
			df[i]++
			if defined == nil {
				defined = definedNames(body)
			}
			if defined[tok] {
				f.defined[i] = true
			}
		}
		files = append(files, f)
	}
	dfLimit := math.Max(rankedMaxDocShare*float64(len(all)), rankedMinDocFreq)
	dirScore := rankedDirWeight / dfLimit
	type scored struct {
		path  string
		score float64
	}
	var scoredFiles []scored
	for _, f := range files {
		score := 0.0
		for _, i := range f.hits {
			switch {
			case f.defined[i]:
				score++
			case float64(df[i]) <= dfLimit:
				score += 1 / float64(df[i])
			}
		}
		if score == 0 {
			continue // no distinctive hit: a directory word alone never ranks
		}
		if f.dir {
			score += dirScore
		}
		scoredFiles = append(scoredFiles, scored{f.path, score})
	}
	sort.SliceStable(scoredFiles, func(i, j int) bool {
		if scoredFiles[i].score != scoredFiles[j].score {
			return scoredFiles[i].score > scoredFiles[j].score
		}
		if len(scoredFiles[i].path) != len(scoredFiles[j].path) {
			return len(scoredFiles[i].path) < len(scoredFiles[j].path)
		}
		return scoredFiles[i].path < scoredFiles[j].path
	})
	out := make([]string, 0, len(scoredFiles))
	for _, r := range scoredFiles {
		out = append(out, r.path)
	}
	return out
}

// readHeadInto fills buf from the start of the file and returns the bytes
// read, or nil when the file cannot be opened. The result aliases buf.
func readHeadInto(path string, buf []byte) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
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
			// Host-only keys (the raw source bytes) never enter the prompt.
			visible := make(map[string]any, len(item.Payload))
			for key, value := range item.Payload {
				if key != "raw" {
					visible[key] = value
				}
			}
			payload, _ := json.Marshal(visible)
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
	start, end := payloadLine(item.Payload["start"]), payloadLine(item.Payload["end"])
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

// gitChangedFileLines lists the files changed since HEAD and the untracked
// files, as paths relative to workDir. Changed paths come first, and each path
// appears once. A failed diff returns its error. A failed untracked listing
// returns an error that names untracked file discovery, so a caller never gets
// a partial list that looks complete.
func gitChangedFileLines(ctx context.Context, workDir, diffPurpose string, options StageOptions) ([]string, error) {
	// core.quotepath=off keeps non-ASCII names unquoted, so the extension match
	// and the stat see the real name.
	diffCommand := []string{"git", "-c", "core.quotepath=off", "diff", "--name-only", "--relative", "HEAD"}
	diffOut, err := runRecordedOutput(ctx, options, "splice.shell", map[string]any{
		"command": diffCommand,
		"cwd":     workDir,
		"purpose": diffPurpose,
	}, workDir, diffCommand)
	if err != nil {
		return nil, err
	}
	untrackedCommand := []string{"git", "-c", "core.quotepath=off", "ls-files", "--others", "--exclude-standard"}
	untrackedOut, err := runRecordedOutput(ctx, options, "splice.shell", map[string]any{
		"command": untrackedCommand,
		"cwd":     workDir,
		"purpose": "untracked file discovery",
	}, workDir, untrackedCommand)
	if err != nil {
		return nil, fmt.Errorf("untracked file discovery in %s: %w", workDir, err)
	}
	seen := make(map[string]struct{})
	var lines []string
	for _, out := range [][]byte{diffOut, untrackedOut} {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if _, ok := seen[line]; ok {
				continue
			}
			seen[line] = struct{}{}
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// truncatedCoverageRun reports a file walk that stopped at its limit. Its
// required incomplete status makes the aggregated report incomplete. The caller
// prepends it to the tool runs, so its summary becomes the report summary.
func truncatedCoverageRun(scope string, scanned, limit int) schemas.VerificationToolRun {
	return schemas.VerificationToolRun{
		Tool:     "coverage",
		Required: true,
		Scope:    scope,
		Status:   schemas.VerificationIncomplete,
		Summary:  fmt.Sprintf("scanned %d file(s); more files were not scanned (limit %d)", scanned, limit),
	}
}
