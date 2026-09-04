# Confirmed root-cause evidence for the repair tails (2026-09-04)

These are trace-level facts extracted from the after-replay-guard runs,
kept beside the results so the next session does not re-derive them.

## The suite-fallback evidence gap (primary)

The Go test runner (`internal/splice/stages/test_runner.go:106-135`)
never parses per-test results. `runCommand` captures stdout/stderr and
exit code only; on any failure the results carry a SINGLE synthesized
entry:

    Tests: [{Name: "suite", Status: "failed", Message: "exit code 1"}]

Consequences, all observed in the fam-05/fam-01 aborted traces:

1. The repair interaction's `failing_evidence` is literally
   `["suite: exit code 1"]` (`repair.go:192` extractFailingTests iterates
   results.Tests, which has only the suite entry). The re-entered
   code_writer gets no test name, no assertion text, no file/line.
2. `failingEvidenceExcerpt()` (test_runner.go:363) DOES extract bounded
   failure blocks - but only into the stage summary string, which never
   reaches the repair interaction payload. There is a plumbing gap
   between the excerpt and `extractFailingTests`.
3. `splitTestCounts` (trajectory.go:447) partitions the single suite
   entry as PREEXISTING (no authored func names match "suite"), so every
   iteration reports `preexisting fail=1, authored 0/0/0` regardless of
   what the writer fixed. Trajectory progress signals are blind.
4. The `go test ./...` command is run WITHOUT `-json` and WITHOUT `-v`
   (testrunner.go:99), so per-test data does not even exist in the
   captured output stream format that would make parsing easy.

## What the model saw on every fam-05 repair iteration

    failing_evidence: ["suite: exit code 1"]
    instruction: "The test runner reports failing tests after your
    implementation. Fix the implementation so these tests pass."

Each iteration re-edited main.go (41->45 lines changed), the same
opaque failure returned, budget aborted. Identical shape on fam-01
(3 repair iterations, same single-line evidence each time) and fam-07.

This is repair-evidence starvation, upstream of anything memory could
fix - and it explains why replay suppression alone did not remove the
tails: the writer was never able to react to failure evidence because
the evidence it received did not name the failure.

## Smallest instrument (candidate next experiment)

Make the Go test path run `go test -json ./...` (or `-v`), parse
per-test results into TestRunResults.Tests, and let the existing
extractFailingTests/splitTestCounts machinery light up. Fallback to the
suite entry only when parsing fails (compile errors: thread the
compiler output excerpt through failingEvidenceExcerpt into the repair
payload). Everything downstream (repair evidence, trajectory signals,
authored/preexisting partition) starts working without further schema
changes.
