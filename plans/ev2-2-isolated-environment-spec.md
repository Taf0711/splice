# EV2-2 Spec: Isolated Environment

Status: ready for implementation after the demo-readiness slice lands.

Source of truth: `plans/eval-v2/PROTOCOL.md` sections 12-13 (directory
layout, hidden-root denial, child-environment sanitization), ROADMAP
checkpoint EV2-2, existing schemas in `internal/eval/v2` (commits
`698bc92`, `0962e757`).

Scope: one checkpoint. Deterministic environment construction and
verification only. No provider calls, no memory snapshots (EV2-3), no
telemetry collection (EV2-4), no runner loop.

## Goal

Give every trial a hermetic execution environment that is built from
the locked manifest alone and provably cannot see hidden material:
experiment-specific binaries, sidecar socket/database, session root,
per-arm workspace roots with fixture resets, deny rules for hidden
roots, and an allowlisted child environment. The environment builder
must refuse to run against a dirty source tree or a binary/hash
mismatch before anything executes.

## Deliverables

### D1: Workspace layout builder

New file `internal/eval/v2/workspace.go`.

- Function `BuildWorkspace(m Manifest, opts WorkspaceOptions)
  (Workspace, error)` that creates the run-state layout from protocol
  section 12 under a caller-supplied root:

  ```text
  <root>/<experiment-id>/
    bin/        # pinned splice + splice-memd binaries (hashed copies)
    config/     # generated per-arm config
    sidecar/    # socket + database, experiment-local
    sessions/   # session storage root override
    workspaces/<task-id>/<arm>/rep<N>-env<B>/   # one dir per trial
    trials/     # journal from EV2-1 lives here
    raw/        # stream-JSON captures
  ```

- Every directory creation is idempotent: rebuilding over an existing
  layout must not delete or reset trial state; only `workspaces/`
  contents are resettable, and reset is explicit
  (`ResetWorkspace(w, key TrialKey)`), never a side effect.
- Binary staging verifies sha256 against the manifest
  (`BinarySHA256`, `Sidecar.BinarySHA256`) and refuses a mismatch by
  naming both hashes.
- Source-tree cleanliness gate: `RequireCleanSource(dir string)`
  returns an error naming dirty files (`git status --porcelain`
  equivalent implemented without shelling out where possible; if it
  shells out, it must pin the git binary path via options).

### D2: Hidden-root deny rules

New file `internal/eval/v2/deny.go`. Schema + policy layer; wiring into
the live sandbox is the caller's job and gets a seam here.

- Type `DenyRuleSet` built from the manifest plus an explicit list of
  hidden roots (checks/, reference/, manifests/, private corpus).
- Rules cover four access shapes per protocol section 12: direct read,
  shell-mediated read, search/glob reach, and symlink-resolved escape
  (a path inside the workspace that resolves outside it).
- Function `(DenyRuleSet) Check(path string, resolution []string) error`
  where `resolution` is the symlink-resolved chain; any element hitting
  a denied root or escaping the trial workspace is an error naming the
  rule and the resolved path.
- No silent denial: every refusal carries which tool class would have
  been required to be denied, for preflight reporting.

### D3: Child environment allowlist

In `workspace.go`: `ChildEnvironment(m Manifest, environ []string)
([]string, error)`.

- Deny-by-default filter of the parent environment: keep only an
  explicit allowlist (PATH, HOME, TMPDIR, LANG, TZ, TERM,
  GOFLAGS-style build vars if needed) plus experiment-injected values
  (session root, sidecar socket, config path), each pointing inside
  the experiment run-state root.
- Reject values containing paths that resolve into hidden roots.
- Output is deterministic and sorted; a test pins exact output for a
  fixed input.

### D4: Preflight probes

New file `internal/eval/v2/preflight.go` — schema and result types
only; the executor that runs the probes arrives with the runner.

- Type `PreflightProbe{Name string, ToolClass string, AttemptPath
  string}` and `PreflightResult{Probe, Denied bool, Detail string}`.
- `PreflightReport.Validate()` requires: one probe per tool class
  (shell, file, search, symlink) per hidden root, all denied, zero
  probes skipped. A missing probe class invalidates the report; this
  is the cycle-2 false-positive guard applied to isolation.
- Contamination checks: fixture bytes identical across arm roots for
  the same task (hash comparison), stale-sidecar detection (socket
  file exists but process is gone -> error instructing cleanup), and
  process-metadata leakage check stubs (argv/env capture types the
  runner will fill).

### D5: Tests

Extend `protocol_test.go`; keep table-driven.

- Layout: build twice, assert idempotence; corrupt a staged binary
  hash, assert refusal names both hashes.
- Reset: resetting one trial leaves sibling trials' journals intact;
  journal survives a workspace reset (EV2-1 pairing).
- Deny rules: each access shape x hidden root combination denied;
  symlink escape through a workspace-internal link caught; allowed
  paths pass.
- Child env: fixed input produces the pinned sorted allowlist; a
  variable whose value points at a hidden root is rejected.
- Preflight: missing probe class fails validation; passing report
  requires all-denied.

## Constraints

- AGENTS.md standards apply (gofmt, vet, fail loudly, `%w`, table
  tests). No em-dashes in new user-facing strings.
- No filesystem writes outside a caller-supplied root in tests; use
  t.TempDir().
- macOS `/var` vs `/private/var` symlink canonicalization must follow
  the rule pinned by `f6c2bf7` (raw path identity preserved,
  canonical form used for containment checks).
- Do not modify EV2-0/EV2-1 types except additive doc comments.
- Every exported symbol names its consumer: layout -> runner (EV2
  later checkpoints), deny rules -> preflight probes + future sandbox
  wiring, child env -> runner's exec call, journal reuse -> resume.

## Acceptance gates

- go build ./... ; go vet ./... ; gofmt -l . clean.
- go test ./internal/eval/v2/ -race -count=1 green.
- Full CI green on both jobs after push.
- A fresh review finds no produced-but-unconsumed export without a
  named consumer, and confirms no hidden-root read path passes
  DenyRuleSet.Check in the test matrix.

## Out of scope

Frozen memory import (EV2-3), telemetry query/symmetry (EV2-4),
task sealing QA (EV2-5), estimators (EV2-6), CLI commands (EV2-7),
and any live execution of trials.
