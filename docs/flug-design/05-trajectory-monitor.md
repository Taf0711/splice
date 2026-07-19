# Trajectory Monitor (Death Spiral Prevention)

## The Problem

Agentic systems get stuck. The classic failure: agent fixes issue A, the fix breaks B, fixing B reintroduces A, and the system loops until the context window is exhausted, hallucinations begin, and the token budget is gone. This is the death spiral.

Important reframe: **this is not an agent problem, it's an orchestrator problem**. Individual agents are myopic by design. They fix the issue in front of them. They have no concept of the global trajectory. The orchestrator is the only component that can see "we've been here before" or "we're getting worse." The fix lives entirely at that layer.

## The Five Death Spiral Patterns

Each has a distinct signature and needs a different recovery strategy:

| Pattern | Signature | Example |
|---|---|---|
| Whack-a-mole | State oscillates between two configurations | Fix A breaks B; fix B breaks A |
| Symptom chasing | Issue count plateaus while specific issues rotate | Each "fix" surfaces a new error |
| Cascading regression | Each iteration introduces more failures | Score strictly decreases |
| Confidence collapse | Agent's self-reported confidence drops monotonically | Model knows it's stuck before orchestrator does |
| Context decay | After many iterations, agent loses original goal | Final fix unrelated to original request |

## The Core Mechanism: State Vectors

After every iteration, the orchestrator computes a deterministic state vector. No LLM involved. Just measurable facts:

```python
class IterationState(BaseModel):
    iteration: int
    timestamp: float

    # Deterministic measurements
    tests_passing: int
    tests_failing: int
    tests_errored: int
    lint_issues_by_severity: dict[Severity, int]
    security_issues_by_severity: dict[Severity, int]
    type_errors: int
    code_size_bytes: int

    # Hash of actual code state
    state_hash: str   # sha256 of all modified files

    # Agent self-reports
    confidence: float
    tokens_consumed: int

    # Diff from previous iteration
    files_changed: list[str]
    lines_added: int
    lines_removed: int
```

These vectors form a time series. The orchestrator applies rules over the series after each iteration:

```python
def evaluate_trajectory(history: list[IterationState]) -> TrajectoryDecision:
    # Rule 1: Hard limits (the floor)
    if len(history) >= MAX_ITERATIONS:
        return TrajectoryDecision.ABORT_HARD_LIMIT
    if sum(s.tokens_consumed for s in history) >= TOKEN_BUDGET:
        return TrajectoryDecision.ABORT_BUDGET

    # Rule 2: Cycle detection (whack-a-mole)
    state_hashes = [s.state_hash for s in history]
    if state_hashes[-1] in state_hashes[:-1]:
        return TrajectoryDecision.ESCALATE_CYCLE_DETECTED

    # Rule 3: Oscillation (whack-a-mole, longer period)
    if len(history) >= 4 and detect_oscillation(state_hashes):
        return TrajectoryDecision.ESCALATE_OSCILLATION

    # Rule 4: Cascading regression
    score_now = compute_score(history[-1])
    score_initial = compute_score(history[0])
    if len(history) >= 3 and score_now < score_initial:
        return TrajectoryDecision.ROLLBACK

    # Rule 5: Plateau (symptom chasing)
    if len(history) >= 3 and not score_improving(history[-3:]):
        return TrajectoryDecision.STEP_BACK

    # Rule 6: Confidence collapse
    confidences = [s.confidence for s in history[-3:]]
    if all(confidences) and is_strictly_decreasing(confidences):
        return TrajectoryDecision.SURFACE_TO_USER

    # Default: continue
    return TrajectoryDecision.CONTINUE
```

This is small, deterministic, and cheap. Zero tokens. Runs after every iteration. It is the spine of the whole anti-spiral system.

## The Score Function

Tests are the ground truth because they're objective. Everything else is weighted by severity:

```python
def compute_score(state: IterationState) -> float:
    return (
        + state.tests_passing * 10
        - state.tests_failing * 8
        - state.tests_errored * 12          # errors worse than fails
        - state.lint_issues_by_severity.get(HIGH, 0) * 3
        - state.lint_issues_by_severity.get(MEDIUM, 0) * 1
        - state.security_issues_by_severity.get(CRITICAL, 0) * 50
        - state.security_issues_by_severity.get(HIGH, 0) * 20
        - state.type_errors * 2
    )
```

Score going up monotonically: real progress.
Score flat: thrashing.
Score down: last "fix" was a regression.

This single number is what every orchestrator decision keys off of.

## Recovery Strategies

### Cycle Detected → Step-Back Agent

A separate, fresh agent (no iteration history loaded) receives a compressed report:

> "Two attempted fixes are oscillating. Issue A and issue B appear coupled. Here are the failing tests for each. Propose a fix that addresses both simultaneously."

Breaks myopia by reframing the problem at a higher level. Step-back agent uses the medium tier (worth the cost) and runs on a clean context.

### Oscillation → Step-Back with Pinned States

Same as cycle, plus shows both oscillating states explicitly with the constraint: "Your fix must not regress to either of these states."

### Cascading Regression → Rollback

Where the git-style snapshot model pays off. After the in-place editing decision, every iteration commits to a per-run shadow git directory that uses the real project as its work tree. This records what Flug tried without touching the user's `.git` directory.

```python
@dataclass(frozen=True)
class SnapshotRepo:
    git_dir: Path      # runs/<id>/snapshot.git
    work_tree: Path    # the real project path

async def snapshot_iteration(repo: SnapshotRepo, iteration: int, message: str) -> str:
    await run_git(repo, ["add", "-A", "--", "."])
    await run_git(repo, ["commit", "--allow-empty", "-m", f"iter {iteration}: {message}"])
    return (await run_git(repo, ["rev-parse", "HEAD"])).strip()
```

Under the hood, every command is shaped like this:

```bash
git --git-dir runs/<id>/snapshot.git --work-tree /path/to/project <args>
```

Checkpoint 2A creates these snapshots. Checkpoint 2E1 adds restore primitives: resolve the target iteration, create a pre-rollback backup commit, then restore the target through the shadow git index. Later CLI and iteration-loop work will call these primitives after the trajectory monitor chooses `ROLLBACK`. It should not run `git reset --hard` against the user's repository.

The idea is conceptually aligned with ShadowGit's local shadow-history approach for AI-assisted coding, referenced in `docs/09-agent-harness-principles.md`. Flug keeps it scoped to per-run trajectory snapshots rather than save-level auto-commits.

Bonus: every run produces a real git history users can inspect with `git --git-dir runs/<id>/snapshot.git log` to see exactly what was tried.

### Plateau (Symptom Chasing) → Force Root Cause Analysis

A specialized "root cause" agent receives the issue history and has one job: produce a hypothesis about what underlying issue is generating these symptoms. It does not fix anything. Its output goes back to the writer with the constraint: "Address the root cause, not the surface symptoms."

```python
class RootCauseAnalysis(BaseModel):
    hypothesized_root_cause: str
    evidence: list[str]
    affected_symptoms: list[str]
    recommended_approach: str
    confidence: float
```

A deliberate interruption of the patch-and-test cycle. Costs one medium-tier call but it's how you escape symptom-chasing.

### Confidence Collapse → Surface to User

The cheapest and most honest recovery: tell the user the system is stuck. Crucially, this is structured, not a bare error:

```
Flug encountered a problem it can't resolve confidently.

What I was trying to fix:
  Add OAuth2 login to the Flask app

What I tried:
  1. Authlib integration with Flask-Session - introduced a CSRF vulnerability
  2. Switched to itsdangerous for state - tests started failing
  3. Reverted to Authlib with custom CSRF - circular import error
  4. Restructured imports - broke 3 unrelated tests

What I think is happening:
  The auth module has a circular dependency with the user model.
  This needs an architectural decision I don't want to make alone.

Options:
  [1] Move shared types to a new auth_types.py module
  [2] Use a service layer pattern
  [3] Lazy-import the user model in auth handlers

Reply with [1], [2], [3], or describe a different approach.
```

Better than silently spiraling for 20 more iterations. User provides one bit of guidance, system unblocks. This is also where Flug becomes a *collaborative* tool rather than a black box, which is itself a UX advantage.

## Context Window Management for Iterations

Death spirals often turn into hallucination because chat history balloons across 10+ iterations. Standard fix: never carry full iteration transcripts forward. Each iteration is reduced to a tiny structured record:

```python
class IterationRecord(BaseModel):
    iteration: int
    summary: str                # 1-2 sentences: "Fixed X by doing Y"
    diff_path: str              # path to actual diff in runs/<id>/diffs/
    test_delta: str             # "tests_passing: 12->14, failing: 3->1"
    outcome: Literal["improvement", "regression", "no_change"]
```

When the writer revises on iteration 8, it sees only 7 of these compact records (each ~100 tokens) plus current state and latest failure. ~700 tokens of history instead of 15,000. Full diffs are on disk if any agent needs to dig in.

## Hard Limits as the Floor (Non-Negotiable)

Even with all the above, ship hard limits as a safety net:

| Limit | Default | Configurable |
|---|---|---|
| Max iterations per request | 5 | yes (`--max-iter`) |
| Max iterations within a single stage | 3 | yes |
| Max total tokens per run | computed from tier | yes |
| Max wall clock time | 5 minutes | yes |
| Max consecutive regressions | 2 | no (correctness-critical) |

These are dumb limits. They don't care about trajectory or score. If hit, the run aborts and the last known good state is restored from the git snapshot. User gets a structured failure report.

The configurable defaults matter because power users will tune them, but the philosophy is: **always better to fail fast and tell the user than grind silently**. Worst UX is a 30-minute run that produces broken code and a $4 bill.

## What This Looks Like to the User

```
$ flug "add OAuth2 login"

▸ Iteration 1: implementing initial design
   Score: 0 -> 8 (8 new tests passing)

▸ Iteration 2: fixing static analysis issues
   Score: 8 -> 12 (lint cleanup, no test changes)

▸ Iteration 3: addressing security audit
   Score: 12 -> 6 (added CSRF protection but 2 tests now fail)
   ⚠ Trajectory: regression detected

▸ Iteration 4: rolling back to iteration 2, retrying with stronger context
   Score: 12 -> 14 (CSRF added without breaking tests)

✓ Pipeline complete. 4 iterations, $0.34, 18,200 tokens.
   Trace: ~/.local/share/flug/runs/<id>/trace.jsonl
```

User sees the trajectory, sees the recovery, sees the cost. Trust develops because they can verify the system isn't lying to itself about progress.

## The Pitch

> Flug prevents the death spiral failure mode through explicit trajectory monitoring: every iteration produces a deterministic state vector, and the orchestrator applies cycle detection, regression checks, and confidence trajectory analysis to decide whether to continue, roll back, or surface to the user. No agentic system should run unbounded iteration loops without this.

This signals to anyone who's deployed agents in production that you've thought about operational realities, not just the happy path.
