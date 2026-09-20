# SPEC — Verified-Revision Anchoring Fix (MVP causal loop)

Branch: feat/mvp-paired-proof. Root cause: probe test
TestVerifiedRevisionProbe2 proves sandboxed git stash create exits 1
while the same command unsandboxed succeeds. verifiedRevision then
silently falls back to headRevision, so Task A cognition anchors at the
pre-Task-A tree. At Task B start, git diff HEAD shows A's edits, every
anchor fails freshness, and the warm arm degrades to cold.

## Decision

Do NOT widen the stage sandbox for git stash create. stash create writes
objects into the repo object database; the stage profile is read-scoped
by design, and widening it for one helper weakens the substrate every
stage shares.

Fix at the layer that owns the invariant instead:

1. Harness (eval): after Task A verifies, the harness commits the arm
   verified tree with the deterministic gitCommitAll (same identity and
   dates as the fixture base commit, so the reset invariant holds).
   Verified work becoming a commit is the natural semantics of a
   verified run, and it applies identically to BOTH arms, so the only
   warm/cold difference stays the cognition store.
2. Capture: verifiedRevision keeps stash create as the first choice
   (real Splice runs that can snapshot the uncommitted worktree), but the
   HEAD fallback becomes CORRECT instead of a degradation when the
   harness has committed the verified tree: HEAD then names exactly the
   verified bytes.
3. Fail loud, not silent: when stash create fails with an error (as
   opposed to returning empty), verifiedRevision reports the reason so
   the capture progress line says anchored at HEAD (snapshot
   unavailable: ...). A silent HEAD anchor was the bug camouflage; it is
   now visible.

## Contract changes

- mvp_eval.go: after a successful Task A (both arms), run
  gitCommitAll(arm.dir) before Task B. Failure is fail-loud (infra error
  for the attempt).
- run.go verifiedRevision: distinguish empty worktree (return HEAD,
  correct) from snapshot creation failed (return HEAD plus a reason
  string).
- captureFromVerifiedRun unchanged: it consumes a revision.
- New tests:
  - probe test updated to assert the eval contract: after committing the
    verified tree, anchors at HEAD classify FRESH at Task B start;
  - TestVerifiedRevisionProbe2 stays as the sandbox-behavior pin.

## Gates

- go test ./internal/splice/ ./internal/memd/ ./internal/cli/
- (cd memd && go test ./...)
- gofmt -l, go vet
- Then the authoritative splice eval mvp --rollouts 3 re-run.
