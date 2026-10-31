# SWE-bench continual-learning taskset

This taskset contains the 50 SymPy tasks from the SWE-bench continual-learning curriculum.
The tasks run in dataset order. The first task uses the `write` phase.
Each later task uses the same workspace and names the first task as its precursor.

## Generate the taskset

Run this command:

```sh
python3 tests/evals/swebcl/gen_taskset.py \
  --repo sympy/sympy \
  --fixture "$HOME/.cache/splice-bench/sympy" \
  --out tests/evals/swebcl/sympy-taskset
```

Use `--limit N` for a short taskset. Use `--exclude-invalid validation.json` after validation.

## Validate the taskset

Set the Python environment first:

```sh
export SWEBCL_PYTHON="$HOME/.cache/splice-bench/venv-sympy39/bin/python"
```

Run validation from the repository root:

```sh
python3 tests/evals/swebcl/validate.py \
  --taskset tests/evals/swebcl/sympy-taskset
```

Use `--limit 3` to validate the first three tasks. The validator copies the fixture under
`$HOME/.cache/splice-bench/validate`. It checks the base failure and the gold pass.

## Run the campaign

Add the environment to `PATH` so SymPy can find its tools:

```sh
export PATH="$HOME/.cache/splice-bench/venv-sympy39/bin:$PATH"
export SWEBCL_PYTHON="$HOME/.cache/splice-bench/venv-sympy39/bin/python"
```

Build the warmcost runner, then run the campaign with shared retention:

```sh
go build -o /tmp/warmcost-eval ./tests/evals/warmcost/cmd/warmcost-eval
/tmp/warmcost-eval \
  --binary /path/to/splice \
  --tasks tests/evals/swebcl/sympy-taskset \
  --out tests/evals/results \
  --retention shared \
  --sidecar-root /path/to/warmcost-sidecars \
  --arms cold,warm \
  --repeats 1
```

The task `setup` script checks out the task commit and writes `.splice/config.json`.
The task prompt contains the problem statement only. Hints and gold patches stay outside the workspace.
