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
| `splice pipeline` | List, show, use, export, import, or edit pipeline topologies. |
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
splice exec --pipeline my-pipeline "run a custom topology"
```

`--pipeline` selects a pipeline topology for one run. The value is a library
name, a file path, or `default`. See
[Custom topologies](PIPELINE.md#custom-topologies).

A `--worktree` run from a subdirectory of the repository uses the same
subdirectory of the worktree. Trust, project config, and `.splice/pipeline.json`
come from that directory. The worktree has only committed files. A run from an
untracked or ignored subdirectory stops with exit 2 and removes the new
worktree. Commit the directory, or run from the repository root.

`--max-wall <duration>` sets the wall-time limit of the pipeline run, for
example `30m`. The default is 10 minutes. See
[Pipeline behavior](PIPELINE.md#stop) for the other sources of this limit.

`--merge-back` merges only a verified run. A completed run that verified
nothing exits `4` and keeps the worktree. The source branch and commit must
not change while the run works. Otherwise Splice does not merge, and the
worktree keeps its changes.

`--merge-back` removes the worktree after a merge or when there are no changes.
It leaves the worktree after a conflict, a skipped merge, or a failed run.

`--allow-unverified-merge` lets `--merge-back` merge a completed run that
verified nothing. This option is not safe. Splice writes a warning with the
reason before it merges.

`--require-verified` makes a completed run that verified nothing exit `4`.
Use it in CI. A `--plan` run is verified only when each task verified. The
reason names the first task that did not verify.

A resumed design plan is reported as not verified. The run did not check
again the tasks that completed before. A gate that needs a verified run,
such as `--merge-back` or `--require-verified`, refuses it.

`--loop` and `--use-spec` runs have no verification step. Splice refuses
`--merge-back` with `--loop`, and `--require-verified` with `--loop` or
`--use-spec`.

`--serve` starts a local browser surface for the run. Splice prints a one-time
URL to stderr. Open the URL in a browser on this computer. The server listens
on 127.0.0.1 only. It stops when the run ends, or after 30 minutes without
activity. Standard output does not change.
The surface includes the pipeline viewer, which shows the live graph of the
run. See [Pipeline behavior](PIPELINE.md#see-the-pipeline-in-a-browser).

Use `--auto low` for conservative automation. Review the policy before you use
`medium`, `high`, or `--skip-permissions-unsafe`.

## Configure models

| Command | Purpose |
|---|---|
| `splice setup` | Create the first provider profile. |
| `splice config` | Show resolved configuration without secret values. |
| `splice flags` | List feature flags, their values, and where each value comes from. |
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

## Inspect state in the TUI

The TUI can show the read-only form of some CLI commands. The output is the
same as the CLI output.

| TUI command | CLI command |
|---|---|
| `/usage` | `splice usage report` |
| `/changes` | `splice changes inspect` |
| `/verify` | `splice verify` |
| `/hooks` | `splice hooks list` |
| `/plugins` | `splice plugins list` |
| `/specialists` | `splice specialist list` |
| `/flags` | `splice flags` |
| `/pipeline [use <name> \| edit [--new] [name]]` | `splice pipeline show`. `/pipeline use <name>` runs `splice pipeline use`. `/pipeline edit [--new] [name]` opens the same editor as `splice pipeline edit`. |

`/verify` runs the checks of the project. It runs only in a trusted
workspace. Use `/trust` first.

To commit, push, add, or remove, use the CLI command.

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
| `2` | The command or input was invalid. This includes a topology that does not load or validate, and a node pin or stage route that does not resolve (also with `--plan`). Splice finds these before it sends a provider request. |
| `3` | Setup failed, so no stage result exists. Examples are the config, a provider that cannot start (also for a valid route), the sandbox, the stage registry, and the trajectory classifier. A failed `--loop` run also exits `3`. |
| `4` | The pipeline ran, but the run ended without a complete result. This includes a stage that failed (also when its provider request failed), a trajectory stop, and the iteration or wall-time limit. The final result gives the reason. |
| `130` | The user or process interrupted the run. |

A completed run that verified nothing also exits `0`. Its final JSON result
sets `verified` to `false`, and `unverified_reason` names the checks that did
not apply. Read `verified` before you treat a `0` exit as verified. With
`--require-verified` or `--merge-back`, this run exits `4` instead.

Other internal failures can return a nonzero code. Stream clients must treat the
terminal `run_end.exitCode` as authoritative.

## TUI slash commands

Type a slash command in the TUI composer. An alias works the same as its
command. Each command belongs to one group.

Some of these commands show the same output as a CLI command. The TUI
commands work only in the interactive session.

### Model

| Command | Aliases | Usage | Description |
|---|---|---|---|
| `/provider` | none | `/provider [status]` | Open the provider setup. |
| `/model` | none | `/model [list\|id]` | Show or change the active model. |
| `/stages` | none | `/stages` | Show and edit the model for each stage. Settings are in `stage-models.json`. |
| `/effort` | none | `/effort [list\|auto\|<level>]` | Show or set the reasoning effort. `/effort list` shows the levels for this model. |

### Session

| Command | Aliases | Usage | Description |
|---|---|---|---|
| `/plan` | none | `/plan` | Show the planning mode status. |
| `/context` | none | `/context` | Show the workspace and runtime context. |
| `/trust` | none | `/trust` | Set trust for this folder or its parent. |
| `/image` | none | `/image <path> \| clear` | Attach a local image or PDF to the next message. `/image clear` removes pending attachments. |
| `/new` | none | `/new` | Start a new session. Use `/resume` to return to the old one. |
| `/resume` | `/sessions` | `/resume [id]` | List recent sessions, or show how to resume one. |
| `/retitle` | none | `/retitle` | Generate short titles for resumable sessions. |
| `/spec` | none | `/spec <task>` | Draft an implementation spec for review before you edit. |
| `/design` | none | `/design` | Enter design mode to plan before execution. |
| `/crystallize` | none | `/crystallize` | Convert the design discussion into a typed plan. |
| `/approve` | none | `/approve` | Approve the pending design plan and run it. |
| `/memory` | none | `/memory [search <query> \| recent \| forget <id> \| prune]` | Show memory statistics. Search, list, forget, or prune memory items. |
| `/exec` | none | `/exec [prompt]` | Run a prompt through the execution pipeline. Design mode is skipped. |
| `/layout` | none | `/layout [list\|preset]` | Show the layout state, or apply a preset: default, compact, execution, review, or minimal. |
| `/init` | none | `/init` | Read the repository and create an `AGENTS.md` file for the agent. |
| `/compact` | none | `/compact [status\|now]` | Compact the transcript now. `/compact status` shows the compaction state. |
| `/transcript` | none | `/transcript` | Turn the detailed transcript view on or off. |
| `/rewind` | none | `/rewind [latest\|<sequence>]` | Restore workspace files to a checkpoint. Remove the later session events. |
| `/style` | none | `/style [list\|balanced\|concise\|explanatory\|review]` | Show or set the response style. |
| `/selfcorrect` | `/sc` | `/selfcorrect [status\|on\|off\|tests\|full\|lsp]` | Show or set the depth of post-edit self-correction. |
| `/turns` | none | `/turns [n]` | Show or set the tool-turn budget for each run in this session. Raise it for long tasks. |
| `/retry` | none | `/retry` | Send your last prompt again. |
| `/edit` | none | `/edit` | Copy your last prompt into the composer so you can edit and send it. |
| `/copy` | none | `/copy` | Copy the last answer to the clipboard. |
| `/export` | none | `/export [path]` | Write the conversation transcript to a file. |
| `/loop` | none | `/loop [interval] <prompt\|/command> \| list \| stop [id\|all]` | Repeat a prompt or command on an interval, such as `/loop 5m /babysit-prs`. Without an interval, the model sets the pace. |
| `/theme` | none | `/theme [list\|auto\|name]` | Pick a color theme. Use `auto` to follow the terminal background. |
| `/usage` | `/cost` | `/usage [--days N] [--since YYYY-MM-DD] [--session ID]` | Show token use and estimated cost from local session history. |
| `/changes` | none | `/changes [--base REF]` | Show local git changes. This command is read-only. Commit and push stay in the CLI. |

### Runtime

| Command | Aliases | Usage | Description |
|---|---|---|---|
| `/permissions` | none | `/permissions` | Show the active permission mode and sandbox grants. |
| `/ps` | none | `/ps` | List running background terminal sessions. |
| `/stop` | none | `/stop [session_id]` | Stop background terminal sessions. Give a session ID to stop one. |
| `/sandbox-setup` | none | `/sandbox-setup` | Run the native sandbox setup for this platform. |
| `/add-dir` | none | `/add-dir [path]` | Grant write access to a directory outside the workspace for this session. Without a path, list the current write roots. |
| `/flags` | none | `/flags` | Show feature flags, their values, and the source of each value. |
| `/web` | none | `/web` | Start the local browser surface and show its one-time link. The link works once, and a browser opens too when the terminal is interactive. The surface stops when the TUI exits. The surface includes the pipeline viewer: the live graph of a run, or the active topology when no run exists. |
| `/pipeline` | none | `/pipeline [use <name> \| edit [--new] [name]]` | Show the active pipeline graph, where it comes from, its warnings, and its command nodes. Use `/pipeline use <name>` to select a library pipeline. Use `/pipeline edit [name]` to edit a pipeline in the browser editor. See [Pipeline behavior](PIPELINE.md#edit-a-pipeline-in-a-browser). |
| `/doctor` | `/health` | `/doctor [fix\|--connectivity]` | Show diagnostics. Use `fix` to open safe fixes, or `--connectivity` to test the provider. |
| `/config` | none | `/config [recaps on\|off]` | Show the active configuration. Use `recaps on` or `recaps off` to change recaps. |
| `/debug` | `/debug-mode` | `/debug` | Show the debug mode status. |
| `/verify` | none | `/verify [--only IDS]` | Detect and run the verification checks of this project. Trusted workspaces only. |

### Tools

| Command | Aliases | Usage | Description |
|---|---|---|---|
| `/tools` | none | `/tools` | List registered tools. |
| `/skills` | none | `/skills` | List installed skills. Run one directly with `/<skill-name>` and its arguments. |
| `/search` | `/find` | `/search <query>` | Search local session events. A query is required. |
| `/mcp` | `/mcp-status` | `/mcp` | Show MCP server status. |
| `/hooks` | none | `/hooks` | List configured hooks. |
| `/plugins` | none | `/plugins` | List installed plugins. |
| `/specialists` | none | `/specialists` | List built-in, user, and project specialists. |

### Meta

| Command | Aliases | Usage | Description |
|---|---|---|---|
| `/help` | none | `/help` | Show the available commands. |
| `/clear` | none | `/clear` | Clear the visible transcript. |
| `/exit` | `/quit` | `/exit` | Exit Splice. |
