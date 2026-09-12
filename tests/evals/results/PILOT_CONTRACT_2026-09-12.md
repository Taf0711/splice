# Cold-only pilot: is the repaired request_context contract usable by a real model?

Date: 2026-09-12.
Branch: `wip/evidence-substitution-production` at `0508a3c` (working tree also
held the test-harness changes in section 8; no pipeline or binary source was
changed).
Scope: reachability check only. No warm arm, no retrieval-only arm, no
retention run, no evidence substitution, no warm-versus-cold comparison.
Status: RUN, one run, three attempts, cap respected.

## 0. Verdict

The repaired contract is usable by this model on this pipeline.

- 22 model `request_context` actions were emitted over the three tasks.
- 3 of them were accepted by the decoder and fired an expansion round. They
  fired on two of the three tasks.
- The host fulfilled every accepted request, and the ledger accounts the
  generation provider call and the expansion provider call for each.
- The model also tried `request_context` on the third task. Every attempt there
  was rejected by the query validator, so no expansion round fired.

The stopping rule in the assignment ("if no task produces a request_context
action, shelve the lever") is NOT triggered. Do not shelve the lever on this
evidence.

This pilot claims no token or cost saving. It measures reachability, not
efficiency.

## 1. Run facts

Exact command (the environment variables are the run parameters):

    ARMS=cold \
    REPEATS=1 \
    MAX_RETRIES=0 \
    RETENTION=fresh \
    MODEL=z-ai/glm-5.3-flash \
    TASKSET_SRC=tests/evals/warmcost/pilot-taskset \
    TASKS_ENV="error-envelope-from-doc clock-table-helper-reuse listen-address-from-config" \
    RUN_BASE=wc-pilot-cold-20260912T121001 \
    tests/evals/warmcost/paid-run.sh

- Model: `z-ai/glm-5.3-flash` (operator config: OpenRouter, chat-completions,
  reasoning effort low).
- Task list: `error-envelope-from-doc`, `clock-table-helper-reuse`,
  `listen-address-from-config`.
- Repeats: 1.
- Arms: `cold` only.
- Retention: `fresh`.
- Abort-retry: disabled (`MAX_RETRIES=0`).
- Cap: 3 attempts total (1 arm x 3 tasks x 1 repeat). The run used exactly 3
  attempts and one run invocation.
- Binary revision: `0508a3c8988d170952b8d4819bd56d8e1c33c556`
  (`git rev-parse HEAD`, recorded in `aggregate.json` provenance and in the run
  log). The binary is built from `./cmd/splice`; `cmd/splice` was not modified,
  so the binary matches `0508a3c`.
- Run id: `wc-pilot-cold-20260912T121001-try0`.
- Task list selection used the new bounded `TASKS_ENV` override in
  `paid-run.sh`. An unknown name aborts before the build, with the message
  `unknown task <name>: <taskset>/tasks/<name>.json does not exist`; an unset
  override keeps the previous default three-task list unchanged.

The `RUN_BASE` value printed by the launcher wrapper was empty in the captured
console text because of a wrapper quoting mistake; the value the runner
actually used is the run id above.

## 2. Pilot corpus

Each task names the production file it touches but deliberately does NOT name
the artifact that carries the missing fact. The default context request reads
every path NAMED in the intent, and otherwise all production sources for the
language, so each dependency below is excluded by construction of the default
request.

| task | unnamed dependency (never named in the prompt) | class | verifier |
| --- | --- | --- | --- |
| `error-envelope-from-doc` | `docs/error-envelope.md` | non-source doc | probe test: 400, 404, and 405 bodies must be the documented `{"error":{"status","code","message"}}` envelope with codes `INVALID_ARGUMENT`, `SESSION_NOT_FOUND`, `METHOD_NOT_ALLOWED` |
| `clock-table-helper-reuse` | `clock_test.go` (`clockCase`, `runClockTable`) | test file | a new `TestStoreExpiryBoundary` must pass, and a `*_test.go` file other than `clock_test.go` must reference `runClockTable` |
| `listen-address-from-config` | `deploy/service.conf` (`listen_address`) | non-source config | `resolveListenAddr()` must return the `listen_address` value the verifier writes into the config; `main.go` must reference `resolveListenAddr` |

Corpus validation (`taskset-v0/validate/validate.py` over
`pilot-taskset/{tasks,fixture}`): 3 tasks validated, 0 broken. Each task fails
its verifier on the untouched fixture, passes with its gold patch, and fails
with its wrong patch. The fixture is the `taskset-v0` fixture plus only the
files above.

## 3. Per-task results

Every number below is a captured ledger field or verifier output. The
"code_writer host handshakes" column counts the `code_writer` context handshake
that runs before the first provider call of a stage invocation. That handshake
issues no provider request, so it is not a provider round and it is not counted
in the provider-call column. The passing task also runs one `test_generator`
handshake of the same kind.

| task | verifier | run_status | provider calls | expansion provider calls | round_share | code_writer host handshakes (no provider call) | accepted request_context actions |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| `error-envelope-from-doc` | PASS | completed | 6 | 2 | 0.3333 | 1 | 1 |
| `clock-table-helper-reuse` | FAIL | failed | 15 | 2 | 0.1333 | 5 | 2 |
| `listen-address-from-config` | FAIL | failed | 6 | 0 | 0.0000 | 2 | 0 |

Cold arm aggregate (`aggregate.json`): attempts 3, verified completions 1,
provider calls 27, expansion provider calls 4, round_share 0.1481, billed USD
0.01015935, cache_share 0.7202. Cost coverage is complete for all three
attempts. This is not a cost result and no saving is claimed.

Provider-call detail per task (stage, iteration, invocation ordinal, context
round, spend source), copied from the attempt artifacts:

- `error-envelope-from-doc`: `code_writer` iteration 1, ordinal 0: three
  `generation` calls at context round 0 (sequences 1 to 3) then two `expansion`
  calls at context round 1 (sequences 4 to 5). One `test_generator` `generation`
  call (sequence 6).
- `clock-table-helper-reuse`: 15 `code_writer` calls, ordinal 0. Two of them
  are `expansion` at context round 1: iteration 2 (sequence 6) and iteration 5
  (sequence 15). The other 13 are `generation` at context round 0.
- `listen-address-from-config`: 6 `code_writer` calls, ordinal 0, all
  `generation` at context round 0. No expansion call exists.

## 4. request_context actions

Three accepted actions fired an expansion round. For each: the stage, the
reason the model gave, the ledger fields of the expansion provider call, and
the fulfillment.

1. Task `error-envelope-from-doc`, stage `code_writer`, iteration 1.
   - Reason: "The intent requires following the documented JSON error envelope
     exactly (field names and per-status rules), but docs/error-envelope.md was
     not delivered in my context views."
   - Ledger expansion calls: context_round 1, spend_source `expansion`,
     sequences 4 and 5.
   - Fulfilled: yes. The raw stream shows
     `[code_writer] context round fulfilled: 3 item(s) delivered` and the
     fulfillment read `docs/error-envelope.md`.
2. Task `clock-table-helper-reuse`, stage `code_writer`, iteration 2.
   - Reason: "The intent requires reusing the existing clock table helper in
     clock_test.go, which has not been delivered in the context views."
   - Ledger expansion call: context_round 1, spend_source `expansion`,
     sequence 6.
   - Fulfilled: yes. `[code_writer] context round fulfilled: 4 item(s)
     delivered`; the fulfillment read `clock_test.go`.
3. Task `clock-table-helper-reuse`, stage `code_writer`, iteration 5.
   - Reason: "The intent requires reusing the existing clock table helper; I
     need its exact API from clock_test.go and existing test conventions from
     session_test.go."
   - Ledger expansion call: context_round 1, spend_source `expansion`,
     sequence 15.
   - Fulfilled: yes. `[code_writer] context round fulfilled: 5 item(s)
     delivered`; the fulfillment read `clock_test.go` and `session_test.go`.

Accounting of both provider calls. Each accepted request is followed by a
provider request that carries spend_source `expansion` at the next context
round, and it is preceded by the `generation` request that produced the model's
action. The ledger records both, so the pair is fully accounted. Where a
post-expansion round made more than one provider attempt (for example task
`error-envelope-from-doc` sequences 4 and 5), each attempt is a separate ledger
record, so "expansion provider calls" is larger than "expansion rounds".

The model attempted `request_context` on the third task too, but the decoder
rejected every attempt, so no expansion round fired there. The captured
rejection reasons are `request_context queries[0]: read_file/outline requires
path` and `request_context queries[0]: max_results must be between 1 and 200`.
The model's own reasoning shows it wanted the config file, for example: "Need
deploy/service.conf content. ... Request it with valid query. ... Previous
request failed due to max_results requirement. Read service.conf, and I can
proceed after". It never formed a query that passed, and it never submitted.

Across the run, 22 `request_context` tool calls were emitted, 3 were accepted,
and 19 were rejected by the query validator. The rejections are dominated by
two schema-shape mistakes: the model put the file in `symbol` instead of `path`,
or it omitted `max_results`/`max_chars`.

## 5. known_limitations

The model requested context rather than reporting a gap in `known_limitations`
on the tasks where it needed unnamed source. `known_limitations` is a field of
the model's `submit_changes` envelope; the ledger does not carry it, but the raw
stream does. The accepted submission and the test-generator submission for
`error-envelope-from-doc` carried these entries:

- "main.go re-emitted as a create proposal using the full content from the
  context view; if the on-disk file drifted since the view was captured, the
  host replacement could drop those drifts."
- "The 404 message was changed from err.Error() to the documented 'session not
  found' since clients never match on message."
- "test_generator and later stages have not yet validated the change."
- "docs/error-envelope.md content was not provided, so tests assert the
  documented field names/codes from the implementation context; the
  acceptance_verifier should confirm the mapping against the doc itself."

The last entry is a gap report from the `test_generator` stage, which does not
receive the code_writer's expansion. It is not a case of the model refusing to
request context.

## 6. Raw artifacts

Under `tests/evals/results/wc-pilot-cold-20260912T121001-try0/`:

- `raw-<task>-cold-r0.jsonl`: the full child stream-json per attempt, including
  the model's `tool_call_delta` argument fragments, the stage reports, and the
  final `PipelineResult`.
- `attempt-<task>-cold-r0.json`: the per-attempt projection, with every
  `PipelineUsageRecord` and the verifier result. It names its raw stream in the
  additive `raw_stream` field.
- `aggregate.json`, `report.md`: the runner aggregate and its rendered report.
- `derived-toolcalls.txt`: a derived reconstruction of the model's tool calls
  from the raw streams. Derived, not raw.

Also `tests/evals/results/PAID_RUN_wc-pilot-cold-20260912T121001-try0.md` and
`tests/evals/results/wc-pilot-cold-20260912T121001-try0.log`.

The raw-stream capture is additive harness instrumentation added for this
pilot. It writes one extra artifact per attempt and changes no measurement.

## 7. Contract gap this pilot exposed

The declared `request_context` query schema marks only `query_type` as required
and offers `path`, `symbol`, `max_results`, and `max_chars` as optional. The
decoder's `ContextQuery.Validate` then requires `max_results` in [1, 200],
`max_chars` in [1, 20000], and `path` for `read_file`. A model that follows the
advertised schema can therefore emit a well-formed `request_context` action that
the host rejects for a rule the schema never states.

This is a real usability finding: it is the direct cause of the third task's
failure to fire a round and of most of the 19 rejections. It is reported, not
changed. No prompt or schema change was made in this pilot.

## 8. Harness changes and gate

Harness changes for this pilot, all additive or opt-in:

- `tests/evals/warmcost/pilot-taskset/`: the three-task corpus and its fixture.
- `tests/evals/warmcost/paid-run.sh`: bounded `TASKS_ENV` task-name override
  with a loud error on an unknown name; default behavior is unchanged when the
  override is unset.
- `tests/evals/warmcost/runner.go` and `types.go`: write the raw stream-json
  artifact per attempt and name it in the attempt JSON.

Gate (captured after the harness changes):

```text
$ gofmt -l .
(no output; exit 0)
$ go vet ./...
(no output; exit 0)
$ go test ./... -count=1 -timeout 30m
ok   github.com/Taf0711/splice/internal/cli                321.173s
ok   github.com/Taf0711/splice/internal/splice              63.429s
ok   github.com/Taf0711/splice/tests/evals/warmcost          0.391s
?    github.com/Taf0711/splice/tests/evals/warmcost/cmd/warmcost-eval  [no test files]
go test exit=0
(zero FAIL lines across the run)
$ cd memd && go test ./... -count=1
ok   github.com/Taf0711/splice/memd                          1.459s
ok   github.com/Taf0711/splice/memd/store                    2.116s
memd go test exit=0
$ go build ./tests/evals/warmcost/...
build ok
$ bash -n tests/evals/warmcost/paid-run.sh
syntax ok
```

## 9. What this does not claim

- No token saving and no cost saving. Round reachability is not efficiency.
- No warm-versus-cold comparison and no retention result.
- No claim that the round is useful. The route fired and was fulfilled, but
  only one of three verifiers passed, and the two failures are payload-shape
  failures, not evidence that the round was unhelpful or helpful.
- One model, one repeat. This is a reachability probe with a single model, not
  a rate estimate.

## 10. Addendum, 2026-09-12: two defects found after the pilot

Two defects were found in the repaired contract while reading the pilot
artifacts. Both are fixed in the working tree. Neither changes the reachability
verdict.

**Corrected reading of this report.** The counts in section 3 are verified: 22
`request_context` actions were emitted across the three tasks, 3 were accepted,
and 19 were rejected by the query validator. The per-task provider counts and
the `generation`/`expansion` split are NOT yet trustworthy: defect 2 below
mislabeled typed-output retries as `generation` (round 0) or `expansion` (round
1), so retry requests were counted inside those two sources.

**Defect 1: the declared query contract under-declared.** The decoder requires
`max_results` (1 to 200), `max_chars` (1 to 20000), and the per-type field:
`path` for read_file and outline, `pattern` for search, `symbol` for find_symbol
and get_symbol. The tool schema marked only `query_type` required. The 19
rejections follow from that gap: the model was never told the bounds are
mandatory, and it put a file path in `symbol` on read_file queries. The schema
now declares `query_type`, `max_results`, and `max_chars` as required, carries
an `anyOf` over `path`, `pattern`, and `symbol`, and states the bounds in each
field description. The prompt states the same rules.

**Defect 2: format-retry attribution was dead.** `StageOptions.OnFormatRetry`
was set by the orchestrator and `callValidatedToolUse` consumed it, but no
stage forwarded it, and the parameter is variadic, so it was silently empty.
Every typed-output retry inherited the round source. The string `format_retry`
appears in no artifact of this run or of the earlier paid run. The five call
sites now forward `options.OnFormatRetry`, and a provider-free test pins the
retry record to `format_retry`.

**Effect on this pilot's numbers.** The expansion provider-call counts in
section 3 include retries. For `error-envelope-from-doc`, three round-0 calls
and two round-1 calls come from three `request_context` plus two
`submit_changes` tool calls, so the true split is one generation plus two
retries at round 0, and one expansion plus one retry at round 1. The
reachability conclusion is unchanged. The per-source decomposition is not
usable until a run records with defect 2 fixed.

**Next step.** Re-run the same capped cold-only pilot after both fixes and
compare the rejection rate. Only then compare warm and cold.
