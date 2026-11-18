# Stream-JSON protocol

Splice uses JSON Lines for headless clients. Each nonempty line contains one JSON
object.

Use schema version `2` for all input and output events.

```json
{"schemaVersion":2,"type":"..."}
```

Output events also include `runId`. Splice redacts output fields before it writes
them to standard output.

## Start a stream run

```bash
splice exec \
  --input-format stream-json \
  --output-format stream-json < request.jsonl
```

Use `--output-format stream-json` with ordinary text input when only the output
needs JSON Lines.

## Input events

The input parser accepts `message` and `prompt` events.

```json
{"schemaVersion":2,"type":"message","role":"user","content":"Inspect this repository."}
{"schemaVersion":2,"type":"prompt","content":"Report only blockers."}
```

Splice joins accepted content in input order with a blank line between items. At
least one event must contain prompt text.

Unknown input fields fail validation. A client can therefore detect protocol
drift instead of losing data silently.

### Image input

A `message` event can include images. Each image contains a MIME type and
standard base64 data without a data-URL prefix.

```json
{"schemaVersion":2,"type":"message","role":"user","content":"Explain this screenshot.","images":[{"mediaType":"image/png","data":"<base64>"}]}
```

Each decoded image has a 10 MiB limit. Unsupported media types and invalid base64
return a protocol error.

## Output event types

A stream can emit these events:

```text
run_start
reasoning
text
tool_call_start
tool_call_delta
tool_call
tool_result
permission_request
permission_decision
permission
usage
pipeline_plan
stage
checkpoint
restore
warning
error
final
run_end
```

Clients must ignore output event types they do not recognize. Additive event
types do not require a schema version change.

## Run lifecycle

A normal stream begins with `run_start` and ends with `run_end`.

```json
{"schemaVersion":2,"type":"run_start","runId":"run_20260810_abc123","sessionId":"session_abc123","cwd":"/repo","provider":"openai","model":"example-model","apiModel":"provider-model-name"}
{"schemaVersion":2,"type":"run_end","runId":"run_20260810_abc123","status":"success","exitCode":0}
```

The terminal `run_end.exitCode` is authoritative. An error event normally
precedes a non-successful `run_end`.

An interrupted run still attempts to emit its terminal events.

## Pipeline progress

Headless `splice exec` runs the typed execution pipeline. Scheduled stages can
include:

- code writer;
- test generator;
- static analyzer;
- security auditor;
- test runner; and
- acceptance verifier.

The selected request tier determines the stage set. Design critique and
step-back analysis are orchestration operations, not scheduled execution stages.

`reasoning` events carry status text, pipeline progress, and model-provided
reasoning text when a provider returns it.

```json
{"schemaVersion":2,"type":"reasoning","runId":"run_20260810_abc123","delta":"Starting pipeline iteration 1\n"}
```

Reasoning events are progress data. Splice does not append them to the final
answer.

### Pipeline plan events

A `pipeline_plan` event lists the complete stage roster before execution.
The list order matches the execution plan.

```json
{"schemaVersion":2,"type":"pipeline_plan","runId":"run_20260810_abc123","stages":["code_writer","test_runner","acceptance_verifier"]}
```

A client can use this roster to calculate stable pipeline progress. Do not
estimate progress from the stages seen so far.

### Stage events

A `stage` event is the documented stage lifecycle contract.

```json
{"schemaVersion":2,"type":"stage","runId":"run_20260810_abc123","name":"code_writer","status":"completed","reason":"wrote main.go","progress":100,"changedFiles":["main.go"]}
```

| Field | Meaning |
|---|---|
| `name` | Stage name |
| `status` | `running`, `completed`, `failed`, `skipped`, or `incomplete` |
| `reason` | A short status summary |
| `progress` | Integer progress from 0 through 100 |
| `changedFiles` | Paths reported by the stage |

### Deprecated stage markers

Older runs also embed a stage marker in `reasoning.delta`. A marker starts with
`\x00STAGE` and ends with `\x00`. The TUI still accepts these markers so a
resumed session can rebuild the pipeline panel. New clients should use the
`stage` event.

## Structured stage output

A model-backed stage returns typed data through a streamed tool-call envelope.

```json
{"schemaVersion":2,"type":"tool_call_start","runId":"run_20260810_abc123","id":"call_0","name":"submit_code"}
{"schemaVersion":2,"type":"tool_call_delta","runId":"run_20260810_abc123","id":"call_0","delta":"{\"files\":["}
```

Concatenate `tool_call_delta.delta` values by `id` to reconstruct the complete
arguments.

These envelopes describe typed stage output. They do not execute a tool, so they
do not receive a `tool_result` event.

## Tool execution

Real tool executions use a `tool_call` and `tool_result` pair.

```json
{"schemaVersion":2,"type":"tool_call","runId":"run_20260810_abc123","id":"call_1","name":"read_file","args":{"path":"README.md"},"sideEffect":"read"}
{"schemaVersion":2,"type":"tool_result","runId":"run_20260810_abc123","id":"call_1","status":"ok","output":"...","truncated":false}
```

A tool result can also include `changedFiles`, a compact `display` object, and
redaction or truncation flags.

When a pipeline stage runs, `tool_call` and `tool_result` include `stage` (the
stage name) and `iteration` (the pass number, starting at 1). The pass number
matches the `usage` event of that pass. Both fields are absent when no stage
runs. A tool call made by repair or step-back after a stage ended carries no
stage.

## Permissions

A prompt-gated tool can emit a permission request.

```json
{"schemaVersion":2,"type":"permission_request","runId":"run_20260810_abc123","id":"call_2","name":"write_file","action":"prompt","permission":"prompt","permissionMode":"ask","sideEffect":"write","reason":"Creates or overwrites files."}
```

An interactive surface can emit the decision:

```json
{"schemaVersion":2,"type":"permission_decision","runId":"run_20260810_abc123","id":"call_2","name":"write_file","action":"allow","permissionGranted":true,"decisionReason":"approved in TUI"}
```

Headless `exec` has no interactive permission responder. A tool that lacks prior
approval can emit a request followed by a denied result.

A permission event can include `risk`, `block`, grant details, and an autonomy
value. Schema version `2` renamed the old sandbox `violation` field to `block`.

The bare `permission` event is a fallback for a permission action that does not
fit the request or decision categories.

## Usage and cost

Splice emits one usage event for each model request when usage attribution is
available.

```json
{"schemaVersion":2,"type":"usage","runId":"run_20260810_abc123","provider":"openai","model":"example-model","stage":"code_writer","iteration":1,"usageSequence":1,"usageReported":true,"promptTokens":1200,"completionTokens":500,"totalTokens":1700}
```

Optional token fields are:

- `cachedInputTokens`;
- `cacheWriteTokens`; and
- `reasoningTokens`.

Optional cost fields are:

- `costUsd`;
- `costStatus`;
- `costEstimated`;
- `costProvenance`;
- `pricingSource`;
- `pricingAsOf`; and
- `unpricedReason`.

The `provider` and `model` fields identify the selected route. `apiModel` is a
`run_start` field and does not appear on usage events.

An unavailable price produces `costStatus: "unpriced"` and no `costUsd`. Clients
must not convert an unknown cost to zero.

## Checkpoints

A checkpoint event reports file snapshots captured before a write.

```json
{"schemaVersion":2,"type":"checkpoint","runId":"run_20260810_abc123","checkpoint":{"sequence":1,"tool":"submit_code","files":["README.md"]}}
```

`restore` is reserved for a future file-checkpoint restore event. Its checkpoint
object can include `filesRestored`, `filesDeleted`, and skipped paths.

Trajectory worktree recovery is separate from this reserved event.

## Final result

Near the end of a pipeline run, `text` contains a short human summary. `final`
contains the serialized final result in its `text` field.

```json
{"schemaVersion":2,"type":"final","runId":"run_20260810_abc123","text":"{\"run_id\":\"run-abc\",\"status\":\"completed\",\"tier\":\"light\",\"stages\":[]}"}
```

The event is a JSON object, but `final.text` is itself a JSON string for pipeline
and design-plan results. Decode `text` when the client needs result fields.

A pipeline result can contain `run_id`, `status`, `tier`, stage records, usage
totals, `abort_reason`, `user_aborted`, `verified`, `unverified_reason`, and
`verification_outcome`. A completed run has `verification_outcome` set to
`verified`, `command_passed`, or `unverified`. `command_passed` means that the
test command exited 0 but gave no per-test result. That run is not verified.
A failed stage record can have `failure_kind`. The values are `stage_error`,
`test_failure`, and `none`. Each `usage_records` entry can
contain `prompt_layout_hash`, `cache_hit`, and `memory_position`. The result can
contain `prompt_layout_flips`, the count of prefix changes during the run.
`user_aborted` is true only when
the user chose to stop the run. Clients must treat `status=aborted` with
`user_aborted=false` as a failure, not a user cancellation.

A completed run also has `verification_checks`: one entry for each
verification stage of the last pass. Each entry has these fields:

- `name`: the stage or node name.
- `kind`: `tests`, `acceptance`, `security_scan`, `static_analysis`, or
  `custom`.
- `outcome`: `passed`, `failed`, `findings`, `skipped`, `incomplete`,
  `command_passed`, `no_tests`, or `not_applicable`. Only `tests` can have
  `command_passed` or `no_tests`.
- `detail`: a short text from typed data, for example `12 passed, 0 failed,
  per-test evidence` or `bandit, gosec`. It can be empty.

A verified result has at least one `passed` check and no `failed` check. A
verified result can have a `tests` check with `no_tests`: a scanner verified
that run, and no test ran. Results from earlier versions do not have the
field.

`nested_sandbox_accepted` is true when Splice ran inside a sandbox and the
user accepted a nested run. Stage commands, command nodes, and agent nodes
then ran under the outer sandbox only, not under the stage sandbox. The field
does not change `verified`. Splice writes it for every status, and the run
trace outcome has the same field. The field is absent when it is false. See
[Stage sandbox](PIPELINE.md#stage-sandbox).

Each stage record can contain `sandbox_evidence`, one entry for each command
that the stage started or tried to start, and `sandbox_evidence_dropped`. An
entry has these fields:

- `stage` and `iteration`: the same values as the stage record.
- `source`: `bash_tool`, `exec_command_tool`, `stage_command`,
  `scanner_tool`, `plugin_tool`, `hook`, or `other`.
- `program`: the file name of the program only, for example `pytest`. For a
  shell command, it is the first word of the command. The value is
  `redacted` when that word is not a plain program name. The record never
  contains the command line, the arguments, or the environment.
- `policy`: `source` (`stage`, `user`, or `none`), `mode`, `network`,
  `enforce_workspace`, and the counts `write_roots`, `deny_read`, and
  `deny_write`. The record contains no path.
- `backend`, `enforcement_level`, and `wrapped`: the sandbox that ran the
  command.
- `escalated`: true when an approved escalation ran the command with no
  sandbox.
- `nested_accepted`: true when the command ran without a wrapper because
  Splice ran inside a sandbox and the user accepted a nested run. Only the
  outer sandbox applied. `wrapped` is then false.
- `outcome`: `ran`, `refused`, `denied`, or `timed_out`. `denied` means that
  the command failed with the signs of a sandbox denial.
- `refusal`, only for `refused`: `no_enforcing_sandbox`,
  `escalation_refused`, `permission_denied`, `hook_blocked`,
  `binary_not_allowed`, `policy_refused`, or `environment_refused`.
- `exit`, not for `refused`: `ok`, `non_zero`, or `unknown`.

A stage record has at most 50 entries. `sandbox_evidence_dropped` counts the
commands after the limit.

A design-plan run returns a design-plan result with completed, failed, and
skipped task IDs. Each task runs as an independent pipeline run.

## Worktree merge result

With `--worktree --merge-back`, Splice uses existing event types:

- `text` for a merge or a no-change result;
- `warning` for a dirty-source skip or a merge conflict; and
- `error` with code `merge_back_failed` for a merge operation error.

The worktree branch remains available when Splice does not merge it.

## Errors

```json
{"schemaVersion":2,"type":"error","runId":"run_20260810_abc123","code":"provider_error","message":"...","recoverable":false}
{"schemaVersion":2,"type":"run_end","runId":"run_20260810_abc123","status":"error","exitCode":3}
```

Clients should display the error message, retain the run ID, and use the final
exit code for automation decisions.

A completed run that did not verify, with `--require-verified` or
`--merge-back`, ends with these events. The `final` event comes first.

```json
{"schemaVersion":2,"type":"error","runId":"run_20260810_abc123","code":"unverified","message":"no check applied: static_analyzer (no analyzer for this repository)","recoverable":false}
{"schemaVersion":2,"type":"run_end","runId":"run_20260810_abc123","status":"unverified","exitCode":4}
```

The `code` field names the error class. `usage_error` is a command line that is
not valid. `input_error` is a topology, a node pin, or a stage route that is not
valid. Splice finds it before the first provider request. Both errors end with
exit code 2. `provider_error` ends with exit code 3.
