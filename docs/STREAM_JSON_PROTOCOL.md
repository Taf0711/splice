# Splice Stream-JSON Protocol

Splice stream-json is a line-delimited protocol for headless clients such as
editor extensions and automation wrappers.

Every line is one JSON object. Empty input lines are ignored. Output events are
redacted before they are written to stdout.

## Execution model

Headless `splice exec` runs Splice's deterministic stage pipeline: the request
is planned into stages (plan critic, code writer, test generator, test runner,
static analyzer, security auditor, step back, with the set depending on the plan tier), and the
pipeline iterates until the pass succeeds, fails, or a budget is exhausted.
This shapes the event stream:

- `reasoning` events carry pipeline lifecycle and progress (iteration starts,
  `[stage] …` activity lines) in addition to any model reasoning deltas. Stage
  lifecycle events (started/running/completed/failed/skipped) are embedded
  inside the `reasoning.delta` JSON string field: the delta contains a payload
  starting with `\x00STAGE` and ending with `\x00` with `name`, `status`,
  `detail`, `progress` (0-100), and `changedFiles`. The TUI parses these from
  the delta field to drive its PIPELINE sidebar; other consumers can ignore them.
- Each LLM-backed stage produces its structured output as a streamed tool
  call: one `tool_call_start`, then `tool_call_delta` fragments. These ids
  never receive a `tool_result`; they are schema envelopes, not executions.
- `tool_call` / `tool_result` pairs are reserved for real tool executions the
  pipeline performs (reads, greps, file writes, shell and test commands).
- `usage` is emitted once per stage LLM call, so expect several per run. The
  event carries `promptTokens` (effective input), `completionTokens`
  (effective output), and `totalTokens`. When the provider reports them,
  `cachedInputTokens`, `cacheWriteTokens`, and `reasoningTokens` are also
  set (all optional, `omitempty`).
- Near the end of the run, `text` carries a short human-readable summary and
  `final` carries the machine-readable pipeline result: a JSON object with
  `runId`, `status` (`completed` | `failed` | `aborted`), `tier`, per-stage
  records, and `abortReason` when applicable.

The configured max-turns limit caps pipeline iterations (one "turn" = one full
stage pass). Interrupted runs (SIGINT, context cancellation) still emit their
terminal `error`/`run_end` events.

When `splice exec --plan <path.json>` executes a design plan, each task runs as
an independent pipeline run. Per-task progress lines are emitted as `reasoning`
events, per-task pipeline stage events keep the same shape described above, and
the final answer is the `DesignPlanResult` JSON with completed, failed, and
skipped task ids.

When `splice exec --worktree --merge-back` succeeds, the merge-back outcome is
reported with existing event types: a `text` event for a merged or no-changes
result, a `warning` event for a skipped (dirty source tree) or conflicted
merge, and an `error` event with code `merge_back_failed` (followed by a
`run_end` with status `error`) when the merge itself errors. The worktree
branch named in the message survives in every non-merged case.

## Version

Current schema version: `2`

Every input and output event must include:

```json
{ "schemaVersion": 2, "type": "..." }
```

Output events also include `runId`.

## Input Events

`splice exec --input-format stream-json` accepts these JSONL events from stdin or
`--file`.

```json
{ "schemaVersion": 2, "type": "message", "role": "user", "content": "Inspect this repo." }
{ "schemaVersion": 2, "type": "prompt", "content": "Return only blockers." }
```

Splice combines accepted input event content in order, separated by blank lines.
Unknown fields are rejected so protocol clients catch drift early.

Schema version `2` renamed sandbox permission metadata from `violation` to
`block`. Clients should read the optional `block` object on permission and tool
events when present.

## Output Events

`splice exec --output-format stream-json` emits schema-versioned JSONL events.

```json
{ "schemaVersion": 2, "type": "run_start", "runId": "run_20260603_abc123", "sessionId": "splice_20260603100000_abc123", "cwd": "/repo", "provider": "openai", "model": "gpt-4.1", "apiModel": "gpt-4.1" }
{ "schemaVersion": 2, "type": "reasoning", "runId": "run_20260603_abc123", "delta": "Thinking..." }
{ "schemaVersion": 2, "type": "text", "runId": "run_20260603_abc123", "delta": "..." }
{ "schemaVersion": 2, "type": "tool_call_start", "runId": "run_20260603_abc123", "id": "call_0", "name": "submit_code" }
{ "schemaVersion": 2, "type": "tool_call_delta", "runId": "run_20260603_abc123", "id": "call_0", "delta": "{\"files\":[{\"path\":" }
{ "schemaVersion": 2, "type": "tool_call", "runId": "run_20260603_abc123", "id": "call_1", "name": "read_file", "args": { "path": "README.md" }, "sideEffect": "read" }
{ "schemaVersion": 2, "type": "permission_request", "runId": "run_20260603_abc123", "id": "call_2", "name": "write_file", "action": "prompt", "permission": "prompt", "permissionMode": "ask", "sideEffect": "write", "reason": "Creates or overwrites files." }
{ "schemaVersion": 2, "type": "permission_decision", "runId": "run_20260603_abc123", "id": "call_2", "name": "write_file", "action": "allow", "permission": "prompt", "permissionGranted": true, "decisionReason": "approved in TUI" }
{ "schemaVersion": 2, "type": "tool_result", "runId": "run_20260603_abc123", "id": "call_1", "status": "ok", "output": "...", "truncated": false }
{ "schemaVersion": 2, "type": "usage", "runId": "run_20260603_abc123", "promptTokens": 12, "completionTokens": 8, "totalTokens": 20 }

With cache and reasoning (optional fields present when the provider reports them):

{ "schemaVersion": 2, "type": "usage", "runId": "run_20260603_abc123", "promptTokens": 12000, "completionTokens": 500, "totalTokens": 12500, "cachedInputTokens": 11000, "cacheWriteTokens": 500, "reasoningTokens": 200 }
{ "schemaVersion": 2, "type": "final", "runId": "run_20260603_abc123", "text": "..." }
{ "schemaVersion": 2, "type": "run_end", "runId": "run_20260603_abc123", "status": "success", "exitCode": 0 }
```

`reasoning` events carry live model reasoning/status deltas and pipeline
progress lines. They are liveness/progress events only: they are not folded
into `text` or the final answer.

`tool_call_start` announces a streamed structured stage output; each
`tool_call_delta` carries only its new fragment (`delta`), never cumulative
arguments. Concatenate deltas per `id` to reconstruct the full arguments.
Both are additive output types within schema version `2`; clients should
ignore output event types they do not recognize.

Permission events may include structured sandbox metadata:

```json
{ "schemaVersion": 2, "type": "permission_request", "runId": "run_20260603_abc123", "id": "call_3", "name": "bash", "action": "prompt", "permission": "prompt", "permissionMode": "ask", "sideEffect": "shell", "reason": "network access requires approval", "risk": { "level": "critical", "categories": ["network"] }, "block": { "code": "network", "toolName": "bash", "action": "prompt", "risk": { "level": "critical", "categories": ["network"] }, "reason": "network access requires approval", "recoverable": true } }
```

Errors are part of the protocol and are followed by `run_end`.

`checkpoint` events capture file snapshots before pipeline stage writes. Each
event carries the checkpoint `sequence` number, the `tool` that triggered it,
and the list of `files` captured:

```json
{ "schemaVersion": 2, "type": "checkpoint", "runId": "run_20260603_abc123", "checkpoint": { "sequence": 1, "tool": "submit_code", "files": ["README.md"] } }
```

`restore` events (currently reserved for future use) will report when file
checkpoints are rolled back. The shape mirrors `checkpoint` with an additional
`filesRestored` and `filesDeleted` count.

`permission` (bare, without `_request` or `_decision` suffix) is emitted as a
fallback for permission events that do not fit the prompt/allow/deny/cancel
action categories. Its payload matches `permission_request` and
`permission_decision` with the same structured fields.

Headless `exec` has no interactive permission responder. If a prompt-gated tool
is not pre-approved, Splice may emit `permission_request` followed by a denied
`tool_result`; interactive surfaces emit `permission_decision` when the user
allows, denies, or always-allows the request.

```json
{ "schemaVersion": 2, "type": "error", "runId": "run_20260603_abc123", "code": "provider_error", "message": "...", "recoverable": false }
{ "schemaVersion": 2, "type": "run_end", "runId": "run_20260603_abc123", "status": "error", "exitCode": 3 }
```
