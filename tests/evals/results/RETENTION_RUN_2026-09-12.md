# Retention measurement: NOT-RUN, blocker on the retention identity

Date: 2026-09-12.
Branch: `wip/evidence-substitution-production` at HEAD
`8c496067ed31e04b1d5f7273ec63b356078897ff` (`8c49606`).
Role: test and eval agent. No product code was changed. Nothing was committed,
staged, or pushed.

## 0. Verdict

NOT-RUN. No provider request was made. The retention protocol cannot be
exercised by the current harness, so running the three arms would produce a
structurally null result that measures nothing.

The blocker is not the corpus wording. It is the memory identity the harness
produces. Each attempt runs in a unique temporary workspace, and that workspace
becomes the memory project identity. The shared sidecar therefore partitions
every captured node and every observation per attempt, and no later attempt can
retrieve an earlier attempt's evidence.

The prerequisite stops here: "If you cannot build a genuine write/read pair,
STOP and report that instead of inventing one." A write/read pair that cannot
exchange evidence is not genuine, so I did not invent one and I did not run.

## 1. Run facts (would-be, and why they did not happen)

- Exact command that was pre-registered for the run, not executed:

      ARMS=cold,warm,warm-retrieval-only \
      REPEATS=3 \
      RETENTION=shared \
      MARGIN=0.05 \
      BOOTSTRAP_SAMPLES=10000 \
      MODEL=z-ai/glm-5.3-flash \
      TASKSET_SRC=tests/evals/warmcost/retention-taskset \
      SIDECAR_ROOT=/tmp/warmcost-retention-sidecar \
      tests/evals/warmcost/paid-run.sh

- Model: `z-ai/glm-5.3-flash`.
- Tasks and phases: NOT-RUN. No corpus was placed under
  `tests/evals/warmcost/retention-taskset`.
- Repeats per task per arm: 3 (pre-registered).
- Attempt cap: arms x tasks x repeats. With the intended write task plus two
  read tasks that is 3 x 3 x 3 = 27 attempts. Used: 0.
- HEAD: `8c496067ed31e04b1d5f7273ec63b356078897ff`.
- Run id: NOT-RUN.
- Sidecar root: `/tmp/warmcost-retention-sidecar` (pre-registered, not created).
- Wall time: no provider time. Analysis only.
- Credential prerequisite: `OPENROUTER_API_KEY` is set, and the operator
  config at `/tmp/splice-cfg/splice/config.json` names provider `openrouter`
  with `apiKeyEnv: OPENROUTER_API_KEY`. The credential is present. The blocker
  is not a credential failure.

## 2. Blocker evidence: memory identity is per attempt

The retention design assumes one shared sidecar gives the warm arms retained
experience across attempts. The code gives each attempt its own memory
identity, so the sidecar is shared but partitioned.

1. The runner copies the fixture into a fresh temporary directory per attempt
   and runs the child with that directory as its working directory:
   - `tests/evals/warmcost/runner.go:632`: `prepareWorkspace` calls
     `os.MkdirTemp("", "warmcost-ws-")`.
   - `tests/evals/warmcost/runner.go:280`: `Dir: workspace` in the exec
     request.
2. The runner invokes `exec` without a worktree and without a project root:
   `tests/evals/warmcost/runner.go:504` `execArgs` passes only
   `--no-trust exec`, `--output-format stream-json`, `--memory <mode>`,
   `--init-session-id`, and `--model`.
3. The memory project root is the working directory unless the process set a
   project root: `internal/splice/memory.go:24`, `memoryProjectRoot` returns
   `options.ProjectRoot` when set, otherwise `workDir`.
4. The exec command sets `ProjectRoot` only for a worktree run:
   `internal/cli/exec.go:920` `if preparedWorktree.Path != "" { runOptions.ProjectRoot = preparedWorktree.RepoRoot }`.
   `preparedWorktree` is prepared only when `options.worktree` is set
   (`internal/cli/exec.go:210`), and the runner never passes it.
   `agent.Options.ProjectRoot` documents the fallback: "Empty means memory
   derives from Cwd exactly as before" (`internal/agent/types.go:381`).
5. The project root is written into every capture:
   `internal/splice/run.go:419` builds the capture set from `projectRoot`, and
   `internal/splice/discovery.go:664` calls `UpsertGraphNode` with
   `Scope: "project"` and `ProjectPath: c.Project`, where `c.Project` comes
   from `canonicalProjectPath(projectRoot)`.
6. Every retrieval path filters by that project path:
   - `internal/splice/discovery.go:106`
     `client.GetExactNodes(ctx, anchors, projectPath, 4)`.
   - `internal/splice/discovery.go:139`
     `client.SearchGraphSemanticallyScoped(ctx, intent, 4, projectPath)`.
   - `memd/store/graph.go:687`
     `AND (? = '' OR n.project_path = ?)`, fed by the same non-empty path.
   - Observations use the same identity and the same filter:
     `internal/splice/memory.go:71,109,135` write `Scope: "project"` and
     `ProjectPath: &projectRoot`, and
     `internal/splice/memoryreason/memoryreason.go:108` rejects a project
     observation whose `ProjectPath` does not match the current root.

Consequence: attempt A stores under `project_path = <workspace-A>`; attempt B
queries `project_path = <workspace-B>`. `<workspace-A> != <workspace-B>`, so B
never retrieves A's nodes or observations. The write task has no reader.

## 3. Second blocker: the substitution freshness gate

Even if the identity matched, the evidence-substitution channel still cannot
fire across attempts. `admitFreshness` (`internal/splice/admission.go:103`)
re-hashes every supporting reference and dependency against the CURRENT
workspace bytes and returns `AdmissionStale` on any mismatch. A capture's
supporting references are the changed files' digests
(`internal/splice/discovery.go:592` `worktreeFileDigests` fills `FileDigests`;
`internal/splice/reuse_record.go:204` copies each into
`SourceRef{Path, Digest}`). The read attempt's workspace is a fresh copy of the
fixture, so it does not contain the write attempt's changed bytes, and the
record is stale.

The one need class that could otherwise reuse a location record,
`NeedInspectEditTarget`, is explicitly excluded from substitution
(`internal/splice/admission.go:163-168`: "edit-target needs current body bytes;
location evidence is not a body view"). So the read/edit path is not
substitutable at all.

## 4. What this means for the corpus prerequisite

- Requirement 1, a write/read pair: a pair can be described (a write task that
  changes a file and a read task that needs that file). It cannot be genuine
  with this harness, because the read attempt cannot receive the write
  attempt's captured evidence. Reported instead of invented.
- Requirement 2, an expansion-triggering dependency: this is buildable and was
  not the blocker. The read task could use the pilot mechanism (an unnamed doc,
  test file, or config) and a live model has been confirmed to request
  context on it (rounds 1 to 3). It changes nothing while requirement 1 is
  blocked.

## 5. Pre-registration (stated before the run, as required)

- Arms: `cold`, `warm`, `warm-retrieval-only`.
- Retention: `shared`. Warm and warm-retrieval-only share one sidecar root.
  Cold gets a fresh sidecar per attempt.
- Order: write-phase tasks before read-phase tasks.
- Repeats per task per arm: 3.
- Correctness noninferiority margin: 0.05, fixed before the run.
- Bootstrap: 10,000 task-clustered samples, seed 1.
- Primary correctness endpoint: verifier pass or fail per attempt.
- Primary cost endpoint: billed USD per verified completion.
- Secondary cost endpoint: billed USD per attempt.
- Win rule: warm is a win only when the matched-pair cost delta is negative
  with a task-clustered interval that excludes zero, and correctness is
  noninferior to the margin.
- Stopping rule: if warm does not reduce provider requests per verified
  completion on a corpus that triggers expansions, stop and recommend shelving
  the lever. Do not tune the mechanism.
- This would have been a new baseline, not pooled with the pilot rounds.

## 6. Required report items

1. Run facts: section 1, NOT-RUN.
2. Per task per arm (verifier, provider calls by spend source, tokens,
   round_share, cache share, billed USD, coverage): NOT-RUN. No attempt exists.
   Do not read this as zero; the value is unknown.
3. Arm totals and the warm-minus-cold matched-pair delta with the task-clustered
   interval: NOT-RUN.
4. The three-channel split by spend source and the reconstruction of the billed
   total: NOT-RUN.
5. Memory preparation costs: NOT-RUN. The harness has no field for them; this is
   a standing gap and would need capture before any claim.
6. Failed attempts in total spend, spend per attempt and per verified
   completion: NOT-RUN.
7. Per-task effects before the aggregate: NOT-RUN.

Claim gating: no total-cost claim is made, and none can be made from this
document. No saving is claimed. No correctness result is claimed.

## 7. Counts (evidence-rule separation)

- Host invocations: 0.
- Provider calls: 0.
- Expansion calls: 0.
- Repair calls: 0.
- Attempts: 0.

No provider credential was consumed. No sidecar was created.

## 8. Gate (captured on this revision)

```text
$ gofmt -l .
(no output; exit 0)
$ go vet ./...
(no output; exit 0)
$ go test ./... -count=1 -timeout 30m
ok   github.com/Taf0711/splice/internal/cli                134.374s
ok   github.com/Taf0711/splice/internal/memd                 0.832s
ok   github.com/Taf0711/splice/internal/splice              21.252s
ok   github.com/Taf0711/splice/tests/evals/warmcost          0.176s
?    github.com/Taf0711/splice/tests/evals/warmcost/cmd/warmcost-eval  [no test files]
go test exit=0
(zero FAIL lines across the run)
$ cd memd && go test ./... -count=1
ok   github.com/Taf0711/splice/memd                          0.719s
ok   github.com/Taf0711/splice/memd/store                    1.416s
memd go test exit=0
$ go build ./tests/evals/warmcost/...
build exit=0
$ bash -n tests/evals/warmcost/paid-run.sh
bash -n exit=0
```

## 9. Contradictions with the earlier records

- `tests/evals/results/WC_RETENTION_PREREG_2026-09-11.md` section 3 states that
  the runner sets the sidecar per class so "an earlier attempt's captured
  evidence can be retrieved by a later attempt". That holds only if the memory
  project identity is stable across attempts. It is not: the runner gives each
  attempt a unique temporary working directory and never sets a project root.
  The prereg's blocker section (section 5) worried about expansion
  non-determinism and missed this identity gap.
- `tests/evals/warmcost/README.md` repeats the same assumption ("an earlier
  attempt's captured evidence can be retrieved by a later attempt"). Same
  correction.
- The pilot rounds (1 to 3) are consistent with this report. They ran the cold
  arm only, or fresh retention with no warm arm, so retention was never
  exercised and no contradiction appears in their numbers.

## 10. Recommended unblock

The owner should decide one of these, then re-open the retention run:

1. Give the eval runner a stable memory project identity for every attempt,
   for example by having `execArgs` pass a fixed project root or a worktree
   whose `RepoRoot` is the run-scoped project, so `memoryProjectRoot` returns
   the same value for every attempt in a run.
2. Or make the memory identity for eval runs a run-level value supplied through
   a supported option, so the shared sidecar is partitioned by run, not by
   attempt.

After that, the corpus still needs the read task to work in bytes that match
the write capture, or the substitution channel stays stale. The memory delivery
channel would then be measurable without digest freshness.

Do not fix this by weakening the default context request or by adding
front-loading. Do not tune the mechanism in this assignment.
