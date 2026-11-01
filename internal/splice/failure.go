package splice

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// FailureKind classifies the evidence source of a failure so repair policy can
// react to compile errors, test failures, and verifier refusals differently.
type FailureKind string

const (
	FailureKindCompile  FailureKind = "compile"
	FailureKindTest     FailureKind = "test"
	FailureKindStatic   FailureKind = "static"
	FailureKindVerifier FailureKind = "verifier"
	FailureKindCommand  FailureKind = "command"
)

// FailureFingerprint is a normalized, comparable identity for one failure
// observation. Two runs of the same semantic failure produce the same
// fingerprint; volatile detail (temp paths, run ids, timestamps, column
// numbers) is normalized away. Distinguishing content (test names, symbol
// names, error messages) is preserved so different failures stay different.
type FailureFingerprint struct {
	Kind       FailureKind `json:"kind"`
	Command    string      `json:"command,omitempty"`
	ExitCode   int         `json:"exit_code,omitempty"`
	Diagnostic string      `json:"diagnostic,omitempty"`
	Symbols    []string    `json:"symbols,omitempty"`
	Files      []string    `json:"files,omitempty"`
}

// Volatile-detail patterns. Order matters: temp paths are stripped before the
// long-hex rule so sandbox folder names are removed whole.
var (
	failureTimestampRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?`)
	failureRunIDRe     = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	failureLongHexRe   = regexp.MustCompile(`\b[0-9a-fA-F]{16,}\b`)
	failureTempPathRe  = regexp.MustCompile(`(?:/private/)?/?(?:var/folders|tmp)/`)
	failureElapsedRe   = regexp.MustCompile(`\(\d+(?:\.\d+)?s\)`)
	failureLineColRe   = regexp.MustCompile(`:(\d+):\d+`)
	failureUnixTimeRe  = regexp.MustCompile(`\b1\d{9}(?:\.\d{1,6})?\b`)
)

// NormalizeFailureText strips volatile detail from a failure string: temp
// directory paths, run ids, timestamps, elapsed markers, and column numbers
// (file.go:12:34 becomes file.go:12). Whitespace collapses. Test names, symbol
// names, and error message text are preserved.
func NormalizeFailureText(s string) string {
	if s == "" {
		return ""
	}
	s = failureTimestampRe.ReplaceAllString(s, "<time>")
	s = failureUnixTimeRe.ReplaceAllString(s, "<time>")
	s = failureRunIDRe.ReplaceAllString(s, "<run>")
	s = stripTempDirs(s)
	s = failureLongHexRe.ReplaceAllString(s, "<id>")
	s = failureElapsedRe.ReplaceAllString(s, "(elapsed)")
	s = failureLineColRe.ReplaceAllString(s, ":$1")
	return strings.Join(strings.Fields(s), " ")
}

// stripTempDirs replaces each temp-directory prefix (/tmp/..., /var/folders/...,
// /private/var/...) up to the segment that contains a file name (a dot in its
// last path segment) with <tmp>, keeping the file name. A token with no
// remaining file segment after the temp root becomes <tmp>.
func stripTempDirs(s string) string {
	fields := strings.Fields(s)
	for i, field := range fields {
		// The macOS temp root answers as /private/var/folders too; normalize
		// the /private prefix before matching.
		field = strings.TrimPrefix(field, "/private/")
		loc := failureTempPathRe.FindStringIndex(field)
		if loc == nil {
			continue
		}
		prefix := field[:loc[0]]
		rest := field[loc[1]:]
		segments := strings.Split(rest, "/")
		// Walk until the last segment; the deepest segment that names a file
		// (contains a dot) and everything after it survives.
		keep := ""
		for j := len(segments) - 1; j >= 0; j-- {
			if strings.Contains(segments[j], ".") {
				keep = strings.Join(segments[j:], "/")
				break
			}
		}
		if keep != "" {
			fields[i] = prefix + "<tmp>/" + keep
		} else {
			fields[i] = prefix + "<tmp>"
		}
	}
	return strings.Join(fields, " ")
}

// NewFailureFingerprint builds a fingerprint over normalized fields. An empty
// kind is inferred from the diagnostic text. Lists are deduplicated and sorted
// so ordering never changes the hash.
func NewFailureFingerprint(kind FailureKind, command string, exitCode int, diagnostic string, symbols, files []string) FailureFingerprint {
	if kind == "" {
		kind = InferFailureKind(diagnostic)
	}
	normalized := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if s = strings.TrimSpace(s); s != "" {
			normalized = append(normalized, s)
		}
	}
	normalizedFiles := make([]string, 0, len(files))
	for _, f := range files {
		f = NormalizeFailureText(f)
		f = strings.TrimPrefix(f, "./")
		if f != "" {
			normalizedFiles = append(normalizedFiles, f)
		}
	}
	return FailureFingerprint{
		Kind:       kind,
		Command:    strings.Join(strings.Fields(command), " "),
		ExitCode:   exitCode,
		Diagnostic: NormalizeFailureText(diagnostic),
		Symbols:    sortedUnique(normalized),
		Files:      sortedUnique(normalizedFiles),
	}
}

// Hash returns a short deterministic sha256 identity over the normalized
// fields. Equal semantic failures hash equal; distinct failures do not.
func (f FailureFingerprint) Hash() string {
	canonical := struct {
		Kind       FailureKind
		Command    string
		ExitCode   int
		Diagnostic string
		Symbols    []string
		Files      []string
	}{f.Kind, f.Command, f.ExitCode, f.Diagnostic, f.Symbols, f.Files}
	data, err := json.Marshal(canonical)
	if err != nil {
		// json.Marshal over this plain struct cannot fail; fall back to a
		// formatting of the diagnostic so a hash always exists.
		data = []byte(fmt.Sprintf("%s|%s", f.Kind, f.Diagnostic))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12]
}

func sortedUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok || v == "" {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// InferFailureKind maps diagnostic text to a failure kind. Compile-shaped
// diagnostics win over test markers because a package that fails to build
// also prints FAIL lines.
func InferFailureKind(diagnostic string) FailureKind {
	d := strings.ToLower(diagnostic)
	switch {
	case strings.Contains(d, "undefined:") ||
		strings.Contains(d, "build failed") ||
		strings.Contains(d, "build-output") ||
		strings.Contains(d, "has no field or method") ||
		strings.Contains(d, "cannot use ") && strings.Contains(d, "as "):
		return FailureKindCompile
	case strings.Contains(d, "verifier"), strings.Contains(d, "acceptance fact"):
		return FailureKindVerifier
	case strings.Contains(d, "--- fail:"), strings.Contains(d, "fail\t"), strings.Contains(d, "failed "):
		return FailureKindTest
	default:
		return FailureKindCommand
	}
}

// failureDiagnosticSymbolRe extracts symbol names from compile diagnostics.
var failureDiagnosticSymbolRe = regexp.MustCompile(`undefined: ([A-Za-z_][A-Za-z0-9_]*)|has no field or method ([A-Za-z_][A-Za-z0-9_]*)`)

// failureFileMentionRe extracts Go file mentions with line numbers.
var failureFileMentionRe = regexp.MustCompile(`((?:\./)?[A-Za-z0-9_./\-]+\.go):\d+`)

func symbolsFromDiagnostic(text string) []string {
	var out []string
	for _, match := range failureDiagnosticSymbolRe.FindAllStringSubmatch(text, -1) {
		for _, group := range match[1:] {
			if group != "" {
				out = append(out, group)
			}
		}
	}
	return out
}

func goFileMentions(text string) []string {
	var out []string
	for _, match := range failureFileMentionRe.FindAllStringSubmatch(text, -1) {
		out = append(out, match[1])
	}
	return out
}

// FingerprintFromTestResults builds the canonical fingerprint for one test
// runner payload. Compile-error entries (name prefix "build ") make the kind
// compile; otherwise failing tests make it test; an empty result set stays
// command.
func FingerprintFromTestResults(results schemas.TestRunResults) FailureFingerprint {
	var diagLines, symbols, files []string
	compile := false
	testFail := false
	for _, tc := range results.Tests {
		if tc.Status != "failed" && tc.Status != "errored" {
			continue
		}
		if strings.HasPrefix(tc.Name, "build ") {
			compile = true
		} else {
			testFail = true
		}
		diagLines = append(diagLines, tc.Name+": "+tc.Message)
		symbols = append(symbols, symbolsFromDiagnostic(tc.Message)...)
		files = append(files, goFileMentions(tc.Message)...)
	}
	if len(diagLines) == 0 {
		// No structured entries: fall back to the raw captured output so the
		// fingerprint still distinguishes this failure from others.
		raw := strings.TrimSpace(results.Stdout + "\n" + results.Stderr)
		if raw != "" {
			diagLines = append(diagLines, raw)
			symbols = append(symbols, symbolsFromDiagnostic(raw)...)
			files = append(files, goFileMentions(raw)...)
		}
	}
	kind := FailureKindCommand
	switch {
	case compile:
		kind = FailureKindCompile
	case testFail:
		kind = FailureKindTest
	}
	return NewFailureFingerprint(kind, strings.Join(results.Command, " "), results.ExitCode,
		strings.Join(diagLines, "\n"), symbols, files)
}
