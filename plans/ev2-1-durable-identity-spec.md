# EV2-1 Spec: Durable Identity for Eval v2

Status: ready for implementation.

Source of truth: `ROADMAP.md` Track EV2 checkpoint EV2-1, protocol
`plans/eval-v2/PROTOCOL.md`, schemas `internal/eval/v2` (commit `698bc92`
on `public/dev`).

Scope: one checkpoint, one commit, green CI. Schemas and deterministic
logic only. No scheduling daemon, no provider calls, no persistence
engine beyond what this checkpoint owns.

## Goal

EV2-0 gave the experiment typed identities (`TrialKey`, `Schedule`,
locked `Manifest`). EV2-1 makes those identities durable across crash,
replay, and resume:

1. Generate the locked Latin-square schedule deterministically from the
   protocol seed.
2. Persist idempotent trial keys before any cleanup or execution step.
3. Resume without duplicates: a replayed or crashed run must produce the
   same schedule and never re-issue an already-persisted trial.

## Deliverables

### D1: Deterministic Latin-square schedule generation

New file `internal/eval/v2/schedule_gen.go`.

- Function `GenerateSchedule(p Protocol, taskIDs []string) (Schedule, error)`.
- Assignment of tasks to environment blocks follows a balanced Latin
  square over arms per repetition, so each arm appears once per block
  row and every task-arm pair still occurs exactly `Repetitions` times.
- Randomization derives only from `p.Seed`. Use `math/rand/v2` with
  `rand.New(rand.NewPCG(seed, derived))`; derive the second word from
  the experiment ID with FNV-1a so two experiments with equal seeds do
  not share stream alignment. No global rand, no time, no map iteration
  order anywhere in the output path.
- Output must be byte-stable for equal inputs: same protocol plus same
  task IDs produce identical `Schedule.Trials` ordering.
- Every generated trial passes `TrialSpec.ValidateFor(p)`; the full
  result passes `Schedule.CompleteFor(p, taskIDs)`.
- Environment-block pairing rule from EV2-0 holds: both members of a
  paired comparison within one task/repetition share one
  `EnvironmentBlock`.
- Reject duplicate task IDs and task IDs that collide with arm names
  or empty strings; name the offending input.

### D2: Trial-key journal (idempotent persistence)

New file `internal/eval/v2/journal.go`. Pure schema + codec layer; the
file-system store arrives with EV2-2's isolation work if needed there.

- Type `TrialJournal` with an append-only `[]JournalEntry`:
  `{ Key TrialKey, PersistedAt string (RFC3339), Status string }` where
  status is `scheduled`, `started`, or `completed` (closed enum).
- Append is idempotent on `TrialKey`: appending an identity that
  already exists at an equal-or-earlier status is a no-op; a status
  regression (completed -> started) is an error naming the key.
- `Validate()` rejects duplicate keys, unknown statuses, malformed
  timestamps, and entries whose keys fail `Validate()`.
- Canonical JSON encoding with stable field order; a round-trip test
  pins encode(decode(x)) == x.
- Function `MissingTrials(j Journal, s Schedule) []TrialKey` returns
  scheduled cells absent from the journal, sorted canonically. This is
  the resume primitive: a resumed run computes this set and continues;
  empty means done.

### D3: Property and adversarial tests

Extend `protocol_test.go` rather than adding many new files.

- Schedule property tests: completeness, exact-count coverage,
  block-balance (each arm once per block), determinism under repeated
  generation, and divergence under a changed seed or task order.
- Crash/replay tests: generate a journal from a partial run, simulate
  a crash after k completions, regenerate the schedule, assert
  `MissingTrials` shrinks monotonically and contains no duplicates.
- Duplicate-resume test: replaying all entries produces zero missing
  trials and zero errors.
- Status-regression rejection test.
- JSON round-trip tests for `TrialJournal` and `Schedule`.

## Constraints

- Follow AGENTS.md: gofmt clean, vet clean, table-driven tests,
  fail loudly with named inputs, `%w` wrapping.
- Do not modify EV2-0 types except to add doc comments. If D1 needs a
  helper exported from schedule.go (for example replacing the
  hand-rolled `itoa` with `strconv.Itoa`), that replacement is in
  scope and welcome.
- No dependency on memd, providers, TUI, or filesystem in this package.
- Docs: append an EV2-1 section note to MEMORY.md entry style used by
  prior checkpoints (one entry, dated, commits + CI referenced) only
  after CI green, in the same commit pattern as prior checkpoints.

## Acceptance gates

- `go build ./... && go vet ./... && gofmt -l .` clean.
- `go test ./internal/eval/v2/ -race -count=1` green.
- Full root `go test ./...` green in CI (the local agenteval timeout
  flake does not count; reference the CI run).
- memd module untouched; its job stays green.
- A fresh review finds no produced-but-unconsumed field (project's
  dominant defect class). Every new exported type names its consumer:
  `GenerateSchedule` consumes Protocol+tasks, the manifest consumes the
  schedule via `CompleteFor`, `MissingTrials` consumes the journal for
  the future runner (EV2-7 CLI).

## Out of scope

Isolated environments, hidden-root denial, memory snapshots, telemetry
collection, estimators, CLI commands, and anything claim-bearing.
Those are EV2-2 onward.
