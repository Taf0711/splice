package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
// Section-11 campaign mode (--conditions three-condition) extends the arm
// table to the three live conditions plus the diagnostic manual arm, with
// a recorded scheduling seed driving the per-experiment arm launch order
// (Section 11.2). The legacy 2-arm behavior stays the default behind
// --conditions cold,warm, so old scripts are not silently changed.
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
	arms, err := campaignArmsFor(options.Conditions)
	if err != nil {
		return err
	}
	experimentID := fmt.Sprintf("mvp-matched-%d", time.Now().UnixNano())
	// Section 11.2: the scheduling seed is recorded and the arm launch
	// order for the experiment is derived from it with math/rand. Arms
	// still run sequentially: no concurrent env mutation.
	schedulingSeed := options.SchedulingSeed
	if schedulingSeed == 0 {
		schedulingSeed = defaultSchedulingSeed
	}
	armOrder := shuffledArms(arms, schedulingSeed)
	orderNames := make([]string, len(armOrder))
	for i, arm := range armOrder {
		orderNames[i] = arm.name
	}
	fmt.Fprintf(stderr, "experiment %s: scheduling seed %d, arm order %v\n",
		experimentID, schedulingSeed, orderNames)
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
			SessionID:       snapSession + "-taska",
			Memory:          "on",
			Treatment:       "full",
			ExperimentID:    experimentID,
			Family:          family.ID,
			Arm:             "snapshot",
			Task:            "A",
			Attempt:         0,
			ArmOrder:        1,
			Prompt:          family.PrecursorTask,
			Cwd:             snapDir,
			Check:           precursorChecksFor(manifestDir, family),
			CheckScriptPath: filepath.Join(manifestDir, family.PrecursorCheckFile),
			ArtifactDir:     mvpArtifactDir(options.OutDir, family.ID, 0, "snapshot", "a"),
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
			// target slots (Rollouts x arms), each marked skipped, not
			// failed. Skipped rows carry the scheduling seed and their
			// arm's launch-order index so the run order stays auditable
			// even on the interrupted path.
			note := "snapshot Task A did not verify; target not run"
			if snapErr != nil {
				note = "snapshot Task A did not verify; target not run: " + truncateForNote(snapErr.Error(), 200)
			}
			setup := "precursor_failed"
			if snapStatus == "timeout" {
				setup = "timeout"
			}
			for _, arm := range arms {
				for attempt := 1; attempt <= options.Rollouts; attempt++ {
					diagnostic := arm.diagnosticOnly
					row := familyPairRow{
						Family: family.ID, Task: "B", Attempt: attempt, Arm: arm.name,
						ExperimentID: experimentID, PipelineRunID: experimentID,
						SnapshotID: snapshotID, SetupOutcome: setup,
						Executed: false, Treatment: armTreatmentFor(arm),
						WarmSetupValid: &falseValue,
						WarmSetupNote:  note,
						InfraStatus:    "precursor_failed",
						Condition:      arm.condition, DiagnosticOnly: &diagnostic,
						SchedulingSeed: schedulingSeed,
						ArmOrderIndex:  armOrderIndex(armOrder, arm.name),
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

		// A4: export the ACTUAL runtime capture set and freeze it into a
		// bundle file shared by every compared arm. A missing natural
		// capture fails natural-capture setup loudly; the labeled
		// reconstruction path is the explicit fallback (Reconstructed=true
		// rides the bundle, the seed set, and every row).
		bundle := snapshotBundle{
			ProducerRunID: seedSet.ProducerRunID,
			Commit:        snapHead,
			Tree:          gitTreeHash(snapDir),
			VerifyCommand: prov.VerifyCommand,
		}
		if client, err := memd.Resolve(ctx); err == nil && client != nil {
			if nodes, expErr := exportNaturalCaptureSet(ctx, client, snapDir, preHead, seedSet.ProducerRunID); expErr == nil {
				bundle.Nodes = nodes
				bundle.CaptureOrigin = "natural"
				seedSet.Reconstructed = false
			} else {
				// Labeled diagnostic fallback: reconstruct from the
				// verified tree, and mark it everywhere.
				bundle.CaptureOrigin = "reconstructed"
				bundle.CaptureOriginNote = truncateForNote(expErr.Error(), 300)
				seedSet.Reconstructed = true
				fmt.Fprintf(stderr, "family %s: natural capture export failed (%v); using RECONSTRUCTED captures (diagnostic mode)\n", family.ID, expErr)
			}
		} else {
			bundle.CaptureOrigin = "reconstructed"
			bundle.CaptureOriginNote = "memory sidecar unavailable for export"
			seedSet.Reconstructed = true
		}
		if bundle.Nodes == nil && seedSet.Reconstructed {
			// Reconstruction: materialize the deterministic payload and
			// export its shape into the bundle so import paths see one
			// representation. The Reconstructed flag rides the bundle.
			bundle.Nodes = seedCapturesToExported(seedSet)
		}
		if options.OutDir != "" {
			bundleDir := filepath.Join(options.OutDir, "snapshots", snapshotID)
			if bErr := exportSnapshotBundle(bundleDir, bundle); bErr != nil {
				cleanup()
				return fmt.Errorf("export snapshot bundle: %w", bErr)
			}
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

		// ---- Phase 2: every arm gets its own stable project directory
		// whose bytes are the verified tree. The graph is keyed by project
		// path, and the memory-off arms must NOT see the seeded cognition.
		armDirs := map[string]string{}
		for _, arm := range arms {
			dir, err := os.MkdirTemp("", "splice-mvp-"+arm.name+"-")
			if err != nil {
				cleanupArms(armDirs)
				cleanup()
				return fmt.Errorf("materialize %s arm: %w", arm.name, err)
			}
			armDirs[arm.name] = dir
		}
		cleanupArms := func() {
			cleanupArms(armDirs)
			cleanup()
		}

		// Materialize every arm from the snapshot commit (identical bytes).
		for _, arm := range arms {
			if err := materializeFromCommit(snapDir, snapHead, armDirs[arm.name]); err != nil {
				cleanupArms()
				return fmt.Errorf("materialize arm from snapshot: %w", err)
			}
		}

		// Seed the seeding arms' cognition from the FROZEN snapshot bundle.
		// automatic replays the full frozen capture payload (B2); manual
		// imports ONLY the records the E4 subject-matching selects over
		// the needs derived from the target task (Section 11.3).
		seedStatus := ""
		if !seedPersisted {
			seedStatus = "seed_skipped_no_sidecar"
		} else if client, err := memd.Resolve(ctx); err == nil && client != nil {
			seedStatus = "replayed"
			for _, arm := range arms {
				if !arm.seed {
					continue
				}
				if arm.name == "manual" {
					if _, mErr := seedManualArm(ctx, client, armDirs[arm.name], bundle, family.TargetTask); mErr != nil {
						cleanupArms()
						return fmt.Errorf("seed manual cognition from snapshot: %w", mErr)
					}
				} else {
					if err := replaySeedCaptures(ctx, client, armDirs[arm.name], seedSet); err != nil {
						cleanupArms()
						return fmt.Errorf("seed warm cognition from snapshot: %w", err)
					}
				}
			}
		} else {
			seedStatus = "seed_skipped_no_sidecar"
		}
		if seedStatus == "seed_skipped_no_sidecar" {
			// Fail loud: a seeding arm without seeded cognition is a
			// cold attempt wearing a warm label, which corrupts the
			// paired measurement.
			cleanupArms()
			return fmt.Errorf("seeding arm for family %s: memory sidecar unavailable; cannot seed snapshot cognition", family.ID)
		}

		// Assert starting state equality for EVERY arm BEFORE any Task B
		// execution (the loop re-asserts per attempt). Commit and tree are
		// verified as separate facts against the snapshot identity.
		for _, arm := range arms {
			if _, aerr := assertCleanSnapshot(armDirs[arm.name], snapHead, snapTree); aerr != nil {
				cleanupArms()
				return fmt.Errorf("initial %s arm snapshot assertion: %w", arm.name, aerr)
			}
		}

		// ---- Phase 3: Task B attempts on the frozen, identical tree, in
		// the scheduling-seed-derived arm order. Arms run sequentially.
		for attempt := 1; attempt <= options.Rollouts; attempt++ {
			for _, arm := range armOrder {
				// Reset the arm's sidecar state so attempt N's captures
				// never leak into attempt N+1, then restore the snapshot
				// cognition for the seeding arms.
				if resetErr := resetArmMemory(ctx, armDirs[arm.name]); resetErr != nil {
					cleanupArms()
					return fmt.Errorf("reset %s memory (family %s attempt %d): %w", arm.name, family.ID, attempt, resetErr)
				}
				if arm.seed {
					if client, err := memd.Resolve(ctx); err == nil && client != nil {
						if arm.name == "manual" {
							if _, err := seedManualArm(ctx, client, armDirs[arm.name], bundle, family.TargetTask); err != nil {
								cleanupArms()
								return fmt.Errorf("re-seed manual cognition (attempt %d): %w", attempt, err)
							}
						} else {
							if err := replaySeedCaptures(ctx, client, armDirs[arm.name], seedSet); err != nil {
								cleanupArms()
								return fmt.Errorf("re-seed warm cognition (attempt %d): %w", attempt, err)
							}
						}
					} else {
						cleanupArms()
						return fmt.Errorf("re-seed %s cognition (attempt %d): memory sidecar unavailable", arm.name, attempt)
					}
				}
				// Reset the worktree to the frozen verified tree (Task B's
				// own edits from attempt N-1 must not leak). Rematerialize
				// FIRST, then assert: the previous attempt's files must
				// not survive.
				if err := materializeFromCommit(snapDir, snapHead, armDirs[arm.name]); err != nil {
					cleanupArms()
					return fmt.Errorf("reset %s worktree (attempt %d): %w", arm.name, attempt, err)
				}

				// ---- Per-attempt clean-snapshot assertion (B1/F7):
				// every Task B gets its own verification of the intended
				// commit AND tree AND a clean index/working tree,
				// immediately BEFORE the model launches. The assertion
				// result lands on the row before the run.
				asserted, assertErr := assertCleanSnapshot(armDirs[arm.name], snapHead, snapTree)
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
					diagnostic := arm.diagnosticOnly
					row := familyPairRow{
						Family: family.ID, Task: "B", Attempt: attempt, Arm: arm.name,
						ExperimentID: experimentID, PipelineRunID: experimentID,
						SnapshotID: snapshotID, Executed: false,
						Treatment:   armTreatmentFor(arm),
						StartCommit: asserted.Commit, StartTree: asserted.Tree,
						CleanStateVerified: cleanState,
						FixtureCommit:      snapHead, FixtureTree: snapTree,
						SeedStatus: seedStatus, WarmSetupValid: &trueValue,
						InfraStatus: "snapshot_dirty", SetupOutcome: "snapshot_dirty",
						Error:     truncateForNote(assertErr.Error(), 300),
						Condition: arm.condition, DiagnosticOnly: &diagnostic,
						SchedulingSeed: schedulingSeed,
						ArmOrderIndex:  armOrderIndex(armOrder, arm.name),
					}
					appendRowWithCheckpoint(rows, options.OutDir, row)
					cleanupArms()
					return fmt.Errorf("family %s attempt %d arm %s: %w", family.ID, attempt, arm.name, assertErr)
				}

				sessionID := fmt.Sprintf("mvp-match-%s-%s-r%d-%d", family.ID, arm.name, attempt, time.Now().UnixNano())
				// Fresh timeout per Task B attempt, cancelled when it ends.
				runCtx, cancel := context.WithTimeout(ctx, familiesRunTimeout)
				_, _, row := mvpRunTracked(deps, ctx, runCtx, runFunc, eval.RunInput{
					SessionID:       sessionID,
					Memory:          arm.memory,
					Treatment:       arm.treatment,
					ExperimentID:    experimentID,
					Family:          family.ID,
					Arm:             arm.name,
					Task:            "B",
					Attempt:         attempt,
					ArmOrder:        armOrderIndex(armOrder, arm.name),
					Prompt:          family.TargetTask,
					Cwd:             armDirs[arm.name],
					Check:           targetChecksFor(manifestDir, family),
					CheckScriptPath: filepath.Join(manifestDir, family.TargetCheckFile),
					OutputPath:      mvpDebugPath(options.OutDir, family.ID, attempt, arm.name, "b"),
					ArtifactDir:     mvpArtifactDir(options.OutDir, family.ID, attempt, arm.name, "b"),
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
				row.Treatment = armTreatmentFor(arm)
				// The start state recorded here is the state the
				// assertion VERIFIED immediately before the launch.
				row.StartCommit = asserted.Commit
				row.StartTree = asserted.Tree
				row.CleanStateVerified = cleanState
				row.FixtureCommit = snapHead
				row.FixtureTree = snapTree
				row.SeedStatus = seedStatus
				// A4: natural-capture provenance reaches every row. A
				// record can be replayed AND reconstructed: separate
				// dimensions. The digest pins the exact payload replayed.
				row.CaptureReconstructed = &seedSet.Reconstructed
				row.CaptureReplayed = boolPtr(arm.seed && seedStatus == "replayed")
				row.CaptureDigest = bundleDigest(bundle.Nodes)
				row.WarmSetupValid = &trueValue
				// Section-11 campaign fields: condition, diagnostic flag,
				// scheduling seed, arm order index.
				row.Condition = arm.condition
				row.DiagnosticOnly = &arm.diagnosticOnly
				row.SchedulingSeed = schedulingSeed
				row.ArmOrderIndex = armOrderIndex(armOrder, arm.name)
				// The F2 WorkflowCostReport for this row's condition: the
				// campaign verdict number. Built over the condition's
				// rows so far in this family (including this attempt).
				if report, rErr := conditionCostReport(conditionRows(*rows, family.ID, arm.condition)); rErr == nil {
					row.WorkflowCost = report
				}
				if ctx.Err() != nil {
					appendRowWithCheckpoint(rows, options.OutDir, row)
					cleanupArms()
					return fmt.Errorf("interrupted")
				}
				appendRowWithCheckpoint(rows, options.OutDir, row)
			}
		}
		cleanupArms()
		fmt.Fprintf(stderr, "family %s: matched-snapshot %d rollouts x %d arms done\n", family.ID, options.Rollouts, len(arms))
	}
	return nil
}

// cleanupArms removes every arm project directory recorded so far.
func cleanupArms(armDirs map[string]string) {
	for _, dir := range armDirs {
		os.RemoveAll(dir)
	}
}

// conditionRows returns the executed Task B rows of one family and
// condition (the per-condition sample the cost report joins).
func conditionRows(rows []familyPairRow, family, condition string) []familyPairRow {
	out := make([]familyPairRow, 0, len(rows))
	for _, row := range rows {
		if row.Family == family && row.Task == "B" && row.Executed && row.Condition == condition {
			out = append(out, row)
		}
	}
	return out
}

// armTreatment returns the treatment label for one arm. The label records
// the arm's memory flag AND the ambient SPLICE_TREATMENT when set, because
// the treatment environment applies process-wide: a warm arm under
// SPLICE_TREATMENT=cold is realized as retrieval-only, not full, and the
// row must say so or the analysis attributes the wrong condition.
//
// This path sets the child's --memory flag from the ARM, not from the
// treatment, so the arm can contradict the treatment's declared retrieval
// dimension. Since scope-only now declares retrieval ON (it used to run
// with --memory off and degenerate into cold), a memory_off arm under
// SPLICE_TREATMENT=scope-only realizes retrieval OFF. The label names that
// contradiction rather than leaving a reader to infer the treatment's
// declared retrieval held.
func armTreatment(arm string) string {
	memory := "memory_off"
	if arm == "warm" || arm == "manual" {
		memory = "memory_on"
	}
	raw := strings.TrimSpace(os.Getenv("SPLICE_TREATMENT"))
	if raw == "" {
		return memory
	}
	label := memory + "+" + raw
	spec, err := splice.ResolveTreatment(raw)
	if err != nil {
		// An unresolvable ambient treatment is recorded verbatim: the row
		// must not claim a realized condition the process cannot resolve.
		return label + "+unresolved_treatment"
	}
	if spec.MemoryRetrieval() != (arm == "warm" || arm == "manual") {
		// The arm's memory flag wins over the treatment's declared
		// retrieval, because it is the flag actually passed to the child.
		return label + "+retrieval_overridden_by_arm"
	}
	return label
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
