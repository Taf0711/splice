# Performance checks

The performance command measures three release smoke signals:

- startup time for `splice --version`;
- time to the first output byte; and
- memory used by the benchmark harness after the child exits.

It does not measure provider response time, pipeline duration, or model quality.
Use [Task benchmark harness](BENCHMARK.md) for verified repository tasks.

## Build the measured binary

```bash
go run ./cmd/splice-release build
go run ./cmd/splice-perf-bench
```

The harness uses `./splice` on Unix systems and `./splice.exe` on Windows.

Write a JSON report with:

```bash
go run ./cmd/splice-perf-bench \
  --output dist/perf/perf-bench.json
```

## Default sample

The default run uses one warmup and five measured samples.

| Signal | Warning threshold |
|---|---:|
| Cold-start p95 | 300 ms |
| First-output p95 | 500 ms |
| Harness end memory | 256 MB |

The p95 calculation uses nearest rank. With five samples, the p95 value is the
slowest sample.

Increase `--iterations` before you use the result as a local baseline.

## Memory source

Linux reads resident memory from `/proc/self/statm`. Other systems use the Go
runtime system-memory value when the standard library has no process RSS value.

The memory value describes the benchmark process. It does not describe the
complete Splice child process tree.

## Override thresholds

```bash
go run ./cmd/splice-perf-bench \
  --cold-start-warn-ms=350 \
  --first-output-warn-ms=600 \
  --harness-end-rss-warn-mb=384
```

Supported variables:

```text
SPLICE_PERF_ITERATIONS
SPLICE_PERF_WARMUP_ITERATIONS
SPLICE_PERF_COLD_START_WARN_MS
SPLICE_PERF_FIRST_OUTPUT_WARN_MS
SPLICE_PERF_HARNESS_END_RSS_WARN_MB
```

## CI output

Use `--ci` to emit GitHub Actions warning annotations:

```bash
go run ./cmd/splice-perf-bench \
  --ci \
  --output dist/perf/perf-bench.json
```

A threshold warning does not fail the process by default. Add
`--fail-on-warning` when the workflow must treat warning drift as a failure.

The repository does not run this benchmark in its current GitHub workflows.
Run it explicitly before a release when startup or binary-size work can affect
the command.
