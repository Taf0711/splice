# Splice Cognition + Learning Vertical Slice — Implementation Plan (2026-09-04)

Directive: implement the cognition + learning architecture end-to-end in one
pass (repair control, diagnostic-directed repair, typed cognition graph,
exact+semantic retrieval, discovery skipping), tested and eval-measurable.
Branch: feat/cognition-vertical-slice from dev @ bdb301c.

## Reality check against the codebase (verified today)

Already exists and will be EXTENDED, not duplicated:
- Replay guard (PR #29, run-local consumed set keyed stage+StableID) — KEEP.
- Freshness: internal/splice/cognition (ClassifyFreshness, FreshnessCache,
  batched git-diff anchoring, false-fresh=0 invariant) — the graph anchors
  reuse this.
- Typed admission: internal/splice/memoryreason (StableID, budgets, trust
  counts) — node admission extends this.
- Persistence: memd SQLite sidecar (observations, run_traces, FTS5) — graph
  nodes/edges/anchors/evidence land here as a new migration; the client is
  internal/memd. No external graph/vector service.
- Repair: internal/splice/repair.go (attemptLocalRepair,
  extractFailingTests, repairStageInput) — fingerprinting + no-progress
  guard + focused payloads extend this.
- Test evidence: internal/splice/stages/test_runner.go — suite-fallback bug
  confirmed (single {suite, exit code 1} entry; failingEvidenceExcerpt never
  reaches the repair payload; no -json parsing). Fixing this is prerequisite
  for evidence-based repair.
- Trajectory: internal/splice/trajectory.go splitTestCounts — partition is
  blind because Tests has only a suite entry.
- Telemetry: schemas.InputMeta + trace accumulator — new counters extend
  this.

## Confirmed root cause for the remaining tails (evidence file:
tests/evals/results/REPAIR_EVIDENCE_FINDINGS.md)
Go test path runs `go test ./...` without -json; failure results carry one
synthesized {suite, exit code N} entry. Repair evidence is literally
["suite: exit code 1"]; failingEvidenceExcerpt output never reaches the
repair payload; splitTestCounts marks the suite entry preexisting, so
authored progress is invisible. This drives the tails and also blocks the
cognition program (evidence starvation upstream of memory).

## Track ownership (disjoint files, parallel subagents)

TRACK A — Repair intelligence (P0). Files owned:
  internal/splice/repair.go, internal/splice/stages/test_runner.go,
  internal/splice/trajectory.go, NEW internal/splice/failure.go,
  internal/splice/diagnostics.go, internal/splice/diagnostics_go.go,
  tests. Deliverables:
  A1. go test -json parsing in test runner (per-test results; suite entry
      only as fallback for compile failures; compiler output excerpt).
  A2. FailureFingerprint (typed: Kind/Command/ExitCode/Diagnostic/Symbols/
      Files; normalize temp paths, run ids, timestamps, line numbers;
      preserve distinguishing content; kinds: compile/test/static/verifier/
      command). Stable hash for comparison.
  A3. No-progress guard: per-repair-trajectory state (fingerprints seen,
      evidence seen, attempted approaches). Same fingerprint + no new
      evidence after one evidence-informed retry -> stop writing, emit
      repair_no_progress, abort trajectory. New fingerprint or new evidence
      -> continue. Bounded.
  A4. Diagnostic resolver (Go): undefined symbol, missing method, failing
      test names -> deterministic repo lookup (go/parser AST + grep via
      existing tools) -> focused evidence bundle into repair payload.
      Generic fallback passthrough.
  A5. Focused repair payload: original goal, change summary, exact failure,
      fingerprint, new evidence, attempted approaches, no-progress
      constraint. No transcript dump.

TRACK B — Cognition graph (P0). Files owned: memd/ (store schema+migration,
  store methods), internal/memd/client.go, NEW memd/store/graph*.go,
  memd/store/semantic.go, tests. Deliverables:
  B1. Tables: cognition_nodes(id, kind, claim, scope, project_path,
      status, confidence, source_run_id, created_revision,
      verified_revision, created_at, verified_at, metadata_json),
      cognition_edges(src_id, dst_id, kind),
      cognition_anchors(node_id, kind, value),
      cognition_evidence(node_id, kind, ref, detail).
      Versioned migration, backward compatible with observations.
  B2. Node kinds: fact, conclusion, decision, procedure, failure, evidence.
      Edge kinds: implemented_by, defined_in, tested_by, supported_by,
      depends_on, contradicts, supersedes, derived_from, related_to.
      Statuses: active, superseded, stale, contradicted, archived,
      ephemeral.
  B3. Exact index: anchors (file/symbol/package/test/revision) queryable
      deterministically (SQL, indexed).
  B4. Semantic index: local hashed n-gram TF cosine in SQLite (no external
      service), interface VectorIndex {Index(nodeID,text), Search(text,k)}.
      Bounded top-K. Safe fallback when unavailable.
  B5. Store + client methods: UpsertNode, UpsertEdge, GetExact(anchors),
      Neighbors(id, edgeKinds, depth, limit), SetStatus, Compact(dupes by
      kind+anchor+canonical claim hash, provenance merged), Collect
      (ephemeral+stale+unreferenced), Contradict(id, evidence).
  B6. Bounded traversal: depth<=2, node budget, edge-kind filter, active
      status only.
  B7. Freshness: anchors validated against current revision (git diff per
      anchor file; symbol existence via grep/AST at integration layer).

TRACK C — Integration + discovery skipping (me, after A+B land):
  internal/splice/stage_input.go, run.go, schemas/trace.go, NEW
  internal/splice/discovery.go, cognition bundle type, replay-guard
  interplay, telemetry counters, eval columns. Deliverables:
  C1. DiscoveryPlan {resolved_by_task, resolved_by_cognition,
      requires_freshness_check, unresolved}; discovery executes only
      unresolved; avoided reads/searches counted.
  C2. CognitionBundle (facts/decisions/procedures/failures/evidence/
      freshness/rejected) delivered through the existing MemoryBundle path
      (admission stays; replay guard stays).
  C3. Fresh-deterministic-evidence overrides cognition: contradiction ->
      mark node contradicted + persist failure node.
  C4. Telemetry counters (section 31 list) in InputMeta + attempts row;
      explainability progress line per hit.
  C5. Capture: verified-run artifacts -> fact/procedure/failure nodes with
      evidence; dedupe/compact; no raw chain-of-thought.
  C6. GC: bounded, deterministic, opportunistic at run end.

## Sequencing (today)
1. A and B dispatched in parallel now (bounded, disjoint files).
2. On return: integrate (C), run gates (gofmt/vet/test/memd/race).
3. Run the 60-attempt families eval with the integrated binary.
4. Before/after report vs baseline-2026-09-03 + after-replay-guard runs.

## Success gates (from directive)
- warm success >= cold; warm median tokens < cold or clear
  discovery-elimination evidence; no warm runaway regressions; no eval
  gaming (no family names, no answer keys).
