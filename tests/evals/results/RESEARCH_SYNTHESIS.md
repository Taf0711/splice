# Research synthesis integrated into the vertical slice (2026-09-04)

Source: /tmp/splice-research/REPAIR_TAIL_RESEARCH.md (deep-reasoning agent,
1219s, 80 calls, grounded in 60-attempt corpus + full transcripts + code
audit + SWE-agent/OpenHands/Aider survey).

## Key confirmations and additions beyond my trace forensics

1. COLD-ARM CONTROL: fam-07 cold r1 reproduces the full thrash (30.9k
   tokens, 51 tools, 6 repair_exhausted, zero memory). The tail is
   provably not a memory defect. The replay guard correctly unmasked it.
2. Repair-instruction misdirection (NEW): "Fix the implementation so
   these tests pass" points away from writer-authored test defects.
   fam-05 r2: writer edited main.go while its own test file was broken.
   Fix: deterministic test-file attribution via priorChangedFiles.
3. Compile-error blind spot (NEW): failureMarkerPrefixes misses Go build
   output (# pkg / file:line: undefined: X). Even the existing excerpt
   extractor returns empty for the exact failure class dominating tails.
4. Deterministic resampling (NEW): fam-07 warm r1 wrote 9 files with only
   4 distinct contents, 2 byte-identical repeats; zero reads of
   main.go/session.go. Determinism of resampling confirms no new
   information between attempts.
5. Budget policy is an amplifier not a cause; raising budgets lengthens
   thrash. Verifier-mismatch hypothesis refuted for these families.
6. Comparable systems: SWE-agent (interface quality is first-order; edit
   tools return lint errors inline), OpenHands StuckDetector
   (deterministic loop classifier + injected stuck message + stop), Aider
   (verbatim test output into chat content). All route raw diagnostics to
   the re-prompt. Splice routes a badge + generic instruction.

## Integrated into Track A (steered)

- A1: add "# " package-error lines to failure markers for compile errors.
- A3: byte-identical repeat-write hashes feed the no-progress guard.
- A5 (new): test-file attribution flips the repair instruction.

## Integrated into Track B (steered)

- FAILURE nodes anchored to symbols/files/revision; Contradict() marks a
  FACT contradicted via a Contradicts edge from a FAILURE node.
- /graph/exact must support anchor kind 'test' for failing-test queries.

## Experiment queue AFTER the vertical slice lands (owner-gated spend)

- E4 (free): -json parse-quality probe unit test.
- E1: evidence plumbing into repair payload (~$3-4 / 60 attempts; $1.50
  scoped to fam-01/05/07/10). Signal: fam-07 warm tools 51-><=20;
  aborts 4/6 -> 0-1; COLD fam-07 r1 improves too (confirmation memory
  was never the cause).
- E2: instruction-direction fix as ablation on E1 (~$1.50).
- E3: repeat-write breaker: measure-only first over archived transcripts
  (free), policy version only if stalls persist.
- E5: acceptance_facts_count telemetry-only probe, deferred.

## What NOT to do (from research, adopted)

No retrieval redesign for this tail; no LLM judge; do not raise budgets
to hide the loop; do not touch eval tasks/verifiers; no model-escalation
retry yet; do not suppress cold-arm failures; do not re-litigate the
replay guard.
