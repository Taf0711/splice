# EVAL-V0 C — Cognition Families: Design + Definitions

Deliverable 41.C of the evaluation-first handoff: 10 cognition families,
each with a precursor task A, a target task B, and a seeded observation
that makes the WARM arm measurably advantaged. Cold arm runs B with an
empty memory store; warm arm runs B against memory containing A's
observation. Only memory state differs (handoff section 46).

## How a family works mechanically (verified in code)

1. Task A runs; the pipeline persists an observation via
   `persistObservation` -> memd `/upsert` with a `topic_key`.
2. Task B's stage input derives keys via `cognition.DeriveKeys` from
   PriorChangedFiles + verification commands + strict intent tokens.
3. A direct hit occurs when B's derived key equals A's stored topic_key
   AND the freshness gate classifies the anchor FRESH (C1b).
4. On a hit, the warm arm skips broad Search + exemplars and receives
   the seeded cognition; the cold arm falls back to broad Search.

Therefore every family below pins: the exact `topic_key` (one of
`file:<path>`, `symbol:<path>#Sym`, `package:<pkg>`), the anchor path
the freshness gate diffs against, and a target prompt that strictly
mentions that anchor so `DeriveKeys` derives the matching key.

## Schema

`cognition-families.json`: array of families, each:

```json
{
  "id": "fam-01-password-reset-session-invalidation",
  "category": "state-mutation-rule",
  "precursor": {
    "task": "tasks-a/01.md",
    "topic_key": "file:internal/auth/session.go",
    "anchor": "internal/auth/session.go",
    "observation": {
      "title": "Password reset must invalidate sessions",
      "content": "Password reset MUST invalidate the affected user's
                  sessions by deleting every session id for that user
                  from the Store. Forgetting this re-opens the account
                  on a stolen device. Implemented in session.go via a
                  user-scoped delete sweep.",
      "owner_agent": "code_writer",
      "memory_type": "invariant",
      "scope": "project",
      "visibility": "shareable"
    }
  },
  "target": {
    "task": "tasks-b/01.md",
    "derived_key_expectation": "file:internal/auth/session.go",
    "verifier": "hidden test proving admin reset also invalidates sessions"
  },
  "expected_warm_advantage": "target mentions the invariant; warm hits the
    seeded observation via file:internal/auth/session.go; cold must
    rediscover the invalidation requirement from the verifier failure"
}
```

## The 10 families

| # | id | category | topic_key (A -> B shared) | precursor | target |
|---|----|----------|---------------------------|-----------|--------|
| 1 | password-reset-invalidation | state-mutation-rule | file:internal/auth/session.go | fix user password reset to invalidate sessions | add ADMIN password reset with the same invalidation semantics |
| 2 | session-ttl-source-of-truth | config-convention | file:internal/auth/session.go | make the session TTL configurable via env | make the sweep interval use the same config source |
| 3 | store-delete-idempotence | api-convention | file:internal/auth/session.go | make Delete idempotent and document it | add DeleteAll-for-user with the same idempotence contract |
| 4 | json-envelope-convention | serialization-format | file:internal/auth/session.go | give Session stable JSON field tags | add the same tags to the new AdminSession response type |
| 5 | handler-error-mapping | error-handling-convention | file:internal/auth/session.go | map store errors to 404/409/415 in handlers | map the new admin endpoints' errors with the same table |
| 6 | race-safety-rule | architecture-fact | file:internal/auth/session.go | audit and fix store locking (RLock for reads) | add the new List endpoint under the same locking discipline |
| 7 | probe-test-style | test-convention | file:internal/auth/session_test.go | add table-driven expiry tests | add table-driven tests for the new admin delete |
| 8 | go-mod-dependency | package-dependency | file:go.mod (via changed files) | pin the module toolchain version | add a dependency that must respect the same toolchain |
| 9 | healthz-schema | api-convention | file:internal/auth/session.go | extend /healthz with a version field | extend /healthz with uptime under the same schema rule |
| 10 | id-validation-rule | security-constraint | file:internal/auth/session.go | add session id validation at the store boundary | apply the same validation to the new admin lookup path |

All 10 share the session-service fixture (same as taskset-v0). Families
2, 8, 9 note: anchors unchanged between A and B by design, so freshness
gates FRESH (the fair case); a regime-change variant belongs to
EVAL-V2's longitudinal suite, not V0.

## What is authored where

- `tests/evals/cognition-families/cognition-families.json` — the manifest
  (this file's content, machine-readable).
- `tests/evals/cognition-families/tasks-a/*.md` — precursor prompts.
- `tasks-b/*.md` — target prompts (with hidden verifier commands in
  sibling `verifiers/`).
- `seed-observations/*.json` — the exact MemoryObservation payloads to
  upsert before the warm run (topic_key set, ContentSHA256 computed by
  the harness, per v2 SnapshotItem rules).

## Honest scope notes

- The runner that executes cold/warm pairs end-to-end is NOT this
  deliverable; `internal/eval` v1 + v2 already contain the paired-arms
  machinery and memory-snapshot governance. Wiring the runner to these
  family definitions is one command-layer task, deliberately separate
  so the definitions can be human-reviewed first (section 34 discipline).
- Live-model execution (10 families x cold/warm x 3 rollouts = 60 runs)
  is owner-gated spend.
