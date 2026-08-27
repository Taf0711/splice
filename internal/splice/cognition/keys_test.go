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
