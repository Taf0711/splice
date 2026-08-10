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

### Stage markers

Stage lifecycle markers are embedded in `reasoning.delta`. A marker starts with
`\x00STAGE` and ends with `\x00`.

The marker payload contains:

| Field | Meaning |
|---|---|
| `name` | Stage name |
| `status` | `running`, `completed`, `failed`, `skipped`, or `incomplete` |
| `detail` | A short status summary |
| `progress` | Integer progress from 0 through 100 |
| `changedFiles` | Paths reported by the stage |

The TUI uses these markers for its pipeline panel. Other consumers can ignore
them.

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
totals, and `abort_reason`.

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
