# Cognition recovery baseline: revisions, feature inventory, causal map

Date: 2026-09-15.
Handoff: `splice-docs/raw/sources/Splice_Cognition_Recovery_Agent_Handoff.md`
(reviews `4777206`; this note records a newer baseline, see below).
Status: P0 deliverable. Facts are code-referenced at the pinned revision.

## 1. Pinned revisions

| component | revision | dirty paths |
| --- | --- | --- |
| product + harness (module `github.com/Taf0711/splice`) | `25d207d` (`25d207dbdd5b5497104c1b923f75214dc25d0c7c`) | 0 |
| sidecar (`memd/`, nested module, same tree) | `25d207d` | 0 |

The handoff reviewed `4777206`. One newer product commit exists:
`25d207d feat(splice): admit evidence-vouched test-source symbols as
locatable`. Per handoff section 6.1 that design must be revised in P2:
the handoff requires the test-helper resolver to be shared by every arm
and forbids warm-exclusive capabilities. See defect D3 below.

## 2. Feature inventory of the binary at this revision

Read from implementations, not comments. Wiring status is the important
column.

| feature | code | wired? |
| --- | --- | --- |
| action contract (`request_context` / `submit_changes`) | `stages/code_writer.go:300`, `stages/action_envelope.go` | advertised; reliability unproven, see P1 |
| graph capture | `discovery.go` (capture path) | yes, on this branch |
| reanchor compatibility | `memd/client.go` `ReanchorGraphByIDs`; harness `runner.go` | yes |
| retrieval + freshness | `discovery.go` (`planStageDiscovery`, `admitFreshNodes`) | yes |
| current-source materialization | `source_view.go`, `symbol_extract.go` | **BUILT, UNWIRED: zero production callers** |
| usage ledger | `schemas.PipelineUsageRecord` | yes |
| evidence substitution | `evidence_substitution.go` | default OFF (`SPLICE_EVIDENCE_SUBSTITUTION`) |
| exemplar delivery | `exemplar_mode.go` | default `both` |
| proposal bases | `stages/proposal_bases.go` (`RecordProposalBases` at `run.go:1456`) | yes |

## 3. Causal map: retrieval to provider-visible source view

Link 1, retrieved node to selected source query.
`stage_input.go:297` calls `buildEvidencePlan` (`evidence_plan.go:113`)
with the delivered fresh nodes. Needs derive in `deriveContextNeeds`
(`needs.go`), admission runs in `admitCandidates`
(`admission.go:411`), and `buildWarmPlan` (`substitution.go:192`)
removes the cold operations the admitted records replace. When
`SubstitutionCount() > 0`, `run.go:1329-1335` sets
`stageOpts.OverrideContextRequest` from
`EvidenceRequestFromOperations` (`evidence_plan.go:43`).

Link 2, where the query executes. `stage.go:50` carries the override;
`helpers.go:35` returns it instead of the default. `code_writer.go:31-40`
emits it as the stage's context handshake WITHOUT a model call when
`input.Context == nil`. The pass loop fulfills it in `expandContextRound`
(`run.go:1417`) through `FulfillContextRequest` (`context.go:22`) using
the guarded deterministic tools: `fulfillGetSymbol` (`context.go:252`),
`fulfillReadFile` (`context.go:101`), `fulfillListFiles`
(`context.go:72`), `fulfillSearch` (`context.go:191`).

Link 3, where the result enters the first provider request.
`expandContextRound` merges the bundle into `input.Context`
(`run.go:1448-1452`, expansions add and never remove), records the
delivered items as proposal bases (`run.go:1456`), and re-invokes the
stage. `code_writer.go:53` selects the context into `RelevantContext`,
which `code_writer.go:65` marshals into the user prompt of the next
provider request. The handshake itself is provider-free, so a prefetch
mechanism that adds a query to this request delivers source before the
first model request.

## 4. Wiring defects (the P2/P3 work, not limits of memory)

D1. Delivered views are recorded, never fetched. `buildWarmPlan`
(`substitution.go:213`) records `DeliveredViews`, but `run.go:1330`
passes only `Warm.Operations` to `EvidenceRequestFromOperations`, and a
substituted operation is removed from that list. The host therefore does
not fetch the substituted subject's source, and the model receives the
record prose without the declaration body. This is the concrete defect
behind "fact delivered, read still failed needing the source."

D2. The current-source layer is unwired. `ToolRunnerSourceReader`,
`BuildSourceView`, and `extractGoSymbol` (`source_view.go:137`,
`source_view.go:216`, `symbol_extract.go:64`) have zero production
callers. D1's repair should route through them, which keeps permission
and registry paths intact.

D3. The trace's per-round context telemetry is disconnected.
`expandContextRound` (`run.go:1417`) never calls `SpendRound`
(`expansion_budget.go:68`), so `RoundsUsed()` stays 0 and every round
records through the initial-handshake path (`run.go:1455`). The ledger's
`context_round` attribution (`registry.go:99`) is the wired per-request
round signal, and it is what the measurement reads.
D4. `25d207d` gates the test-file symbol index on memory-vouched files,
which gives warm an exclusive capability. Handoff section 6.1 requires
the shared resolver to serve every arm and section 7.5 forbids inventing
a cold operation to pass the `SubstitutionCount()` gate, which is how
that commit makes the chain fire. P2 revises this: the test-source index
becomes a shared resolver (cold included), with decoy protection by
ambiguity and package identity, and prefetch is represented as its own
typed decision rather than a manufactured eliminated operation.

## 5. The measured baseline the mechanism must beat

From the committed retention raw stream and ledger
(`wc-retention-confirm2-20260913T011502Z-try0`, read attempt, warm):

| spend source | provider requests | input plus output tokens |
| --- | ---: | ---: |
| generation | 2 | 10,216 |
| expansion | 2 | 11,827 |
| format retry | 4 | 24,203 |
| total | 8 | 46,246 |

Format retries are 52.3 percent of the attempt. The verifier failed. The
removal target for the prefetch mechanism is the expansion source: 2
provider requests and 11,827 tokens spent discovering a dependency the
retained record already located. The P1 repair targets the format-retry
source, which is the larger half.
