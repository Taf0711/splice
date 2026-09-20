# Cold-only pilot, round 2: the same run on the fixed binary (`c56e78e`)

Date: 2026-09-12.
Branch: `wip/evidence-substitution-production` at `c56e78e`.
Scope: controlled re-run of the round-1 cold-only pilot. No warm arm, no
retrieval-only arm, no retention run, no evidence substitution, no
front-loading, no warm-versus-cold comparison.
Status: RUN, one run, three attempts, cap respected.

Baseline: `tests/evals/results/PILOT_CONTRACT_2026-09-12.md` and its addendum,
plus the artifacts under
`tests/evals/results/wc-pilot-cold-20260912T121001-try0/`.

## 1. Run facts

Exact command (environment variables are the run parameters):

    ARMS=cold \
    REPEATS=1 \
    MAX_RETRIES=0 \
    RETENTION=fresh \
    MODEL=z-ai/glm-5.3-flash \
    TASKSET_SRC=tests/evals/warmcost/pilot-taskset \
    TASKS_ENV="error-envelope-from-doc clock-table-helper-reuse listen-address-from-config" \
    RUN_BASE=wc-pilot-cold-20260912T164646Z \
    tests/evals/warmcost/paid-run.sh

- Model: `z-ai/glm-5.3-flash` (operator config: OpenRouter, chat-completions,
  reasoning effort low). Same model as round 1.
- Tasks: `error-envelope-from-doc`, `clock-table-helper-reuse`,
  `listen-address-from-config`. Same corpus and prompts as round 1.
- Repeats: 1. Arms: `cold` only. Retention: `fresh`.
- Abort-retry: disabled (`MAX_RETRIES=0`).
- Cap: 3 attempts total (1 arm x 3 tasks x 1 repeat). The run used exactly 3
  attempts and one run invocation.
- `git rev-parse HEAD`: `c56e78edd1b1818009acec5b2d3cdf49321caf94`
  (`c56e78e`). The worktree was clean, so the binary was built from `c56e78e`.
  `aggregate.json` provenance records this revision, and the console log prints
  `revision=c56e78edd1b1818009acec5b2d3cdf49321caf94`.
- Run id: `wc-pilot-cold-20260912T164646Z-try0`.
- Pre-run print (captured in `console.log` under the run directory): command,
  model, task list, repeat count, cap, and HEAD were printed before the first
  request.

## 2. Check A: did Fix A reduce rejections?

Counting rule: a `request_context` action is a model tool call whose declared
`action` field is `request_context`. The count comes from the derived tool-call
log (the reconstructed `tool_call_delta` arguments in the raw stream), the same
way round 1 counted.

Definitions:

- emitted: model tool calls with `action=request_context`.
- decoder-accepted: passed `DecodeStageAction` and the query validation in
  `ValidateActionContextRequest`.
- fulfilled: the host completed the expansion and the next provider call is
  recorded with `spend_source=expansion`.
- rejected: not fulfilled.

| round | emitted | decoder-accepted | fulfilled | rejected | decoder-accepted/emitted | fulfilled/emitted |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 (`0508a3c`) | 22 | 3 | 3 | 19 | 3/22 = 0.136 | 3/22 = 0.136 |
| 2 (`c56e78e`) | 8 | 8 | 4 | 4 | 8/8 = 1.000 | 4/8 = 0.500 |

In round 1 every rejection was at the decoder's query validation. In round 2
every emitted action passed the decoder's query validation; the 4 rejections
happened later, at fulfillment.

Previously invalid shapes. Checked over all 8 round-2 queries:

- 0 of 8 `read_file` queries carry `symbol` instead of `path`.
- 0 of 8 queries omit `max_results` or `max_chars`.
- 0 decoder-level rejection strings (`request_context queries[...]`,
  `both actions`, `unknown action`) appear in any round-2 raw stream.

Per-rejection detail for round 2. All 4 rejections have the same cause: the
model omitted `request_context.reason`. The declared schema marks `reason` as an
optional property (required list is `[queries]`), but `ContextRequest.Validate`
(`internal/splice/schemas/plan.go:236`) requires it. Each query below is
well-formed under the fixed query contract.

1. `error-envelope-from-doc`, stage `code_writer`, iteration 1.
   - Query: `{"query_type":"read_file","path":"docs/error-envelope.md","max_results":5,"max_chars":20000}`.
   - Reason field: omitted.
   - Captured rejection: `splice.stageRunError: fulfill context: reason is required`.
2. `clock-table-helper-reuse`, stage `code_writer`, iteration 1.
   - Query: `{"query_type":"read_file","path":"clock_test.go","max_results":10,"max_chars":8000}`.
   - Reason field: omitted.
   - Captured rejection: `splice.stageRunError: fulfill context: reason is required`.
3. `clock-table-helper-reuse`, stage `code_writer`, repair re-entry (iteration 3,
   invocation ordinal 1).
   - Queries: `{"query_type":"read_file","path":"clock_test.go","max_results":1,"max_chars":8000}`
     and `{"query_type":"read_file","path":"session_expiry_boundary_test.go","max_results":1,"max_chars":4000}`.
   - Reason field: omitted.
   - Captured rejection: `repair: code_writer re-entry: fulfill context: reason is required`.
4. `listen-address-from-config`, stage `code_writer`, repair re-entry (iteration
   1, invocation ordinal 1).
   - Query: `{"query_type":"read_file","path":"main_test.go","max_results":5,"max_chars":8000}`.
   - Reason field: omitted. The same action carried
     `known_limitations: ["Failure is in main_test.go line 44 quoting; no view of that file was provided, so requesting it before proposing edits."]`.
   - Captured rejection: `repair: code_writer re-entry: fulfill context: reason is required`
     (also the `error` event with code `incomplete`).

Mixed payloads, not counted as request_context actions. Two `clock-table-helper-reuse`
tool calls declared `action=submit_changes` and also carried a `request_context`
field plus a `files` array. The declared-action decoder rejects that mix, but
`TryDecodeStageAction`'s backward-compatibility fallback then treats the payload
as a bare submit, so the `request_context` is silently ignored. In call 3 the
submit then failed `CodeWriterOutput.Validate` with `intent is required`; in call
5 the submit was applied and the stage completed. This is reported as an
observation because it can hide a model's intent to request context.

Verdict, Check A: HOLDS. The two round-1 invalid shapes no longer appear, and
decoder-level rejections fell from 19 of 22 to 0 of 8. The residual 4 rejections
are a NEW reason, not a pass: the host requires `reason`, the schema does not,
and this is a finding for the contract owner.

## 3. Check B: is `format_retry` now billed under its own source?

Records with `spend_source=format_retry` in the round-2 cold arm:

| task | sequence | stage | iteration | invocation ordinal | context round | cost USD |
| --- | ---: | --- | ---: | ---: | ---: | ---: |
| `error-envelope-from-doc` | 4 | `code_writer` | 2 | 0 | 1 | 0.00065375 |
| `listen-address-from-config` | 3 | `code_writer` | 1 | 0 | 1 | 0.00058075 |

These are the two typed-output retries that occurred in the run. For
`error-envelope-from-doc`, the code_writer's post-expansion round made two
provider attempts: the first submit failed validation, the retry is sequence 4.
For `listen-address-from-config`, the same pattern in iteration 1: the first
submit failed, the retry is sequence 3. No other typed-output retry occurred:
the `clock-table-helper-reuse` failures were a fulfillment error and a
post-validation error, neither of which enters the typed-output retry loop, so
that task correctly has no `format_retry` record.

Round 1 contrast: `format_retry` appears in no round-1 artifact. In round 1 the
retries inherited the round source, so the split was unusable. In round 2 the
retries are split out.

Per-source split for the cold arm (from `aggregate.json`,
`billed_usd_by_source`, and the `decomposition` block):

| source | records | cold USD |
| --- | ---: | ---: |
| `generation` | 8 | 0.00524524 |
| `format_retry` | 2 | 0.0012345 |
| `expansion` | 4 | 0.00386135 |
| `repair` | 2 | 0.00167035 |
| total | 16 | 0.01201144 |

Sum check: 0.00524524 + 0.0012345 + 0.00386135 + 0.00167035 = 0.01201144,
which equals the arm's billed total. The runner's `validateSourceIdentity`
enforces this at build time and errors on a mismatch; the builder did not error
(run exit 0, `aggregate.json` written, `decomposition` lists `format_retry`).

Verdict, Check B: HOLDS. `format_retry` records exist where a typed-output retry
occurred, and the per-source total reconciles to the billed total.

## 4. Per-task results

Host handshakes are the `code_writer` context handshakes that run before the
first provider call of a stage invocation. They issue no provider request, so
they are not provider rounds and are not counted in the provider-call column.
`test_generator` also performs a handshake in the two tasks where it ran.

| task | verifier | run_status | provider calls | expansion provider calls | format_retry calls | repair calls | round_share | code_writer host handshakes | request_context emitted/fulfilled |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `error-envelope-from-doc` | PASS | completed | 5 | 1 | 1 | 0 | 0.2000 | 2 | 2 / 1 |
| `clock-table-helper-reuse` | FAIL | failed | 6 | 2 | 0 | 1 | 0.3333 | 4 | 4 / 2 |
| `listen-address-from-config` | FAIL | failed | 5 | 1 | 1 | 1 | 0.2000 | 2 | 2 / 1 |

Accepted requests and fulfillment. Each accepted request is followed by a
provider call recorded with `spend_source=expansion`, and it is preceded by the
generation (or repair) call that produced it. The host fulfilled each one; the
raw stream shows the `context round fulfilled` report and the `read_file` of the
requested path.

1. `error-envelope-from-doc`, reason: "The intent requires following the
   documented error envelope exactly; only docs/error-envelope.md defines its
   field names and per-status behavior, and it was not delivered in my context
   views." Fulfilled with `docs/error-envelope.md` (3 items). Ledger: sequence 2
   `generation` at round 0, sequence 3 `expansion` at round 1.
2. `clock-table-helper-reuse`, reason: "The intent requires reusing the existing
   clock table helper in clock_test.go; that file was not delivered in my
   context views and I must not guess its helper's shape." Fulfilled with
   `clock_test.go` (4 items). Ledger: sequence 2 `generation` at round 0,
   sequence 3 `expansion` at round 1.
3. `clock-table-helper-reuse`, reason: "I need the existing clock table helper
   (in clock_test.go) and session_test.go conventions before writing
   TestStoreExpiryBoundary." Fulfilled with `clock_test.go` and
   `session_test.go` (5 items). Ledger: sequence 4 `generation` at round 0,
   sequence 5 `expansion` at round 1.
4. `listen-address-from-config`, reason: "resolveListenAddr() must parse the
   config file, and deploy/service.conf was not delivered." Fulfilled with
   `deploy/service.conf` (3 items). Ledger: sequence 1 `generation` at round 0,
   sequence 2 `expansion` at round 1.

Verifier output (captured):

- `error-envelope-from-doc`: PASS, `ok  \tdemo\t0.399s`.
- `clock-table-helper-reuse`: FAIL,
  `./session_expiry_boundary_test.go:7:33: undefined: testing` (the applied test
  file reused `runClockTable` but did not import `testing`).
- `listen-address-from-config`: FAIL,
  `main_test.go:44:27: missing ',' in argument list` (a syntax error in the
  file the `test_generator` stage wrote; the task's own first verifier step,
  `main.go` references `resolveListenAddr`, passed).

Arm totals, cold only (`aggregate.json`): attempts 3, verified completions 1,
provider calls 16, expansion provider calls 4, round_share 0.25, input tokens
70310, output tokens 7031, cached input tokens 17088, cache-write tokens 0,
reasoning tokens 774, billed USD 0.01201144. Cost coverage: 3 of 3 attempts
complete, 0 partial.

`known_limitations` quotes. No code_writer declined to request context and only
reported a gap. Two relevant reports were captured:

- `listen-address-from-config`, the rejected request_context action above
  carried: "Failure is in main_test.go line 44 quoting; no view of that file was
  provided, so requesting it before proposing edits." The model did request the
  file, but it left `reason` empty, so the host rejected the action.
- `error-envelope-from-doc`, the `test_generator` submission carried:
  "docs/error-envelope.md was not provided, so per-status code strings were taken
  from the writer's implementation of main.go."

## 5. Contradictions with round 1, and what I could not verify

- Round 1 recorded no `format_retry` anywhere; round 2 has 2. This is the
  intended effect of Fix B, and it changes the per-source split. Any round-1
  per-source statement that mixes retries into `generation` or `expansion` is
  corrected by round 2's records, not by re-labeling round 1.
- Round 1 rejected 19 of 22 requests at the query decoder. Round 2 rejected 0 of
  8 there. The residual round-2 rejections are a different failure layer
  (`reason` at fulfillment). So "rejections went down" is only true for the
  decoder layer; total emitted requests that were not fulfilled fell from 19 to
  4 but did not reach zero.
- Round 1 produced no expansion on `listen-address-from-config` (round_share
  0). Round 2 produced one, and the verifier's `main.go` reference check for
  `resolveListenAddr` passed before the build error. The task still failed, on
  the `test_generator` file's syntax error.
- Round 1's `clock-table-helper-reuse` submissions were never applied, because
  they omitted `intent`. Round 2 applied one that reused `runClockTable`, but it
  did not import `testing`, so the build failed.
- Verifier outcomes are identical between rounds (1 of 3 pass, the same task),
  so this pilot does not show a correctness change.

Not verified:

- The runner cleans each attempt workspace, so I cannot inspect the final files.
  Statements about what was applied rest on verifier output and the raw stream,
  not on a workspace diff.
- This is a comparison of two model trajectories on two revisions, not a
  per-attempt toggle. The change in emitted counts (22 to 8) is not attributable
  to Fix A alone, because model sampling differs between runs. Do not read it as
  a rate.
- The rejection reason for an intermediate retry that is later superseded is not
  captured. In this run all 4 round-2 rejections were terminal, so all 4 reasons
  are captured exactly.
- No token or cost saving is claimed. This is a reachability and attribution
  check with one model and one repeat.

## 6. Raw artifacts

Under `tests/evals/results/wc-pilot-cold-20260912T164646Z-try0/`:

- `raw-<task>-cold-r0.jsonl`: the full child stream-json per attempt.
- `attempt-<task>-cold-r0.json`: the ledger projection, with `raw_stream` set.
- `aggregate.json`, `report.md`: the runner aggregate and its rendered report.
- `derived-toolcalls.txt`: reconstructed model tool calls.
- `console.log`: the launcher console log, including the pre-run facts and the
  pre-registration lines.

Also `tests/evals/results/PAID_RUN_wc-pilot-cold-20260912T164646Z-try0.md` and
`tests/evals/results/wc-pilot-cold-20260912T164646Z-try0.log`.

## 7. Gate

```text
$ gofmt -l .
(no output; exit 0)
$ go vet ./...
(no output; exit 0)
$ go test ./... -count=1 -timeout 30m
ok   github.com/Taf0711/splice/internal/cli                337.124s
ok   github.com/Taf0711/splice/internal/splice              58.362s
ok   github.com/Taf0711/splice/tests/evals/warmcost          0.384s
?    github.com/Taf0711/splice/tests/evals/warmcost/cmd/warmcost-eval  [no test files]
go test exit=0
(zero FAIL lines across the run)
$ cd memd && go test ./... -count=1
ok   github.com/Taf0711/splice/memd                          1.232s
ok   github.com/Taf0711/splice/memd/store                    2.459s
memd go test exit=0
$ go build ./tests/evals/warmcost/...
build exit=0
$ bash -n tests/evals/warmcost/paid-run.sh
bash -n exit=0
```

## Addendum: defects C and D found, fixed, and committed

After the round-2 report two additional defects were found and fixed in commit
`c56e78e`s child. Both are in the same schema-validator drift family.

**Defect C.** `ContextRequest.Validate` (internal/splice/schemas/plan.go:237)
requires a non-empty `reason`. The declared request_context schema
(`actionContextRequestSchema` in internal/splice/stages/code_writer.go)
listed `reason` as an optional string. 4 of 8 round-2 request_context actions
were rejected for a missing `reason` (for example, `clock-table-helper-reuse`
calls 01 and 06 had `reason: None`).

Fix: add `reason` to the schema's required list and update the prompt to state
it is mandatory.

**Defect D.** `TryDecodeStageAction` catches any `DecodeStageAction` error and,
when a bare `files` array is present, returns a submit. A payload with
`action: "submit_changes"` that also carries a `request_context` (for example,
round-2 `clock-table-helper-reuse` call 03) was silently downgraded to a
submit instead of failing loud with a mutual-exclusivity error.

Fix: restrict the bare-files fallback to payloads with NO `action` field. A
declared-action error now propagates. The legacy bare `files` payload without
an `action` field still decodes as a submit.

**Drift guard.** A new test `TestRequestContextSchemaMatchesValidators` pins
both directions: the schema's required fields are sufficient for the
validators, and every validator-required field is declared in the schema. This
stops the recurring whack-a-mole pattern.
