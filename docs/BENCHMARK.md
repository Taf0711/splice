# Task benchmark harness

The task benchmark runs `splice exec` against prepared repositories and checks
the result with repository commands.

This harness measures a complete model and Splice configuration. It does not
isolate model quality from pipeline quality.

No task-success number is published in this repository.

## Result record

Each run produces JSON with these fields:

| Field | Purpose |
|---|---|
| `suite` | Task-set ID |
| `model` | Requested model |
| `mode` | Optional execution preset |
| `selfCorrect` | Value passed to the harness |
| `version` | Splice version |
| `commit` | Source commit |
| `date` | UTC run time |
| `tasksAttempted` | Number of attempted tasks |
| `tasksPassed` | Number of verified tasks |
| `passRate` | Passed tasks divided by attempted tasks |
| `tasks` | Per-task status and evidence |

Keep the JSON record with every published result. A pass rate without its model,
commit, suite, and verifier is not reproducible.

## Execution path

The harness starts this form of command for each task:

```bash
splice exec --output-format stream-json --model <model> "<task prompt>"
```

It reads the terminal `run_end.exitCode`. When a task defines a
`verificationCommand`, that command is authoritative.

A task passes only when the external verifier exits successfully. A successful
agent exit does not override a failed verifier.

## Current self-correct limit

The harness accepts `--self-correct` and forwards the value to `splice exec`.
The deterministic pipeline does not use the interactive post-edit self-correct
loop.

Therefore, do not publish a baseline versus self-correct delta from this harness.
The two runs do not test different correction behavior.

Use the trajectory results inside the pipeline record when you need evidence of
pipeline retries or recovery.

## Task-set format

A task set is a JSON manifest. Each task can provide:

- an ID and prompt;
- a workspace fixture;
- a verification command; and
- descriptive metadata.

The checked-in sample is suitable for parser and dry-run checks:

[`cmd/splice-perf-bench/testdata/terminal-bench-sample.json`](../cmd/splice-perf-bench/testdata/terminal-bench-sample.json)

Its referenced workspace directories are not included. Do not use it for a live
score without replacement fixtures.

Validate the record path without a model:

```bash
go run ./cmd/splice-perf-bench tasks \
  --suite cmd/splice-perf-bench/testdata/terminal-bench-sample.json \
  --model dry-run \
  --dry-run \
  --json
```

A dry run records each task as skipped.

## Run a real suite

Create a task set whose `workspaceFixture` paths exist. Then build the release
binary and run the harness:

```bash
go run ./cmd/splice-release build

VERSION=$(git describe --tags --always)
COMMIT=$(git rev-parse --short HEAD)
SUITE=/path/to/task-suite.json
MODEL=MODEL_ID

go run ./cmd/splice-perf-bench tasks \
  --suite "$SUITE" \
  --binary ./splice \
  --model "$MODEL" \
  --version "$VERSION" \
  --commit "$COMMIT" \
  --output dist/bench/tasks.json
```

The version and commit can also come from:

```text
SPLICE_BENCH_VERSION
SPLICE_BENCH_COMMIT
```

Use a fixed machine, provider route, model ID, task-set revision, and timeout for
a comparison.

## Publish a result

Publish these items together:

1. The task-set file and fixture revision.
2. The Splice commit and version.
3. The requested provider and model route.
4. The execution mode and autonomy value.
5. The result JSON.
6. The host operating system and architecture.
7. Any external verifier dependencies.

Report failures and blocked tasks. Do not remove them from the denominator after
the run.

## Reproduce a result

1. Check out the recorded commit.
2. Restore the recorded suite and fixtures.
3. Build the release binary.
4. Use the recorded model and configuration.
5. Run the same command.
6. Compare per-task evidence before you compare `passRate`.

Read [Performance checks](PERFORMANCE.md) for process startup measurements. Read
[Offline agent evaluations](AGENT_EVALS.md) for fixture and score contracts.
