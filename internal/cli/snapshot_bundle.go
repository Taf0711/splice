package cli

// Work package A (A4): the immutable snapshot bundle and natural-capture
// provenance. One bundle per experiment holds the verified tree identity
// plus the ACTUAL runtime capture set exported through the sidecar
// (export/import, never reconstruction). All treatment comparisons in the
// experiment reuse the same bundle. Reconstruction stays a diagnostic
// mode: it is explicitly labeled on the seed set and must reach the row,
// so a reconstructed capture can never pass as natural.
//
// Honesty rules:
//   - a missing capture export FAILS natural-capture setup instead of
//     silently reconstructing (the caller decides whether to fall back to
//     the labeled diagnostic path);
//   - producer identity (run id, revisions, claims, anchors, evidence,
//     content digests) is preserved across import; only project identity
//     is remapped;
//   - a record can be replayed AND reconstructed: separate dimensions.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Taf0711/splice/internal/memd"
)

// snapshotBundleSchemaVersion pins the bundle file shape.
const snapshotBundleSchemaVersion = "splice.snapshot-bundle/v1"

// snapshotBundle is the immutable artifact shared by every compared arm of
// one experiment: the verified tree identity plus the actual runtime
// capture payload exported from the sidecar.
type snapshotBundle struct {
	Schema string `json:"schema"`

	// Verified tree identity.
	ProducerRunID string `json:"producer_run_id"`
	StartRevision string `json:"start_revision"`
	Commit        string `json:"commit"`
	Tree          string `json:"tree"`

	// CaptureOrigin is where the payload came from: natural (the producer
	// run's own persisted captures, exported) or reconstructed (the
	// deterministic rebuild from the verified tree, a labeled diagnostic).
	CaptureOrigin string `json:"capture_origin"`
	// CaptureOriginNote names WHY a natural export fell back to
	// reconstruction (empty on the natural path).
	CaptureOriginNote string `json:"capture_origin_note,omitempty"`

	// The capture payload: complete exported nodes. On the reconstructed
	// path these are the deterministic rebuild's nodes with Reconstructed
	// flagging the origin; on the natural path they are the sidecar's
	// verbatim export.
	Nodes []memd.ExportedCaptureNode `json:"nodes,omitempty"`
	// CaptureDigest is the sha256 over the canonical JSON of Nodes, so an
	// import can prove byte-identity of what it replayed.
	CaptureDigest string `json:"capture_digest,omitempty"`

	// Provenance.
	VerifyCommand string `json:"verify_command,omitempty"`
}

// bundleDigest returns the sha256 of the bundle's capture payload in
// canonical (marshal) form. A nil node set digests as the empty set, and
// the digest of an empty capture set is defined (never empty string), so a
// "no captures" bundle is still provably identical to itself.
func bundleDigest(nodes []memd.ExportedCaptureNode) string {
	data, err := json.Marshal(nodes)
	if err != nil {
		// Marshal of this shape cannot fail; a failure means a
		// programming error and must not silently produce "".
		panic(fmt.Sprintf("snapshot bundle: marshal capture payload: %v", err))
	}
	return sha256Hex(data)
}

// exportSnapshotBundle writes the bundle file into dir. The bundle file is
// the artifact every compared arm reads; a write failure is an error (the
// campaign's shared-input invariant depends on the file existing).
func exportSnapshotBundle(dir string, bundle snapshotBundle) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create snapshot bundle dir: %w", err)
	}
	bundle.Schema = snapshotBundleSchemaVersion
	bundle.CaptureDigest = bundleDigest(bundle.Nodes)
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot bundle: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "snapshot-bundle.json"), append(data, '\n'), 0o644)
}

// importSnapshotBundle reads a bundle file back and verifies its capture
// digest. A digest mismatch is a loud error: the bundle is the shared
// input of every compared arm, and replaying tampered captures would
// measure a different experiment than declared.
func importSnapshotBundle(path string) (snapshotBundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return snapshotBundle{}, fmt.Errorf("read snapshot bundle: %w", err)
	}
	var bundle snapshotBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return snapshotBundle{}, fmt.Errorf("parse snapshot bundle: %w", err)
	}
	if bundle.Schema != snapshotBundleSchemaVersion {
		return snapshotBundle{}, fmt.Errorf("snapshot bundle schema %q, want %q", bundle.Schema, snapshotBundleSchemaVersion)
	}
	if digest := bundleDigest(bundle.Nodes); digest != bundle.CaptureDigest {
		return snapshotBundle{}, fmt.Errorf("snapshot bundle capture digest mismatch: file says %s, content digests %s", bundle.CaptureDigest, digest)
	}
	return bundle, nil
}

// exportNaturalCaptureSet exports the producer run's ACTUAL capture set
// from the sidecar. It fails (does not reconstruct) when the sidecar has
// no capture set for the run at the revision: a missing natural capture
// must fail natural-capture setup loudly, never silently fall back to the
// deterministic reconstruction path.
func exportNaturalCaptureSet(ctx context.Context, client *memd.Client, projectPath, revision, producerRunID string) ([]memd.ExportedCaptureNode, error) {
	if client == nil {
		return nil, fmt.Errorf("export natural capture set: memory sidecar unavailable")
	}
	nodes, err := client.ExportCaptureSet(ctx, projectPath, revision, producerRunID)
	if err != nil {
		return nil, fmt.Errorf("export capture set for run %s: %w", producerRunID, err)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("export natural capture set: run %s captured nothing at revision %s in %s; natural-capture setup requires an actual capture set (reconstruction is a labeled diagnostic mode, not a fallback)",
			producerRunID, revision, projectPath)
	}
	return nodes, nil
}

// importSnapshotCaptureSet replays a bundle's exported nodes into the
// target project via the sidecar import endpoint. Project identity is
// remapped by the sidecar; producer identity rides verbatim.
func importSnapshotCaptureSet(ctx context.Context, client *memd.Client, targetProject string, bundle snapshotBundle) error {
	if client == nil {
		return fmt.Errorf("import snapshot capture set: memory sidecar unavailable")
	}
	if len(bundle.Nodes) == 0 {
		return fmt.Errorf("import snapshot capture set: bundle for run %s carries no captures", bundle.ProducerRunID)
	}
	imported, err := client.ImportCaptureSet(ctx, targetProject, bundle.Nodes)
	if err != nil {
		return fmt.Errorf("import snapshot capture set: %w", err)
	}
	if imported != int64(len(bundle.Nodes)) {
		return fmt.Errorf("import snapshot capture set: imported %d of %d nodes", imported, len(bundle.Nodes))
	}
	return nil
}
