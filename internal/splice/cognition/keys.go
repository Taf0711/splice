// Package cognition implements the deterministic structural fast path of the
// C0 slice: conservative topic-key derivation from stage context and a
// conservative freshness gate over the observation's source commit. It is
// retrieval-only observability, never control flow: a miss, stale, unknown,
// or error falls back byte-identically to the existing Memory.Search path.
// No LLM is ever consulted in this package.
package cognition

import (
	"regexp"
	"sort"
	"strings"
)

// Key classes. The C0 derivation emits only these; test: and error: classes
// are deferred until a deterministic resolver exists (a test name is only
// keyed when it resolves to a source file, and error identifiers need stable
// structured tooling, not prose).
const (
	KeyFile    = "file:"
	KeySymbol  = "symbol:"
	KeyPackage = "package:"
)

// DeriveInput is the structural context from which cognition keys are
// derived. Only strong, deterministically-identifiable inputs produce keys;
// request-intent prose is mined only with the strict patterns below.
type DeriveInput struct {
	// RequestIntent is the distilled request intent. Parsed ONLY for strict
	// repo-relative path and path#Symbol tokens; never fuzzy matching.
	RequestIntent string
	// PriorChangedFiles maps earlier stage names to the repo-relative paths
	// those stages reported changing. Structured, already-verified evidence;
	// the strongest key source.
	PriorChangedFiles map[string][]string
	// VerificationCommands are structured verifier commands (from acceptance
	// facts), mined for strict ./pkg/... package targets.
	VerificationCommands []string
}

// sourceExtensions are the file extensions a mined path must end in for the
// token to count as a repo-relative source path.
var sourceExtensions = map[string]bool{
	"go": true, "py": true, "ts": true, "js": true, "rs": true, "java": true,
	"c": true, "cc": true, "cpp": true, "h": true, "hpp": true, "sh": true,
	"md": true, "json": true, "yaml": true, "yml": true, "toml": true,
}

// repoPathToken matches a repo-relative path token: identifier-like segments
// joined by '/', ending in a known source extension. Segments start with an
// alphanumeric or underscore, so './', '../', and hidden-dot segments are
// excluded by construction. At least one '/' is required.
// Extension alternation is ordered longest-first so a longer real extension
// wins over its shorter prefix (.json over .js, .cpp/.cc over .c). The
// original shortest-first order produced phantom keys like file:x/y.js from
// x/y.json (found by the E1b eval suite).
var repoPathToken = regexp.MustCompile(`[A-Za-z0-9_][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_][A-Za-z0-9_.-]*)+\.(?:json|yaml|java|toml|cpp|hpp|yml|go|py|ts|js|rs|cc|c|h|sh|md)`)

// symbolToken matches a repo-relative path immediately followed by #Symbol,
// e.g. internal/auth/session.go#ResetPassword.
var symbolToken = regexp.MustCompile(`([A-Za-z0-9_][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_][A-Za-z0-9_.-]*)+\.(?:java|cpp|hpp|go|py|ts|js|rs|cc|c|h))#([A-Za-z_][A-Za-z0-9_]*)`)

// pkgTarget matches a strict package target in a verification command:
// ./pkg/... with the pkg path being identifier-like segments. The leading
// ./ and trailing /... are required, so prose never matches.
var pkgTarget = regexp.MustCompile(`\./([A-Za-z0-9_][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_][A-Za-z0-9_.-]*)*)/\.\.\.`)

// validPathContext rejects a mined path token that is really part of a URL,
// absolute path, or email: the character immediately before the token is
// ':', '@', or '/'. The token regex matches from its first segment, so a
// preceding '/' can only mean the path started earlier than the match.
func validPathContext(text string, start int) bool {
	if start <= 0 {
		return true
	}
	switch text[start-1] {
	case ':', '@', '/':
		return false
	}
	return true
}

// findRepoRelativePaths mines strict repo-relative path tokens from prose.
// Each match must pass the surrounding-context filter; nothing fuzzy.
func findRepoRelativePaths(text string) []string {
	indexes := repoPathToken.FindAllStringIndex(text, -1)
	paths := make([]string, 0, len(indexes))
	for _, loc := range indexes {
		if !validPathContext(text, loc[0]) {
			continue
		}
		path := text[loc[0]:loc[1]]
		// An IPv4-literal first segment (192.168.0.1/...) is a host route,
		// not a repo-relative path (found by the E1b eval suite).
		if isIPLikeFirstSegment(path) {
			continue
		}
		ext := path[strings.LastIndex(path, ".")+1:]
		if !sourceExtensions[ext] {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

// isIPLikeFirstSegment reports whether the path's first segment is a
// numeric dot-separated blob such as 192.168.0.1. Repo-relative paths do
// not start with one.
func isIPLikeFirstSegment(path string) bool {
	seg := path
	if i := strings.Index(path, "/"); i >= 0 {
		seg = path[:i]
	}
	digits := true
	dots := 0
	for _, r := range seg {
		switch {
		case r >= '0' && r <= '9':
		case r == '.':
			dots++
		default:
			return false
		}
	}
	return digits && dots >= 2
}

// findSymbols mines strict path#Symbol tokens from prose.
func findSymbols(text string) []string {
	matches := symbolToken.FindAllStringSubmatch(text, -1)
	symbols := make([]string, 0, len(matches))
	for _, m := range matches {
		symbols = append(symbols, m[1]+"#"+m[2])
	}
	return symbols
}

// sortedStageNames returns the PriorChangedFiles keys in sorted order so key
// derivation is deterministic regardless of map iteration order.
func sortedStageNames(files map[string][]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DeriveKeys returns the deterministic, conservative topic keys for one stage
// invocation, in sorted order, deduplicated, and bounded to strong evidence:
//
//  1. file:<path> for every path in the structured prior changed-file lists;
//  2. package:<pkg> for every strict ./pkg/... target in the verification
//     commands;
//  3. file:<path> and symbol:<path>#Sym for strict tokens in the request
//     intent.
//
// Low-confidence or unresolvable inputs produce NO key (an empty result is
// correct: the caller falls back to the broad search). Keys are exact-match
// topics: they must equal the TopicKey an observation was persisted with.
func DeriveKeys(in DeriveInput) []string {
	seen := make(map[string]bool)
	var keys []string
	add := func(key string) {
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		keys = append(keys, key)
	}

	for _, stage := range sortedStageNames(in.PriorChangedFiles) {
		for _, path := range in.PriorChangedFiles[stage] {
			add(KeyFile + path)
		}
	}
	for _, cmd := range in.VerificationCommands {
		for _, m := range pkgTarget.FindAllStringSubmatch(cmd, -1) {
			add(KeyPackage + m[1])
		}
	}
	for _, path := range findRepoRelativePaths(in.RequestIntent) {
		add(KeyFile + path)
	}
	for _, sym := range findSymbols(in.RequestIntent) {
		add(KeySymbol + sym)
	}

	sort.Strings(keys)
	return keys
}

// AnchorPathForKey returns the repo-relative freshness anchor for a key, or
// "" when the key class has no resolvable file/directory anchor. For symbol
// keys the containing file is the anchor (symbol-level invalidation is a C1b
// concern). Package keys anchor on the package directory: git diff on a
// directory covers everything under it.
func AnchorPathForKey(key string) string {
	switch {
	case strings.HasPrefix(key, KeyFile):
		return strings.TrimPrefix(key, KeyFile)
	case strings.HasPrefix(key, KeySymbol):
		rest := strings.TrimPrefix(key, KeySymbol)
		if i := strings.Index(rest, "#"); i >= 0 {
			return rest[:i]
		}
	case strings.HasPrefix(key, KeyPackage):
		return strings.TrimPrefix(key, KeyPackage)
	}
	return ""
}
