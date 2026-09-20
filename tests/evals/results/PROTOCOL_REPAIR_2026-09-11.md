# Protocol repair: the stage action contract

Date: 2026-09-11.
Branch: `wip/evidence-substitution-production`. Base revision `bc7b9d5` plus
the uncommitted repair below.
Scope: the model-facing stage action contract only. No context delivery
change, no front-loading, no retention or arm change.
Status: implemented and gated. The paid retention run stays stopped until the
validation run passes.

## 1. The defects, re-verified

The experiment expected to measure expansion rounds. The model-facing
contract could not express the context request that the experiment measures.

1. `submitCodeToolDefinition` (`internal/splice/stages/code_writer.go`) declared
   only flat submit properties (`files`, `language`, `intent`, `dependencies`,
   `known_limitations`, `confidence`) with `required: [files, language, intent,
   confidence]`. It never declared `request_context`.
2. `DecodeStageAction` (`internal/splice/stages/action_envelope.go`) required
   exactly one of `request_context` or `submit_changes`.
3. `DecodeStageAction` returned the WHOLE envelope as `ProposalArgs` for the
   `submit_changes` case, but `parseCodeWriterArgs` read `files` at the TOP
   level. The advertised submit envelope was therefore also unusable.
4. `prompts/code_writer.md` said "return a context request" but named no field
   and gave no example.

Consequence: only the legacy bare flat payload worked end to end. The paid run
submitted successfully with zero context requests in 100 provider calls.

## 2. Adapter evidence for the representation

`zeroruntime.ToolDefinition.Parameters` is `map[string]any` and is passed
opaque. The adapters treat it as follows:

- OpenAI passes it straight to `parameters` (`internal/providers/openai/provider.go`).
- Anthropic passes it straight to `input_schema` (`internal/providers/anthropic/provider.go`).
- Gemini sanitizes it against an ALLOWLIST (`internal/providers/gemini/provider.go`).
  The allowlist contains `enum`, `items`, `properties`, `required`, `maxItems`,
  and `anyOf`. It does NOT contain `oneOf`.

`enum` is already used by shipped tool schemas in this package
(`provider.go`, `plan_critic.go`, `helpers.go`).

Decision: use an explicit `action` discriminator with an `enum`. It is
portable across all three adapters. `oneOf` is avoided because Gemini would
drop it. The flat submit payload stays flat, which preserves
`parseCodeWriterArgs` and the top-level `memory_disposition` handling.

## 3. How each defect was fixed

1. The schema now declares `action` (`enum` of `request_context` and
   `submit_changes`, required), a bounded `request_context` payload, and the
   existing flat submit fields. `required` is `[action]`; the decoder enforces
   the per-action condition, because a flat JSON Schema object cannot express
   it. The `request_context` payload allows at most 4 queries of type
   `read_file`, `outline`, `search`, `find_symbol`, or `get_symbol`.
2. The decoder is the single source of truth. It dispatches on `action`,
   accepts the legacy envelope form and the legacy flat payload, and rejects an
   unknown action, a missing payload, and a mixed action. `TryDecodeStageAction`
   keeps the bare-files fallback.
3. `submit_changes` is now unwrapped to its inner object before it reaches the
   stage parser, in both the declared and the legacy envelope forms. An
   envelope without a `files` array fails loud instead of decoding as an empty
   proposal.
4. The prompt now states the two actions, gives a concrete example of each,
   states the query bound, and says to report a gap in `known_limitations`
   instead of inventing source. It also now matches the host's revision-context
   instruction: a file written by an earlier iteration is re-emitted with
   `change_type: "create"` and full content, because no `base_ref` view was
   delivered for it.

## 4. Bounded handling

The bound already existed and is wired. `expandContextRound` rejects a
repeated query through `expansionLedger.Check` with an explicit error
("no new evidence would arrive"), and an exhausted budget returns
`errExpansionBudgetExhausted`. Neither path loops silently or pushes the model
to invent source. Tests were added for both.

## 5. Wording corrections

Two claims in the earlier analysis were stronger than the evidence:

- Say "no declared, supported model-facing route" instead of "unreachable by
  construction". With strict schema enforcement the envelope cannot be emitted
  as a valid tool argument; without strict enforcement a model may emit an
  undeclared field that reaches the decoder. Zero observed expansions
  therefore cannot establish that the tasks need no expansion.
- Say "not host-forced" instead of "host-impossible". Once the action is
  exposed, a model can choose it in response to missing information. Whether it
  chooses it remains model behavior.

## 6. Gate

```text
gofmt -l .                                  clean
go vet ./...                                exit 0
go test ./internal/splice/... -count=1      ok
go test ./... -count=1 -timeout 30m         exit 0, no FAIL
cd memd && go test ./... -count=1           ok
```

Note on the default timeout. `go test ./... -count=1` with the default 10m
per-package timeout fails on `internal/cli` in this environment. The failure is
a package timeout, not a test failure: `TestRunExecWorktreeMergeBackCleanupPolicy`
passes alone in 55s, and the package passes in 400s to 490s under a raised
timeout with zero failures. The package is load-dependent and unrelated to this
change, which touches only `internal/splice/stages`. Raise the timeout for full
uncached runs.

Named evidence:

- `TestSubmitCodeSchemaDeclaresBothActions` (schema exposes both actions)
- `TestCodeWriterPromptDeclaresTheActionContract` (prompt shows both examples)
- `TestDeclaredActionRequestContextRoundTrip` (advertised request shape decodes)
- `TestDeclaredActionSubmitChangesFlatAndEnvelope` (both submit shapes parse)
- `TestDeclaredActionRejectsBothNeitherAndUnknown` (fail loud)
- `TestSubmitEnvelopeWithoutFilesFailsLoud` (unwrap guard)
- `TestLegacyFormsStillDecode` (backward compatibility)
- `TestExpansionLedgerRejectsRepeatedQuery`, `TestExpansionBudgetExhaustionIsExplicit` (no-progress and bound)
- `TestProviderExpansionRoundLedgerLabelsGenerationThenExpansion` (scripted provider: generation then expansion, both accounted)

## 7. What this does not claim

The repair makes the context request reachable. It does not show that a model
will use it, and it does not show that warm runs are cheaper. Those need a
cold-only pilot first, then a separate warm-versus-cold result on the repaired
version. The previously recorded runs are not pooled with the repaired version.
