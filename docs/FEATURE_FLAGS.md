# Feature flags

A feature flag is a named on or off switch. A flag turns one Splice behavior
on or off. Each flag has a name, such as `memory.prefetch`.

## Set a flag

Use any of these forms. Later forms win over earlier forms.

| Form | Example |
|---|---|
| User configuration | `"flags": {"memory.prefetch": true}` in the user `config.json` |
| Project configuration | `"flags": {"tui.pipeline.enabled": false}` in `<workspace>/.splice/config.json` |
| Legacy variable | `SPLICE_MEMORY_PREFETCH=on` |
| Environment list | `SPLICE_FLAGS="memory.prefetch,-pipeline.stage.test_generator"` |
| Command line | `splice --flags memory.prefetch` or `splice exec --flags memory.prefetch` |

Project configuration can set only project-scope flags. A project file that
sets a user-scope flag is an error. See the Scope column in the table below.

The `SPLICE_FLAGS` list and the `--flags` option use the same grammar:

- `name` turns the flag on.
- `-name` turns the flag off.
- `name=1`, `name=true`, or `name=on` turns the flag on.
- `name=0`, `name=false`, or `name=off` turns the flag off.

A name can appear only once in one list.

The root `--flags` option goes before the subcommand. It applies to the
interactive TUI and to commands that read configuration, such as `exec`,
`doctor`, `flags`, and `serve`. Other commands reject `--flags` with a usage
error. Root `--trust` and `--flags` may come in either order. A root
`--flags` value may appear only once. When an `exec --flags` value and a root
`--flags` value set the same name, the `exec` value wins.

Use `SPLICE_TREATMENT` only for evaluation runs. When it is set, it can
override `memory.scope`. `splice flags` does not show this override.

## Legacy variables

Some flags also read an older environment variable. The legacy variable sets
the flag at the legacy step of the precedence order. Values are compared
without case and without surrounding spaces. An empty value means not set.

- `SPLICE_MEMORY_PREFETCH`: `on` turns the flag on. `off` turns it off. Any
  other value is an error.
- `SPLICE_EVIDENCE_SUBSTITUTION`: `on` turns the flag on. `off` turns it off.
  Any other value is an error.
- `SPLICE_SCOPE_MODE`: `on` turns the flag on. `off` turns it off. Any other
  value is an error.
- `SPLICE_BUDGET_FIT`: `fit` turns the flag on. `off` turns it off. Any other
  value is an error.
- `SPLICE_PASS_EVIDENCE`: `on` turns the flag on. `off` turns it off. Any
  other value is an error.
- `SPLICE_FORMAT_ON_WRITE`: `1`, `true`, `on`, or `yes` turns the flag on.
  `0` or `false` turns it off. Any other non-empty value turns it on.
- `SPLICE_CHECKPOINTS`: `on` turns the flag on. `off` turns it off. Any other
  non-empty value turns it on.
- `SPLICE_DISABLE_PROMPT_CACHE_KEY`: this variable is a disable switch. It
  turns `provider.prompt_cache_key` OFF. `1`, `true`, `on`, or `yes` turns the
  flag off. `0` or `false` leaves the flag on. Any other non-empty value turns
  the flag off.

## Precedence

Splice applies these sources in order. A later source replaces an earlier one.

1. Default value in the registry
2. User configuration
3. Project configuration
4. Legacy environment variable
5. `SPLICE_FLAGS`
6. `--flags`

## Check flags

Run `splice flags` to list each flag. The list shows the value, the source that
set it, and the stability. A value that differs from its default ends in `*`.

```bash
splice flags
splice flags --json
```

In the interactive TUI, run `/flags` to see the same flags. The command is
read-only. To change a flag, edit the configuration or an environment variable.

`splice doctor` prints one warning when an experimental flag is on.

## Stability

A stable flag is a supported behavior switch.

An experimental flag is a treatment under measurement. Experimental flags are
off by default. An experimental flag can change or go away in a later release.

## Pass evidence

The flag `pipeline.pass_evidence` is experimental and off by default.

Splice measures each pass against the end of the pass before it.
The HEAD summary counts every change since HEAD.
The pass measure counts only the change that one pass made.

Splice records the pass evidence on every run, whatever the flag says.
The record appears in the run trace and in each decision record.
The flag decides which evidence the trajectory reads.

With the flag on, Splice does these things:

- The trajectory reads the changed files and line counts of each pass.
  A pass that changed nothing reads as no change.
- A no-progress rule reason ends with the code writer name and the text
  "changed no files this pass."
- Splice prints one notice for each pass with files that no stage reported.
  The notice names up to five files and counts the rest.
- The step-back report lists the files of each pass, not the files changed
  since HEAD.
- The changed files of the run outcome in the trace are the files of all
  passes together. A file that was changed before the run and that no pass
  changed is not in this list.

With the flag off, the trajectory reads the HEAD-relative values.
The rule text does not change. The notice does not appear.

## Flag table

| Flag | Default | Stability | Scope | Legacy variable | Description |
|---|---|---|---|---|---|
| `pipeline.stage.security_auditor` | on | stable | user | - | Run the security auditor stage in the pipeline. |
| `pipeline.stage.test_generator` | on | stable | user | - | Run the test generator stage in the pipeline. |
| `tui.pipeline.enabled` | on | stable | project | - | Route interactive TUI turns through the pipeline. /exec always uses the pipeline. |
| `pipeline.decision_advisor` | off | experimental | user | - | Enable the decision advisor per its decision_advisor config mode. |
| `pipeline.trajectory_classifier` | on | stable | user | - | Run the trajectory classifier; by default it uses a ChatGPT sign-in profile, else the rules decide. |
| `memory.prefetch` | off | experimental | user | `SPLICE_MEMORY_PREFETCH` | Prefetch the source file that a verified memory record names. |
| `memory.evidence_substitution` | off | experimental | user | `SPLICE_EVIDENCE_SUBSTITUTION` | Substitute verified memory evidence for reads in the first stage request. |
| `memory.scope` | off | experimental | user | `SPLICE_SCOPE_MODE` | Scope context acquisition and tool behavior to the cognition scope. |
| `pipeline.budget_fit` | on | stable | user | `SPLICE_BUDGET_FIT` | Apply learned stage budgets; off freezes every stage at its static budget. |
| `pipeline.pass_evidence` | off | experimental | user | `SPLICE_PASS_EVIDENCE` | Measure each pass's workspace change since the previous pass, not since HEAD. |
| `tools.format_on_write` | off | stable | user | `SPLICE_FORMAT_ON_WRITE` | Run the language formatter on each file that a tool writes. |
| `sessions.checkpoints` | on | stable | user | `SPLICE_CHECKPOINTS` | Capture file checkpoints before each mutating tool call. |
| `provider.prompt_cache_key` | on | stable | user | `SPLICE_DISABLE_PROMPT_CACHE_KEY` | Send the session prompt cache key to the OpenAI-compatible provider. |
