package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Taf0711/splice/internal/eval"
	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice"
)

// runMvpMatchedSnapshots implements the matched Task B comparison: Task A
// runs ONCE per family (warm arm, memory on, externally verified), the
// verified tree is frozen, and the frozen capture payload of that verified
// run is REPLAYED for every Task B attempt in the warm arm (project
// identity remapped, provenance kept). The cold arm starts from the
// identical committed tree with no cognition.
//
// This isolates the memory/context policy effect on an identical coding
// task. It deliberately does NOT hand-author the cognition: the captures
// carry the actual Task A run id and the verification command the harness
// executed, and every Task B attempt re-asserts the intended snapshot
// commit AND tree AND a clean working tree immediately before the model
// launches (B1). The replayed-capture labeling (Reconstructed) rides the
// seed set and row telemetry, so a replayed capture is never mistaken for
// the producer run's own persisted node.
//
// Lifecycle guarantees (work package A): every run gets a FRESH
// context.WithTimeout inside the loop (the outer cancellation still
// propagates, and each derived context is cancelled when the attempt
// ends), and every completed or skipped row is checkpointed to the
// attempts JSONL immediately, so an interruption retains the most recent
// completed target.
func runMvpMatchedSnapshots(
	ctx context.Context,
	deps appDeps,
	options mvpEvalOptions,
	manifest mvpFamilyManifest,
	manifestDir string,
	fixtureDir string,
	runFunc eval.RunFunc,
	stderr interface{ Write([]byte) (int, error) },
	rows *[]familyPairRow,
) error {
	experimentID := fmt.Sprintf("mvp-matched-%d", time.Now().UnixNano())
	prov := harnessProvenance(options, manifestDir)

	for _, family := range manifest.Families {
		snapshotID := fmt.Sprintf("snap-%s-%d", family.ID, time.Now().UnixNano())

		// ---- Phase 1: run Task A once, in a dedicated snapshot project.
		snapDir, err := os.MkdirTemp("", "splice-mvp-snap-")
		if err != nil {
			return fmt.Errorf("materialize snapshot arm: %w", err)
		}
		cleanup := func() {
			os.RemoveAll(snapDir)
		}
		if err := copyFixtureTree(fixtureDir, snapDir); err != nil {
			cleanup()
			return fmt.Errorf("populate snapshot arm: %w", err)
		}
		if resetErr := resetArmMemory(ctx, snapDir); resetErr != nil {
			cleanup()
			return fmt.Errorf("reset snapshot memory: %w", resetErr)
		}

		snapSession := fmt.Sprintf("mvp-snap-%s-%d", family.ID, time.Now().UnixNano())
		// Fresh timeout per Task A run, cancelled when the run ends.
		snapCtx, snapCancel := context.WithTimeout(ctx, familiesRunTimeout)
		snapStatus, snapErr, snapRow := mvpRunOnceTracked(deps, ctx, snapCtx, runFunc, eval.RunInput{
			SessionID:   snapSession + "-taska",
			Memory:      "on",
			Prompt:      family.PrecursorTask,
			Cwd:         snapDir,
			Check:       precursorChecksFor(manifestDir, family),
			ArtifactDir: mvpArtifactDir(options.OutDir, family.ID, 0, "snapshot", "a"),
		}, experimentID, options, prov, family.ID, 0, "snapshot", "A", options.OutDir)
		snapCancel()
		appendRowWithCheckpoint(rows, options.OutDir, snapRow)
		if ctx.Err() != nil {
			cleanup()
			return fmt.Errorf("interrupted")
		}
		if snapStatus != "success" {
			cleanup()
			// Record the failed setup as a real outcome of the workflow:
			// ONE setup failure row plus the correct number of skipped
			// target slots (Rollouts x 2 arms), each marked skipped, not
			// failed.
			note := "snapshot Task A did not verify; target not run"
			if snapErr != nil {
				note = "snapshot Task A did not verify; target not run: " + truncateForNote(snapErr.Error(), 200)
			}
			setup := "precursor_failed"
			if snapStatus == "timeout" {
				setup = "timeout"
			}
			for _, arm := range []string{"cold", "warm"} {
				for attempt := 1; attempt <= options.Rollouts; attempt++ {
					row := familyPairRow{
						Family: family.ID, Task: "B", Attempt: attempt, Arm: arm,
						ExperimentID: experimentID, PipelineRunID: experimentID,
						SnapshotID: snapshotID, SetupOutcome: setup,
						Executed: false, Treatment: armTreatment(arm),
						WarmSetupValid: &falseValue,
						WarmSetupNote:  note,
						InfraStatus:    "precursor_failed",
					}
					appendRowWithCheckpoint(rows, options.OutDir, row)
				}
			}
			fmt.Fprintf(stderr, "family %s: snapshot Task A did not verify; skipping target\n", family.ID)
			continue
		}

		// Commit the verified tree so the snapshot HEAD names the verified
		// bytes (the stage sandbox refuses stash create; see the anchoring
		// spec). Changed files come from porcelain status BEFORE the commit.
		changedFiles := splice.WorktreeChangedFiles(ctx, snapDir)
		preHead := gitHeadCommit(snapDir)
		if _, err := gitCommitAll(snapDir); err != nil {
			cleanup()
			return fmt.Errorf("commit verified snapshot tree: %w", err)
		}
		snapHead := gitHeadCommit(snapDir)
		if snapHead == preHead {
			cleanup()
			return fmt.Errorf("verified snapshot tree produced no commit")
		}

		// Real Task A provenance (F8/B2): the producer run id is the
		// Task A session id and the verification command is the precursor
		// check the harness actually executed. A failed Task A never
		// reaches this point (the status gate above), so seeding is gated
		// on external verification.
		prov := captureProvenanceFromRow(snapRow, precursorChecksFor(manifestDir, family))
		seedSet := buildSeedCaptureSet(snapDir, prov, changedFiles, snapHead)
		if seedSet.ProducerRunID == "" {
			cleanup()
			return fmt.Errorf("seed capture set for family %s: no producer run id", family.ID)
		}

		// Persist the frozen capture set once per family, under the
		// snapshot project identity, with the real run id on every node.
		seedPersisted := false
		if client, err := memd.Resolve(ctx); err == nil && client != nil {
			if err := persistSeedCaptureSet(ctx, client, snapDir, seedSet); err != nil {
				cleanup()
				return fmt.Errorf("persist seed capture set: %w", err)
			}
			seedPersisted = true
			// Reanchor exactly this run's capture set (F9/B3): the
			// producer-run-qualified filter selects only the nodes THIS
			// verified run persisted, never nodes other runs captured.
			captureSet, cerr := client.CaptureSetIDsForRun(ctx, snapDir, preHead, seedSet.ProducerRunID)
			if cerr != nil {
				cleanup()
				return fmt.Errorf("resolve snapshot capture set: %w", cerr)
			}
			if len(captureSet) > 0 {
				if _, rerr := client.ReanchorGraphByIDs(ctx, snapDir, captureSet, preHead, snapHead); rerr != nil {
					cleanup()
					return fmt.Errorf("reanchor snapshot cognition: %w", rerr)
				}
			}
		}

		// The snapshot's tree hash is the per-attempt assertion target
		// (B1): commit and tree are separate facts and separate row
		// fields.
		snapTree := gitTreeHash(snapDir)
		if snapTree == "" {
			cleanup()
			return fmt.Errorf("read snapshot tree hash: git rev-parse HEAD^{tree} failed for %s", snapDir)
		}

		// ---- Phase 2: the warm arm gets its own stable project
		// directory whose bytes are the verified tree. The graph is keyed
		// by project path, and the cold arm must NOT see the seeded
		// cognition.
		warmDir, err := os.MkdirTemp("", "splice-mvp-warm-")
		if err != nil {
			cleanup()
			return fmt.Errorf("materialize warm arm: %w", err)
		}
		coldDir, err := os.MkdirTemp("", "splice-mvp-cold-")
		if err != nil {
			os.RemoveAll(warmDir)
			cleanup()
			return fmt.Errorf("materialize cold arm: %w", err)
		}
		cleanupArms := func() {
			os.RemoveAll(warmDir)
			os.RemoveAll(coldDir)
			cleanup()
		}

		// Materialize both arms from the snapshot commit (identical bytes).
		for _, dir := range []string{warmDir, coldDir} {
			if err := materializeFromCommit(snapDir, snapHead, dir); err != nil {
				cleanupArms()
				return fmt.Errorf("materialize arm from snapshot: %w", err)
			}
		}

		// Seed the warm arm's cognition by REPLAYING the frozen capture
		// payload (B2): the persisted captures of the successful Task A
		// run are rematerialized with only the project identity remapped.
		seedStatus := ""
		if !seedPersisted {
			seedStatus = "seed_skipped_no_sidecar"
		} else if client, err := memd.Resolve(ctx); err == nil && client != nil {
			if err := replaySeedCaptures(ctx, client, warmDir, seedSet); err != nil {
				cleanupArms()
				return fmt.Errorf("seed warm cognition from snapshot: %w", err)
			}
			seedStatus = "replayed"
		} else {
			seedStatus = "seed_skipped_no_sidecar"
		}
		if seedStatus == "seed_skipped_no_sidecar" {
			// Fail loud: a warm attempt without seeded cognition is a
			// cold attempt wearing a warm label, which corrupts the
			// paired measurement.
			cleanupArms()
			return fmt.Errorf("warm arm %s: memory sidecar unavailable; cannot replay snapshot cognition", family.ID)
		}

		// Assert starting state equality for BOTH arms BEFORE any Task B
		// execution (the loop re-asserts per attempt). Commit and tree are
		// verified as separate facts against the snapshot identity.
		for armName, armDir := range map[string]string{"warm": warmDir, "cold": coldDir} {
			if _, aerr := assertCleanSnapshot(armDir, snapHead, snapTree); aerr != nil {
				cleanupArms()
				return fmt.Errorf("initial %s arm snapshot assertion: %w", armName, aerr)
			}
		}

		// ---- Phase 3: Task B attempts on the frozen, identical tree.
		for attempt := 1; attempt <= options.Rollouts; attempt++ {
			for _, arm := range []struct {
				name   string
				dir    string
				memory string
			}{{"cold", coldDir, "off"}, {"warm", warmDir, "on"}} {
				// Reset the arm's sidecar state so attempt N's captures
				// never leak into attempt N+1, then restore the snapshot
				// cognition for the warm arm.
				if resetErr := resetArmMemory(ctx, arm.dir); resetErr != nil {
					cleanupArms()
					return fmt.Errorf("reset %s memory (family %s attempt %d): %w", arm.name, family.ID, attempt, resetErr)
				}
				if arm.name == "warm" {
					if client, err := memd.Resolve(ctx); err == nil && client != nil {
						if err := replaySeedCaptures(ctx, client, warmDir, seedSet); err != nil {
							cleanupArms()
							return fmt.Errorf("re-seed warm cognition (attempt %d): %w", attempt, err)
						}
					} else {
						cleanupArms()
						return fmt.Errorf("re-seed warm cognition (attempt %d): memory sidecar unavailable", attempt)
					}
				}
				// Reset the worktree to the frozen verified tree (Task B's
				// own edits from attempt N-1 must not leak). Rematerialize
				// FIRST, then assert: the previous attempt's files must
				// not survive.
				if err := materializeFromCommit(snapDir, snapHead, arm.dir); err != nil {
					cleanupArms()
					return fmt.Errorf("reset %s worktree (attempt %d): %w", arm.name, attempt, err)
				}

				// ---- Per-attempt clean-snapshot assertion (B1/F7):
				// every Task B gets its own verification of the intended
				// commit AND tree AND a clean index/working tree,
				// immediately BEFORE the model launches. The assertion
				// result lands on the row before the run.
				asserted, assertErr := assertCleanSnapshot(arm.dir, snapHead, snapTree)
				cleanState := "success"
				if assertErr != nil {
					cleanState = "failed"
				}

				if assertErr != nil {
					// A dirty or drifted start state is an infrastructure
					// failure of this attempt, never a model outcome:
					// the model NEVER launches on an unverified start
					// state. Record the assertion result and stop,
					// because proceeding would measure a different
					// experiment than intended.
					row := familyPairRow{
						Family: family.ID, Task: "B", Attempt: attempt, Arm: arm.name,
						ExperimentID: experimentID, PipelineRunID: experimentID,
						SnapshotID: snapshotID, Executed: false,
						Treatment:   armTreatment(arm.name),
						StartCommit: asserted.Commit, StartTree: asserted.Tree,
						CleanStateVerified: cleanState,
						FixtureCommit:      snapHead, FixtureTree: snapTree,
						SeedStatus: seedStatus, WarmSetupValid: &trueValue,
						InfraStatus: "snapshot_dirty", SetupOutcome: "snapshot_dirty",
						Error: truncateForNote(assertErr.Error(), 300),
					}
					appendRowWithCheckpoint(rows, options.OutDir, row)
					cleanupArms()
					return fmt.Errorf("family %s attempt %d arm %s: %w", family.ID, attempt, arm.name, assertErr)
				}

				sessionID := fmt.Sprintf("mvp-match-%s-%s-r%d-%d", family.ID, arm.name, attempt, time.Now().UnixNano())
				// Fresh timeout per Task B attempt, cancelled when it ends.
				runCtx, cancel := context.WithTimeout(ctx, familiesRunTimeout)
				_, _, row := mvpRunTracked(deps, ctx, runCtx, runFunc, eval.RunInput{
					SessionID:   sessionID,
					Memory:      arm.memory,
					Prompt:      family.TargetTask,
					Cwd:         arm.dir,
					Check:       targetChecksFor(manifestDir, family),
					OutputPath:  mvpDebugPath(options.OutDir, family.ID, attempt, arm.name, "b"),
					ArtifactDir: mvpArtifactDir(options.OutDir, family.ID, attempt, arm.name, "b"),
				}, options.OutDir)
				cancel()
				row.Family = family.ID
				row.Task = "B"
				row.Attempt = attempt
				row.Arm = arm.name
				row.SessionID = sessionID
				row.Precursor = "success"
				row.ExperimentID = experimentID
				row.PipelineRunID = experimentID
				row.SnapshotID = snapshotID
				row.Executed = true
				row.Treatment = armTreatment(arm.name)
				// The start state recorded here is the state the
				// assertion VERIFIED immediately before the launch.
				row.StartCommit = asserted.Commit
				row.StartTree = asserted.Tree
				row.CleanStateVerified = cleanState
				row.FixtureCommit = snapHead
				row.FixtureTree = snapTree
				row.SeedStatus = seedStatus
				row.WarmSetupValid = &trueValue
				if ctx.Err() != nil {
					appendRowWithCheckpoint(rows, options.OutDir, row)
					cleanupArms()
					return fmt.Errorf("interrupted")
				}
				appendRowWithCheckpoint(rows, options.OutDir, row)
			}
		}
		cleanupArms()
		fmt.Fprintf(stderr, "family %s: matched-snapshot %d rollouts x 2 arms done\n", family.ID, options.Rollouts)
	}
	return nil
}

// armTreatment returns the treatment label for one arm (memory on/off).
func armTreatment(arm string) string {
	if arm == "warm" {
		return "memory_on"
	}
	return "memory_off"
}

// appendRowWithCheckpoint appends one completed or skipped row and
// immediately persists the attempts JSONL, so an interruption retains the
// most recent completed target. A checkpoint write failure is printed to
// stderr (fail loud in the log, never silently dropped) but does not abort
// the evaluation: losing the incremental checkpoint is not a correctness
// failure of any attempt.
func appendRowWithCheckpoint(rows *[]familyPairRow, outDir string, row familyPairRow) {
	*rows = append(*rows, row)
	if outDir == "" {
		return
	}
	if err := writeFamiliesRows(outDir, *rows); err != nil {
		fmt.Fprintf(os.Stderr, "checkpoint write failed: %v\n", err)
	}
}
