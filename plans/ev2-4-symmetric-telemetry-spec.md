# EV2-4 Spec: Symmetric Telemetry

Status: ready for implementation.

Source of truth: `plans/eval-v2/PROTOCOL.md` section 10 (telemetry
contract) and 8.7 (trace-versus-stream-fallback sensitivity check),
ROADMAP checkpoint EV2-4, existing schemas in `internal/eval/v2`
(through `de0d0de5`: `TelemetryRecord`, `TokenUsage`,
`ProviderRequestTelemetry`, `StageTelemetry`), and the memd trace wire
contract (`internal/splice/schemas/trace.go`: `RunOutcome`,
`TraceQueryFilter`; client `internal/memd/client.go.QueryTraces`).

Scope: one checkpoint. Schema and verification layer only. No live
sidecar spawning, no runner loop, no analysis estimators (EV2-6).

## Goal

Make the cycle-2 failure class impossible: telemetry that silently
missing or zero-filled can never produce a verdict. This checkpoint
delivers exact trace lookup by run/session identity, actual-route
verification against the manifest, a completeness gate over every
required field, and proof that the validated stream fallback is
equivalent to trace-store data. The rule: missing values are never
zero-filled; they invalidate.

## Deliverables

### D1: Exact trace lookup schema

New file `internal/eval/v2/telemetry_lookup.go`.

- `TraceLookupKey{ ExperimentID, TaskID, Arm string, RepetitionID int,
  EnvironmentBlock int }`. This is `TrialKey` plus run/session:
  prefer embedding `TrialKey` plus `RunID`, `SessionID string`.
- `func LookupFilter(k TraceLookupKey) schemas-compatible filter spec`.
  Constraint: this package cannot import memd or splice schemas.
  Therefore define the filter struct here with identical field names
  and JSON tags, and add a pairing test contract documented for the
  runner checkpoint: a test in the runner package must assert
  `v2.TraceQuerySpec` round-trips through `memd.Client.QueryTraces`
  unchanged. If memd's filter gains fields, that pairing test fails CI.
- Exactness rules: no prefix matching, no repo-scoped fallbacks. A
  query returns zero or exactly one `RunOutcome` per attempt; more
  than one is an error naming the duplicate identities.

### D2: Route verification

New file `internal/eval/v2/route_verify.go`.

- `VerifyRoutes(actual []StageRoute, m Manifest, k TraceLookupKey)
  error`:
  - every stage in the actual set must exist in the manifest's
    locked routes with byte-equal provider, model, and
    reasoning-effort;
  - a stage present in the manifest but absent from actual is an
    error (route drift);
  - an unexpected extra stage is an error;
  - errors name the stage, expected route, and observed route.
- Rationale comment: routes are captured from the running system, not
  copied from configuration (EV2-0 manifest contract); this function
  is where capture meets lock.

### D3: Completeness gate

New file `internal/eval/v2/completeness.go`.

- `CheckCompleteness(r TelemetryRecord) CompletenessReport` where the
  report lists every violated requirement as a typed entry
  `{Field string, Rule string}` rather than failing fast, so a single
  pass reports all gaps.
- Reuse `TelemetryRecord.Validate()` as the floor; this layer adds
  claim-run-specific requirements on top:
  - `Source` must be present (already enforced);
  - dispositions complete flag true (already enforced);
  - pricing coverage must be `full` for claim mode — `partial` or
    `none` invalidates the record (never rescaled to zero);
  - every stage fingerprint set non-empty (already enforced via
    StageFingerprints.Validate).
- `func (r CompletenessReport) Complete() bool` and
  `InvalidReasons() []string` sorted canonically.
- Zero-fill detector: `RejectZeroFilled(r TelemetryRecord) error`
  flags suspicious all-zero patterns that suggest silent zero-filling
  upstream (all token fields zero while wall time > 0 and stages ran)
  — this is the explicit cycle-2 guard. It is advisory at this layer
  and becomes binding in the runner.

### D4: Fallback equivalence schema

New file `internal/eval/v2/fallback_equiv.go`.

- Protocol 8.7 requires comparing trace-only token data against the
  validated stream fallback. Define:
  - `FallbackPair{ TrialKey, FromTrace TokenUsage, FromStream
    TokenUsage, Match bool }`;
  - `CompareFallback(a, b TokenUsage) (bool, []string)` — per-field
    equality with canonical field ordering in the diff list;
  - sensitivity-check aggregation type `FallbackEquivalenceSummary{
    Pairs int, Matched int, Mismatches []FallbackPair }` with
    `Validate()` requiring matched == pairs unless mismatches listed.
- Semantics decision to document in code: equivalence means exact
  uint64 equality per token field. Tolerance-free by design; any
  mismatch feeds the EV2-6 sensitivity analysis, not silent
  acceptance.

### D5: Tests

Extend `environment_test.go` or add `telemetry_test.go`.

- Lookup: filter construction carries full identity; duplicate result
  detection logic unit-tested.
- Routes: match passes; provider drift, model drift, effort drift,
  missing stage, extra stage each fail with named fields.
- Completeness: valid record passes; each required-field violation
  appears in InvalidReasons; partial pricing invalidates in claim
  shape; zero-fill detector fires on the all-zero-but-time pattern
  and stays quiet on legitimately zero-cost local-model runs (wall
  time > 0, tokens > 0, cost = 0 is legitimate).
- Fallback: equal pairs match; single-field drift produces exactly
  one diff entry with canonical field name; summary validation.

## Constraints

- AGENTS.md standards; gofmt/vet clean; fail loudly; `%w`; table
  tests; t.TempDir() where filesystem is touched (it mostly is not).
- Package stays dependency-free of memd/providers/cli/splice-schemas.
  Where a splice-schema type would be natural, define a minimal
  mirror here and document the pairing-test obligation.
- No em-dashes in new user-facing strings.
- Do not modify existing EV2 types except additive doc comments.
- Every export names its consumer: lookup -> runner trace fetch,
  VerifyRoutes -> runner pre-execution assertion, completeness ->
  runner post-attempt gate + report, RejectZeroFilled -> binding at
  runner level, fallback types -> EV2-6 sensitivity inputs.

## Acceptance gates

- go build ./... ; go vet ./... ; gofmt -l . clean.
- go test ./internal/eval/v2/ -race -count=1 green.
- Full CI green on both jobs after push.
- Fresh review confirms: no code path can convert missing telemetry
  into zero or into a verdict input; route drift cannot pass; the
  zero-fill detector demonstrably fires on the cycle-2 pattern.

## Out of scope

Analysis estimators and power simulation (EV2-6), task QA/sealing
(EV2-5), CLI commands (EV2-7), live sidecar wiring and the pairing
test implementation (runner checkpoint after EV2-7), and anything
claim-bearing.
