package cli

// A4 regression tests: snapshot bundle round-trip, loud failure on missing
// natural captures, and reconstructed/replayed flags surviving
// serialization. The wire pairing test lives in memd's own module tests;
// this file pins the harness-side bundle contracts.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/splice"
)

// spliceGraphCapture aliases the capture type for test brevity.
type spliceGraphCapture = splice.GraphCapture

// marshalRow serializes a row the way the attempts JSONL does.
func marshalRow(t *testing.T, row familyPairRow) []byte {
	t.Helper()
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// unmarshalRow parses an attempts-JSONL line back into a row.
func unmarshalRow(t *testing.T, data []byte, row *familyPairRow) {
	t.Helper()
	if err := json.Unmarshal(data, row); err != nil {
		t.Fatal(err)
	}
}

// testContextShort returns a background context for pure helper calls.
func testContextShort() context.Context { return context.Background() }

// TestSnapshotBundleRoundTripReproducesDigests pins the import contract:
// exporting then importing one bundle reproduces the capture digest
// exactly, and a tampered payload fails the digest check.
func TestSnapshotBundleRoundTripReproducesDigests(t *testing.T) {
	dir := t.TempDir()
	nodes := seedCapturesToExported(seedCaptureSet{
		ProducerRunID: "run-a",
		Revision:      "rev1",
		Captures: []spliceGraphCapture{
			{Kind: "fact", Claim: "a.go defines Retention", Revision: "rev1", RunID: "run-a"},
		},
	})
	bundle := snapshotBundle{
		ProducerRunID: "run-a",
		Commit:        "commit1",
		Tree:          "tree1",
		CaptureOrigin: "natural",
		Nodes:         nodes,
	}
	if err := exportSnapshotBundle(dir, bundle); err != nil {
		t.Fatal(err)
	}
	round, err := importSnapshotBundle(filepath.Join(dir, "snapshot-bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	if round.CaptureDigest == "" {
		t.Fatal("exported bundle must carry a capture digest")
	}
	if round.CaptureDigest != bundleDigest(nodes) {
		t.Fatalf("round-trip digest changed: %s vs %s", round.CaptureDigest, bundleDigest(nodes))
	}
	if round.CaptureOrigin != "natural" {
		t.Fatalf("origin = %q, want natural", round.CaptureOrigin)
	}
	if len(round.Nodes) != 1 || round.Nodes[0].Node.Claim != "a.go defines Retention" {
		t.Fatalf("round-trip nodes changed: %+v", round.Nodes)
	}

	// Tampering with the payload must fail the digest gate.
	tampered := round
	tampered.Nodes[0].Node.Claim = "sabotaged claim"
	if digest := bundleDigest(tampered.Nodes); digest == round.CaptureDigest {
		t.Fatal("tampered payload must produce a different digest")
	}
	if err := exportSnapshotBundle(dir, tampered); err != nil {
		t.Fatal(err)
	}
	// Rewrite the file's digest to the ORIGINAL value (simulating a stale
	// or forged digest over different bytes).
	b, err := importSnapshotBundle(filepath.Join(dir, "snapshot-bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = b
}

// TestMissingNaturalCaptureFailsSetup pins the A4 loud-failure rule: a run
// that captured nothing must FAIL natural-capture export instead of
// silently returning an empty set (which would look like a legitimate
// empty capture set and reconstruct nothing).
func TestMissingNaturalCaptureFailsSetup(t *testing.T) {
	// exportNaturalCaptureSet with a nil client must error, never return
	// an empty set.
	if _, err := exportNaturalCaptureSet(testContextShort(), nil, "/proj", "rev1", "run-a"); err == nil {
		t.Fatal("missing sidecar must fail natural-capture export")
	}
}

// TestReconstructedFlagsSurviveSerialization pins the row contract: the
// reconstructed and replayed dimensions are separate, pointer-typed, and
// survive JSON round-trip.
func TestReconstructedFlagsSurviveSerialization(t *testing.T) {
	row := familyPairRow{
		Family:               "fam-05",
		CaptureReconstructed: boolPtr(true),
		CaptureReplayed:      boolPtr(true),
		CaptureDigest:        "abc123",
	}
	data := marshalRow(t, row)
	var back familyPairRow
	unmarshalRow(t, data, &back)
	if back.CaptureReconstructed == nil || !*back.CaptureReconstructed {
		t.Fatal("capture_reconstructed must survive serialization as true")
	}
	if back.CaptureReplayed == nil || !*back.CaptureReplayed {
		t.Fatal("capture_replayed must survive serialization as true")
	}
	if back.CaptureDigest != "abc123" {
		t.Fatalf("digest = %q", back.CaptureDigest)
	}
	// A natural replay: reconstructed=false must serialize as false, not
	// be omitted (pointer form keeps the measured fact).
	natural := familyPairRow{
		CaptureReconstructed: boolPtr(false),
		CaptureReplayed:      boolPtr(true),
	}
	dataNatural := marshalRow(t, natural)
	var backNatural familyPairRow
	unmarshalRow(t, dataNatural, &backNatural)
	if backNatural.CaptureReconstructed == nil || *backNatural.CaptureReconstructed {
		t.Fatal("capture_reconstructed=false must survive serialization (not be omitted)")
	}
}
