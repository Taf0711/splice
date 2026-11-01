# Offline agent evaluations

Splice agent evaluations are maintainer fixtures for code-agent behavior without
a live model call. They define a task and the files an agent should change.
They also define verification commands and score rules for a captured run.

These local fixtures do not prove provider quality or live model execution.
They give tests and CLI workflows a stable suite for copied workspaces and saved
output. The evaluation harness supports offline tests. It makes live model calls
only when the selected agent command does.

## Suite format

Sample suites live under `internal/agenteval/testdata/`. Tiny fixture
workspaces live under `internal/agenteval/testdata/fixtures/`.

Each suite JSON file contains:

- `id`: stable suite identifier for filters and reports.
- `name` and `description`: maintainer-facing suite metadata.
- `tasks`: coding-agent tasks with prompts, file expectations, verification
  commands, and offline scoring inputs.

Task fields used by the sample suite:

- `id`: stable task identifier for filters and reports.
- `name` and `description`: short task metadata.
- `tags`: stable category labels such as `docs`, `go`, or `wrapper`.
- `difficulty`: a coarse task size such as `easy`, `medium`, or `hard`.
- `prompt`: the user request to give an agent.
- `workspaceFixture`: the fixture workspace to copy before running the task.
- `expectedChangedFiles`: files that should change for a complete solution.
- `forbiddenChangedFiles`: files that must not change during the task.
- `requiredTraceEvents`: JSONL agent events that must appear in benchmark
  stdout.
- `contextChecks`: required and forbidden files checked in the materialized
  workspace before verification commands run.
- `verificationCommands`: commands a maintainer or harness can run after the
  agent output is applied.

The score contract matches command results by `verificationCommands[].id` and
compares changed files against `expectedChangedFiles`. It rejects any
`forbiddenChangedFiles` and checks the workspace with `contextChecks`.
It can also require agent trace events. The loader rejects unknown JSON fields.

Example richer task rubric:

```json
{
  "tags": ["docs", "jsonl"],
  "difficulty": "easy",
  "forbiddenChangedFiles": ["go.mod", "package.json"],
  "requiredTraceEvents": ["tool:read_file", "verify:go-test"],
  "contextChecks": {
    "requiredFiles": ["docs/STREAM_JSON_PROTOCOL.md"],
    "forbiddenFiles": ["node_modules/cache.txt"]
  }
}
```

## Modes

### Validate mode

`splice eval` defaults to `validate` mode. This mode performs schema and contract
checks only. It parses the suite, rejects invalid task definitions, and reports
the task and check counts. It does not copy fixtures, invoke an agent, score a
workspace, or execute verification commands.

```bash
go run ./cmd/splice eval --suite internal/agenteval/testdata/sample_suite.json
```

Use JSON output when another local tool needs the validation summary:

```bash
go run ./cmd/splice eval --suite internal/agenteval/testdata/sample_suite.json --json
```

### Run mode

`splice eval run` scores one modified Git workspace. It does not copy fixtures
or invoke an agent. Use a Git worktree with a prepared fixture and an agent
attempt. A deterministic local script can make the attempt.

The runner executes each `verificationCommands` entry. It collects changed files
with `git status --porcelain` and emits the task-success report.

`--workspace` is required. It must identify the prepared fixture worktree, not
the current directory. This rule protects the source repository from fixture
verification commands.

```bash
go run ./cmd/splice eval run \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --workspace /tmp/splice-eval-workspace
```

Persist the report for comparison between prompt or model changes:

```bash
go run ./cmd/splice eval run \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --workspace /tmp/splice-eval-workspace \
  --report-dir /tmp/splice-eval-report \
  --json
```

### Bench mode

`splice eval bench` runs the full benchmark harness for one task or suite. It
copies each fixture into `--work-root` and creates a clean Git baseline.
It then runs an agent and uses the same score rules as run mode.

Omit `--agent-command` to use the built-in pipeline runner. The runner resolves
the current Splice executable and starts this argv:

```text
<splice-executable> --no-trust exec --output-format stream-json [--model <requested-model>] <prompt>
```

The child uses the production provider configuration, model route, tool
registry, sandbox, permission mode, memory, and event setup. The child uses the
materialized workspace as its current directory. The `--no-trust` flag keeps
fixture-owned project extensions disabled because the child is untrusted.

Pass `--agent-command` to select an external agent. This option remains the
external-agent override. The command receives the materialized workspace as its
current directory.

Agent commands are passed as argv, without shell interpolation. The harness
expands these placeholders in each argument:

- `{workspace}`: copied task workspace path.
- `{prompt}`: task prompt from the suite.
- `{task_id}`: selected task ID.
- `{model}`: current model ID for model-matrix benchmark runs.

Use `--model <id>` more than once, or `--models a,b,c`, to run the same task
matrix across several models. The model value is the requested primary model.
Model routing stays active in model matrices, so stages can use other models.
The report lists the requested model and the models used by the requests. When no model is supplied,
the harness preserves the previous single-run behavior and `{model}` expands to
an empty string.

The harness parses usage events as the agent runs. Usage totals remain correct when
diagnostic stdout exceeds 8 MiB. Captured stdout and stderr each have an 8 MiB
limit, but usage events remain available to the collector.

Example using a real local agent command:

```bash
go run ./cmd/splice eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --work-root /tmp/splice-evals \
  --agent-command splice exec --cwd {workspace} {prompt}
```

Example with model selection:

```bash
go run ./cmd/splice eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --work-root /tmp/splice-evals \
  --model gpt-5 \
  --agent-command splice exec --model {model} --cwd {workspace} {prompt}
```

Include `{task_id}` when the agent wrapper needs stable per-task logging,
branching, or fixture-specific behavior:

```bash
go run ./cmd/splice eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --work-root /tmp/splice-evals \
  --agent-command splice-agent-wrapper --task {task_id} --workspace {workspace} --prompt {prompt}
```

The same wrapper can emit JSONL trace events to stdout for future trace scoring:

```json
{"type":"tool","name":"read_file"}
{"event":"verify","name":"go-test"}
```

Those events are required by adding keys such as `tool:read_file` and
`verify:go-test` to `requiredTraceEvents`.

For an offline test, point `--agent-command` at your own local script. The
script can edit the copied workspace without a model call:

```bash
go run ./cmd/splice eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --work-root /tmp/splice-evals \
  --agent-command /path/to/offline-agent --workspace {workspace} --task {task_id} --prompt {prompt}
```

Set `--timeout` to a Go duration so a stalled or interactive agent cannot block
the harness forever. The timeout applies per
task and cancels materialization, the agent process, and scoring:

```bash
go run ./cmd/splice eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --work-root /tmp/splice-evals \
  --timeout 5m \
  --agent-command /path/to/offline-agent --workspace {workspace} --prompt {prompt}
```

Use `--report-dir` to save the CLI report. The file name is always
`agent-eval-report.json`. Bench mode records suite status, task counts, failures,
and each task and model run. Use `--keep-workspaces` to inspect workspaces after
the run:

```bash
go run ./cmd/splice eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task add-npm-wrapper-argv-helper \
  --work-root /tmp/splice-evals \
  --keep-workspaces \
  --report-dir /tmp/splice-eval-report \
  --json \
  --agent-command /path/to/offline-agent --workspace {workspace} --task {task_id} --prompt {prompt}
```

## Changed-file score limit

The changed-file score inspects the workspace with `git status --porcelain`
against the baseline commit. An agent that *commits*
its own changes (or otherwise leaves a clean working tree) defeats this check.
The committed edits no longer appear as changed files, so `expectedChangedFiles`
will not match. Agents under bench should leave their edits uncommitted.

Run the package tests when changing the suite schema or scorer:

```bash
go test ./internal/agenteval
```

For a faster manual fixture check:

```bash
go test ./internal/verify ./internal/selfverify
```

Or parse the JSON directly with any strict JSON parser. For example:

```bash
python -m json.tool internal/agenteval/testdata/sample_suite.json
```

The `internal/agenteval` tests load every JSON file under
`internal/agenteval/testdata/` and reject missing task IDs, empty verification
commands, and malformed changed-file expectations.

## Report JSON

Scored reports use contract `splice.agenteval.report.v1`.

- `suiteId` and `taskId`: identify the suite and selected task.
- `status`: overall `pass`, `fail`, `blocked`, or `error`.
- `ok`: true only when every result passes.
- `summary`: total result counts by status.
- `changedFiles`: normalized files collected from the workspace.
- `results`: one result per verification command, plus configured
  `changed_files`, `forbidden_changed_files`, `context_checks`, and
  `trace_events` checks.
- `error`: task-selection or report-level error, when present.

Command results include the command ID, display name, command argv, status,
exit code, stdout, stderr, and an optional message. File-based results include
expected, actual, missing, and unexpected files. Trace results include expected,
actual, and missing event keys.

The CLI wrapper uses contract `splice.cli.eval.v1`. The benchmark report uses
contract `splice.agenteval.benchmark.v2`. The score report uses contract
`splice.agenteval.report.v1`. These contracts version independently.

All eval costs are estimates. Each usage record can include the cost
provenance, price source, and price date. Unknown price never becomes
zero. Cost coverage is one of `complete`, `partial`, `unavailable`, or
`not_applicable`.

The benchmark CSV uses these columns, in this order:

```text
taskId,runner,requestedModel,modelsUsed,status,pass,inputTokens,outputTokens,cachedInputTokens,cacheWriteTokens,reasoningTokens,estimatedCostUSD,costCoverage,pricedUsageRecords,unpricedUsageRecords,errorUsageRecords,latencyMs,stageBreakdown
```

The `runner` value is `splice_pipeline` or `external_command`. The
`requestedModel` value identifies the primary model requested for the row. The
`modelsUsed` value lists the routed models seen in usage and stage records.
When the top-level cost is unknown, `estimatedCostUSD` is an empty CSV cell.
Inside `stageBreakdown`, an unknown cost uses `cost=unknown`.

### External agent CSV migration

An external agent remains valid with `--agent-command`:

```bash
splice eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --agent-command /opt/my-agent --workspace {workspace} --prompt {prompt}
```

Change a CSV parser that used the old fields:

```text
old: model, costUSD
new: requestedModel, estimatedCostUSD, modelsUsed, costCoverage
```

Read an empty `estimatedCostUSD` cell as unknown. Do not convert it to zero.
Split `modelsUsed` on commas. Use `costCoverage` before you use a cost value.

## Score interpretation

Scores are offline quality signals, not pass/fail release gates by default. The
statuses below are produced when a harness supplies captured command results and
changed files.

- `pass`: every verification command exited successfully, changed files matched
  `expectedChangedFiles`, forbidden files stayed untouched, configured context
  checks passed, and required trace events were present.
- `fail`: at least one command failed, changed files were missing or
  unexpected, a forbidden file changed, a context file check failed, or a
  required trace event was missing.
- `blocked`: the harness could not run the task or collect the expected inputs.
- `error`: the suite, task ID, command ID, or captured input could not be
  interpreted.

Real task-success measurement comes from the combination of prompt, fixture,
verification commands, and changed-file expectations. Prefer comparing results
between runs of the same suite revision. Do not compare results across suites
unless the task mix and scoring contract are unchanged.
