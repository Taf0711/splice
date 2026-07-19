# Execution Worktrees

## Status

Archived Python-era design. Architecture decided; Track W (W1, W2, W5) was
implemented in an archived private repository, and the
body below describes that design with its Python file paths.

Splice (Track F-Zero) note: the Go port lands worktree isolation and
merge-back in F8b with two deliberate divergences from this document
(recorded in `MEMORY.md` 2026-07-08). One worktree per exec run rather than
per plan task: tasks are sequential and fail-fast, so per-task isolation adds
lifecycle complexity without benefit until tasks parallelize. Merge-back is
opt-in (`splice exec --worktree --merge-back`) rather than default, so
inherited `--worktree` behavior stays unchanged. `worktrees.MergeBack` in
`internal/worktrees/worktrees.go` commits the worktree's work, pins the
`splice/<name>` recovery branch, and merges with `--no-ff`; statuses are
`merged`, `no_changes`, `skipped_dirty`, and `conflict`, and the recovery
branch survives every non-merged case. Per-agent commit stacks (W3) and
worktree rollback (W4) are deferred; see `ROADMAP.md` known gaps.

## Motivation

The current in-place execution model (E1, 2026-06-05) edits the user's project directly,
using the user's normal git repository as the undo net and a shadow git directory at
`runs/<id>/snapshot.git` as Flug's own rollback store. That model works but has two
structural limits:

1. **No real commit stack.** Each iteration snapshots a complete project state but there
   is no per-agent provenance: you cannot `git log` and see "Code Writer wrote these
   three files, Test Generator added two tests."
2. **Rollback touches user's working tree.** Restoring an iteration via the shadow index
   (`git read-tree --reset -u`) modifies the user's files in place with no staging or
   confirmation step between "Flug chose which commit to restore" and "the user's project
   now looks like that commit."

Worktrees solve both. Each attempt gets its own isolated directory, invisible to the
user's working tree while the run is in flight. Writers commit to named branches inside
that directory. Rollback is `git reset` on the worktree's own branch, never touching
the user's index or HEAD.

The design also creates the substrate Track L needs: every memory observation from a run
can carry `source_branch` and `source_commit`, linking cross-run learning to the exact
code state that produced it.

## Architecture

### One Worktree Per Attempt

```text
.flug/worktrees/<run-id>/iter<N>/
```

A detached git worktree (`git worktree add`) branched off `<base-commit>` (the user's
current HEAD or last-known-good commit). All agents in that attempt share it. Observers
read from it; writers mutate it.

When the attempt ends (succeeded, failed, or aborted), the worktree is pruned or kept
per `prune` policy. A successful final attempt is the merge candidate for W5.

Precondition: the project must be a git repository with at least one commit. On a non-git
project, `create_attempt_worktree` returns `None` and edits proceed in-place (the caller
handles `None` gracefully). On a git project with a dirty working tree, the worktree is
still created and isolated; the user's uncommitted changes stay in their working tree.

### Per-Mutating-Agent Commit Stack

Only agents that write files get a branch. Read-only observers annotate via run artifacts
(existing trace/stage JSON files), not git.

```text
base commit (user's HEAD)
  |
  |-- flug/<run-id>/iter<N>/code_writer       <- Code Writer commit
       |
       |-- flug/<run-id>/iter<N>/test_generator  <- Test Generator commit (on top)
```

Rules:
- **Writers** (`code_writer`, `test_generator`): apply `FileChange`s through
  `tools/file_changes.py`, then the orchestrator commits to the agent's named branch.
- **Observers** (`static_analyzer`, `test_runner`, `security_auditor`): run against
  the current worktree HEAD (orchestrator feeds them the changed content via the existing
  `CodebaseTools`/`ContextRequest` pull channel), emit findings into run artifacts,
  never touch git.
- **The orchestrator is the sole git actor.** No agent ever runs a git command directly.

Branch naming is deterministic: `flug/<run-id>/iter<N>/<agent-name>`. The `test_generator`
branch is always created on top of the `code_writer` branch, not the base commit, so `git
log` for an attempt shows: base -> code_writer commit -> test_generator commit.

### Commit Messages

Each orchestrator-managed commit carries the stage name and a bounded summary:

```
flug: iter 2 / code_writer

Modified 3 files: calculator.py, tests/test_calc.py, README.md
Requested by: run abc123 / iter 2
```

This makes `git log --oneline` inside the worktree useful for debugging without requiring
the trace JSONL.

## Merge-Back on Success

`@needs-human` decision W5: what happens when a run succeeds?

Default lean: merge the winning `test_generator` branch (the deepest writer branch) into
the user's working tree automatically, behind the `assess_git_safety` guard, with an
explicit merge commit. If the working tree is dirty or the merge would produce conflicts,
surface the branch name and prompt the user to merge manually.

This replaces the current behavior of having Code Writer apply `FileChange`s to the live
project during execution. Under Track W, no change hits the user's project until the run
succeeds.

W5 is a separate checkpoint, explicitly gated on human approval before implementation.

## Rollback

Rollback under Track W is `git reset --hard <ref>` on the worktree branch, then a new
attempt branching from that ref. The user's working tree is not touched.

`flug rollback <run-id> --to-iter N` resolves the agent branch at iteration N and
creates a new worktree from that commit. The old worktree stays prunable.

`flug diff <run-id> [--iter A B]` is `git diff <base>..<test_generator-branch>` (or
between two agent branches across iterations).

This replaces the shadow-git-dir diff/restore primitives in `flug/storage/snapshot.py`.
That module and its tests are removed in W4.

## Non-Git Projects

`create_attempt_worktree` returns `None` for non-git projects (or git repos with no
initial commit). Callers treat `None` as "no isolation available" and proceed with
in-place editing, matching the E1 behavior. No scratch repository is created.

Rationale: a scratch-copy approach was implemented but removed (Phase 1 remediation,
2026-06-28). The complexity of tracking scratch-copy state, merging back changes, and
handling the edge case where a user converts their project to a real git repo mid-run
was not worth the isolation benefit for what is already a known-unsupported configuration.
Users who need isolation should `git init` their project first.

The caller (cli.py, tui/app.py, design_runner.py) calls `prune(worktree)` only when the
handle is not `None`, so the `None` path is a clean no-op with no resource leak.

## Integration with Memory Sidecar (Track L)

After the orchestrator commits a writer's `FileChange`s, it records the commit SHA on the
resulting stage record. In M7 (deterministic writes), the sidecar client tags each
persisted observation with `source_branch` and `source_commit` pulled from the active
stage context.

The benefit at two timescales:
- **Intra-run**: `revision_context` for the next iteration is `git diff <last-good>..<current>`
  (exact changes, not a summarizer guess). Already-planned as `_build_revision_context`
  enhancement in W4.
- **Cross-run**: the same breaking commit becomes a memory observation; the next run's
  Code Writer bundle warns it before it repeats the same mistake.

## Checkpoint Track W

- **W1 (done).** `flug/storage/worktree.py`: `create_attempt_worktree(run_dir, run_id, project_path) -> WorktreeHandle | None`,
  `commit_agent_work(handle, agent, message) -> str (sha)`, `diff(handle, base, head) -> str`,
  `rollback_to(handle, ref)`, `prune(handle)`. Git repos get detached worktree; non-git
  returns `None`. Unit tests in temp git repos (8 tests).
- **W2.** Wire a per-attempt worktree into `_run_iteration_loop`. Writers operate in the
  worktree; orchestrator commits after each. User's working tree untouched during execution.
- **W3.** Per-agent commit stack (`test_generator` branches on top of `code_writer`). Observer
  findings annotate SHA via run artifacts (existing stage JSON keyed to commit SHA).
- **W4.** Upgrade `_build_revision_context` to `git diff <last-good>..<current>`; remap
  `_try_restore_best`/rollback to `git reset`/branch-switch on the worktree. Retire
  `snapshot.py` and its tests. Remap `flug diff`/`flug rollback` to real branches.
- **W5 (`@needs-human`).** Merge-back-on-success UX.

## Verification

- **W1**: `pytest` in a temp git repo: worktree created at correct path, commit visible in
  `git log`, user's tree unchanged during execution, scratch-repo fallback works.
- **W2**: end-to-end with a fake provider: a run produces a writer branch; `git show` inside
  the worktree shows the agent's files; user's working tree is unmodified until merge-back.
- **W3**: `git log --oneline` inside the worktree shows base -> code_writer -> test_generator.
- **W4**: forced regression rolls back via `git reset` to last-good ref; `flug diff`/rollback
  operate on real branches; snapshot.py tests removed (no regressions).
- **W5**: merge produces correct files in user's tree on clean case; prompts the user on
  dirty/conflict case.
- **Standard gate after each**: ruff, mypy, focused pytest, schema validation, Bandit,
  commit + push + wait for CI.
