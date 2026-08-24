# EV2-3 Spec: Frozen Memory Policy

Status: ready for implementation.

Source of truth: `plans/eval-v2/PROTOCOL.md` sections 5 (frozen memory
corpus, snapshot contract, retrieval audit), ROADMAP checkpoint EV2-3,
existing schemas in `internal/eval/v2` (through `0974877a`), the memd
sidecar wire contract (`memd/protocol.go`, client in
`internal/memd/client.go`), and the reasoning-memory contract shipped in
EV2-P0 (`8a4f9ae`, `e9c0bd8`, `4f2e189`, `edad633`).

Scope: one checkpoint. Snapshot schema + loader, write-denial policy,
and deterministic selection verification. Schema and policy layer only:
no live sidecar spawning, no trial execution, no corpus content. The
actual corpus build is EV2-10 work; this checkpoint makes it loadable,
verifiable, and impossible to write to.

## Goal

Make the three-arm frozen memory contract real before any snapshot
exists: a typed snapshot format that preserves stable delivered IDs and
content hashes, an import path that mounts read-only, an enforcement
layer where trace writes succeed and observation/exemplar writes are
rejected in claim mode, and deterministic verification that selected
and post-compaction delivered IDs match the sealed selection audit.

## Deliverables

### D1: Snapshot schemas

New file `internal/eval/v2/memory_snapshot.go`.

- `MemorySnapshot` containing, per protocol section 5.2:
  - `ManifestJSONSHA256 string` (hash of the embedded manifest copy);
  - `Items []SnapshotItem`;
  - `CorpusProvenanceSHA256 string`;
  - `AdmissionPolicySHA256 string`;
  - `SelectorSHA256 string`;
  - `IDMap map[string]string` (snapshot item ID -> delivered ID;
    required only when import rekeys rows, else empty);
  - `SnapshotSHA256 string`.
- `SnapshotItem`: `DeliveredID`, `ContentSHA256`, `Kind`
  (`observation` or `exemplar`, closed enum), `SourceTaskID`,
  `RepositoryClass`, `CreatedAtRFC3339`, `FreshnessLabel`
  (`current`, `stale`, `conflict`; audit label only), and a
  provenance blob that must not contain hidden answers.
- Validation rules:
  - every `DeliveredID` unique and non-empty, matching the
    `observation:<row-id>` shape or the exemplar contract shape;
  - every `ContentSHA256` valid sha256 hex;
  - `IDMap` bijective when present; a rekeyed snapshot without a map
    is invalid;
  - no holdout task IDs may appear in any item's source fields (the
    holdout task ID list is passed to validation as an argument);
  - placebo pool items are tagged so arm assembly can distinguish
    them (a `PoolMembership []string` field accepting
    `relevant` / `placebo`).
- Canonical JSON encode/decode with byte-stable round-trip test.

### D2: Selection audit schema

In the same file or `selection_audit.go`.

- `SelectionAudit` recording expected outcomes per task:
  `{ TaskID string, ExpectedSelectedIDs []string,
  ExpectedPostCompactionIDs []string, RetrievalMiss bool }`.
- Rules: task IDs unique; a retrieval-miss entry must carry empty
  expected-selected lists; no entry may reference a task absent from
  the manifest's task set (validated against `[]TaskSpec`).
- `selection_audit.json` is immutable once written; provide
  `AuditSHA256(a SelectionAudit) string` for locking.

### D3: Import with read-only mounting semantics

Function `ImportSnapshot(data []byte, m Manifest)
(ImportedSnapshot, error)`:

- verifies `SnapshotSHA256` matches recomputed content hash over all
  fields except itself (define the canonical preimage explicitly);
- verifies `CorpusProvenanceSHA256` equals the manifest's
  `CorpusProvenanceSHA256`;
- returns `ImportedSnapshot` exposing only read accessors plus the
  workspace path where a caller materialized it;
- documents (and tests) that callers must place the file read-only
  on disk; the type refuses mutation by construction (no exported
  mutators).

### D4: Write-denial policy

New file `internal/eval/v2/memory_policy.go`.

- `MemoryWritePolicy` with mode from `RunMode`:
  - development mode: observation/exemplar upserts allowed;
  - claim mode: denied.
- `func (p MemoryWritePolicy) CheckUpsert(kind string) error` —
  returns an error naming kind, mode, and rule id
  `memory_write_denied` in claim mode; nil otherwise. Trace writes
  (`UpsertTrace`, `QueryTraces`) are never gated by this policy.
- A thin adapter seam `MemoryStorePolicyAdapter` describing the two
  memd client calls that must be gated (`Upsert`, `MarkReviewed`)
  without importing memd — this package stays dependency-free per
  EV2 constraints; the wiring lands with the runner. Document the
  pairing: if memd gains another write endpoint, a pairing test in
  the runner checkpoint must fail CI until the policy covers it.

### D5: Deterministic selection verification

New file `internal/eval/v2/selection_verify.go`.

- `VerifySelection(actual SelectedDelivery, audit SelectionAudit,
  taskID string) error` where `SelectedDelivery` carries observed
  `SelectedIDs` and `PostCompactionIDs`.
- Comparison is exact-set, order-insensitive, and reports symmetric
  difference members in the error, sorted canonically. A mismatch is
  an infrastructure failure (rule `invalidation`), never silently
  tolerated.
- Property tests: matching sets pass; each direction of missing/extra
  fails with named IDs; retrieval-miss tasks require both lists empty.

### D6: Tests (extend environment_test.go or add memory_policy_test.go)

- Snapshot: valid round trip; duplicate delivered ID rejected; bad
  hash rejected; rekeyed-without-map rejected; holdout-ID leakage
  rejected; placebo/relevant pool tagging validated; canonical JSON
  byte-stability.
- Audit: miss entries need empty selections; unknown task rejected;
  hash stable.
- Policy: claim-mode upsert denial names rule id; development-mode
  allowance; trace paths unaffected.
- Verification: exact-set semantics including both-direction drift;
  canonical sorted diff output.

## Constraints

- AGENTS.md standards; gofmt/vet clean; fail loudly with named
  inputs; `%w` wrapping; table-driven tests; t.TempDir() only.
- This package still imports nothing from memd, providers, or cli.
- No em-dashes in new user-facing strings.
- Do not modify existing EV2 types except additive doc comments.
- Every export names its consumer: snapshot -> runner import (EV2-7),
  policy -> runner's memory-store wrapper, VerifySelection ->
  post-run telemetry check (EV2-4 pairs with this via
  DeliveredMemoryIDs), audit hash -> manifest locking at EV2-10.
- Honor the standing rule: if a produced field has no consumer yet,
  name the future consumer in its doc comment or do not ship it.

## Acceptance gates

- go build ./... ; go vet ./... ; gofmt -l . clean.
- go test ./internal/eval/v2/ -race -count=1 green.
- Full CI green on both jobs after push.
- Fresh review confirms: no write path exists from ImportedSnapshot;
  claim-mode denial cannot be bypassed by kind-string variation;
  selection diffs always name the offending IDs.

## Out of scope

Building the actual training-only corpus or placebo pool (EV2-10),
selector implementation, telemetry symmetry (EV2-4), live sidecar
process management (EV2-2 already owns the socket layout; process
wiring is the runner's), and anything claim-bearing.
