# Splice Performance Benchmarks

The performance harness tracks three release-facing signals:

- Cold start: process startup time for `splice --version`.
- Binary first output: time from spawning the built `splice --version` command to
  the first stdout or stderr chunk.
- Harness end memory: RSS for the Go benchmark harness after the spawned
  command exits, plus the delta from the pre-spawn RSS sample.

Cold start uses the built Go binary at `./splice` or `./splice.exe`. Run
`go run ./cmd/splice-release build` before the benchmark so it measures the
production runtime.
On Linux the harness memory metric reads RSS from `/proc/self/statm`; on other
hosts `readHarnessMemoryMb()` falls back to `runtime.ReadMemStats()` and reports
`MemStats.Sys` in MB when process RSS is not available from the standard library.

This smoke benchmark does not measure provider TTFT or Go agent memory. A
provider-aware Go benchmark should be added separately when the runtime exposes a
deterministic local streaming path.

## Run Locally

```bash
go run ./cmd/splice-perf-bench
```

Run against a freshly built binary:

```bash
go run ./cmd/splice-release build
go run ./cmd/splice-perf-bench
```

Write the JSON report used by CI:

```bash
go run ./cmd/splice-perf-bench --output dist/perf/perf-bench.json
```

Default warning thresholds:

- Cold start p95: 300 ms
- Binary first-output p95: 500 ms
- Harness end RSS max: 256 MB

The default sample count is intentionally small for CI smoke coverage. `p95` uses nearest-rank percentile selection, so with the default 5 measured samples it is the slowest sample. Increase `--iterations` for local baseline investigations.

Override thresholds with CLI flags:

```bash
go run ./cmd/splice-perf-bench --cold-start-warn-ms=350 --first-output-warn-ms=600 --harness-end-rss-warn-mb=384
```

Or with environment variables:

```bash
ZERO_PERF_COLD_START_WARN_MS=350 go run ./cmd/splice-perf-bench
```

Supported environment variables:

- `ZERO_PERF_ITERATIONS`
- `ZERO_PERF_WARMUP_ITERATIONS`
- `ZERO_PERF_COLD_START_WARN_MS`
- `ZERO_PERF_FIRST_OUTPUT_WARN_MS`
- `ZERO_PERF_HARNESS_END_RSS_WARN_MB`

These environment variables retain the upstream ZERO_ prefix; a rename to SPLICE_ is planned.

## CI Behavior

> **Note:** The `Performance Smoke` job described below is **not yet present** in
> `.github/workflows/`. The only workflows committed today are `ci.yml` (build,
> vet, test, cross-build, and the memd sidecar job) and `release-please.yml`
> (changelog/version releases). This section documents the intended CI behavior;
> the job will be added when performance benchmarking is wired into CI.

The `Performance Smoke` job builds the binary, runs
`go run ./cmd/splice-perf-bench --output dist/perf/perf-bench.json --ci`, and
uploads `dist/perf/perf-bench.json`.

Threshold drift is emitted as GitHub Actions warnings. The job fails only if the benchmark cannot run, the build fails, or `--fail-on-warning` is passed explicitly.
