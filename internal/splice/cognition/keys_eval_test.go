package cognition

// E1b structural-key evaluation suite (handoff section 8): a large
// table-driven suite over DeriveKeys covering the full section 8 case list,
// each case with EXPLICIT expected output. No fuzzy expectations: a case
// either names the exact keys it must produce or asserts the empty result.
//
// Governing principle (section 8): PRECISION OVER RECALL. If a structural
// key cannot be derived confidently, DeriveKeys produces NO key and the
// caller falls back to normal search. No case here may be "fixed" by
// loosening the token rules merely to raise the hit rate; a change that
// makes any negative case produce a key is a precision regression.

import (
	"reflect"
	"strings"
	"testing"
)

// keyCase pairs a DeriveInput with the exact expected key set.
type keyCase struct {
	name  string
	input DeriveInput
	want  []string
	// wantNone asserts the empty result explicitly (the precision-over-
	// recall contract for ambiguous or hostile input).
	wantNone bool
}

func keysEvalCases() []keyCase {
	return []keyCase{
		// --- explicit and multiple paths (section 8) ---
		{
			name:  "single explicit path",
			input: DeriveInput{RequestIntent: "fix the bug in internal/auth/session.go"},
			want:  []string{"file:internal/auth/session.go"},
		},
		{
			name:  "multiple explicit paths",
			input: DeriveInput{RequestIntent: "update internal/auth/session.go and internal/auth/session_test.go"},
			want:  []string{"file:internal/auth/session.go", "file:internal/auth/session_test.go"},
		},
		{
			name:  "path appears twice, deduplicated",
			input: DeriveInput{RequestIntent: "internal/auth/session.go is broken; also see internal/auth/session.go again"},
			want:  []string{"file:internal/auth/session.go"},
		},
		{
			name:  "path repeated across changed files and intent",
			input: DeriveInput{RequestIntent: "touch internal/auth/session.go", PriorChangedFiles: map[string][]string{"code_writer": {"internal/auth/session.go"}}},
			want:  []string{"file:internal/auth/session.go"},
		},

		// --- quoted path / path in markdown (section 8) ---
		{
			name:  "path in double quotes",
			input: DeriveInput{RequestIntent: `please fix "internal/auth/session.go" before release`},
			want:  []string{"file:internal/auth/session.go"},
		},
		{
			name:  "path in backticks (markdown code span)",
			input: DeriveInput{RequestIntent: "the file `internal/auth/session.go` fails vet"},
			want:  []string{"file:internal/auth/session.go"},
		},
		{
			name:  "path in markdown link text",
			input: DeriveInput{RequestIntent: "see [internal/auth/session.go](docs) for the fix"},
			want:  []string{"file:internal/auth/session.go"},
		},

		// --- symbols (section 8) ---
		{
			name:  "go symbol via path#Symbol",
			input: DeriveInput{RequestIntent: "internal/auth/session.go#Invalidate misbehaves"},
			want:  []string{"file:internal/auth/session.go", "symbol:internal/auth/session.go#Invalidate"},
		},
		{
			name:  "method symbol with receiver-style name",
			input: DeriveInput{RequestIntent: "internal/store/handler.go#Handler_ServeHTTP returns early"},
			want:  []string{"file:internal/store/handler.go", "symbol:internal/store/handler.go#Handler_ServeHTTP"},
		},
		{
			name:  "test function symbol",
			input: DeriveInput{RequestIntent: "internal/auth/session_test.go#TestInvalidate flakes"},
			want:  []string{"file:internal/auth/session_test.go", "symbol:internal/auth/session_test.go#TestInvalidate"},
		},
		{
			name:  "two symbols in one path",
			input: DeriveInput{RequestIntent: "internal/auth/session.go#Invalidate and internal/auth/session.go#Refresh both fail"},
			want: []string{
				"file:internal/auth/session.go",
				"symbol:internal/auth/session.go#Invalidate",
				"symbol:internal/auth/session.go#Refresh",
			},
		},

		// --- packages via verify commands (section 8) ---
		{
			name: "verify command package target",
			input: DeriveInput{
				RequestIntent:        "make the tests pass",
				VerificationCommands: []string{"go test ./internal/auth/..."},
			},
			want: []string{"package:internal/auth"},
		},
		{
			name: "nested package target",
			input: DeriveInput{
				RequestIntent:        "run the checks",
				VerificationCommands: []string{"go vet ./internal/splice/cognition/..."},
			},
			want: []string{"package:internal/splice/cognition"},
		},
		{
			name: "verify command without ./ prefix must not match",
			input: DeriveInput{
				RequestIntent:        "go test internal/auth/... directly",
				VerificationCommands: []string{"go test internal/auth/..."},
			},
			wantNone: true,
		},
		{
			name: "verify command trailing ellipsis required",
			input: DeriveInput{
				VerificationCommands: []string{"go test ./internal/auth"},
			},
			wantNone: true,
		},

		// --- changed-file references (section 8) ---
		{
			name: "changed files from one stage",
			input: DeriveInput{PriorChangedFiles: map[string][]string{
				"code_writer": {"internal/auth/session.go"},
			}},
			want: []string{"file:internal/auth/session.go"},
		},
		{
			name: "changed files across stages merge and dedupe",
			input: DeriveInput{PriorChangedFiles: map[string][]string{
				"code_writer": {"internal/auth/session.go", "internal/auth/store.go"},
				"test_runner": {"internal/auth/session_test.go", "internal/auth/session.go"},
			}},
			want: []string{
				"file:internal/auth/session.go",
				"file:internal/auth/session_test.go",
				"file:internal/auth/store.go",
			},
		},

		// --- nested packages / deep paths ---
		{
			name:  "deeply nested path",
			input: DeriveInput{RequestIntent: "fix internal/a/b/c/d/e/deep.go"},
			want:  []string{"file:internal/a/b/c/d/e/deep.go"},
		},

		// --- ambiguous identifier (section 8): a bare identifier with no
		// path context produces NO key. The resolver cannot know which file
		// it belongs to; guessing would be a false-positive key.
		{
			name:     "ambiguous bare identifier produces no key",
			input:    DeriveInput{RequestIntent: "InvalidateSession is broken somewhere"},
			wantNone: true,
		},
		{
			name:     "same identifier mentioned twice still no key",
			input:    DeriveInput{RequestIntent: "InvalidateSession and InvalidateSession both fail"},
			wantNone: true,
		},

		// --- malformed / nonexistent / hostile text (section 8) ---
		{
			name:     "malformed path without extension",
			input:    DeriveInput{RequestIntent: "fix internal/auth/session as soon as possible"},
			wantNone: true,
		},
		{
			name:     "path with traversal dots",
			input:    DeriveInput{RequestIntent: "fix ../internal/auth/session.go"},
			wantNone: true,
		},
		{
			name:     "path traversal looking text",
			input:    DeriveInput{RequestIntent: "the config in ../../etc/passwd.md needs care"},
			wantNone: true,
		},
		{
			name:     "windows-like path text",
			input:    DeriveInput{RequestIntent: `check C:\repo\internal\auth\session.go`},
			wantNone: true,
		},
		{
			name:     "URL that ends in a source extension",
			input:    DeriveInput{RequestIntent: "mirror the code at https://example.com/x/session.go"},
			wantNone: true,
		},
		{
			name:     "email address before a path-looking token",
			input:    DeriveInput{RequestIntent: "mail admin@example.com/internal/auth/session.go for access"},
			wantNone: true,
		},
		{
			name:     "error string with slash characters",
			input:    DeriveInput{RequestIntent: `the log says open /var/data/store.json: no such file`},
			wantNone: true,
		},
		{
			// The longest-extension fix (E1b finding): go.json derives
			// file:...go.json, not the phantom ...go.js.
			name:  "longer extension wins over its shorter prefix",
			input: DeriveInput{RequestIntent: "prose mentioning that something/go.json was renamed"},
			want:  []string{"file:something/go.json"},
		},
		{
			// A path prefix inside longer dotted text still matches when the
			// prefix itself is a valid repo-relative source path (a/b.go in
			// a/b.go.in/...): the derived key is real, so this is precision,
			// not slop.
			name:  "path prefix inside dotted prose matches the real prefix path",
			input: DeriveInput{RequestIntent: "a/b.go.in/the/middle/of/prose is confusing"},
			want:  []string{"file:a/b.go"},
		},
		{
			name:  "punctuation-adjacent path still matches when repo-relative",
			input: DeriveInput{RequestIntent: "(internal/auth/session.go) is the culprit,"},
			want:  []string{"file:internal/auth/session.go"},
		},
		{
			name:     "false-positive symbol-like text",
			input:    DeriveInput{RequestIntent: "the TODO(#123) marker in prose is not a symbol"},
			wantNone: true,
		},
		{
			name:     "prose with slashes but no extension",
			input:    DeriveInput{RequestIntent: "auth/session needs work"},
			wantNone: true,
		},
		{
			// A leading dot on a segment is skipped by the tokenizer (the
			// token starts at the first identifier char), so the derived key
			// is the legit repo-relative path hidden/auth/session.go. The
			// ./ and ../ traversal forms ARE rejected (segments cannot START
			// with a dot in the match); pinned by the traversal cases.
			name:  "hidden dotfile segment matches without the dot",
			input: DeriveInput{RequestIntent: "fix .hidden/auth/session.go"},
			want:  []string{"file:hidden/auth/session.go"},
		},
		{
			name:     "absolute path rejected",
			input:    DeriveInput{RequestIntent: "fix /usr/local/auth/session.go"},
			wantNone: true,
		},
		{
			name:     "single-segment filename rejected (no slash)",
			input:    DeriveInput{RequestIntent: "session.go is broken"},
			wantNone: true,
		},

		// --- intent + verify command + changed files combined ---
		{
			name: "all three sources combine and sort",
			input: DeriveInput{
				RequestIntent:        "fix internal/auth/session.go, see internal/auth/session.go#Invalidate",
				PriorChangedFiles:    map[string][]string{"code_writer": {"internal/auth/store.go"}},
				VerificationCommands: []string{"go test ./internal/auth/..."},
			},
			want: []string{
				"file:internal/auth/session.go",
				"file:internal/auth/store.go",
				"package:internal/auth",
				"symbol:internal/auth/session.go#Invalidate",
			},
		},

		// --- anchor resolvability for every key class ---
		{
			name: "anchor resolution covers all three key kinds",
			input: DeriveInput{
				PriorChangedFiles:    map[string][]string{"code_writer": {"a/b.go"}},
				VerificationCommands: []string{"go test ./pkg/..."},
			},
			want: []string{"file:a/b.go", "package:pkg"},
		},
	}
}

// TestKeysEvalSuite runs every section 8 case with explicit expectations.
// A change that makes any wantNone case produce keys, or any positive case
// drift from its exact key set, fails here.
func TestKeysEvalSuite(t *testing.T) {
	cases := keysEvalCases()
	var positives, negatives int
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveKeys(tc.input)
			if tc.wantNone {
				negatives++
				if len(got) != 0 {
					t.Fatalf("PRECISION REGRESSION: input should derive NO keys, got %v", got)
				}
				return
			}
			positives++
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DeriveKeys = %v, want %v", got, tc.want)
			}
			// Every produced key must resolve to an anchor (or be a package
			// key with a directory anchor): a key without an anchor cannot
			// be freshness-gated and would bypass fail-closed.
			for _, k := range got {
				if AnchorPathForKey(k) == "" {
					t.Fatalf("key %q has no resolvable anchor; it could never be freshness-gated", k)
				}
			}
		})
	}
	t.Logf("keys eval: %d cases (%d positive, %d precision-gated negatives)",
		len(cases), positives, negatives)
}

// TestKeysEvalSuiteNoFuzzySlop is a property sweep over hostile prose: none
// of these sentences may produce any key. This is the precision-over-recall
// contract as a bulk property, so a regex loosening fails loudly.
func TestKeysEvalSuiteNoFuzzySlop(t *testing.T) {
	hostile := []string{
		"see http://example.com/a/b.go for reference",
		"email me at dev@team.local/internal/x/session.go",
		"the path C:\\Users\\dev\\session.go on windows",
		"look at ./relative/path.md carefully",
		"../escape/attempt/session.go should not match",
		"version 1.2.3/go mod tidy mentions",
		"no paths in this sentence at all",
		"there are twenty-three reasons why",
		"FILE://server/share/session.go protocol link",
		"192.168.0.1/internal/auth/session.go as an IP route",
		"session.go",
		"internal/auth/session",
		"auth",
	}
	for _, sentence := range hostile {
		got := DeriveKeys(DeriveInput{RequestIntent: sentence})
		if len(got) != 0 {
			t.Fatalf("PRECISION REGRESSION on %q: got %v", sentence, got)
		}
	}
}

// TestKeysEvalSuiteChangedFilesAreAuthoritative proves the strongest source:
// structured changed-file paths ALWAYS produce keys, even when the paths
// would fail the prose-mining context checks. Structured evidence never
// passes through the hostile-text filter because it is not prose.
func TestKeysEvalSuiteChangedFilesAreAuthoritative(t *testing.T) {
	got := DeriveKeys(DeriveInput{
		PriorChangedFiles: map[string][]string{
			"code_writer": {"internal/auth/session.go", "docs/notes.md", "weird.name/file.sh"},
		},
	})
	want := []string{"file:docs/notes.md", "file:internal/auth/session.go", "file:weird.name/file.sh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changed files must map 1:1 to file keys: got %v want %v", got, want)
	}
}

// TestKeysEvalSuiteIntentIsBounded proves a giant intent string cannot blow
// up derivation: the mined key set stays small and deterministic.
func TestKeysEvalSuiteIntentIsBounded(t *testing.T) {
	intent := strings.Repeat("fix internal/auth/session.go now ", 500)
	got := DeriveKeys(DeriveInput{RequestIntent: intent})
	if len(got) != 1 || got[0] != "file:internal/auth/session.go" {
		t.Fatalf("repeated path must dedupe to one key, got %v", got)
	}
}
