package cli

// Work package B (F8/B2): the matched-snapshots seed path freezes the
// ACTUAL capture payload of the successful Task A run and REPLAYS the
// persisted captures into each warm project with only the project identity
// remapped. Provenance is never fabricated: the producing run id and the
// verification command Task A actually executed ride the capture set, and a
// missing actual command stays explicitly "unknown" instead of inventing
// one. A retained deterministic-reconstruction path is labeled
// reconstructed in provenance and telemetry.

import (
	"context"
	"fmt"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice"
)

// captureProvenance is the real, externally verified provenance of one Task
// A snapshot run. ProducerRunID is the actual Task A session id (splice exec
// keys the sidecar run identity on the session id, so the F9
// source_run_id filter joins captures to this run). VerifyCommand is the
// verification command Task A actually executed; when it could not be
// recovered the harness records UnknownProvenance instead of a guess.
type captureProvenance struct {
	ProducerRunID string
	VerifyCommand string
}

// unknownProvenanceCommand marks a capture whose executed verification
// command could not be recovered from the run's evidence. It is a value on
// the capture, never a fabricated command.
const unknownProvenanceCommand = "unknown"

// captureProvenanceFromRow reads the actual Task A verification command off
// the completed run's evidence: the verifier script the harness executed for
// Task A (hash and bytes pinned by the row) is the command that decided the
// run's verified status. When no check ran, the provenance stays explicitly
// unknown rather than naming a command Task A never executed.
func captureProvenanceFromRow(snapRow familyPairRow, precursorCheck string) captureProvenance {
	prov := captureProvenance{ProducerRunID: snapRow.SessionID}
	if snapRow.VerifierHash != "" && precursorCheck != "" {
		prov.VerifyCommand = precursorCheck
	}
	return prov
}

// seedCaptureSet is the frozen capture payload of one verified Task A run,
// persisted once per family and replayed per attempt.
type seedCaptureSet struct {
	// ProducerRunID is the actual producing run id carried by every
	// capture, so the F9 CaptureSetIDsForRun filter selects exactly this
	// run's nodes.
	ProducerRunID string
	// Revision is the verified revision the captures anchor at.
	Revision string
	// Captures are the persisted capture payloads (claims, anchors, and
	// evidence are byte-identical across replays; only Project is
	// remapped per target project identity).
	Captures []splice.GraphCapture
	// Reconstructed is true only when the payload came from the
	// deterministic reconstruction path instead of the run's own capture
	// records. Telemetry carries this flag so a replayed lab capture is
	// never mistaken for the producer run's own persisted nodes.
	Reconstructed bool
}

// buildSeedCaptureSet freezes the capture payload of the successful Task A
// run. The producing run id and the verification command come from the
// actual Task A evidence; the changed-file facts are derived from the
// verified snapshot tree (the same deterministic artifacts the verified run
// persisted), and every capture carries the real run id so the F9 filter
// applies.
func buildSeedCaptureSet(snapDir string, prov captureProvenance, changedFiles []string, revision string) seedCaptureSet {
	set := seedCaptureSet{
		ProducerRunID: prov.ProducerRunID,
		Revision:      revision,
		Reconstructed: true,
	}
	if prov.ProducerRunID == "" {
		// Without a producer run id the capture set cannot be qualified
		// by run; fail loud instead of seeding unattributed nodes.
		return set
	}
	command := prov.VerifyCommand
	if command == "" {
		command = unknownProvenanceCommand
	}
	set.Captures = splice.CaptureFromVerifiedRun(snapDir, "completed", changedFiles, command, revision, prov.ProducerRunID)
	// Replace fabricated evidence text with honest provenance: the
	// captured procedure node must not claim a test_run record that never
	// happened when the command is unknown.
	if prov.VerifyCommand == "" {
		for i := range set.Captures {
			if set.Captures[i].Kind != "procedure" {
				continue
			}
			for j := range set.Captures[i].Evidence {
				if set.Captures[i].Evidence[j].Kind == "test_run" {
					set.Captures[i].Evidence[j].Detail = "verification command not recorded by the producing run"
				}
			}
		}
	}
	return set
}

// persistSeedCaptureSet persists the frozen capture set once per family,
// under the SNAPSHOT project identity (the producer run's own project). The
// persisted nodes carry the real run id, so the F9 producer-run filter
// selects them at reanchor time.
func persistSeedCaptureSet(ctx context.Context, client *memd.Client, snapDir string, set seedCaptureSet) error {
	if client == nil {
		return fmt.Errorf("persist seed capture set: memory sidecar unavailable")
	}
	if set.ProducerRunID == "" {
		return fmt.Errorf("persist seed capture set: no producer run id (family snapshot %s)", snapDir)
	}
	for _, c := range set.Captures {
		if _, err := splice.PersistGraphCapture(ctx, client, snapDir, c); err != nil {
			return fmt.Errorf("persist capture %s: %w", c.Kind, err)
		}
	}
	return nil
}

// replaySeedCaptures rematerializes the FROZEN capture payload into the
// warm target's project identity: claims, anchors, and evidence stay
// byte-identical to the persisted snapshot captures, and only the project
// path is remapped. No capture is re-derived from the target workspace per
// attempt, and no model call happens on the seed path.
func replaySeedCaptures(ctx context.Context, client *memd.Client, warmDir string, set seedCaptureSet) error {
	if client == nil {
		return fmt.Errorf("replay seed captures: memory sidecar unavailable")
	}
	if set.ProducerRunID == "" {
		return fmt.Errorf("replay seed captures: no producer run id (warm project %s)", warmDir)
	}
	if len(set.Captures) == 0 {
		return fmt.Errorf("replay seed captures: empty capture set for run %s (warm project %s)", set.ProducerRunID, warmDir)
	}
	for _, c := range set.Captures {
		replay := c
		replay.Project = warmDir
		if _, err := splice.PersistGraphCapture(ctx, client, warmDir, replay); err != nil {
			return fmt.Errorf("replay capture %s: %w", replay.Kind, err)
		}
	}
	return nil
}
