# SPEC: TUI Key Dispatch + Receipt Truth Fixes (bugs found in adversarial review)

Status: approved scope for two review findings on `feat/tui-workflow-surfaces`
(commits `10f35b0`, `d22a5fe`). One small commit per fix. Green gates before
each commit.

## BUG 1. Handoff keys [M]/[X]/[D] are dead on real terminals

### Evidence

- `internal/tui/worktree_review.go` `handleHandoffKey` matches
  `keyCode(msg) == 'M' | 'X' | 'D'` (uppercase).
- ultraviolet's input decoder lowercases `Code` for every letter key and
  moves the shifted letter to `ShiftedCode` + `ModShift`
  (`decoder.go:1093-1105`; the Kitty path normalizes to lowercase too).
  bubbletea v2's `Key.Code` doc states the same contract.
- Conclusion: `Code == 'M'` never arrives from a real terminal. The keys
  never fire. The acceptance tests pass only because `testKey('M')`
  fabricates `Key{Code:'M'}` directly, a shape no terminal produces.
- Overlay probes (`TestReviewProbe*`, run via `go test -overlay`) reproduced
  the dead dispatch with real key shapes and proved the synthetic shape is
  the only one that works.

### Required behavior

- With a pending preserved handoff and no blocking modal, pressing shift+m,
  shift+x, or shift+d (any case, with or without shift) dispatches the
  documented action through the SAME seams as today (`tuiMergeBackWorktree`,
  `tuiPreserveWorktree`+`tuiRemoveWorktree`, diff review open).
- Plain lowercase m/x/d while the composer is focused still must NOT
  dispatch: the handoff keys stay exit-scoped (armed only after the review
  picker resolves; the picker owns input while up). Plain letters reach the
  composer. This is today's intended behavior and must not regress.

### Fix contract

- Dispatch case-insensitively. Prefer `keyText(msg)` compared against both
  cases, or `unicode.ToLower(keyCode(msg))`. Keep the early-return contract
  of `handleHandoffKey` (false for unhandled keys).
- While a preserved handoff is armed, `updateModel` must not swallow keys
  the handoff does not handle (unchanged: `handled=false` falls through).

### Regression probes (must land with the fix)

Add to `internal/tui/acceptance_wiring_test.go` (or a sibling test file):

1. `TestHandoffKeyDispatchWithRealTerminalShapes`: for each of
   - `tea.KeyPressMsg(tea.Key{Code:'m', ShiftedCode:'M', Text:"M",
     Mod: tea.ModShift})` (legacy terminal shift+m),
   - the same for x and d,
   the dispatch fires exactly once and the right stubbed seam runs
   (`tuiMergeBackWorktree`, `tuiPreserveWorktree`+`tuiRemoveWorktree`,
   diff open). This test FAILS on the current code.
2. `TestHandoffPlainLetterDoesNotDispatch`: plain `m`/`x`/`d`
   (`Code:'m', Text:"m"`, no shift) never dispatch while the composer can
   receive them (no merge/discard/diff stub call, handoff still armed).

## BUG 2. Internal aborts project CANCELLED (staged-not-applied) receipts

### Evidence

- `internal/splice/run.go` `finishPresentation` projects `cancelled` when
  `result.Status == "aborted" && abortReason(result) != ""`.
- EVERY internal abort sets a reason through `finishWithReason`
  ("wall time exceeded" `run.go:457/468`, "reached max iterations"
  `run.go:657`, "rollback requires an isolated worktree" `run.go:546`,
  "surface_to_user" aborts `run.go:615/644`, decision aborts
  `run.go:654`).
- Overlay probes confirmed: max-iterations and wall-time aborts produce
  `Health=cancelled`, `Completion.Status=cancelled` with staged-not-applied
  semantics. This contradicts the function's own contract comment
  ("a run aborted for internal reasons still projects failed") and shows a
  false "you chose to stop" card for a genuine failure.
- The guard keys on a caller-controlled field (reason non-empty) instead of
  the property required (the user chose to stop).

### Required behavior (contract v0.5 receipts)

- `cancelled` means: the USER chose to stop (Ctrl+C / context cancel, or an
  explicit user abort decision). Staged-not-applied accounting stays.
- `failed` means: the run ended without user intent (stage failure,
  max iterations, wall time, rollback refuse, internal surface abort).
- A user-initiated abort decision arrives with `err = context.Canceled` at
  the TUI boundary (`runIterationLoop` maps user cancels to
  `context.Canceled`), so the TUI's `failedExecutionCard` already
  classifies it correctly. The presentation snapshot must tell the same
  truth.

### Fix contract

- Make the abort kind TYPED, not inferred from prose. Add an explicit kind
  to the result (or a boolean `UserAborted` / kind enum on
  `schemas.PipelineResult`) set ONLY at the sites that represent a user
  choice to stop:
  - user-abort surface decision (`agent.SurfaceToUserAbort` where the
    context is a user stop, `run.go:644`),
  - explicit user-cancel paths already mapped to `context.Canceled` (these
    bypass `finishWithReason` entirely; no change).
- `finishPresentation` projects `cancelled` ONLY for the typed user-abort
  kind. All other `aborted` results project `failed` with the reason in
  the card body.
- The reducer contract is unchanged: `Apply(RunEvent{Status:"cancelled"})`
  still produces health=cancelled. The RUNTIME stops feeding it internal
  aborts.
- No em-dashes in touched user-facing strings. STE wording in comments.

### Regression probes (must land with the fix)

Add to `internal/splice` tests:

1. `TestFinishPresentationInternalAbortsProjectFailed`: table over internal
   aborts (wall time, max iterations, rollback refuse) asserting
   `Health != cancelled` and `Completion.Status != "cancelled"`, with the
   reason preserved in the completion detail. Fails on current code.
2. `TestFinishPresentationUserAbortedProjectsCancelled`: the typed
   user-abort result projects `cancelled` with staged accounting.
3. Producer/consumer pairing: every `finishWithReason` call site maps to
   the intended kind (a small table test listing sites and kinds, so a new
   internal abort site cannot silently re-enter the cancelled path).

## Out of scope (recorded, not fixed here)

- Header worktree chip ellipsis truncation at 80 columns (DoD 18 drop-whole
  rule). Cosmetic; separate change.
- New `case 'D'` review-diff behavior itself (landed in `d22a5fe`); this
  spec only fixes its key matching.

## Gates per commit

`gofmt -l .` empty, `go vet ./...`, `go test ./... -count=1`, race sweep on
`./internal/tui/ ./internal/splice/ ./internal/presentation/`. memd module
untouched (no CI impact). Re-run goldens only if a presentation struct
changes (Bug 2 adds a field; regenerate both golden dirs).
