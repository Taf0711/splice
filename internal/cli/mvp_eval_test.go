package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestArtifactCapturePreservesVerifierOutput pins that a failed verifier's
// output is preserved per attempt: the JSONL boolean alone cannot explain
// the failure mode.
func TestArtifactCapturePreservesVerifierOutput(t *testing.T) {
	dir := t.TempDir()
	artifactDir := filepath.Join(dir, "artifacts")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate the artifact write path directly: the verifier output must
	// survive on disk with the session id prefix.
	sessionID := "mvp-test-session"
	verifierText := "FAIL demo/internal/session [build failed]\nundefined: RecordDunningNotice\n"
	writeVerifierArtifact(artifactDir, sessionID, []byte(verifierText))
	data, err := os.ReadFile(artifactPath(artifactDir, sessionID, "verifier.txt"))
	if err != nil {
		t.Fatalf("verifier artifact missing: %v", err)
	}
	if !strings.Contains(string(data), "undefined: RecordDunningNotice") {
		t.Fatalf("verifier artifact lost the failure cause: %q", data)
	}
}

// TestTaskIdentityIsExplicit pins that Task A and Task B rows carry an
// explicit task field, never an inferred session-suffix convention.
func TestTaskIdentityIsExplicit(t *testing.T) {
	row := familyPairRow{Family: "f", Task: "B", Arm: "warm"}
	if row.Task != "B" {
		t.Fatalf("task identity lost")
	}
	// A row would carry Task "A"; the summary must select on the field,
	// not on the session id suffix. This test pins the field exists and
	// round-trips through JSON.
	data := `{"family":"f","task":"A","arm":"cold","success":true}`
	var decoded familyPairRow
	if err := json.Unmarshal([]byte(data), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Task != "A" {
		t.Fatalf("task round-trip failed: %q", decoded.Task)
	}
}

// TestSummarizeMvpCountsSetupFailuresSeparately pins that a failed
// precursor is reported as a setup failure and excluded from the target
// analysis, not silently merged into target rows.
func TestSummarizeMvpCountsSetupFailuresSeparately(t *testing.T) {
	manifest := mvpFamilyManifest{Families: []mvpFamilyEntry{{ID: "fam-x"}}}
	rows := []familyPairRow{
		{Family: "fam-x", Task: "B", Arm: "warm", Attempt: 1, InfraStatus: "precursor_failed", Success: false},
		{Family: "fam-x", Task: "B", Arm: "cold", Attempt: 1, Success: true},
	}
	var out strings.Builder
	summarizeMvp(&out, manifest, rows)
	text := out.String()
	if !strings.Contains(text, "setup failures excluded") {
		t.Fatalf("setup failure not reported separately:\n%s", text)
	}
	// The warm arm must show 0 target attempts (the target never ran).
	if !strings.Contains(text, "warm: success 0/0") {
		t.Fatalf("warm arm must report zero executed targets:\n%s", text)
	}
}

func TestVerifierExitFromMarker(t *testing.T) {
	if err := verifierExitFromMarker([]byte("ok\nVERIFIER_EXIT=0\n")); err != nil {
		t.Fatalf("exit 0 rejected: %v", err)
	}
	err := verifierExitFromMarker([]byte("FAIL demo/internal\nVERIFIER_EXIT=1\n"))
	if err == nil || !strings.Contains(err.Error(), "verifier exit 1") {
		t.Fatalf("exit 1 not detected: %v", err)
	}
	if err := verifierExitFromMarker([]byte("no marker here")); err == nil {
		t.Fatal("missing marker must fail loud")
	}
}

func TestArtifactCapturePreservesVerdict(t *testing.T) {
	// The capture path must not flip a failing verifier to success: the
	// success decision comes from the parsed marker.
	out := []byte("build failed\nVERIFIER_EXIT=2\n")
	if err := verifierExitFromMarker(out); err == nil {
		t.Fatal("failing verifier must fail")
	}
	if got := artifactPath("dir", "session", "patch.diff"); got != filepath.Join("dir", "session-patch.diff") {
		t.Fatalf("artifactPath = %q", got)
	}
}
