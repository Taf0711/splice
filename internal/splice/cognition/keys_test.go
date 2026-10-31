package cognition

import (
	"reflect"
	"testing"
)

func TestDeriveKeys_ChangedFiles(t *testing.T) {
	got := DeriveKeys(DeriveInput{
		PriorChangedFiles: map[string][]string{
			"test_runner": {"internal/auth/session_test.go", "internal/auth/session.go"},
			"code_writer": {"internal/auth/session.go"},
		},
	})
	want := []string{
		"file:internal/auth/session.go",
		"file:internal/auth/session_test.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveKeys = %v, want %v", got, want)
	}
}

func TestDeriveKeys_RequestIntentStrictPaths(t *testing.T) {
	got := DeriveKeys(DeriveInput{
		RequestIntent: "Fix session invalidation in internal/auth/session.go and update internal/auth/session_test.go",
	})
	want := []string{
		"file:internal/auth/session.go",
		"file:internal/auth/session_test.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveKeys = %v, want %v", got, want)
	}
}

func TestDeriveKeys_RequestIntentRejectsFuzzyAndUrls(t *testing.T) {
	got := DeriveKeys(DeriveInput{
		RequestIntent: "the user seems to be talking about auth. see https://example.com/x/y.go and fix it",
	})
	if len(got) != 0 {
		t.Fatalf("DeriveKeys = %v, want no keys (fuzzy prose + URL must not match)", got)
	}
}

func TestDeriveKeys_Symbols(t *testing.T) {
	got := DeriveKeys(DeriveInput{
		RequestIntent: "ResetPassword in internal/auth/session.go#ResetPassword misbehaves",
	})
	// The path before # also matches the file-path regex, so both the
	// containing file and the specific symbol are emitted (both correct).
	want := []string{
		"file:internal/auth/session.go",
		"symbol:internal/auth/session.go#ResetPassword",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveKeys = %v, want %v", got, want)
	}
}

func TestDeriveKeys_PackageTargets(t *testing.T) {
	got := DeriveKeys(DeriveInput{
		VerificationCommands: []string{"go test ./internal/auth/... ./internal/session/...", "go vet ./..."},
	})
	want := []string{
		"package:internal/auth",
		"package:internal/session",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveKeys = %v, want %v", got, want)
	}
}

func TestDeriveKeys_DeduplicatesAndSorts(t *testing.T) {
	got := DeriveKeys(DeriveInput{
		RequestIntent: "fix internal/auth/session.go and internal/auth/session.go again",
		PriorChangedFiles: map[string][]string{
			"code_writer": {"internal/auth/session.go"},
		},
	})
	want := []string{"file:internal/auth/session.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveKeys = %v, want %v", got, want)
	}
}

func TestDeriveKeys_EmptyInput(t *testing.T) {
	got := DeriveKeys(DeriveInput{})
	if len(got) != 0 {
		t.Fatalf("DeriveKeys(empty) = %v, want none", got)
	}
}

func TestDeriveKeys_RejectsAbsoluteAndDotPaths(t *testing.T) {
	got := DeriveKeys(DeriveInput{
		RequestIntent: "/home/user/proj/internal/auth.go ./internal/auth.go ../shared/auth.go",
	})
	if len(got) != 0 {
		t.Fatalf("DeriveKeys = %v, want none (absolute and dot-relative paths are not repo-relative)", got)
	}
}

func TestDeriveKeys_RejectsNonSourceExtensions(t *testing.T) {
	got := DeriveKeys(DeriveInput{
		RequestIntent: "check internal/auth/README and internal/auth/notes.txt",
	})
	if len(got) != 0 {
		t.Fatalf("DeriveKeys = %v, want none (no source extension)", got)
	}
}

func TestAnchorPathForKey(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"file:internal/auth/session.go", "internal/auth/session.go"},
		{"symbol:internal/auth/session.go#ResetPassword", "internal/auth/session.go"},
		{"package:internal/auth", "internal/auth"},
		{"", ""},
		{"file:", ""},
		{"symbol:", ""},
		{"test:TestSessionInvalidation", ""},
	}
	for _, tc := range cases {
		if got := AnchorPathForKey(tc.key); got != tc.want {
			t.Fatalf("AnchorPathForKey(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// TestDeriveKeys_BareSourceFilename is the fam-05 delivery regression: the
// task text names a root-level file without a directory ("in main.go") and
// the natural capture was anchored on exactly that file. The delivery chain
// derived zero keys from this intent, so no discovery question was asked
// and the matched record was never delivered.
func TestDeriveKeys_BareSourceFilename(t *testing.T) {
	intent := "New admin endpoints must map their store errors using the SAME table as the /session handler. Refactor mapStoreError in main.go if needed and use it from any new admin handlers you add. The mapping table must stay the single source of truth for store-error-to-status translation."
	got := DeriveKeys(DeriveInput{RequestIntent: intent})
	want := []string{"file:main.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveKeys = %v, want %v", got, want)
	}
}

func TestDeriveKeys_BareFilenameAdversarial(t *testing.T) {
	cases := []struct {
		name   string
		intent string
		want   []string
	}{
		{
			// The last segment of a multi-segment path is never
			// double-derived as a bare filename.
			name:   "multi-segment path yields one key",
			intent: "update internal/auth/session.go and its tests",
			want:   []string{"file:internal/auth/session.go"},
		},
		{
			// Prose fragments that look extension-shaped but are not
			// source files ("e.g." parses as no file; "go.mod" has no
			// source extension). A token like "a.toml" parses as a real
			// file reference and IS keyed: recall over false alarm.
			name:   "prose lookalikes derive nothing",
			intent: "compare e.g. the config in go.mod first",
			want:   nil,
		},
		{
			// A bare extension with no name is not a file.
			name:   "extension only derives nothing",
			intent: "the go source in .go files",
			want:   nil,
		},
		{
			// Non-source extensions stay unkeyed.
			name:   "non-source extension derives nothing",
			intent: "read the notes in TODO.txt first",
			want:   nil,
		},
		{
			name:   "bare filename after a slash prefix is part of a path",
			intent: "see session/store.go then main.go",
			want:   []string{"file:main.go", "file:session/store.go"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveKeys(DeriveInput{RequestIntent: tc.intent})
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DeriveKeys = %v, want %v", got, tc.want)
			}
		})
	}
}
