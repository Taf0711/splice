package splice

import (
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Identical semantic failures must hash identically.
func TestFailureFingerprintIdenticalSemanticFailureHashesEqual(t *testing.T) {
	first := NewFailureFingerprint(
		FailureKindCompile, "go test ./...", 1,
		"./lookup_table_test.go:45:13: undefined: newSessionStore",
		[]string{"newSessionStore"}, []string{"./lookup_table_test.go"},
	)
	second := NewFailureFingerprint(
		FailureKindCompile, "go   test ./...", 1,
		"  ./lookup_table_test.go:45:99:   undefined:   newSessionStore  ",
		[]string{"newSessionStore"}, []string{"lookup_table_test.go"},
	)
	if first.Hash() != second.Hash() {
		t.Fatalf("hashes differ for identical semantic failures: %s vs %s", first.Hash(), second.Hash())
	}
}

// Volatile detail (temp paths, run ids, timestamps, columns) must normalize
// away so the same failure hashes the same across runs.
func TestFailureFingerprintVolatileChangesKeepHashStable(t *testing.T) {
	base := NewFailureFingerprint(FailureKindTest, "go test ./...", 1,
		"/var/folders/xx/abc/T/sandbox123/store_test.go:12:34: got 3, want 4 run 9f8e7d6c1a2b3d4e at 2026-09-04T12:00:00Z",
		nil, []string{"/tmp/run-550e8400-e29b-41d4-a716-446655440000/store_test.go"})
	changed := NewFailureFingerprint(FailureKindTest, "go test ./...", 1,
		"/tmp/other-run-123e4567-e89b-12d3-a456-426614174000/store_test.go:12:99: got 3, want 4 run 0a1b2c3d4e5f60718 at 2026-09-05T09:30:00.123Z",
		nil, []string{"/private/var/folders/yy/def/W/sandbox999/store_test.go"})
	if base.Hash() != changed.Hash() {
		t.Fatalf("volatile path/run/timestamp changes must not change the hash:\n%s\nvs\n%s", base.Diagnostic, changed.Diagnostic)
	}
}

// Distinguishing content (test name, message) must survive normalization.
func TestFailureFingerprintDifferentFailuresHashDifferently(t *testing.T) {
	first := NewFailureFingerprint(FailureKindTest, "go test ./...", 1, "--- FAIL: TestAdd: got 3, want 4", nil, nil)
	second := NewFailureFingerprint(FailureKindTest, "go test ./...", 1, "--- FAIL: TestSubtract: got 1, want 0", nil, nil)
	if first.Hash() == second.Hash() {
		t.Fatal("different test names and messages must produce different hashes")
	}
	if first.Diagnostic != "--- FAIL: TestAdd: got 3, want 4" {
		t.Fatalf("test name/message normalized away: %q", first.Diagnostic)
	}
}

func TestFailureFingerprintKindInference(t *testing.T) {
	tests := []struct {
		name       string
		diagnostic string
		want       FailureKind
	}{
		{"undefined symbol", "./main.go:12: undefined: NewStore", FailureKindCompile},
		{"build failed", "# example.com/x\nbuild failed", FailureKindCompile},
		{"missing method", "s.Close undefined (type *Store has no field or method Close)", FailureKindCompile},
		{"cannot use", "cannot use c (variable of type *Client) as *Conn", FailureKindCompile},
		{"go test fail marker", "--- FAIL: TestAdd", FailureKindTest},
		{"verifier wording", "verifier rejected the change", FailureKindVerifier},
		{"fallback", "command exited strangely", FailureKindCommand},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InferFailureKind(tt.diagnostic); got != tt.want {
				t.Fatalf("InferFailureKind(%q) = %q, want %q", tt.diagnostic, got, tt.want)
			}
		})
	}
}

func TestFingerprintFromTestResults(t *testing.T) {
	compileResults := schemas.TestRunResults{
		Command:  []string{"go", "test", "-json", "./..."},
		ExitCode: 1,
		Tests: []schemas.TestCaseResult{
			{Name: "build example.com/fixture [example.com/fixture.test]", Status: "errored",
				Message: "./missing.go:3:9: undefined: NotAMethod"},
		},
	}
	compileFingerprint := FingerprintFromTestResults(compileResults)
	if compileFingerprint.Kind != FailureKindCompile {
		t.Fatalf("kind = %q, want compile", compileFingerprint.Kind)
	}
	if len(compileFingerprint.Symbols) != 1 || compileFingerprint.Symbols[0] != "NotAMethod" {
		t.Fatalf("symbols = %v, want [NotAMethod]", compileFingerprint.Symbols)
	}

	testFingerprint := FingerprintFromTestResults(schemas.TestRunResults{
		Command:  []string{"go", "test", "-json", "./..."},
		ExitCode: 1,
		Tests: []schemas.TestCaseResult{
			{Name: "TestAdd", Status: "failed", Message: "got 3, want 4"},
		},
	})
	if testFingerprint.Kind != FailureKindTest {
		t.Fatalf("kind = %q, want test", testFingerprint.Kind)
	}
	if compileFingerprint.Hash() == testFingerprint.Hash() {
		t.Fatal("compile and test fingerprints must differ")
	}

	// Raw-output fallback when no structured entries exist.
	raw := FingerprintFromTestResults(schemas.TestRunResults{
		Command:  []string{"false"},
		ExitCode: 1,
		Stdout:   "panic: boom",
	})
	if raw.Diagnostic == "" {
		t.Fatal("raw output must feed the fingerprint when Tests is empty")
	}
}

func TestNormalizeFailureTextPreservesDistinguishingContent(t *testing.T) {
	got := NormalizeFailureText("/var/folders/x/T/w/audit_test.go:20:34: last audit action = \"delete\", want tombstone (0.05s)")
	want := "<tmp>/audit_test.go:20: last audit action = \"delete\", want tombstone (elapsed)"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}
