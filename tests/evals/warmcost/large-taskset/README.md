# large-taskset

The warmcost form of `tests/evals/large-families`: two write/read families
over a 31-file, 10-package Go fixture. The fixture is larger than the
initial-context fallback bound (8 files), so the choice of files matters.

The prompts are shortened from `large-families/cognition-mvp-families.json`
to fit the 320-character intent bound. They keep every name, signature, and
rule the checks test. The checks are the `large-families/verifiers` scripts.
The offline gate is `TestLargeTasksetGates`.
