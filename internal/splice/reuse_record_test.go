package splice

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/memd"
)

// e2VerifiedVer returns the verification observation of a run whose test
// stage executed and passed.
func e2VerifiedVer() captureVerification {
	return captureVerification{
		TestStageRan:      true,
		TestsExecuted:     6,
		TestsFailed:       0,
		AcceptanceTotal:   1,
		AcceptancePassed:  1,
		TestCommand:       "go test ./...",
		EnvironmentStdLib: true,
		ObservedResult:    "executed 6 test(s), 0 failed",
	}
}

func TestCaptureContractCompletedWithoutExecutedChecksIsNotVerified(t *testing.T) {
	dir := t.TempDir()
	// Completed status, zero executed checks: the trace says completed but
	// nothing executed. Legacy signature (zero captureVerification).
	captures := captureFromVerifiedRun(dir, "completed", []string{"a.go"}, "go test ./...", "rev1", "run-1")
	if len(captures) == 0 {
		t.Fatal("structural captures should still form")
	}
	for _, c := range captures {
		if c.VerificationStatus != VerificationStatusUnverified {
			t.Errorf("capture status = %q, want unverified", c.VerificationStatus)
		}
		rec := buildReuseRecord(c)
		if rec == nil {
			// procedure with observed result empty stays a hint: correct.
			continue
		}
		if rec.VerificationStatus == VerificationStatusPassed {
			t.Errorf("record passed verification with zero executed checks: %+v", rec)
		}
	}

	// Explicit zero-observation capture with runtime origin: still not
	// verified (a completed status is not executed evidence).
	prov := captureFromVerifiedRunVerified(dir, "completed", nil, "", "rev1", "run-1", captureVerification{}, CaptureOriginRuntime)
	for _, c := range prov {
		if c.VerificationStatus == VerificationStatusPassed {
			t.Errorf("provisional-less run labeled passed: %+v", c)
		}
	}
}

func TestCaptureContractRequiresExecutedAndPassed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	captures := captureFromVerifiedRunVerified(dir, "completed", []string{"a.go"}, "go test ./...", "rev1", "run-1", e2VerifiedVer(), CaptureOriginRuntime)
	if len(captures) == 0 {
		t.Fatal("verified run produced no captures")
	}
	for _, c := range captures {
		if c.VerificationStatus != VerificationStatusPassed {
			t.Errorf("%s capture status = %q, want passed", c.Kind, c.VerificationStatus)
		}
	}
	// The procedure record must meet the full floor: actual command,
	// observed result, environment assumption.
	var proc *GraphCapture
	for i := range captures {
		if captures[i].Kind == "procedure" {
			proc = &captures[i]
		}
	}
	if proc == nil {
		t.Fatal("no procedure capture")
	}
	rec := buildReuseRecord(*proc)
	if rec == nil {
		t.Fatal("procedure capture failed the record floor")
	}
	if rec.TestCommand == "" || rec.ObservedResult == "" || rec.EnvironmentAssumed == "" {
		t.Errorf("procedure record missing command/result/environment: %+v", rec)
	}
	if rec.Identity == "" || rec.ContentVersion == "" || rec.WorktreeIdentity != "rev1" {
		t.Errorf("record identity/provenance incomplete: %+v", rec)
	}
}

func TestReuseRecordFloorKeepsInsufficientNodesHints(t *testing.T) {
	// A procedure without observed result cannot become a record.
	c := GraphCapture{
		Kind: "procedure", Claim: "Verification passes with: go test ./...",
		Project: "/p", RunID: "r1", Revision: "rev",
		Anchors:     []memd.GraphAnchor{{Kind: "test", Value: "go test ./..."}},
		TestCommand: "go test ./...",
	}
	if rec := buildReuseRecord(c); rec != nil {
		t.Fatalf("procedure without observed result admitted: %+v", rec)
	}
	// A failure without a fingerprint cannot become a record.
	f := GraphCapture{
		Kind: "failure", Claim: "the retry loop dropped events",
		Project: "/p", RunID: "r1", Revision: "rev",
		Anchors: []memd.GraphAnchor{{Kind: "file", Value: "a.go"}},
	}
	if rec := buildReuseRecord(f); rec != nil {
		t.Fatalf("failure without fingerprint admitted: %+v", rec)
	}
	// Adding the fingerprint admits it.
	f.FailureFingerprint = "sha:abc"
	if rec := buildReuseRecord(f); rec == nil {
		t.Fatal("failure with fingerprint rejected")
	}
}

func TestLegacyNodeWithoutRecordIsHint(t *testing.T) {
	if rec := parseReuseRecord(nil); rec != nil {
		t.Fatal("nil metadata parsed as a record")
	}
	if rec := parseReuseRecord(map[string]any{"other": 1}); rec != nil {
		t.Fatal("metadata without payload parsed as a record")
	}
	// An older schema version stays a hint.
	old := map[string]any{"reuse_record": map[string]any{"schema_version": 0, "kind": "fact"}}
	if rec := parseReuseRecord(old); rec != nil {
		t.Fatalf("older schema parsed as a record: %+v", rec)
	}
}

func TestChangedContentUnderSameIdentityIsNewVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n// v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := captureFromVerifiedRunVerified(dir, "completed", []string{"a.go"}, "go test ./...", "rev1", "run-1", e2VerifiedVer(), CaptureOriginRuntime)
	rec1 := buildReuseRecord(first[len(first)-1])
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n// v2 changed content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := captureFromVerifiedRunVerified(dir, "completed", []string{"a.go"}, "go test ./...", "rev2", "run-2", e2VerifiedVer(), CaptureOriginRuntime)
	rec2 := buildReuseRecord(second[len(second)-1])
	if rec1.Identity != rec2.Identity {
		t.Fatalf("identities differ: %q vs %q", rec1.Identity, rec2.Identity)
	}
	if rec1.ContentVersion == rec2.ContentVersion {
		t.Fatal("changed content under same identity kept the same content version")
	}
}

func TestCaptureDigestsReflectWorktreeBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	captures := captureFromVerifiedRunVerified(dir, "completed", []string{"a.go"}, "go test ./...", "rev1", "run-1", e2VerifiedVer(), CaptureOriginRuntime)
	if len(captures) == 0 || captures[0].FileDigests["a.go"] == "" {
		t.Fatalf("digest manifest missing: %+v", captures)
	}
	// Different worktree bytes under the SAME project root produce a
	// different digest: the root alone never certifies the source.
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n// dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	again := captureFromVerifiedRunVerified(dir, "completed", []string{"a.go"}, "go test ./...", "rev1", "run-2", e2VerifiedVer(), CaptureOriginRuntime)
	if again[0].FileDigests["a.go"] == captures[0].FileDigests["a.go"] {
		t.Fatal("dirty bytes hashed identically to verified bytes")
	}
}

func TestPersistedRecordSurvivesSidecarRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake, client := newFakeSidecar(t)
	captures := captureFromVerifiedRunVerified(dir, "completed", []string{"a.go"}, "go test ./...", "rev1", "run-1", e2VerifiedVer(), CaptureOriginRuntime)
	if len(captures) == 0 {
		t.Fatal("no captures")
	}
	var id int64
	for _, c := range captures {
		var err error
		id, err = persistGraphCapture(context.Background(), client, c)
		if err != nil {
			t.Fatalf("persist: %v", err)
		}
	}
	if id < 1 {
		t.Fatal("node id not positive")
	}
	node, ok := fake.nodes[id]
	if !ok {
		t.Fatalf("node %d not found after persist", id)
	}
	var meta map[string]any
	if node.MetadataJSON == nil {
		t.Fatal("persisted node carries no metadata: restart would degrade to hints")
	}
	if err := json.Unmarshal([]byte(*node.MetadataJSON), &meta); err != nil {
		t.Fatalf("metadata undecodable: %v", err)
	}
	rec := parseReuseRecord(meta)
	if rec == nil {
		t.Fatal("persisted node lost its reuse record: restart would degrade to hints")
	}
	if rec.SchemaVersion != ReuseRecordSchemaVersion || rec.Kind != "fact" || rec.ProducerRun != "run-1" {
		t.Errorf("record fields wrong: %+v", rec)
	}
	if len(rec.Supporting) == 0 || rec.Supporting[0].Digest == "" {
		t.Errorf("record missing supporting digest: %+v", rec.Supporting)
	}
}
