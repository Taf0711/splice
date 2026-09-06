package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Taf0711/splice/internal/eval"
	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice"
)

// runMvpMatchedSnapshots implements the matched Task B comparison: Task A
// runs ONCE per family (warm arm, memory on, externally verified), the
// verified tree is frozen, and the naturally captured cognition is
// deterministically reconstructed from that verified tree for every Task B
// attempt in the warm arm. The cold arm starts from the identical committed
// tree with no cognition. Starting tree hashes are asserted equal before
// every Task B execution.
//
// This isolates the memory/context policy effect on an identical coding
// task. It deliberately does NOT hand-author the cognition: the captures
// are the same deterministic artifacts captureFromVerifiedRun derives from
// the verified tree (file + symbol anchors from go/parser, the verified
// procedure), re-persisted from the committed revision.
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
	runCtx := runCtxFor(ctx)

	for _, family := range manifest.Families {
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
			return fmt.Errorf("reset snapshot memory: %w", err)
		}

		snapSession := fmt.Sprintf("mvp-snap-%s-%d", family.ID, time.Now().UnixNano())
		snapStatus, snapErr := mvpRunOnce(deps, ctx, runCtx, runFunc, eval.RunInput{
			SessionID:   snapSession + "-taska",
			Memory:      "on",
			Prompt:      family.PrecursorTask,
			Cwd:         snapDir,
			Check:       precursorChecksFor(manifestDir, family),
			ArtifactDir: mvpArtifactDir(options.OutDir, family.ID, 0, "snapshot", "a"),
		}, rows, family.ID, 0, "snapshot", "A", options.OutDir)
		if snapStatus != "success" {
			cleanup()
			// Record the failed setup as a real outcome of the workflow.
			for _, arm := range []string{"cold", "warm"} {
				for attempt := 1; attempt <= options.Rollouts; attempt++ {
					*rows = append(*rows, familyPairRow{
						Family: family.ID, Task: "B", Attempt: attempt, Arm: arm,
						InfraStatus:   "precursor_failed",
						WarmSetupNote: "snapshot Task A did not verify; target not run",
					})
				}
			}
			fmt.Fprintf(os.Stderr, "family %s: snapshot Task A did not verify; skipping target\n", family.ID)
			continue
		}
		_ = snapErr

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

		// Reanchor the captured nodes from the pre-verify HEAD to the
		// snapshot commit.
		if client, err := memd.Resolve(ctx); err == nil && client != nil {
			if _, err := client.ReanchorGraph(ctx, snapDir, preHead, snapHead); err != nil {
				cleanup()
				return fmt.Errorf("reanchor snapshot cognition: %w", err)
			}
		}

		// ---- Phase 2: reconstruct the cognition deterministically in the
		// target arm's project identity. The graph is keyed by project
		// path, and the cold arm must NOT see it, so the warm arm gets its
		// own stable project directory whose bytes are the verified tree.
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

		// Seed the warm arm's cognition by re-persisting the deterministic
		// captures against the committed tree in the WARM project identity.
		if err := seedCognitionFromSnapshot(ctx, snapDir, warmDir, snapHead, changedFiles, family.ID); err != nil {
			cleanupArms()
			return fmt.Errorf("seed warm cognition from snapshot: %w", err)
		}

		// Assert starting tree equality BEFORE any Task B execution.
		warmTree := treeHash(warmDir)
		coldTree := treeHash(coldDir)
		if warmTree == "" || warmTree != coldTree {
			cleanupArms()
			return fmt.Errorf("starting tree hash mismatch: warm=%q cold=%q", warmTree, coldTree)
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
					if err := seedCognitionFromSnapshot(ctx, snapDir, warmDir, snapHead, changedFiles, family.ID); err != nil {
						cleanupArms()
						return fmt.Errorf("re-seed warm cognition (attempt %d): %w", attempt, err)
					}
				}
				// Reset the worktree to the frozen verified tree (Task B's
				// own edits from attempt N-1 must not leak).
				if err := materializeFromCommit(snapDir, snapHead, arm.dir); err != nil {
					cleanupArms()
					return fmt.Errorf("reset %s worktree (attempt %d): %w", arm.name, attempt, err)
				}

				sessionID := fmt.Sprintf("mvp-match-%s-%s-r%d-%d", family.ID, arm.name, attempt, time.Now().UnixNano())
				row := familyPairRow{
					Family:    family.ID,
					Task:      "B",
					Attempt:   attempt,
					Arm:       arm.name,
					SessionID: sessionID,
					Precursor: "success",
				}
				out, runErr := runFunc(ctx, eval.RunInput{
					SessionID:   sessionID,
					Memory:      arm.memory,
					Prompt:      family.TargetTask,
					Cwd:         arm.dir,
					Check:       targetChecksFor(manifestDir, family),
					OutputPath:  mvpDebugPath(options.OutDir, family.ID, attempt, arm.name, "b"),
					ArtifactDir: mvpArtifactDir(options.OutDir, family.ID, attempt, arm.name, "b"),
				})
				row.Success = out.Success
				row.Tokens = out.Tokens
				row.Telemetry = out.TelemetryFound
				row.ToolCalls = out.ToolCalls
				row.FileReads = out.FileReads
				row.SearchCalls = out.SearchCalls
				if runErr != nil {
					row.Success = false
					row.Error = truncateForNote(runErr.Error(), 300)
				}
				if row.Telemetry {
					collectRunTelemetry(ctx, deps, arm.dir, sessionID, &row)
				}
				*rows = append(*rows, row)
				if ctx.Err() != nil {
					cleanupArms()
					return fmt.Errorf("interrupted")
				}
			}
		}
		cleanupArms()
		fmt.Fprintf(os.Stderr, "family %s: matched-snapshot %d rollouts x 2 arms done\n", family.ID, options.Rollouts)
	}
	return nil
}

// precursorChecksFor and targetChecksFor resolve the verifier scripts the
// same way the main flow does (relative to the manifest directory).
func precursorChecksFor(manifestDir string, family mvpFamilyEntry) string {
	data, err := os.ReadFile(filepath.Join(manifestDir, family.PrecursorCheckFile))
	if err != nil {
		return ""
	}
	return string(data)
}

func targetChecksFor(manifestDir string, family mvpFamilyEntry) string {
	data, err := os.ReadFile(filepath.Join(manifestDir, family.TargetCheckFile))
	if err != nil {
		return ""
	}
	return string(data)
}

// materializeFromCommit checks out the snapshot commit's tree into dst and
// initializes git there with the SAME commit hash (so tree hashes and
// anchored revisions are comparable across arms). Uses git worktree-free
// plumbing: clone the snapshot repo at that commit.
func materializeFromCommit(snapDir, commit, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if out, err := exec.Command("git", "-C", snapDir, "worktree", "add", "--detach", dst, commit).CombinedOutput(); err != nil {
		// A worktree may already exist from a previous attempt; remove and
		// retry once.
		_ = exec.Command("git", "-C", snapDir, "worktree", "remove", "--force", dst).Run()
		if out2, err2 := exec.Command("git", "-C", snapDir, "worktree", "add", "--detach", dst, commit).CombinedOutput(); err2 != nil {
			return fmt.Errorf("worktree add: %v: %s: %s", err2, out, out2)
		}
	}
	// The worktree shares the snapshot repo's object store; the sidecar's
	// freshness diffs run inside dst and resolve the same commit.
	return nil
}

// seedCognitionFromSnapshot re-persists the deterministic captures derived
// from the verified snapshot tree into the target project identity. This is
// natural cognition (the same captureFromVerifiedRun artifacts the verified
// run produced), reconstructed without a model call, preserving provenance:
// source_run_id names the snapshot run and verified_revision is the
// verified commit.
func seedCognitionFromSnapshot(ctx context.Context, snapDir, projectDir, revision string, changedFiles []string, familyID string) error {
	client, err := memd.Resolve(ctx)
	if err != nil {
		return fmt.Errorf("resolve sidecar: %w", err)
	}
	if client == nil {
		return fmt.Errorf("memory sidecar unavailable")
	}
	captures := splice.CaptureFromVerifiedRun(snapDir, "completed", changedFiles, "go test ./...", revision, "snapshot-"+familyID)
	for _, c := range captures {
		if _, err := splice.PersistGraphCapture(ctx, client, projectDir, c); err != nil {
			return fmt.Errorf("persist capture %s: %w", c.Kind, err)
		}
	}
	return nil
}

// treeHash returns the commit hash naming the tree at dir ("" on failure).
func treeHash(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return string(out[:len(out)-1])
}
