# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.0](https://github.com/Taf0711/splice/compare/v0.2.0...v0.3.0) (2026-11-01)

This release changes most parts of Splice. Read "Breaking changes / action required" before you upgrade from 0.2.0.

### Breaking changes / action required

* **trust:** the project `.splice/config.json` now applies only in a trusted workspace. In 0.2.0, Splice merged the project file in every workspace and held back only hooks, plugins, and MCP servers. If a project needs its config, trust the workspace with `/trust` in the TUI, the `--trust` flag, or `defaultProjectTrust` in the user config. After `/trust`, the project config applies from the next launch. A project file cannot set `worktrees.directory` or `pipeline.max_wall_seconds`. Splice ignores these keys in a project file.
* **trust:** an untrusted workspace now also gets none of these project resources: MCP servers in `splice mcp` commands (`splice mcp check` too), project specialists in `.splice/specialists` (also in `splice specialist list` and `show`), language servers, format on write, and the self-correct test plan. The TUI runs no `git`, `gh`, or `glab` at startup until you trust the workspace. Trust the workspace to use these resources again.
* **project files:** Splice refuses a project file that is larger than 1 MiB, that is not a regular file, or that is a symbolic link to a path outside the workspace. The rule applies to the project config, hooks, plugins, specialists, commands, `.splice/pipeline.json`, and the instruction files `AGENTS.md`, `SPLICE.md`, and `CLAUDE.md`. Replace such a link with a file in the repository, or make the file smaller.
* **tui:** a pipeline run in the TUI now works in an isolated Git worktree by default. Your checkout does not change until you accept the result. When the run ends, choose Accept, Reject, or Keep. Accept merges the work and removes the worktree. Reject keeps the branch `splice/<name>` and removes the worktree. Keep leaves the worktree on disk. Accept is not available when the main checkout has uncommitted changes. Design and spec-draft runs still work in the live checkout. To run pipeline turns in the live checkout, set `"worktrees": {"enabled": false}` in `config.json`.
* **routing:** a `stage-models.json` entry that cannot resolve now stops the run before the first provider request, and the error names the stage. In 0.2.0, the stage used the primary model without a word. An invalid `stage-models.json` also stops a run that needs it. An entry whose model the endpoint of the profile cannot serve exits with code `2` and names `stage-models.json`. Run `splice config describe` to see the model of each stage, and correct the entries before you upgrade a CI job.
* **pipeline:** a required check that cannot run now fails the run. Examples are a check tool that is not installed and a check command that the sandbox refuses. The abort reason names the stage and the check. Splice does not retry, because a second pass cannot install a tool. In 0.2.0, such a run could show as verified. Install the missing tool. A check that does not apply to the workspace is skipped and does not fail the run.
* **sandbox (Linux):** pipeline stages and command nodes now need the native sandbox, which uses bubblewrap. Without bubblewrap, Splice refuses to run them, so a run with a required check fails. `splice sandbox setup` now exits with code `3` and names the install command until bubblewrap is installed. `splice doctor` shows the same command. Install it with `apt install bubblewrap`, `dnf install bubblewrap`, or `apk add bubblewrap`. Splice has no option to run these stages without the sandbox.
* **sandbox:** the file tools (`read_file`, `grep`, and `glob`) now refuse common credential files in every permission mode, `unsafe` mode too: `~/.git-credentials`, `~/.netrc`, `~/.npmrc`, `~/.pypirc`, `~/.pip`, `~/.config/pip`, `~/.cargo/credentials`, `~/.cargo/credentials.toml`, `~/.m2/settings.xml`, `~/.vault-token`, `~/.gem/credentials`, `~/.terraform.d/credentials.tfrc.json`, and `~/Library/Keychains`. Shell commands still read their own configuration files. A request that asks the agent to read one of these files with a file tool now fails.
* **exec:** `splice exec --worktree --merge-back` now merges only a verified run. A completed run that verified nothing exits `4` and keeps its worktree. `--merge-back` also refuses when the source branch or commit changed during the run, or when the source HEAD is detached. The worktree then keeps its changes, not committed, for a manual merge. To merge an unverified run anyway, add `--allow-unverified-merge`. This option is not safe. Splice writes a warning with the reason before it merges.
* **exec:** a completed run that no check verified still exits `0`. Its final JSON result now sets `verified` to `false` and gives `unverified_reason`. In CI, add `--require-verified` to make such a run exit `4`, or read `verified` before you treat exit `0` as verified.
* **exec:** headless `splice exec` now writes one line for each subprocess that a pipeline stage starts: `[spawn] ...`, or `[spawn-refused] ...` with the reason. The `text` output writes these lines to stderr. The `json` and `stream-json` outputs write them as `warning` events. Update a script that treats any stderr text or any `warning` event as a failure.
* **trajectory classifier:** when a ChatGPT sign-in profile exists, a pipeline run now sends trajectory classifier requests through that sign-in after a pass or a repair that did not succeed. Each request carries a bounded summary of the passes: scores, test counts, changed file paths, and short failure text. It carries no file contents and no credentials. To turn the classifier off, use `--flags -pipeline.trajectory_classifier`, or set `"flags": {"pipeline.trajectory_classifier": false}` in the user config.
* **tui:** `/exec` now runs in `auto` permission mode, the same as `splice exec`. The transcript states this mode. In 0.2.0, `/exec` used the permission mode of the conversation. Examine the permission mode before you use `/exec` on a sensitive repository.
* **flags:** a flag value that is not valid now stops every command that reads configuration, not only a run. This applies to the `flags` key, `SPLICE_FLAGS`, `--flags`, and the legacy variables that have a strict value set. The root `--flags` option fails with a usage error on a command that does not read configuration. A project `config.json` that sets a user-scope flag is an error. `SPLICE_FORMAT_ON_WRITE`, `SPLICE_CHECKPOINTS`, and `SPLICE_DISABLE_PROMPT_CACHE_KEY` still work. `SPLICE_CHECKPOINTS` now ignores case and surrounding spaces.
* **agent nodes:** a headless run, such as `splice exec`, cannot ask for approval. It now stops with exit `2` before the first provider request when an agent node in `ask` permission can write, run commands, or use the network. The error names the node. Set the `permission` of that node to `auto`, or run the topology in the TUI.
* **stream-json:** a client must accept these additions. A new `stage` event carries the stage lifecycle. A new `pipeline_plan` event lists the stages before execution. `run_end.status` can be `unverified`. The `error.code` field can be `unverified` or `input_error`. The stage markers inside `reasoning.delta` still appear in this release, but they are deprecated. Use the `stage` event.

### Added

* **pipeline:** custom pipeline topologies. A topology file replaces the default pipeline. It can remove, rename, or reorder stages, and add `command`, `prompt`, and `agent` nodes. Start with `splice pipeline export default my-pipeline.json`, edit the file, then run `splice pipeline import my-pipeline.json` and `splice pipeline use my-pipeline`. Library entries are in `~/.config/splice/pipelines/` (or `$XDG_CONFIG_HOME/splice/pipelines/`). See docs/PIPELINE.md.
* **pipeline:** `splice pipeline list`, `show`, `use`, `export`, `import`, and `edit`. `list` shows the active topology, its source, the project file, and the library entries. `show` lists the nodes, edges, command nodes, and agent nodes. `import` checks the file before it writes it, lists each command node and agent node, and asks before it installs or replaces an entry. Add `--yes` to skip the question. An import from a URL must use `https`, must be 1 MiB or smaller, and cannot redirect to a different host. `use` refuses a file path; import the file first.
* **pipeline:** topology sources. Splice uses the first source that applies: `splice exec --pipeline <name|path>`, then a trusted project `.splice/pipeline.json`, then the library entry that `splice pipeline use` selects (`active_pipeline` in the user `config.json`), then the embedded default. `--pipeline` wins over a trusted project file, and Splice writes a warning that names the file. A requested topology that Splice cannot find, or a file that does not validate, stops the run. The name `default` always selects the embedded topology.
* **pipeline:** a project topology runs only in a trusted workspace. Splice reads `.splice/pipeline.json` from the directory that has the trust decision: the launch directory in the TUI, the workspace directory for `splice exec`, and the new worktree for `splice exec --worktree`. In an untrusted workspace, Splice ignores the file and writes a warning.
* **pipeline:** at the start of each run, Splice names each command node, for example `[topology] command node vet will run: go vet ./...`. The TUI shows these lines. The `json` and `stream-json` outputs contain them. The `text` output writes them to stderr with the `[splice]` prefix. When the wall-time limit is not the default, the notice shows it.
* **pipeline:** the topology file format: `version` (only `1`), `name`, `description`, `author`, `splice_min_version`, `nodes`, `edges`, and `budget`. A name has 1 to 64 lowercase letters, digits, `-`, or `_`, and starts with a letter or a digit. Splice refuses a field that it does not know, and the error names the field. An older release refuses a file that sets a higher `splice_min_version`.
* **pipeline:** edges carry one of three payloads: `summary` (the default), `output` (the summary and at most 2,000 characters of the upstream data), or `none` (order only). Edges cannot make a cycle. The default topology is now a graph, and a stage receives only the summaries of its incoming edges.
* **pipeline:** `command` nodes run one shell command through the `bash` tool, under the sandbox and the permission mode of the session. Splice passes each `command` element as one argument. A nonzero exit or a permission denial fails the node and the pass. Splice keeps at most 4,000 characters of output. A downstream model-backed node receives the output as untrusted data. A command node does not verify a run.
* **pipeline:** `prompt` nodes make one model request with no tools. The template can use `{{intent}}`, `{{summaries}}`, and `{{memory}}` (with `capabilities.pull_memory`). Splice keeps at most 4,000 characters of the answer. An empty answer or a model error fails the node.
* **pipeline:** tiers and budgets per node. `tiers` sets the tiers where a node runs. `budget` sets `input_max` and `output_max`. Splice compiles the topology at each tier when it loads the file. A tier whose node budgets exceed its token envelope stops the load, and the error names the tier and the remedy. `budget.total_input` and `budget.total_output` replace the envelope at every tier.
* **pipeline:** model pins. A model-backed node can set `"model": {"provider_profile": ..., "model": ..., "reasoning_effort": ...}`. A pin has priority over `stage-models.json` for that node. A pin that cannot resolve fails the run before the first provider request, and the error names the node. Escalation overrides pins. The progress line names the pinned nodes that it overrides.
* **pipeline:** a topology without a check stage gets a warning at run start: `topology has no verification node; revision loops are disabled`. A tier that runs no check stage gets its own warning. Such a run completes as not verified, with the reason `topology <name> has no verification node` or `<tier> tier runs no checks`.
* **pipeline:** two example topologies ship in docs/examples: `pipeline-write-vet-test.json` and `pipeline-investigate.json`.
* **agent nodes:** a topology can add `agent` nodes. An agent node runs a model loop with the tools in its `tools` allowlist (1 to 16 names), a turn limit `max_turns` (1 to 50, default 8), and a required `budget`. Eligible tools are `apply_patch`, `bash`, `delete_file`, `edit_file`, `exec_command`, `glob`, `grep`, `list_directory`, `lsp_navigate`, `read_file`, `read_minified_file`, `update_plan`, `web_fetch`, and `write_file`. A file with an agent node must set `splice_min_version` to `0.3.0` or newer.
* **agent nodes:** permission. With `"permission": "ask"`, the node asks before every call that writes, runs a command, or uses the network, also when the sandbox allows the call. A saved grant or a command prefix grant does not skip the question. Each approval applies to one call. A canceled approval cancels the run. With `"auto"`, the node can make a call that needs approval only when the sandbox allows it. A run in `unsafe` mode is capped at `auto` for agent nodes.
* **agent nodes:** sandbox. An agent node never runs outside the sandbox. The sandbox policy must be on, and a native backend must be available (bubblewrap on Linux; run `splice sandbox setup` on Windows). Otherwise the run stops before the first provider request with `agent nodes need an enforcing sandbox` and the reason. A call that asks for `require_escalated` is refused. Splice does not retry a sandbox-denied call outside the sandbox.
* **agent nodes:** budget and failures. The `budget` of an agent node is its own hard cap, added on top of the tier envelope. Splice sums the input and output tokens of every request in the loop and stops the loop when either sum exceeds its cap. The node fails, and the error names the node, when the model calls a tool that is not in `tools`, a call is refused, the loop reaches `max_turns`, a budget is exceeded, or the provider does not report usage for a completed request.
* **agent nodes:** disclosure. The trust card, `/pipeline`, `splice pipeline show`, the import confirmation, and the run-start notice list each agent node with its tools, its permission, its turn limit, its budget, and the first 200 characters of its prompt. An allowlist with `bash` or `exec_command` is marked `can run commands`. An allowlist with `web_fetch` is marked `network`.
* **browser:** a local pipeline viewer. `/web` in the TUI starts a local server and shows a one-time link. A browser opens too when the terminal is interactive. `splice exec --serve` prints a one-time link to stderr for its run, and standard output does not change. The viewer shows the live graph of the run: the status of each node as a word and a symbol, its pass, its tokens, and its cost. Select a node with `j`, `k`, or the arrow keys and push Enter to open its details: model, effort, budget bars, changed files, last summary, and the command line of a command node. After a run, a timeline replays the run step by step (`[` and `]`). When no run exists, the viewer shows the static graph of the active topology.
* **browser:** a topology editor. `/pipeline edit [name]` in the TUI and `splice pipeline edit [--new] [name|default]` open the editor on a draft. The editor checks each change with the same checks as `splice pipeline import`, shows each problem next to its field, and saves drafts in `<config>/splice/pipelines/.drafts/` (readable only by your user). A second edit of the same source opens the latest draft; `--new` starts a new draft. Publish needs a valid draft and a confirmation dialog that lists each command node, each agent node with its full prompt, the warnings, and the changes to an entry that exists. Publish writes only to the user library, never to `.splice/pipeline.json`, and does not change the active pipeline. "Use it now" selects the entry and refuses while a run is active.
* **tui:** `/pipeline` shows the active topology as a graph: nodes in groups by dependency depth, their types, the tiers that skip them, their dependencies, the source, the warnings, and the command nodes. `/pipeline use <name>` selects a library entry. A terminal narrower than 64 columns shows a flat list.
* **tui:** a live pipeline graph. During a run, the sidebar PIPELINE section shows each node with a status marker, its pass, its tokens, and its cost. A node that the tier skips shows `skipped at <tier>`. A node that a flag removed shows `removed:` and the cause. After a run, a node that never started shows `not run`, and a node that was running at a cancel shows `stopped`. A cost of `cost n/a` or `partial` means that a request has no price.
* **tui:** Ctrl+G opens the run evidence pane: checks with pass, fail, and incomplete counts, findings, interventions, and the receipt. When the pane shows a graph, select a node with the up and down arrows (with an empty input) and push Enter to open its details. The details show the configured model with its source and, during a run, the model, the effort, and the budget that the run applied. Esc closes the details, then the pane.
* **tui:** terminal receipts. Each pipeline run ends with one card: VERIFIED, DONE · NOT VERIFIED, FAILED, or CANCELLED. DONE · NOT VERIFIED names the checks that did not apply. FAILED shows the full reason. CANCELLED states whether the work is in a worktree or in the shared workspace. Each card lists its action keys, for example `[O]` to open the diff and `[R]` to resume. A run that a limit stopped (iterations, wall time, or a trajectory stop) ends as FAILED with its reason. CANCELLED is only for a run that the user stopped.
* **tui:** the status line says "needs you" while a run waits for your answer: a question, a permission, a spec review, or trust. A blocking question shows a NEEDS YOU card with a wait timer. The status line also shows the run phase and, while a run is active, an Esc cancel hint.
* **tui:** a new launch screen. An empty session shows the repository, branch, model, effort, tools, MCP servers, skills, and tests, a card for the last session when one exists, and start actions. The sidebar is present from the first frame.
* **tui:** a worktree review. After a worktree run, Splice asks Accept, Reject, or Keep. Reject asks one optional question about the reason. A failed or canceled worktree run shows a HANDOFF card with `[O]` open the worktree in `$EDITOR`, `[M]` merge back, `[X]` discard (Splice keeps the branch first), and `[D]` diff. The diff view lists files by change size and supports `n` next file, `a` approve all, `o` open in `$EDITOR`, and `[E]`/Ctrl+E to copy the hunk at the top of the window.
* **tui:** design lifecycle cards. `/crystallize` shows a CRYSTALLIZING card, then an IMPLEMENTATION PLAN card with numbered tasks and check counts, and a CRITIQUE card with REQUIRED and ADVISORY findings. Required findings block approval. The design agent can pin settled decisions, and a DECISIONS card and sidebar module show them. Pins survive `/resume`.
* **tui:** new slash commands. `/flags`, `/pipeline`, `/web`, `/hooks`, `/plugins`, `/specialists`, `/usage` (alias `/cost`), `/changes`, and `/verify` show the same output as their CLI commands. `/verify` runs project commands, so it needs a trusted workspace. Commit, push, add, and remove stay in the CLI.
* **tui:** `/memory forget [observation|node] <id>` removes one memory item, and `/memory prune` runs memory maintenance now. `/memory` lists entry IDs.
* **tui:** `/layout <preset>` applies and saves a layout preset: `default`, `compact`, `execution`, `review`, or `minimal`. `/layout list` shows the state and the active preset.
* **tui:** Ctrl+N cycles the transcript verbosity: quiet, normal, and detailed (the default). The change affects only what the TUI shows. Set another key with `keybindings.toggleNarration`.
* **tui:** a trajectory pane. During a run, Ctrl+T shows or hides the pass scores, the restore markers, and the interventions. The pane is hidden by default and opens by itself on a regression, with a notice that says so. Outside a run, Ctrl+T still cycles the reasoning effort.
* **tui:** at a width of 80 to 119 columns, the TUI shows a compact pipeline strip, and Ctrl+B opens the sidebar modules as a full-width sheet. The sidebar needs 120 columns. On a wide single-column frame, a run shows an evidence band in the transcript.
* **tui:** `/stages` is now one editor. Each row shows its effort; Left and Right change it, and Enter opens one searchable model list for all providers. A model from another provider moves that row to that provider.
* **tui:** the `/model` picker shows prices and a cost rail, and Left and Right change the effort of the selected row. Ctrl+R in `/model` gets the latest models.dev catalog. The TUI also refreshes an old catalog cache in the background at startup.
* **tui:** `/mcp add` shows a STAGED ADD card before anything runs: the exact command or URL, the transport, and the config file it writes. `[A]` applies, `[D]` adds the server disabled, `[X]` discards, and `[E]` edits the command. A marketplace entry opens this flow, not a prefilled composer.
* **tui:** an untrusted workspace with executable project config shows an UNTRUSTED PROJECT CONFIG card that lists what the config would change. The trust menu has a row that opens the project config in `$EDITOR` without a change to trust.
* **tui:** the trust card now lists each hook command, each MCP server command or URL, `verify.testCommand`, the decision advisor target, the project specialists, the project plugins with their commands, and the command nodes and agent nodes of `.splice/pipeline.json`. Secret values are redacted. Each list stops at 20 rows and counts the rest.
* **tui:** `SPLICE_GLYPH_TIER` selects the marker set: `ascii`, `safe_unicode`, or `rich_unicode`. A UTF-8 locale uses the rich set. `NO_COLOR` always uses ASCII markers.
* **tui:** when `HERDR_ENV=1` and `HERDR_PANE_ID` are set, the TUI reports its state (idle, working, or blocked) to the `herdr` command.
* **trajectory classifier:** when you are signed in with ChatGPT, Splice now watches the progress of each run and steps back when a run repeats the same failure. It uses `gpt-6.1-sol` through your ChatGPT sign-in and does not bill per request. Without a usable ChatGPT sign-in, the built-in rules decide, as before, and the run prints one line that says so. The classifier also runs for design plans. `splice doctor` and the TUI doctor view show the state: on with the profile and the model, rules only with the reason, or off.
* **decision advisor:** a `decision_advisor` section in the user config selects another classifier backend: `openrouter_decisions`, `http` (the Splice HTTP decision contract `splice.decision.v1`), or `provider` (a configured provider profile). The section applies when `pipeline.decision_advisor` is on. `mode` can be `off`, `shadow` (record decisions; the rules control the run), or an active mode. `trajectory.thresholds` sets the threshold of each action. A section with `mode` `off` also keeps the default classifier off. Set `SPLICE_DECISION_RECORDS_DIR` to write the decision records of each run to `<dir>/<run-id>.jsonl`. See docs/decision-backends.md.
* **flags:** behavior switches are now feature flags. `splice flags` lists each flag, its value, the source that set it, and its stability; `--json` gives a machine-readable list. `/flags` shows the same list in the TUI. Set a flag with the `flags` key in the user config, the `flags` key in a project config (project-scope flags only), `SPLICE_FLAGS`, or `--flags` before a command that reads configuration (`exec`, `doctor`, `flags`, `serve`, and the TUI). `splice exec --flags` wins for the names that it sets. See docs/FEATURE_FLAGS.md.
* **flags:** `splice doctor` warns when an experimental flag is on. Experimental flags are off by default. `pipeline.pass_evidence` is experimental: it makes the trajectory read the change of each pass, not the change since HEAD.
* **exec:** `splice exec --loop` runs the agent tool loop without the TUI and without the fixed pipeline. A loop that stops without finishing exits `4`. A loop that fails with an error exits `3`. `--loop` has no verification step, so Splice refuses `--merge-back` and `--require-verified` with `--loop`, and `--require-verified` with `--use-spec`.
* **exec:** `splice exec --max-wall <duration>` sets the wall-time limit of the pipeline run (default 10 minutes). You can also set `SPLICE_MAX_WALL` or `pipeline.max_wall_seconds` in the user `config.json`. A value of zero or less, or a value that does not parse, stops the command with an error. In `config.json`, `0` uses the default.
* **exec:** `--require-verified` makes a completed run that verified nothing exit `4`. Use it in CI. A `--plan` run is verified only when each task verified, and the reason names the first task that did not verify. A resumed design plan is not verified.
* **exec:** `--memory <on|off>` turns the memory sidecar on or off for one run. `off` makes a cold run.
* **exec:** `--inherit-worktree-trust` lets a worktree inherit the trust decision of its trusted source launch directory. It is off by default and is experimental.
* **memory:** a verified run that changes files now records the task, the files that the model asked for, and the files that the run changed. With the experimental flag `memory.prefetch` on, a later related task gets up to four of those files in its first context round. At the end of a run with memory, Splice prints `[memory] delivered N file(s), N changed by the run, N requested in expansions that memory did not deliver`.
* **memory:** the memory sidecar does maintenance at start and then at most once a day. It archives the data of a project whose directory no longer exists, deletes that data after 30 days, and compacts the database. Set `SPLICE_MEMD_MAINTENANCE=0` to turn maintenance off.
* **config:** `splice config describe [--tier <tier>] [--json]` shows the active pipeline and its source, the token envelope, the model of each node with the source that supplied it, and the `stage-models.json` entries.
* **config:** `verify.testCommand` and `verify.testDir` set the test command of the test runner. `{tests}` and `{test_modules}` expand to the test files that the pass changed. The command runs in the stage sandbox with the normal command allowlist.
* **worktrees:** `splice worktrees prune` removes unlocked and clean managed worktrees whose HEAD commit is still reachable from the source HEAD or a `splice/*` branch. It reports each worktree that it keeps.
* **worktrees:** an optional `.splice/worktree-seed` file in the source repository lists paths to copy into each new worktree, one per line. A `symlink: ` prefix makes a link instead of a copy. An absolute path, a path that leaves the repository, or a path that does not exist is an error.
* **providers:** for a local Ollama model, Splice reads the model capabilities from the daemon. A model that reports no tool support gets a preflight warning, and a local reasoning model now receives `--reasoning-effort`. The setup wizard lists the installed models.
* **providers:** `SPLICE_OPENROUTER_PROVIDER_ORDER` pins the OpenRouter upstreams, as a comma list in order, with no fallback.
* **providers:** `SPLICE_STREAM_CONTENT_STALL_TIMEOUT` sets the time limit for a stream that sends keep-alive messages but no output. Set a larger value for a model that reasons for a long time.
* **harness:** an interface for external harnesses. The source tree adds `cmd/splice-pi-bridge`, which runs one pipeline, prints stream-json events, and reads control commands on stdin. See docs/HARNESS_INTERFACE.md and docs/PI_HARNESS.md.
* **stream-json:** the `final` pipeline result adds `verified`, `unverified_reason`, `user_aborted`, `failure_kind` on a failed stage record (`stage_error`, `test_failure`, or `none`), `prompt_layout_flips`, and, on each usage record, `prompt_layout_hash`, `cache_hit`, and `memory_position`. `user_aborted` is true only when the user stopped the run.
* **docs:** new guides: docs/COMMANDS.md, docs/CONFIGURATION.md, docs/FEATURE_FLAGS.md, docs/decision-backends.md, docs/HARNESS_INTERFACE.md, docs/PI_HARNESS.md, and docs/WIRING.md. docs/PIPELINE.md and docs/STREAM_JSON_PROTOCOL.md are rewritten.

### Changed

* **pipeline:** a pass with failing tests now gets up to two local repairs in the same pass. The code writer sees the failing tests and their messages, and the test runner runs again. The next pass also sees the failing tests, not only a count.
* **pipeline:** a stage request whose connection drops (reset, EOF, or timeout) is sent again at most two times, after 2 and 8 seconds. A canceled stage is not sent again.
* **pipeline:** a tool call that the output cap cuts off is sent again with a larger cap, up to 32,768 tokens, and the model is told to send edits instead of full file content.
* **pipeline:** a stage input that is larger than its budget is now made smaller (optional memory, older summaries, and older changed files first) instead of stopping the run. The request intent and acceptance facts are never removed. The token budget rule now counts generated tokens only.
* **pipeline:** pipeline stages now receive the repository instruction files `AGENTS.md`, `SPLICE.md`, `.splice/AGENTS.md`, and `CLAUDE.md`.
* **pipeline:** the code writer and the test generator must read a file before they change it. A blind overwrite of a file that the run did not read is refused.
* **pipeline:** three passes with no workspace change start one step-back analysis. A later stretch of three passes with no change stops the run. A rollback without an isolated worktree becomes a step back.
* **pipeline:** a completed pass is verified only when at least one check passed, or a test run executed tests and none failed. A pass whose checks only reported findings, or whose test run executed no tests, is not verified.
* **pipeline:** before a run, Splice writes advisory warnings: a check stage that can ask for permission in `ask` mode, a `beforeTool` hook that can intercept a stage tool, and a model whose catalog entry declares no tool calling.
* **pipeline:** static checks, security scans, and the git reads of Splice run in the native sandbox with no network access, only inside the workspace, and only with a fixed set of programs: `bun`, `cargo`, `git`, `go`, `gosec`, `node`, `npm`, `npx`, `pnpm`, `python`, `python3`, `ruff`, `tsc`, `trivy`, and `yarn`.
* **pipeline:** approved design plans (`splice exec --plan` and `/approve`) always use the default topology. The design runner says once that it does not use the active pipeline. `splice exec` refuses `--plan` with `--pipeline`. Approved design plans now use the routes in `stage-models.json`.
* **routing:** a cheaper-tier stage now uses only models that the endpoint of your provider serves. For an official OpenAI profile, this means an OpenAI model instead of Qwen or Kimi. The offline stage picker in onboarding also offers only models that the endpoint serves.
* **effort:** Splice fits the reasoning effort to the model that serves each request. A level that a model does not have moves to the nearest level that it has. Stages that use their own model (`stage-models.json`, cheaper tiers, and node pins) now send the effort of the run; in 0.2.0 they sent none. Claude Opus and Sonnet 4.6 and later, Fable, and Mythos use adaptive thinking. Haiku 4.5 now shows the `low`, `medium`, and `high` levels.
* **models:** the embedded models.dev snapshot moves from 2026-08-06 to 2026-09-24. The curated catalog adds Claude Fable 5.1, Opus 5.5, Opus 5, and Sonnet 5.
* **usage:** `splice usage` and the run totals now price each request from the catalog of the provider that served it. A custom OpenAI-compatible provider that serves a model that OpenAI lists is now unpriced for that model, instead of priced at the OpenAI list price. A gateway that lists a curated model at its own price keeps that price. A stored request with no recorded provider counts as partially priced, and `splice usage` names the profile that priced it.
* **cost:** requests through a ChatGPT sign-in no longer show a per-request dollar cost. Splice records them as unpriced, because that access is a subscription. A model whose ID ends in `:free`, or that its provider lists at zero input and output rates, is priced at zero.
* **cost:** classifier and decision requests are now in the request ledger of the run, so run totals include them. A decision request with no usage is unpriced, not $0.
* **memory:** Splice saves only verified runs to memory. The memory of a worktree run uses the repository root, so all worktree runs of a repository share one project memory.
* **memory:** the memory sidecar now waits 10 seconds for a request and 10 seconds to start, instead of 2 and 3 seconds. Splice checks the health again one time before it turns memory off. Set `SPLICE_MEMD_TIMEOUT` and `SPLICE_MEMD_SPAWN_TIMEOUT` (Go durations) to change these limits. The warning names them.
* **worktrees:** a `--worktree` run from a subdirectory uses the same subdirectory of the worktree. Trust, the project config, and `.splice/pipeline.json` come from that directory. A run from a subdirectory that is not committed stops with exit `2`, removes the new worktree, and says how to correct it.
* **sandbox:** the sandbox Go build cache is now shared by all worktrees of one repository. Before, each workspace path got its own cache.
* **worktrees:** `--merge-back` now removes the worktree after a merge or when there are no changes. It keeps the worktree after a conflict, a skipped merge, or a failed run. The TUI review merges exactly the content that it showed you. If the worktree changed after the review, the merge stops.
* **sessions:** a worktree session appears in `/resume` from the worktree and from the source repository, with a `wt:` label. A design session takes its title from the first prompt, not "Design conversation". The right column names the model. After `/resume`, a last row names the next step, and the pipeline panel, the lifecycle, and the receipt of the session come back.
* **exec:** `splice exec` exits `2` for invalid run input that Splice finds before the first provider request: a topology that does not load or validate, a node pin or stage route that does not resolve, and the same for each task of `--plan`. Stream-json reports these errors with the code `input_error`. A provider that cannot start (for example, a missing OAuth token) exits `3`. A stage that fails during the run exits `4`.
* **exec:** `splice exec` refuses `--serve` with `--loop` or `--use-spec`, because those runs send no events to the viewer.
* **doctor:** `splice doctor` shows the trajectory classifier state, warns about experimental flags, names the bubblewrap install command on Linux, and reports a bad flag value as its own `config.resolve` check instead of "No LLM provider is configured". When a profile has a stored key that Splice cannot read, `doctor` names the cause. The connectivity probe uses the stored credential, and it reports a credential read error instead of a generic 401.
* **tui:** the whole frame now folds to ASCII on a terminal whose locale is set and is not UTF-8, or with `SPLICE_GLYPH_TIER=ascii`. Locale resolution now follows `LC_ALL`, then `LC_CTYPE`, then `LANG`. An unset locale stays Unicode.
* **tui:** error rows, notice rows, and failed tool errors now wrap instead of being cut off.
* **tui:** `/help` lists command names only; the palette shows the descriptions. `/tools` groups tools by source and marks a degraded MCP server. `/doctor` shows all checks. Command replies use one short line with the outcome, and a blocked reply names the remedy.
* **tui:** Ctrl+B at 80 to 119 columns opens the context sheet. A failed run shows a red bar with the word "failed". The strip shows full stage names when they fit.
* **tui:** `/trust` says that a trust change applies to the next run for the project topology, and to the next launch for hooks, plugins, MCP servers, and the project config.
* **tui:** long sessions render faster. A new turn settles in time that follows the size of the turn, not of the history, and settled transcript rows keep their measured height.

### Security

* **browser:** the browser surfaces listen on 127.0.0.1 only, on a free port. Each request must carry a Host header for 127.0.0.1 or localhost on that port. The one-time link works once, and only a GET request uses it. It becomes an HttpOnly, SameSite=Strict session cookie that is scoped to a random path prefix and named for the port. A request that changes state also needs a matching Origin and an action token. Every response has a Content Security Policy with `default-src 'self'`, `frame-ancestors`, `base-uri`, and `form-action`. The pages make no external requests. A server stops after 30 minutes without authenticated activity, when the TUI exits, or when the `exec --serve` run ends.
* **browser:** text that reaches a browser page passes through redaction: the provider keys of every route of the run, stored keys, OAuth bearer tokens, custom header values, and other known secret values. This applies to notices, stage summaries, node names, file paths, and the topology name. Splice reads stored keys for redaction only when a browser surface is active.
* **topology files:** Splice refuses control characters and other characters that do not print in node names, descriptions, authors, and model fields, so a topology cannot hide text in the terminal. Every disclosure line passes through a sanitizer. The publish dialog removes format characters, such as bidirectional overrides, from the full prompt.
* **git:** Splice's own `git` reads run the clean and smudge filters of a repository with a private temporary directory, which Splice removes after the read. Every `git` command that Splice starts sets `safe.bareRepository=explicit` and `core.fsmonitor=false`, so a repository cannot run its `core.fsmonitor` program through Splice.
* **sandbox:** on macOS and Windows, the deny paths now match in any letter case. In 0.2.0, a path such as `.NPMRC` or `.SSH/id_rsa` could open a denied file on a file system that ignores case. Splice reads Windows sandbox environment names without case.
* **sandbox:** the pass measurement and the change summary of Splice now read only regular files inside the workspace. A symbolic link to a path outside the workspace, a FIFO, or a device is not read.
* **tools:** in a pipeline stage and an agent node, `apply_patch` now follows the read-before-write rule, the same as `write_file` and `edit_file`.

### Fixed

* **ChatGPT sign-in:** when the model called several tools in one response, the tools could receive no arguments (for example "path is required"). Splice now keeps the arguments that the backend sends at the end of each call.
* **pipeline:** a run in a repository without Go, TypeScript, JavaScript, or Python files was treated as Go, and the run failed because no check could run. Such runs now complete, marked not verified. A root `go.work` file now counts as Go, and a Python file that the run adds is now found.
* **cost:** pipeline runs through OpenRouter could record a cost of $0 for paid requests, so `splice usage` and run totals under-reported spend. Each request now keeps the charge that OpenRouter reports. A reported $0 for paid usage falls back to the catalog price. A bring-your-own-key request reports the upstream cost. A later zero usage report no longer replaces a real charge.
* **cost:** a provider that reports reasoning tokens apart from output tokens no longer causes the cost of the request to be lost. Splice adds the two counts.

## [0.2.0](https://github.com/Taf0711/splice/compare/v0.1.4...v0.2.0) (2026-08-10)


### Features

* **design:** the design agent can now request to crystallize the plan or approve it for execution. Revisions keep the current plan and critique context. The approve transition stays available after a resume when no critique requires a fix. Splice records who requested each transition. ([3a1421f](https://github.com/Taf0711/splice/commit/3a1421f8479a8c6cb4681518690fc05d920702d4))


### Bug Fixes

* **memory:** only `code_writer` and `test_generator` query memory now. Other stages skip the sidecar search, so they do not pull in results that do not apply. ([4f63d05](https://github.com/Taf0711/splice/commit/4f63d05a2ba59a1241ca32daa5cd31a5b5416b8f))
* **reasoning:** a request for `xhigh` or `max` could give less thinking than `high`, including the least. An unsupported tier now steps to the nearest supported tier. Supported tiers now come from the models.dev snapshot when available. ([1fe01ea](https://github.com/Taf0711/splice/commit/1fe01eab3990ca3075eb0c2c984554f3ae535f9e))
* **tui:** `/stages` now refreshes model lists from each saved provider. The escalation and stage pickers previously showed only the short static catalog. ([9f8324a](https://github.com/Taf0711/splice/commit/9f8324a66a0520d034a2f835d5040063a9b10515))
* **tui:** the sidebar layout stays stable during brief content changes. The composer width follows the chat column, and swarm rows stay with their run. ([935e247](https://github.com/Taf0711/splice/commit/935e247f42578394a7dc21ed2a966e18f13c4e97))
* **update:** stop canceled notices before cache writes ([5276f95](https://github.com/Taf0711/splice/commit/5276f957af81217e61fcabf36d191feaf9db8090))

## [0.1.4](https://github.com/Taf0711/splice/compare/v0.1.3...v0.1.4) (2026-08-06)

### Added

* **cli:** Splice tells you when a newer release exists. A run checks at most once a day and prints one line naming the command that suits how you installed it. The check never delays or fails a run. Only a terminal sees it: piped output, `splice exec` in protocol mode, and the interactive TUI carry no notice. `SPLICE_DISABLE_UPDATE_NOTICE` turns the notice off and leaves `splice update` working; `SPLICE_DISABLE_UPDATES` turns off both. `splice --update` checks and reports; installing stays `splice update --apply`.
* **stages:** a plan's acceptance criteria now run. A criterion carrying a command was reaching the code writer as prose and nothing ever executed it, so a run could report success on code that compiled, passed its tests, and did not do what was asked. A verification stage runs each criterion that has a command and reports one result each. A criterion nobody automated is recorded as skipped, not failed.
* **tui:** an untrusted workspace is now marked in the footer, and `/trust` records the decision. Previously a user who declined trust, or who was defaulted to untrusted, had no way to see it and no way to change it without editing a file. The decision takes effect on restart, because the session already decided at startup whether to load project commands, hooks, MCP servers, and plugins.

### Security

* **auth:** OAuth tokens are protected at rest. They defaulted to a plaintext file while API keys defaulted to the keychain or an encrypted file, so the stronger secret had the weaker protection. Tokens now resolve the same policy API keys use, and existing tokens move across at startup. Set `SPLICE_OAUTH_STORAGE=file` to keep plaintext. Migration keeps the plaintext copy until the protected write is read back and verified, so a locked keychain leaves the login working.
* **sandbox:** credential directories are read-denied by default: SSH, cloud, GPG, Kubernetes, container registry, GitHub CLI, and Splice's own token store. The sandbox previously granted read access across the filesystem, so any sandboxed command could read these. Add `sandbox.allowRead` to the global user config to re-include a path. Project config cannot grant it.

### Fixed

* **stages:** the security audit stage failed on any machine with `gosec`, `bandit`, or a SARIF scanner installed. The scanners write log lines to stderr and their report to stdout, and Splice read both together, so the report could not be parsed. The stage failed, the writer retried, and the run ended before its test stages. Substantial and architectural runs were affected.
* **sandbox:** a sandboxed `go` build could not write its build cache, because the cache sits outside the sandbox. One stage passed and the next failed on a cache entry that was never written. Splice now points `GOCACHE` at a directory the sandbox can write. An explicit `GOCACHE` still wins.
* **stages:** a pipeline run could fail to finish even when every stage ran. The test generator was told what the code writer did in prose, not which files it produced, so it wrote tests against names that did not exist and the run failed on undefined symbols. On a retry it could not rewrite the test file it had written itself. It now receives the writer's actual paths, and a retry replaces its own earlier file.
* **design:** the plan critic could block a plan indefinitely. It never saw its own earlier critiques, so each revision drew new objections, and a medium-severity concern could stop execution. It now sees the previous plan and critique, only high and critical severity blocks, and it reads what the design conversation established rather than guessing.

### Changed

* **sandbox (Linux):** the sandbox now enforces. It located its helper on `$PATH` only, and no release archive ships that helper, so enforcement degraded to unconfined without saying so. Commands that ran before may now run confined wherever `bwrap` is installed.
* **sandbox:** SSH-based Git inside the sandbox needs `~/.ssh` in `sandbox.allowRead`. Approving network access does not restore a denied key.

### Miscellaneous Chores

* **release:** cut 0.1.4 ([d3b5e44](https://github.com/Taf0711/splice/commit/d3b5e44d4687b7dbfa9908ad5c21db8b8a1bc484))

## [0.1.3](https://github.com/Taf0711/splice/compare/v0.1.2...v0.1.3) (2026-07-30)


### Breaking Changes

* All environment variables now use the `SPLICE_` prefix. Splice does not read the old `ZERO_` names, and it gives no warning when it finds them. Rename these variables in your shell, your CI configuration, and your scripts. For example, change `ZERO_API_KEY` to `SPLICE_API_KEY`. ([3b3b01c](https://github.com/Taf0711/splice/commit/3b3b01c0e65d035605cc036c41864197458e5287))


### Documentation

* The README, the security policy, and the install guide are rewritten. ([b4e3388](https://github.com/Taf0711/splice/commit/b4e3388))


### Features

* **agenteval:** derive eval cost from the request samples ([9e137e0](https://github.com/Taf0711/splice/commit/9e137e06e41f84c81b098330a90a3509e94be8da))
* **agenteval:** parse usage events while the agent runs ([15dc68e](https://github.com/Taf0711/splice/commit/15dc68e46697619e790b8e4c272c67379f29f9b4))
* **agenteval:** publish the benchmark v2 report contract ([eed860d](https://github.com/Taf0711/splice/commit/eed860d790cf85895de8ff4653c7cc2bc6ed8396))
* **cli:** report eval runner, routed models, and cost coverage ([a740bb7](https://github.com/Taf0711/splice/commit/a740bb7869527c40a0d982faec5a7bd84bab1d1c))
* **cli:** run eval benchmarks through the production pipeline by default ([4ff0a2d](https://github.com/Taf0711/splice/commit/4ff0a2d47ce40d524794aab0a3901bf5325fdbc0))
* **compaction:** keep the agent inside the cheapest pricing tier ([eee9f67](https://github.com/Taf0711/splice/commit/eee9f67cc6691049576565ac8347d7edd5c7135d))
* complete attributed pipeline cost accounting ([7440a8e](https://github.com/Taf0711/splice/commit/7440a8ed8bc5b79db5931e2fb386c6a4c68dc27b))
* **design:** ask the provider to search the web ([9c5651d](https://github.com/Taf0711/splice/commit/9c5651d81b7822a0a8ab202f91c2493f8c1ce230))
* **design:** emit task_started before each plan task dispatches ([09b3826](https://github.com/Taf0711/splice/commit/09b3826c99d5103147a26977999f3d61f065ff13))
* **design:** give the design agent research tools and read-only roots ([4e44b86](https://github.com/Taf0711/splice/commit/4e44b86950f00eb6f85715284bd1158f2712850a))
* **modelregistry:** price long-context tiers from models.dev ([5d6e762](https://github.com/Taf0711/splice/commit/5d6e7625c99e586b4fc98f7e81dbd18603b0b285))
* **modelregistry:** price models the curated catalog does not carry ([00d93f2](https://github.com/Taf0711/splice/commit/00d93f2e2256f55b868550524a24d733ff26ae13))
* **modelregistry:** ship a models.dev snapshot and stop curating prices ([8b3ad05](https://github.com/Taf0711/splice/commit/8b3ad055de41ee858d6c096afb3dc06c3199bba4))
* **openrouter:** run web search on the provider ([fe19ea3](https://github.com/Taf0711/splice/commit/fe19ea395cec2ee62a310ff5aeac68d987284d9e))
* **runtime:** add plumbing for provider-executed web search ([3976759](https://github.com/Taf0711/splice/commit/39767591db35d960ed07b4d1a5885c6c83349773))
* **tui:** show honest session cost with explicit coverage ([0de1c81](https://github.com/Taf0711/splice/commit/0de1c81f84632f2bcd254111a9b2251b6ceb03ed))
* **usage:** persist cost estimates with provenance and coverage ([0215c28](https://github.com/Taf0711/splice/commit/0215c28994ae1fbeae0957a2981b994322f763b2))
* **usage:** round a displayed cost to cents at a dollar and above ([0703156](https://github.com/Taf0711/splice/commit/07031566efc0f8b648c2dd8c10f62963f3246c0d))
* **usage:** show a cost figure when pricing coverage is partial ([6bad51a](https://github.com/Taf0711/splice/commit/6bad51ab921c49bab699eb2c8bb4fb17717b49e8))


### Bug Fixes

* **doctor:** correct the stale connectivity fallback message ([ce91459](https://github.com/Taf0711/splice/commit/ce914596fabf87f1cab49d1a68738c68f0746a27))
* **dtools:** resolve the workspace root through symlinks ([e5d5542](https://github.com/Taf0711/splice/commit/e5d55422905b4f1a32e3d611de24f347ee820c43))
* **exec:** honor compaction config in the spec-draft run ([391887c](https://github.com/Taf0711/splice/commit/391887c5bbd01926ed5f3958fef352a5a9fa4c94))
* **tui:** give the TUI catalog its provider profile ([2c3089b](https://github.com/Taf0711/splice/commit/2c3089b76b62ee73256b79cc17b64dc8c255404b))
* **tui:** keep a critique that failed to persist ([887c97b](https://github.com/Taf0711/splice/commit/887c97b5e8bd11e3b30221031e0464b04eec8f39))
* **tui:** keep a crystallized plan when the critic fails ([c1f3b78](https://github.com/Taf0711/splice/commit/c1f3b782fae3cec53a434742a204c83684b031c3))
* **tui:** persist attributed usage for non-pipeline runs ([d8e7574](https://github.com/Taf0711/splice/commit/d8e7574d85efb92ed0a6dc8caaba9acd81b70021))
* **tui:** stream crystallize output and record its cost ([48778b9](https://github.com/Taf0711/splice/commit/48778b9032aa3a6311a7745181b174318e437675))
* **usage:** show fewer cost digits and use one formatter ([f563ba6](https://github.com/Taf0711/splice/commit/f563ba6ef0a16e0afeb65a6e5d4ea131ed34fa05))


### Performance Improvements

* **exec:** drop the unread context window from the pipeline run options ([e858f58](https://github.com/Taf0711/splice/commit/e858f5823ee9729de00fe40b03175e6b364e10f3))
* **tui:** stop re-measuring the transcript on every wheel tick ([c63edd0](https://github.com/Taf0711/splice/commit/c63edd04ca70d31dba260e305dd1748a991e10b9))


### Miscellaneous Chores

* **release:** cut 0.1.3 ([ad495db](https://github.com/Taf0711/splice/commit/ad495db473dd21af4f65815903fb0ba1a28d7811))

## [0.1.2](https://github.com/Taf0711/splice/compare/v0.1.1...v0.1.2) (2026-07-20)


### Security

* **cli:** workspace trust gate. Project-scope executables (MCP stdio servers, hooks, plugins) loaded from `.splice/` are no longer spawned automatically when the workspace is untrusted. Trust is resolved from CLI flags (`--trust` / `--no-trust`), the `SPLICE_TRUST_WORKSPACE` env var, the persisted `~/.config/splice/trust.json` store (ancestor lookup, parent trust covers children), and the `defaultProjectTrust` setting (`ask` / `always` / `never`, default `ask`). Untrusted workspaces skip project resources and print a warning; this closes a remote-code-execution vector where cloning a malicious repository and running splice would execute configured commands. ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **secrets:** credential environment variables are now scrubbed from child processes (bash, exec, hooks, MCP stdio, plugins, sandbox runner). Known credential names (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `AWS_SECRET_ACCESS_KEY`, etc.) and suffix patterns (`_API_KEY`, `_TOKEN`, `_SECRET`, `_PASSWORD`) are stripped before spawn, with `SPLICE_CHILD_ENV_ALLOWLIST` for explicit passthrough. Prevents prompt-injected `env` / `printenv` from exfiltrating provider keys. ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **sandbox:** unparseable / obfuscated shell commands now force an explicit approval prompt instead of being auto-allowed under an active native sandbox. ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **sandbox:** the safe-git command classifier now rejects `--git-dir`, `--work-tree`, and `-c` (global and inline) so an approved command prefix cannot be used to operate on an arbitrary repository outside the workspace. ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **dtools:** the deterministic-tool path resolver now calls `filepath.EvalSymlinks` and rejects symlinks pointing outside the workspace. Git preserves symlinks on clone, so a repository could previously ship a symlink to a file outside the workspace and have the security scanners read it. ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **sandbox:** the opt-in seccomp Unix-socket block now fails closed (exit 125) instead of running the command without the filter. ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **mcp:** plaintext `http://` MCP server URLs now emit a warning at config load (loopback / localhost excepted). ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))


### Bug Fixes

* **update:** correct the npm package name from `@gitlawb/splice` to `@taf0711/splice`. The npm update path referenced a package name the maintainer does not own; if unregistered, npm self-update would break, and if registered by a third party it was a supply-chain takeover vector. ([#5](https://github.com/Taf0711/splice/pull/5))
* **cli:** the `mcp tools list` command now resolves workspace trust instead of unconditionally loading project MCP servers, closing the last gate gap. ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **tui:** setup pipeline stage picker shows discovered models, count, scroll indicator, and current mark ([a1676dd](https://github.com/Taf0711/splice/commit/a1676dd0dfde862fd0280bbbbb0ce2ea2b3d9b36))
* **tui:** setup pipeline picker shows selected model detail line ([b3b9872](https://github.com/Taf0711/splice/commit/b3b987297e24ed3d5e02cabb955377a3ac92dfad))
* **tui:** pipeline picker shows discovered models, not just the catalog ([15e9a9b](https://github.com/Taf0711/splice/commit/15e9a9be56ff1a53e59a2bbf820bfa43f0c1e86a))
* **tui:** pipeline picker detail line shows the model name ([96131d7](https://github.com/Taf0711/splice/commit/96131d74f2557ad9ada58bb3c274106eb1ae43c5))
* **tui:** Enter opens pipeline stage picker, Right advances to Safety ([fa8166b](https://github.com/Taf0711/splice/commit/fa8166be92752b734e0aeebdfc477835e5f38347))
* **ci:** npm trusted publishing needs Node 24 (npm CLI 11.5.1+) ([c5c6fd7](https://github.com/Taf0711/splice/commit/c5c6fd7dda13f4fd7a2b572da992f13da7951f8a))
* **tui:** setup wizard per-stage model picker uses search and filtered list ([#3](https://github.com/Taf0711/splice/pull/3)) ([da9f47a](https://github.com/Taf0711/splice/commit/da9f47a8310fe8185a6b118d30fe2ff528b221f3))

## [0.1.1](https://github.com/Taf0711/splice/compare/v0.1.1...v0.1.1) (2026-07-19)


### Bug Fixes

* **ci:** npm trusted publishing needs Node 24 (npm CLI 11.5.1+) ([c5c6fd7](https://github.com/Taf0711/splice/commit/c5c6fd7dda13f4fd7a2b572da992f13da7951f8a))
* **tui:** setup wizard per-stage model picker uses search and filtered list ([#3](https://github.com/Taf0711/splice/issues/3)) ([da9f47a](https://github.com/Taf0711/splice/commit/da9f47a8310fe8a6b118d30fe2ff528b221f3))

## 0.1.1 (2026-07-19)


### Features

* initial public release of Splice ([480083e](https://github.com/Taf0711/splice/commit/480083e74785fe9af85938a1a1f15960b51e7823))
