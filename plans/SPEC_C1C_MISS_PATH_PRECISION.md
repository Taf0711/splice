# SPEC: Track C1c — Miss-Path Precision

Source of truth: `SPLICE_MEMORY_RETRIEVAL_FRESHNESS_RESEARCH_REPORT.md`
sections 27, 28, 29, 30, 31, 32 (report wins where this spec disagrees).
Branch: `feat/c1c-miss-path-precision` (worktree `~/Documents/splice-c1c`),
base = `dev` at `43a7766` (C1b). One deliverable per commit, gates green
before the next. Owner stop available after any commit.

## Goal

The miss path (broad FTS Search + exemplars) currently delivers up to 5
observations (count-capped, rank-ordered by BM25 only) plus 3 exemplars,
regardless of relevance or size. C1c replaces count-based admission with
rank-then-budget admission: FTS generates candidates, a deterministic
reranker orders them by multiple features, and a token budget admits only
the top prefix. Expected outcome: fewer delivered tokens per miss at equal
or better utility. The report calls this "likely largest next token
reduction" (section 46).

## Non-goals (report guardrails)

- Keep SQLite FTS5 (section 26). No go-git, no libgit2, no trigram/prefix
  index changes (section 31: only after real miss telemetry proves need),
  no neural reranking (section 51.11).
- No provider/LLM involvement anywhere in this track.
- The C0 direct-hit fast path is untouched: it runs before the miss path
  and its behavior must not change.

## Current seam (facts, verified in code)

- `prepareStageInput` (`internal/splice/stage_input.go:221`): direct path,
  else `p.Memory.Search(newMemoryQuery(...))` + exemplars +
  `memoryreason.Admit`.
- `newMemoryQuery` (`internal/splice/memory.go:36`): Limit 5, 200-rune
  intent as the FTS query.
- `memoryreason.Admit` (`memoryreason.go:54`): metadata filters, then
  count caps (5 obs / 3 exemplars), 500-rune truncation.
- `memd` store Search orders by FTS5 `rank` (BM25). The wire client
  (`internal/memd/client.go:143`) drops the rank; the sidecar protocol
  already carries it (`memd/protocol.go:350`).
- Token estimator: `bytesPerTokenEstimate = 4` over JSON bytes
  (`stage_input.go:30`).

## Deliverables

### D1 — Retrieval metadata (rank surfaces to the orchestrator)

- `memd` server `/search` response gains per-observation `rank` (BM25
  value, negative, more negative = more relevant). Wire only: no schema
  change, no behavior change.
- `internal/memd.Client.Search` parses rank into a parallel field on a new
  `RankedBundle` type (bundle + `[]float64` ranks aligned by index), so
  `schemas.MemoryObservation` stays storage-pure. Old sidecars that omit
  rank still work: ranks default to zero and D2 must treat "no rank
  information" as a single tie group (order preserved).
- `MemoryStore` interface gains no method; `Search` keeps its signature.
  The rank-aware result is additive: callers that do not need ranks keep
  working.

### D2 — Deterministic reranker (`internal/splice/memoryrank/`)

New package, stdlib-only, no LLM, no embedding (report section 28).

- Input: candidate bundle (with optional ranks), the stage's derived
  cognition keys (reuse `cognition.DeriveKeys` output), the request
  intent, project root, stage name, now unix.
- Tokenization (report section 27): identifier-aware split of
  CamelCase, snake_case, path segments, and dotted names into components;
  the exact original identifier is always retained. Applied to intent,
  topic keys, titles, and observation content for feature computation
  only (no index changes).
- Features (all 0/1 or bounded [0,1], deterministic):
  exactTopic (observation TopicKey equals a derived key), exactPath
  (path token appears in content/title), identifierOverlap (Jaccard
  over component sets, bounded), sameStage (SourceStage equals
  requesting stage), provenance (pinned > review-cleared > plain),
  confidence (normalized), recency (updated_at decayed, bounded).
  Freshness is NOT a reranker feature here: the miss path has no
  per-observation freshness truth, and fabricating one violates fail-
  closed. It stays a direct-path concern.
- Score = fixed weight sum (report section 28 shape). Weights live in one
  table with a comment that they are initial values to be benchmarked,
  not truth.
- Ties: stable order by (score desc, original rank asc when present,
  original index). Same input always yields the same order.
- Output: candidates in new order with scores attached (for traces).

### D3 — Token-budget admission (`memoryreason`)

- `Admit` keeps all metadata filters and dedupe EXACTLY as-is (fail
  loudly on invalid; counts preserved).
- The count cap becomes a prefix rule over the reranked order: admit
  observations from the top until the observation token budget is
  exhausted; a hard MaxObservations ceiling (5) remains as a bound, not
  the goal. Budget: 300 tokens for observations (report section 29
  example), measured with the existing 4-bytes-per-token estimator over
  each item's JSON. An item that does not fit is skipped; the next
  smaller item may still fit (first-fit over the ranked order), then
  admission stops after the first subsequent non-fit... no: simpler and
  deterministic — admission walks the ranked list once and admits every
  item that fits the remaining budget, up to the ceiling. One pass, no
  backtracking.
- Exemplars: same one-pass rule with their own budget (120 tokens) and
  ceiling (3).
- Rejection counts gain `OverBudget` alongside `OverLimit`.
- Direct-path admission keeps its existing behavior (fresh direct hits
  bypass the reranker; their ranking is topic-exactness already).

### D4 — Exemplar ablation modes + benchmark

- Config via environment (`SPLICE_EXEMPLAR_MODE`): `both` (default,
  current behavior), `obs-only`, `exemplar-only`, `none` (report
  section 30). Unset = `both` = today's behavior. Invalid values fail
  closed at startup with a named error (no silent default).
- Emission: the ablation mode is recorded on the trace memory lookup
  record so benchmarks can group by mode.
- Benchmark: 4-mode cold-path comparison on the eval fixture corpus
  (tokens per run, tool calls, verified success, latency). Results
  recorded in the C1c track wiki page before any default flips. The
  default does NOT change in this track even if ablation favors it:
  that decision is owner-gated with the numbers in front of the owner.

## Regression discipline (per repo rules)

- Reranker: property tests (same input = same order; empty/nil inputs
  never panic; scores in [0,1] feature bounds), table tests over
  fabricated corpora (no provider, milliseconds), a stable-order test
  across 1000-shuffle runs of the same input.
- Token admission: exactness tests (budget boundary admits or skips
  deterministically; zero-budget admits nothing; huge budget admits up
  to ceiling), pairing test that the estimator used for admission is the
  same estimator compaction uses.
- Producer/consumer pairing: memd sends rank iff store returns it; the
  client parses it; the reranker treats missing rank as a tie group. A
  probe with an OLD sidecar response (no rank fields) must rerank
  identically to a zero-rank new response.
- Mock at the provider seam: extend the fake MemoryStore tests in the
  C0 matrix; A-J outcomes must stay byte-identical under `both` mode.

## Gates per commit

gofmt, vet, `go test ./...` (root), memd module tests for D1,
`-race` on touched packages. Goldens unaffected (no presentation
changes). Benchmark runs are local and not CI-gated (per repo policy,
slow behavioral checks stay out of CI).

## Commit sequence

1. `feat(memd): expose FTS rank on /search` (D1, memd + client)
2. `feat(splice): deterministic memory reranker` (D2)
3. `feat(splice): token-budget memory admission` (D3)
4. `feat(splice): exemplar ablation modes` (D4 config)
5. `test(splice): C1c ablation benchmark + results` (D4 bench, wiki page)

Stop-and-evaluate after commit 5 per report section 53. Do not proceed
into C1d without owner direction after seeing C1c results.
