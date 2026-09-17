package cli

// ADDENDUM 3 (option 1) regression: the pre-B match-matrix gate. The TTL pair
// failed because Task A's frozen capture carried no record that spoke of any
// need Task B derived. These tests pin the gate's code path: needs are derived
// against the frozen A tree, every bundle record is tested with the E4
// predicate, and an empty matrix fails loud before any B spend.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice"
)

const fam05TargetIntent = "# Target: fam-05-handler-error-mapping\n\n" +
	"New admin endpoints must map their store errors using the SAME table as " +
	"the /session handler. Refactor mapStoreError in main.go if needed and use " +
	"it from any new admin handlers you add. The mapping table must stay the " +
	"single source of truth for store-error-to-status translation.\n"

// fam05MatchWorkspace writes the frozen A tree: main.go declares the
// mapStoreError table the target task names.
func fam05MatchWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	main := `package main

import (
	"errors"
	"net/http"
)

var ErrNotFound = errors.New("not found")

func mapStoreError(err error) int {
	if errors.Is(err, ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func sessionHandler() {}
func main() {}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func matchNode(t *testing.T, rec *splice.ReuseRecord, claim string) memd.ExportedCaptureNode {
	t.Helper()
	var metadataJSON *string
	if rec != nil {
		data, err := json.Marshal(map[string]any{"reuse_record": rec})
		if err != nil {
			t.Fatal(err)
		}
		s := string(data)
		metadataJSON = &s
	}
	return memd.ExportedCaptureNode{
		Node: memd.GraphNode{
			Kind: "fact", Claim: claim, Status: "active",
			SourceRunID: strPtr("run-snap"), MetadataJSON: metadataJSON,
		},
		ClaimHash: "hash-" + claim,
	}
}

func TestBuildMatchMatrixMatchesNamedOperation(t *testing.T) {
	dir := fam05MatchWorkspace(t)
	matching := &splice.ReuseRecord{
		SchemaVersion: splice.ReuseRecordSchemaVersion, Kind: "fact", Identity: "id-1",
		AnsweredNeed: "locate:main.go#mapStoreError",
		Conclusion:   "main.go defines mapStoreError; verified",
		Supporting:   []splice.SourceRef{{Path: "main.go", Symbol: "mapStoreError", Digest: "d1"}},
		ProducerRun:  "run-snap", CaptureOrigin: splice.CaptureOriginRuntime,
		WorktreeIdentity: "rev-1", VerificationStatus: splice.VerificationStatusPassed, Status: "active",
	}
	other := &splice.ReuseRecord{
		SchemaVersion: splice.ReuseRecordSchemaVersion, Kind: "fact", Identity: "id-2",
		AnsweredNeed: "locate:session.go#ResetPassword",
		Conclusion:   "session.go defines ResetPassword; verified",
		Supporting:   []splice.SourceRef{{Path: "session.go", Symbol: "ResetPassword", Digest: "d2"}},
		ProducerRun:  "run-snap", CaptureOrigin: splice.CaptureOriginRuntime,
		WorktreeIdentity: "rev-1", VerificationStatus: splice.VerificationStatusPassed, Status: "active",
	}
	nodes := []memd.ExportedCaptureNode{
		matchNode(t, matching, "main.go defines mapStoreError"),
		matchNode(t, other, "session.go defines ResetPassword"),
		matchNode(t, nil, "untyped hint"),
	}
	matrix := buildMatchMatrix(fam05TargetIntent, dir, nodes)

	var sawMapStoreError bool
	for _, need := range matrix.Needs {
		if strings.Contains(need.Subject, "mapStoreError") && need.Kind == splice.NeedLocateNamedOperation && need.Required {
			sawMapStoreError = true
		}
	}
	if !sawMapStoreError {
		t.Fatalf("derived needs %+v do not include a required locate need for mapStoreError", matrix.Needs)
	}
	if matrix.MatchedRecords != 1 {
		t.Fatalf("matched records = %d, want exactly the mapStoreError record", matrix.MatchedRecords)
	}
	if len(matrix.Records) != 3 {
		t.Fatalf("records = %d, want 3 (including the untyped hint)", len(matrix.Records))
	}
	if got := matrix.Records[2].Reason; !strings.Contains(got, "hint") {
		t.Fatalf("untyped node reason = %q, want a hint reason", got)
	}
	if got := matrix.Records[1].Reason; got == "" {
		t.Fatalf("unmatched record %s carries no reason", matrix.Records[1].ClaimHash)
	}
}

func TestMatchMatrixGateStopsOnEmptyMatrix(t *testing.T) {
	dir := fam05MatchWorkspace(t)
	other := &splice.ReuseRecord{
		SchemaVersion: splice.ReuseRecordSchemaVersion, Kind: "fact", Identity: "id-2",
		AnsweredNeed: "locate:session.go#ResetPassword",
		Conclusion:   "session.go defines ResetPassword; verified",
		Supporting:   []splice.SourceRef{{Path: "session.go", Symbol: "ResetPassword", Digest: "d2"}},
		ProducerRun:  "run-snap", CaptureOrigin: splice.CaptureOriginRuntime,
		WorktreeIdentity: "rev-1", VerificationStatus: splice.VerificationStatusPassed, Status: "active",
	}
	outDir := t.TempDir()
	var stderr bytes.Buffer
	matrix, err := runMatchMatrixGate(&stderr, mvpEvalOptions{OutDir: outDir},
		mvpFamilyEntry{ID: "fam-05-handler-error-mapping", TargetTask: fam05TargetIntent},
		"snap-1", dir, []memd.ExportedCaptureNode{matchNode(t, other, "session")})
	if err == nil {
		t.Fatalf("gate passed on a record that matches no derived need; matrix=%+v", matrix)
	}
	if !strings.Contains(err.Error(), "match matrix gate") {
		t.Fatalf("gate error = %v, want a match matrix gate error", err)
	}
	written := filepath.Join(outDir, "snapshots", "snap-1", "match-matrix.json")
	data, rerr := os.ReadFile(written)
	if rerr != nil {
		t.Fatalf("match matrix was not recorded on the stop path: %v", rerr)
	}
	var recorded matchMatrix
	if uerr := json.Unmarshal(data, &recorded); uerr != nil {
		t.Fatalf("decode recorded matrix: %v", uerr)
	}
	if recorded.MatchedRecords != 0 || recorded.Family != "fam-05-handler-error-mapping" {
		t.Fatalf("recorded matrix = %+v, want zero matched for the family", recorded)
	}
	if !strings.Contains(stderr.String(), "match matrix for fam-05-handler-error-mapping") {
		t.Fatalf("stop path did not print the matrix to stderr: %q", stderr.String())
	}
}

// TestFam05PairManifestResolves keeps the wired fam-05 campaign pair honest:
// the manifest must parse, the fixture must exist relative to the taskset, and
// both verifier files must be present and non-empty. This is the offline
// resolution the paid launcher performs before any provider request.
func TestFam05PairManifestResolves(t *testing.T) {
	manifestDir := filepath.Join("..", "..", "tests", "evals", "cognition-families")
	data, err := os.ReadFile(filepath.Join(manifestDir, "fam-05-pair.json"))
	if err != nil {
		t.Fatalf("read fam-05 pair manifest: %v", err)
	}
	var manifest mvpFamilyManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse fam-05 pair manifest: %v", err)
	}
	if len(manifest.Families) != 1 || manifest.Families[0].ID != "fam-05-handler-error-mapping" {
		t.Fatalf("manifest families = %+v, want the single fam-05 pair", manifest.Families)
	}
	fixture := filepath.Join(manifestDir, manifest.Fixture)
	if info, err := os.Stat(fixture); err != nil || !info.IsDir() {
		t.Fatalf("fixture %s does not resolve to a directory: %v", fixture, err)
	}
	family := manifest.Families[0]
	for _, name := range []string{family.PrecursorCheckFile, family.TargetCheckFile} {
		body, err := os.ReadFile(filepath.Join(manifestDir, name))
		if err != nil {
			t.Fatalf("read verifier %s: %v", name, err)
		}
		if len(strings.TrimSpace(string(body))) == 0 {
			t.Fatalf("verifier %s is empty", name)
		}
	}
}
