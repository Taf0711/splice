# Command guide

This guide groups the public CLI commands by task. The built-in help is the
exact flag reference.

```bash
splice --help
splice exec --help
splice <command> --help
```

## Run Splice

| Command | Purpose |
|---|---|
| `splice` | Open the interactive TUI. |
| `splice exec` | Run one prompt, a saved plan, or a stream request. |
| `splice daemon` | Manage the local background worker. |
| `splice cron` | Manage local scheduled jobs. |
| `splice serve` | Start a supported protocol server. |
| `splice acp` | Start the Agent Client Protocol server for an editor. |

Common headless forms:

```bash
splice exec "fix the failing test"
splice exec --file request.md
splice exec --image screenshot.png "explain this error"
splice exec --worktree "try the change in isolation"
splice exec --worktree --merge-back "apply and merge a checked change"
splice exec --output-format stream-json "run the checks"
```

`--merge-back` removes the worktree after a merge or when there are no changes.
It leaves the worktree after a conflict, a skipped merge, or a failed run.

Use `--auto low` for conservative automation. Review the policy before you use
`medium`, `high`, or `--skip-permissions-unsafe`.

## Configure models

| Command | Purpose |
|---|---|
| `splice setup` | Create the first provider profile. |
| `splice config` | Show resolved configuration without secret values. |
| `splice providers` | List, add, select, and check provider profiles. |
| `splice models` | List known models and capabilities. |
| `splice auth` | Manage supported OAuth logins. |
| `splice doctor` | Check configuration and provider health. |

Read [Configuration](CONFIGURATION.md) and
[OAuth and provider login](oauth-subscriptions.md) before you add a custom
endpoint.

## Inspect repository context

| Command | Purpose |
|---|---|
| `splice context` | Report the current context budget. |
| `splice repo-map` | Build a deterministic repository map. |
| `splice repo-info` | Describe the local Git repository. |
| `splice verify` | Detect and run local verification commands. |
| `splice changes` | Inspect, commit, push, or open a pull request. |

Run `splice changes --help` before a command that changes Git state.

## Manage sessions and plans

| Command | Purpose |
|---|---|
| `splice sessions` | List lineage, preview rewind, rewind, compact, or prune. |
| `splice search` | Search local session events. `splice find` is an alias. |
| `splice spec` | Review and decide on saved specification drafts. |
| `splice worktrees` | Prepare or prune isolated Git worktrees. |
| `splice usage` | Report token use and available cost estimates. |

A plain one-shot run can use an ephemeral session. Ephemeral sessions do not
appear in the default session list.

`splice worktrees prepare` creates or reuses an isolated Git worktree.
It locks the worktree and removes other worktrees that satisfy the prune rules.

An `exec` run unlocks its worktree when the run stops. After manual work, use
`git worktree unlock <path>` before reuse or removal.

`splice worktrees prune` removes an unlocked and clean managed worktree only
when its HEAD commit remains reachable from source HEAD or a `splice/*` branch.
It reports all skipped managed worktrees and keeps them. Ignored files also
keep a worktree in place.

## Manage extensions

| Command | Purpose |
|---|---|
| `splice specialist` | Manage named specialist profiles. |
| `splice skills` | Install and inspect local skills. |
| `splice plugins` | Install and inspect plugins. |
| `splice tools` | Create or list plugin tools. |
| `splice hooks` | Manage hooks. |
| `splice mcp` | Manage MCP servers, tools, OAuth, and permissions. |
| `splice backends` | Inspect extension backend state. |

These extensions apply to interactive agent runs. The typed execution pipeline
uses its fixed stage and tool contracts. See [Pipeline behavior](PIPELINE.md).

## Maintain Splice

| Command | Purpose |
|---|---|
| `splice sandbox` | Inspect policy, setup, checks, and persistent grants. |
| `splice update --check` | Check the latest release. |
| `splice update --apply` | Download, verify, and install the latest release. |
| `splice upgrade` | Install the latest release. |
| `splice eval` | Validate or run offline evaluation suites. |

## Headless exit codes

`splice exec` uses these main exit codes:

| Code | Meaning |
|---:|---|
| `0` | The run completed successfully. |
| `2` | The command or input was invalid. |
| `3` | Provider setup or execution failed. |
| `4` | The run ended without a complete result. |
| `130` | The user or process interrupted the run. |

Other internal failures can return a nonzero code. Stream clients must treat the
terminal `run_end.exitCode` as authoritative.
