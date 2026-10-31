# Pipeline behavior

Splice has a chat-first design surface and a typed execution surface. The
orchestrator connects them and owns every transition.

This document describes public run behavior. It does not publish hidden prompts,
provider credentials, or private model reasoning.

## Two phases

### Design phase

A new TUI session starts in design mode. The user can discuss scope, constraints,
and acceptance conditions before any code change.

`/crystallize` converts the current design state into a typed plan. A critic
checks the plan before approval. `/approve` starts the approved task graph.

The design agent can request the same transitions only after an explicit user
request. See [Design transitions](DESIGN_TRANSITIONS.md).

### Execution phase

`splice exec` enters the execution phase directly. `splice exec --plan` executes
a saved design plan in task order.

The orchestrator performs these steps:

1. Classify the request.
2. Build and validate an execution plan.
3. Give each stage a bounded input.
4. Run model-backed or local stages.
5. Measure the completed pass.
6. Finish, revise, recover, ask the user, or stop.

Stages do not send raw results directly to another stage. The orchestrator passes
small summaries, changed-file data, and approved context.

## Request tiers

The current classifier selects four tiers:

| Tier | Intended request size |
|---|---|
| `trivial` | A small and direct code change |
| `light` | A focused change that needs local checks |
| `substantial` | A multi-part change that needs tests and security checks |
| `architectural` | A broad change with architectural risk |

The schema retains `standard` for saved-plan compatibility. The current
classifier does not select it.

Classification uses deterministic request signals. Unknown tiers return an
error instead of a default plan.

## Stage sets

| Tier | Stage order |
|---|---|
| `trivial` | code writer |
| `light` | code writer, static analyzer, test runner, acceptance verifier |
| `standard` | code writer, test generator, static analyzer, test runner, acceptance verifier |
| `substantial` | code writer, test generator, static analyzer, security auditor, test runner, acceptance verifier |
| `architectural` | Same stage set as `substantial` |

A stage can be absent when its plan marks it as optional and no implementation
is available. A required missing stage fails the pass.

## Stage responsibilities

### Code writer

The code writer receives the distilled intent, approved context, prior summaries,
and revision guidance. It returns typed file changes and a confidence value.

Splice validates the response before it writes files. Invalid typed output gets
a bounded corrective retry. A repeated invalid result stops the stage.

### Test generator

The test generator receives the intent and the code-writer summary. It returns
typed test file changes.

Only the `standard`, `substantial`, and `architectural` plans include this stage.

### Static analyzer

The static analyzer uses local checks. It can parse or format Go, compile Python
syntax, check JavaScript syntax, and call configured TypeScript or lint tools.

The stage makes no model request. It reports passed, failed, incomplete, or not
applicable verification.

### Security auditor

The security auditor uses available local security tools. Supported checks can
include Bandit, gosec, SARIF input, and Trivy.

The stage makes no model request. A missing optional tool produces explicit
incomplete or not-applicable evidence.

### Test runner

The test runner detects and executes the repository test command through the
registered shell tool. Sandbox and permission rules still apply.

The result records each known test as passed, failed, or errored.

### Acceptance verifier

Each acceptance fact can include a verification command. This stage executes the
commands through the same safety layer.

A failed or errored acceptance fact blocks completion.

## Context and local memory

The original request is distilled before later stages receive it. A stage can
request a bounded file read, directory list, or search result through the tool
registry.

The optional memory sidecar supplies a small project memory bundle only to the
code writer and test generator. Other stages do not query it.

Memory writes are fail-soft. A memory error appears in progress output but does
not replace a valid pipeline result.

## Pass success

After a complete pass, Splice builds an iteration state. A pass succeeds when:

- no stage failed;
- no test failed or returned an error;
- no acceptance fact failed or returned an error;
- no high or critical lint finding exists;
- no high or critical security finding exists; and
- no required verification check is incomplete.

A required check is incomplete when the check cannot run. A tool that is not
present, or a command that the sandbox refuses, causes an incomplete check. An
incomplete verification stage stays in the result.

Splice does not complete a pass when a required check is incomplete. The run
ends with status failed. The abort reason names the stage and the check. A
second pass cannot repair a tool that is not present, so Splice does not retry.

A check that does not apply is skipped. Splice does not fail a run for a skipped
check. A workspace language with no check, or a workspace with no files for a
check, is not applicable. The stage record reads skipped.

## Verified result

A completed pass is verified only when at least one check passed, or a test run
executed tests and none failed. No stage may be incomplete or failed. A
completed pass that is not verified keeps status completed. The result sets
`verified` to false and gives `unverified_reason`, which names the checks that
did not apply. Graph capture requires `verified` to be true. The TUI shows the
DONE · NOT VERIFIED receipt for this case. `splice exec` exits 0 for it. Read
the `verified` field in the final JSON result.

When a stage fails before the pass completes, Splice records the failure and
provides revision context for the next allowed pass. That failed pass does not
create a trajectory state.

## Trajectory state

For each complete but unsuccessful pass, Splice records:

- test and acceptance counts;
- lint and security findings by severity;
- stage confidence;
- token use;
- changed paths and diff line counts;
- generated code size;
- incomplete verification count; and
- a stable hash of the code-writer result.

The state hash identifies repeated proposed code. It is not a hash of the full
workspace.

## Quality score

Splice uses this score to compare completed passes:

| Signal | Score change |
|---|---:|
| Passed test | `+10` |
| Failed test | `-8` |
| Errored test | `-12` |
| Passed acceptance fact | `+10` |
| Failed or errored acceptance fact | `-12` |
| High lint finding | `-3` |
| Medium lint finding | `-1` |
| Critical security finding | `-50` |
| High security finding | `-20` |

Other state fields remain evidence but do not change this score.

## Decision order

The monitor evaluates rules in this order:

1. Stop at the iteration limit.
2. Stop at the token budget.
3. Detect an `A, B, A, B` hash oscillation.
4. Detect any repeated current hash.
5. Request step-back analysis after three passes with no workspace change.
6. Request rollback when the third or later score is below the first score.
7. Request step-back analysis after three passes without score improvement.
8. Ask the user when confidence falls across three passes.
9. Continue with revised context.

A successful pass finishes before these rules run. Therefore, success on the
last allowed pass still completes the request.

## Decision effects

### Continue

The next pass receives the original intent, score history, last failures, and a
bounded changed-file list.

### Escalate

A cycle or oscillation can select the configured escalation model. Splice makes
this change at most once per run.

If no escalation model exists, Splice keeps the current model and adds recovery
context.

### Roll back

Rollback needs an isolated worktree. Splice captures a workspace snapshot before
the first pass and after each complete pass.

It restores the highest-scored prior snapshot. A score tie selects the latest
eligible snapshot. Without worktree recovery, a rollback decision becomes a step
back.

### Step back

A three-pass plateau starts one fresh analysis request. That request receives the
recent scores, failing test names, changed files, and the plateau reason.

Its typed root-cause proposal becomes revision context for the next pass. It does
not appear as a scheduled pipeline stage.

Three passes with no changed files and no added or removed lines also start one
step-back request. A later three-pass stretch with no workspace change aborts.

### Ask the user

A three-pass confidence decline asks the interactive user to continue with new
guidance or abort. A headless run has no callback and aborts.

This branch does not restore a workspace snapshot before it asks the user.

### Stop

The run stops after the hard iteration or token limit. It also stops after a
second three-pass stretch with no workspace change. The loop also checks a
wall-time limit before each pass and stage. The limit bounds an active stage.

The defaults are five passes and 600 seconds. `--max-turns` does not change the
pipeline pass limit.

To change the wall-time limit, use one of these sources. The first source in
the list that has a value applies:

1. The `splice exec --max-wall <duration>` flag, for example `--max-wall 30m`.
2. The `SPLICE_MAX_WALL` environment variable, with the same duration format.
3. The `pipeline.max_wall_seconds` key in the user `config.json`.

The TUI reads the environment variable and the user configuration. For the
flag and the environment variable, a value of zero or less, or a value that
does not parse, stops the command with an error. In `config.json`, the value
`0` uses the default. A negative value, or a value above 9223372036 seconds,
stops the command with an error. A project `config.json` cannot set this key.
When the limit is not the default, the run-start notice shows it.

## Interactive feature boundary

The deterministic pipeline does not run the complete interactive agent loop.
These interactive features do not apply to the fixed stage sequence:

| Feature or flag | Effect in the deterministic pipeline |
|---|---|
| Specialist delegation | No effect. Pipeline stages do not use the specialist task tool. |
| Skills | No run-time skill load for a stage. |
| MCP deferred tools | No deferred MCP schema load for a stage. |
| `--self-correct` | No effect. The pipeline uses its trajectory loop instead. |
| `--allow-escalation` | No direct effect. Pipeline escalation comes from stage-model configuration. |
| Between-turn file diagnostics | No effect between fixed stages. |
| Agent-loop compaction | No effect in a direct pipeline run. |
| Tool filters and hooks | The pipeline tool runner honors `EnabledTools`, `DisabledTools`, and beforeTool or afterTool hooks. Read-only context lookup does not apply those filters. |

The TUI agent loop and specification draft flow can use these features outside
the fixed pipeline.

## Stage output caps

A model-backed stage sends its `OutputMax` budget as the request output cap.
The provider uses the smaller positive value from that cap and its configured
default. A zero budget keeps the provider default.

## Usage and cost records

Each model request can report input, output, cached, cache-write, and reasoning
tokens. The result keeps per-stage latency and request attribution.

Splice uses a provider-reported cost when available. Otherwise, it can use a
known price record. An unknown cost remains unknown and never becomes zero.

### Prompt layout and the prefix cache

A provider can cache the prefix of a request. For Splice, the prefix is the
system prompt and the tool schema. A change to the prefix discards the cache for
later requests of the same stage.

Each request records `prompt_layout_hash`. The hash covers the system prompt and
the tool schema. The tool schema does not change when memory is present or
absent, so the prefix stays stable for a stage.

A record states `cache_hit` when the provider reported usage. The field stays
empty when usage is missing, because a missing report is not a cache miss. A
record also states `memory_position`. Splice places dynamic content after the
prefix.

The result counts prefix changes in `prompt_layout_flips`. Zero is the contract.
Splice writes a progress line when the count is not zero. A change adds cost and
does not fail the run.

The stream `final` event contains the serialized pipeline result in its `text`
field. See [Stream-JSON protocol](STREAM_JSON_PROTOCOL.md).

## Workflow topology

The pipeline is data. The embedded default topology lives in
`internal/splice/topology.go`. A tier compiles that topology into an ordered
stage list and a token budget.

A topology declares nodes and edges. An edge carries one of three payloads:
`summary`, `output`, or `none`. A stage receives the summaries that its
incoming edges deliver. A stage with no incoming edge receives no upstream
summary.

The default topology declares these edges:

- `code_writer` to `test_generator`
- `code_writer` to `static_analyzer`
- `test_generator` to `security_auditor`
- `code_writer` to `security_auditor`
- `static_analyzer` to `security_auditor`
- `static_analyzer` to `test_runner`
- `test_runner` to `acceptance_verifier`

The graph is not a linear chain. Two stages can run from one upstream. The
executor orders the stages with a deterministic topological sort.

A legacy plan carries no edges. The executor then keeps the cumulative
summary view. A compiled plan carries edges, so the executor scopes the input
to the declared edges.

The compiler emits a warning for a graph problem that does not block a run.
To replace the default topology, see [Custom topologies](#custom-topologies).

## Custom topologies

A topology file replaces the default pipeline. It can remove, rename, or
reorder stages. It can also add command nodes, prompt nodes, and agent nodes.

Design plans (`splice exec --plan` and plans that you approve in the TUI)
always use the default topology.

### Start a custom topology

1. Export the default topology to a file:

   ```bash
   splice pipeline export default my-pipeline.json
   ```

2. Edit the file. Change `name` to a new name, for example `my-pipeline`.
3. Install the file in the user library:

   ```bash
   splice pipeline import my-pipeline.json
   ```

4. Make it the active pipeline:

   ```bash
   splice pipeline use my-pipeline
   ```

5. Examine the result with `splice pipeline show`.

Import validates the file before it writes it. Import also lists each command
node and asks for confirmation. Add `--yes` to skip the question.

To use a file for one run only, give its path to `splice exec`:

```bash
splice exec --pipeline ./my-pipeline.json "fix the failing test"
```

For a complete file, see
[pipeline-write-vet-test.json](examples/pipeline-write-vet-test.json). It
runs `go vet ./...` after the code writer at the `light` tier and larger.

### Topology sources

Splice uses the first source that applies:

1. The `--pipeline` option of `splice exec`. The value is a library name or a
   file path.
2. The project file `.splice/pipeline.json`, only in a trusted workspace.
3. The library entry that `splice pipeline use` selects. Splice keeps this
   name as `active_pipeline` in the user `config.json`.
4. The embedded default topology.

A `--pipeline` value that contains a path separator or ends in `.json` is a
file path. Other values are library names.

The user library is `$XDG_CONFIG_HOME/splice/pipelines/`, or
`~/.config/splice/pipelines/`. Each entry is `<name>.json`.
`splice pipeline list` shows the folder.

When `--pipeline` overrides a trusted project file, Splice writes a warning.

When Splice cannot find a requested topology, the run stops with an error.
This rule applies to `--pipeline` and to `active_pipeline`. A file that does
not validate also stops the run.

The name `default` always selects the embedded topology. Use it with
`--pipeline default`, `splice pipeline use default`, `show default`, and
`export default`. A library entry cannot have this name.

### Project topology and trust

A project topology can run shell commands. Splice reads
`.splice/pipeline.json` only in a trusted workspace. In an untrusted
workspace, Splice ignores the file and writes a warning.

Trust applies to one directory. Splice reads the project file from the
directory that has the trust decision. It does not look at the Git top level.

- The TUI reads the file from the launch directory.
- `splice exec` reads the file from the workspace directory (`--cwd` or the
  current directory).
- `splice exec --worktree` reads the file from the new worktree. Thus, only a
  committed file applies.

Splice refuses a project file that is larger than 1 MiB. It also refuses a
symbolic link that points outside the workspace.

Before you trust a workspace, the TUI trust card lists the project command
nodes. The card also lists project hooks, plugins, and MCP servers. After
`/trust`, the project topology applies from the next run.

At the start of a run, Splice names each command node:

```text
[topology] command node vet will run: go vet ./...
```

The TUI shows these lines. The `json` and `stream-json` output of
`splice exec` also contains them. The `text` output writes them to stderr with
the `[splice]` prefix. Use `splice pipeline show` to examine the command nodes
before a run.

### File format

| Field | Required | Description |
|---|---|---|
| `version` | Yes | The schema version. Only `1` is valid. |
| `name` | Yes | The topology name. See the name rule below. |
| `description` | No | Text that `splice pipeline show` displays. |
| `author` | No | Text. Splice does not use it. |
| `splice_min_version` | No | The oldest Splice release that can load the file, for example `0.3.0`. |
| `nodes` | Yes | One or more nodes. |
| `edges` | No | The connections between nodes. |
| `budget` | No | `total_input` and `total_output`, the whole-run token limits. Both must be more than zero. |

A name has 1 to 64 characters. It can contain lowercase letters, digits, `-`,
and `_`. The first character must be a letter or a digit. The name `default`
is reserved.

An older Splice release refuses a file with a higher `splice_min_version`. The
error names the required version. A source build skips this check.

A topology that uses a field added after a release must set
`splice_min_version` to that release or later. Without it, an older Splice
reports an unknown field instead.

Splice refuses a field that it does not know. The error names the field. A
typo such as `"tier"` for `"tiers"` thus stops the load.

Node fields:

| Field | Description |
|---|---|
| `name` | A unique node name. Edges use this name. |
| `type` | A builtin stage type, `command`, or `prompt`. |
| `tiers` | The tiers where the node runs. A node without `tiers` runs at all tiers. |
| `command` | The command words. Only for `command` nodes, and required there. |
| `prompt` | The prompt template. Only for `prompt` nodes, and required there. |
| `model` | A model pin. Only for model-backed nodes. |
| `budget` | `input_max` and `output_max` for the node. |
| `capabilities` | Optional behavior flags. See [Capabilities](#capabilities). |

The builtin stage types are `code_writer`, `test_generator`,
`static_analyzer`, `security_auditor`, `test_runner`, and
`acceptance_verifier`. A node name can differ from its type.

`code_writer`, `test_generator`, and `prompt` nodes are model-backed. The
other types make no model request.

Edge fields:

| Field | Description |
|---|---|
| `from` | The upstream node name. |
| `to` | The downstream node name. |
| `payload` | `summary` (the default), `output`, or `none`. |

`summary` sends the upstream summary. `output` sends the summary and at most
2,000 characters of the upstream data as JSON. `none` sends nothing. It only
makes `from` run before `to`.

The edges must not make a cycle. Two edges cannot connect the same pair of
nodes in the same direction.

### Tiers and budgets

The tier names are `trivial`, `light`, `standard`, `substantial`, and
`architectural`. The classifier does not select `standard` at this time.

A node that runs at a tier cannot depend on a node that does not run at that
tier. Each tier must have at least one node.

Each model-backed node has a token budget:

- A builtin node gets the default budget of its stage for the tier.
- A prompt node gets the `test_generator` budget for the tier.
- A command node has a zero budget.

An explicit node `budget` replaces this value. A model-backed node needs
values more than zero. A node that makes no model request needs zero values.

Splice compiles the topology at each tier when it loads the file. The node
budgets at a tier must fit in the token envelope of that tier. The envelope is
the total of the default stage budgets for the tier, plus a small reserve.

When a tier is over its envelope, the load stops. The error names the tier:

```text
tier trivial: topology write-vet-test: node budgets total 5000 input tokens, over the 2500 input envelope for this tier. Set "tiers" on the nodes that run only at larger tiers, shrink a node "budget", or raise budget.total_input
```

To correct the error, do one of these steps:

- Set `tiers` on the nodes that you need only at larger tiers.
- Set a smaller node `budget`.
- Set `budget.total_input` and `budget.total_output` for the topology. These
  values replace the envelope at every tier.

This node adds a review to the example file. The `tiers` and `budget` fields
keep each tier in its envelope:

```json
{
  "name": "review",
  "type": "prompt",
  "tiers": ["substantial", "architectural"],
  "prompt": "Name the largest risk in this change.\n\nRequest: {{intent}}\n\n{{summaries}}",
  "budget": {"input_max": 1500, "output_max": 1000}
}
```

Add the edge `{"from": "code_writer", "to": "review"}`. Without `budget`, the
node gets the `test_generator` budget, and the `substantial` tier is over its
envelope.

### Model pins

A model pin selects the model for one model-backed node:

```json
"model": {"provider_profile": "fast", "model": "gpt-5-mini", "reasoning_effort": "low"}
```

| Field | Description |
|---|---|
| `provider_profile` | Required. The name of a provider profile. See `splice providers list`. |
| `model` | Required. The model name. |
| `reasoning_effort` | Optional. `minimal`, `low`, `medium`, or `high`. |

A pin has priority over `stage-models.json` for that node. A node that makes
no model request cannot have a pin.

When a pin cannot resolve, the run fails before the first provider request.
The error names the node:

```text
preflight: stage code_writer model: node "code_writer" references unknown provider profile "nosuch"
```

A stage route in `stage-models.json` follows the same rule. A route that
cannot resolve fails the run before the first provider request, and the error
names the stage. The stage does not run on the run model. A stage with no
entry in `stage-models.json` keeps the run model. An invalid
`stage-models.json` also fails the run before the first provider request. The
error gives the cause. `splice exec` exits with code 2 for these errors. This
is also true for `splice exec --plan`, which checks every task before the first
task runs. A valid route to a provider that cannot start (for example, a
missing OAuth token or a credential store error) exits with code 3.

Escalation overrides pins. After the run changes to the escalation model, that
model replaces each pin. The progress line names the pinned nodes, for example
`overrides the model pin of node(s) code_writer`.

### Command nodes

A command node runs one shell command. It makes no model request.

```json
{"name": "vet", "type": "command", "command": ["go", "vet", "./..."]}
```

Splice passes each `command` element as one argument. On a POSIX host, Splice
quotes each element for `sh`. An element with a space stays one argument.

Use `["sh", "-c", "..."]` for shell syntax, such as `&&`, pipes, or variables.
For example, `["sh", "-c", "go vet ./... && go test ./..."]` runs two commands.

On Windows, Splice joins the elements with spaces. The `cmd.exe` shell reads
that line, and Splice does not quote the elements.

Splice runs the command through its `bash` tool. The sandbox and the
permission mode of the session apply.

A nonzero exit status fails the node. A permission denial also fails the
node, and the error says `denied by permission policy`. A failed node fails
the pass.

Splice keeps at most 4,000 characters of the command output. A downstream
model-backed node receives the output as untrusted data. Splice writes a
warning for each edge from a command node to a model-backed node.

A command node does not verify a run. Only the builtin check stages can make a
run verified.

### Prompt nodes

A prompt node makes one model request with no tools. It cannot change files.

The template can use these variables:

| Variable | Value |
|---|---|
| `{{intent}}` | The request. |
| `{{summaries}}` | The summaries from the incoming edges, at most 2,000 characters. |
| `{{memory}}` | The project memory, at most 2,000 characters. Set `capabilities.pull_memory` to `true` to fill it. |

Splice does not change an unknown `{{name}}`. The text stays as you wrote it.

Splice keeps at most 4,000 characters of the answer. The node summary is the
first line of the answer. To send more of the answer downstream, use an
`output` edge.

A model error fails the node. An empty answer also fails the node. A failed
node fails the pass.

### Agent nodes

An agent node runs a model loop with tools. The model can call only the tools
in the node's `tools` list. Use an agent node when a step must read or change
files. Use a prompt node when a step needs no tools.

```json
{
  "name": "investigate",
  "type": "agent",
  "prompt": "Find where {{intent}} is handled. Name the files.",
  "tools": ["read_file", "grep", "glob", "list_directory"],
  "max_turns": 8,
  "permission": "ask",
  "budget": {"input_max": 60000, "output_max": 8000}
}
```

| Field | Required | Description |
|---|---|---|
| `tools` | Yes | The tool allowlist. It has 1 to 16 names from the list below. |
| `prompt` | Yes | The task text. It uses the same variables as a prompt node. |
| `max_turns` | No | The turn cap, from 1 to 50. The default is 8. |
| `permission` | No | `ask` or `auto`. Empty means the run mode. |
| `budget` | Yes | The explicit cap. Set `input_max` and `output_max`. The cap is added on top of the tier envelope. The loop stops when either is exceeded. |
| `model`, `tiers` | No | The same meaning as for other model-backed nodes. |

A file with an agent node must set `splice_min_version` to `0.3.0` or newer.
An agent node without a `budget` fails at load time. The node takes no derived
budget, because a loop can spend far more than one prompt request.
An agent node cannot set `model_free` or `produces_verification`. The
`tools`, `max_turns`, and `permission` fields are valid only on an agent node.

Eligible tools:

- `apply_patch`
- `bash`
- `delete_file`
- `edit_file`
- `exec_command`
- `glob`
- `grep`
- `list_directory`
- `lsp_navigate`
- `read_file`
- `read_minified_file`
- `update_plan`
- `web_fetch`
- `write_file`

Splice refuses any other name at load time, and the error names the tool.
Browser, terminal, and artifact tools are not offered to agent nodes. Those
tools keep an interactive session open, or they write outside the workspace.
A node asks for approval only for allow or deny, so it cannot approve a
session or an always-allow grant. If you cancel an approval prompt of a node,
Splice cancels the run.

#### Permission and sandbox

An agent node never runs outside the sandbox. Splice applies these rules:

- The node needs an enforcing sandbox. The sandbox policy must not be
  disabled, and a native sandbox backend must be available. Otherwise the node
  fails before it starts. The error says `agent nodes need an enforcing
  sandbox` and gives the reason.
- A run in `unsafe` mode is capped at `auto` for agent nodes.
- The node runs in `auto` when the run is `auto` or `unsafe`, unless the node says `ask`.
- `permission: "ask"` on the node: the node asks, for every run mode.
- Run mode `ask`: the node asks, for every node setting.
- `permission: "auto"` on the node: the node runs in `auto`. A run mode of `spec-draft` or `member-auto` stays.
- No node setting: the run mode, capped as above.
- In `auto`, the node may call a tool that needs approval only when the sandbox allows that call. A call the sandbox would prompt for, such as a write outside the workspace, is refused.
- A call that asks for `require_escalated` is refused. A command prefix grant does not run a command outside the sandbox. Splice does not retry a sandbox-denied call outside the sandbox.

In `ask`, the node prompts for each call that writes, runs a command, or uses
the network. These tools prompt: `apply_patch`, `bash`, `delete_file`,
`edit_file`, `exec_command`, `web_fetch`, and `write_file`. The node prompts
also when the sandbox allows the call, for example a write in a trusted
workspace. A saved grant or a command prefix grant does not skip the prompt.
Each approval applies to one call only. Read-only tools do not prompt.

A headless run, such as `splice exec`, has no prompt handler. It cannot ask.
Splice checks each agent node before the first provider request. When a node
is in `ask` and its `tools` list has a tool that prompts, the run stops with
exit 2. The error names the node:

```text
agent node <name> asks before writes, commands, and network calls, but this run cannot ask; set its permission to auto or run it in the TUI.
```

An `ask` node with only read-only tools runs in a headless run. To let the node
write in a headless run, set `permission` to `auto`. The sandbox still applies.

#### Budgets and failures

The `budget` of an agent node is its own hard cap. Splice adds the cap on top of
the tier envelope. It does not draw the cap from the envelope. The reason is that
the agent loop sends the whole context on every turn. The system prompt alone
takes several thousand input tokens, so a small cap always stops the loop before
the first useful answer. The shipped example uses `input_max` 60000 and
`output_max` 8000, with `max_turns` 8. Splice shows the sum of the agent caps as
a separate line, `+ agent nodes`, in each disclosure surface.

A request in the loop does not get a per-request output limit. Splice checks the
budget after each response. A node may therefore exceed `output_max` by one
response, and the stop then applies. The `input_max` budget caps the total input tokens of the loop. Splice sums the
input of every request. Each turn sends the context again, so the sum grows
faster than the context does. The `output_max` budget caps the total output
tokens. Splice stops the loop when either total exceeds its budget. The node
fails, and the error names the budget.

A node fails, and the error names the node, in these cases:

- The model calls a tool that is not in `tools`. Splice refuses the call. The
  model cannot reach past the list through `tool_search`.
- A tool call is refused, for example by permission or a hook.
- The loop reaches `max_turns` before the task completes.
- The loop exceeds the input budget or the output budget.
- The provider does not report the usage of a request that completes, or
  reports usage that is not valid. Splice cannot count that request, so it
  cannot enforce the budget. The error says `usage not reported; the budget
  cannot be enforced`. A budgeted agent node always fails on a provider that
  does not report usage on a stream. A request that stops on an error or a
  stall does not cause this failure, so the loop can send it again.
- The final answer is cut off, or the provider fails.

#### Output

The first line of the final answer is the node summary. Splice keeps at most
4,000 characters of the answer. A downstream node receives the summary as
untrusted data, the same way it receives command output. Files that a tool
changed count as changes of the node in the pass record.

#### Disclosure

Splice lists each agent node, with its tools and permission, in the trust card,
in `/pipeline`, in `splice pipeline show`, in the import confirmation, and in
the run start notice. An allowlist with `bash` or `exec_command` is marked
`can run commands`. An allowlist with `web_fetch` is marked `network`. The
`ask` permission shows the text `prompts for every write, command, and network
call`.

### Capabilities

| Field | Description |
|---|---|
| `pull_memory` | `true` gives the node the project memory. Only for model-backed nodes. |
| `pull_context` | `true` gives the node repository context. Only for `code_writer` and `test_generator` types. |
| `produces_verification` | `true` marks the node as a check for the revision loop. A `command` or `prompt` node must not set it. The run refuses the topology. |

A node that makes no model request refuses `pull_memory` and `pull_context`.

### Topology without checks

The check stages are `static_analyzer`, `security_auditor`, `test_runner`,
and `acceptance_verifier`. A topology without a check stage gets this warning once at run start:

```text
[topology] warning: topology has no verification node; revision loops are disabled
```

A completed run of this topology is not verified. The JSON result gives the
reason `topology <name> has no verification node`.

A topology can have a check stage that is not active at the run tier. That
tier then gets a different warning:

```text
[topology] warning: no verification node is active at this tier; revision loops are disabled
```

The reason for that run is `<tier> tier runs no checks`, because the tier
is the cause. The TUI shows the
DONE · NOT VERIFIED receipt, and the JSON result sets `verified` to `false`.

### Pipeline commands

| Command | Purpose |
|---|---|
| `splice pipeline list` | Show the active topology, its source, the project file, and the library entries. |
| `splice pipeline show [name\|path\|default]` | Show the nodes, edges, and command nodes. Without an argument, show the active topology. |
| `splice pipeline use <name>` | Set `active_pipeline` to a library entry or `default`. |
| `splice pipeline export <name\|path\|default> [file]` | Write a topology to a file, or to stdout. |
| `splice pipeline import <path\|url> [--yes]` | Validate a topology and install it in the user library. |
| `splice pipeline edit [--new] [name\|default]` | Edit a topology in the browser editor. See [Edit a pipeline in a browser](#edit-a-pipeline-in-a-browser). |

`use` refuses a file path. Import the file first.

`import` applies the name rule and the `splice_min_version` check. It refuses
the name `default`. When an entry with the same name exists, `import` asks
before it replaces the entry. `--yes` replaces it without a question.

A URL import must use `https`. The file must be 1 MiB or smaller. Splice
refuses a redirect to a different host.

In the TUI, `/pipeline` shows the active topology and its source. The view is
read-only.

### See the pipeline in the TUI

Type `/pipeline` to see the graph of the active topology. The view shows the
nodes in groups by dependency depth. Each row shows the node name, its type,
and the tiers that skip the node. The text `<- a, b` names the nodes that run
before the node.

The view also shows these items:

- The source of the topology.
- The compile warnings, and the tiers where each warning applies.
- The command nodes and the exact command line of each node.

During a run, the sidebar PIPELINE section shows the live graph. Each row
shows a status marker, the pass number, the tokens, and the cost. A node that
the run tier skips shows `skipped at <tier>`. The tokens and the cost are
the sum of all passes of the node. A node that a feature flag removed shows `removed:`
and the cause, for example a flag that is off. A stage that had nothing to check shows
`skipped`. After a run ends, a node that never started shows `not run`, and a node that
was still running, for example after a cancel, shows `stopped`. A cost of `cost n/a` or a
`partial` cost means that a request has no price.

Push Ctrl+G to open the evidence pane. The pane shows the same graph. When no
run exists, the pane shows the static graph. To see the details of a node, do
these steps:

1. Make sure that the input is empty.
2. Push the up or down arrow key to select a node. Letter keys always type
   into the input. PgUp and PgDn scroll the pane.
3. Push Enter to open the node details.
4. Push Esc to close the details. Push Esc again to close the pane.

The details show the node type and the configured model with its origin.
During a run, the details also show the model and the effort that the run
sends. The text `escalated` shows when run escalation selected that model.
The budget line compares the token use with the budget that the run applied.
The details also show the last summary, and, for a command node, the command
line and its result. When a node has more than one pass, the summary starts
with the pass that reported it, for example `from pass 1:`. For an agent node,
the details also show its tools, its permission, and its turn limit.

A completed node shows a Changed row in its details. The row lists up to five
changed files. It then counts the other files. A node that completed with no
changes shows `none`. The row does not appear before the node runs.

A terminal that is narrower than 64 columns shows the graph as a flat list.
Each row then shows its dependencies on a separate line.

### See the pipeline in a browser

The browser pipeline viewer shows the same graph as the TUI. It reads the
same data, so the two views show the same facts.

To open the viewer, do these steps:

1. In the TUI, type `/web`. For a headless run, use `splice exec --serve`.
2. Open the one-time link in a browser on this computer. The link opens the
   pipeline viewer.

When no run exists, the viewer shows the static graph of the active
topology. The page also shows the source of the topology, its warnings, its
command nodes, and its agent nodes. For each agent node, the page shows the
same facts as the trust card: the tools and what they can reach, the
permission, the turn limit, the budget, and the start of the prompt.

During a run, each node shows its status, its pass, its tokens, and its
cost. A status has a word and a symbol, so color is not the only signal.
When a node completes, the edges to the nodes after it pulse once. When the
system setting for reduced motion is on, the edges change color only.

Use these keys on the page:

| Key | Action |
|---|---|
| `j` or the down arrow | Select the next node. |
| `k` or the up arrow | Select the previous node. |
| Enter | Open the details of the selected node. |
| Esc | Close the details. |
| `[` and `]` | Move one step back or forward on the timeline. |

You can also click a node to open its details. The details show the same
facts as the Ctrl+G pane in the TUI. For an agent node, the details also show
its tools, its permission, and its turn limit. For a node that completed, the
details show the files that it changed. Two bars show the input and the output
tokens of the latest pass against the budget, with the percentage in words.

After a run ends, the timeline slider replays the run. Step 0 is the plan,
before a node runs. Each next step adds one stage record, in order. The last
position of the slider is the end of the run, and it shows the final state.

During a replay, the header says which step shows. The node details then
show the facts of the node at that step.

The viewer is read-only. It makes no external request. It removes the
provider keys of the run and other known secrets from the text that it shows.
Splice reads the stored keys only when the browser surface runs. If Splice
cannot read them, the viewer hides the run and shows the static graph.

When the TUI session changes, for example after `/new` or `/resume`, the page
stops its updates and tells you to run `/web` again. A run of
`splice exec --serve` stops the server when the run ends. The page then keeps
the last state that it received, but it cannot load the node details.

### Edit a pipeline in a browser

The browser editor changes a topology as a draft. Then it installs the draft
in the user library. It never writes the project file `.splice/pipeline.json`.
To put a topology in a project, use `splice pipeline export`.

To open the editor, use one of these entry points:

| Entry point | Result |
|---|---|
| `/pipeline edit` in the TUI | A draft of the active topology. A library entry is edited. A project file or the embedded default is copied. |
| `/pipeline edit <name>` in the TUI | A draft of the library entry `<name>`. `default` copies the embedded default. |
| The Edit a copy link in the viewer | A copy of the active topology. |
| `splice pipeline edit [--new] [name\|default]` | The same drafts, without the TUI. |

The TUI shows a one-time link, and opens a browser when the terminal is
interactive. `splice pipeline edit` writes the link to stderr. Its server
stops when you push Ctrl-C, or after 30 minutes without use.

A second edit of the same source opens the latest draft of that source. A
source is the embedded default, a project file, or a library entry. Add
`--new` to start a new draft. The drafts page lists each draft and its
source. Use it to open a draft again or to delete it.
The drafts page can also start a draft from a blank pipeline, the built-in
default, the active topology, a library entry, or a shipped example.

#### Drafts and validation

The editor saves each change to the draft file. The drafts are in
`<config>/splice/pipelines/.drafts/`. Only your user can read them. The
editor saves a draft that is not valid too, so you do not lose work.

Splice checks each change with the same checks as `splice pipeline import`.
The checks include the schema, the compile at each tier, the token envelope,
and the name rule. The top bar shows the result and the draft state, for
example `[+] valid` and `saved 4s ago`. Each problem shows next to its field
in the inspector. A node with a problem shows `[!]` and the number of its
problems. The Problems list under the canvas shows all problems. Select a
problem to go to its node and field.

#### The editor page

The page has three areas:

- Nodes, on the left: the node types, with a search field and a line about
  each type. Select a type to add a node.
- The canvas, in the center: the graph, drawn as the pipeline viewer draws
  it. The tools under the graph zoom, fit, reset, and lay out the graph.
  The `?` tool lists the keys.
- The inspector, on the right: the pipeline settings, or the selected node.
  The JSON tab shows the exact file that publish writes. You cannot edit
  it there. Use Copy to copy it.

On a screen that is narrower than 900 pixels, Nodes and the inspector open
as sheets at the bottom of the screen.

| Key | Action |
|---|---|
| Tab | Select the next node. |
| Arrow keys | Move the selected node. Hold Shift to move it more. |
| Enter | Edit the selected node in the inspector. |
| `n` | Search the node types to add a node. |
| `a` | Add a node after the selected node. |
| `e` | Connect the selected node to another node. Select the target with Tab or the arrow keys, then push Enter. |
| Delete | Delete the selected node and its edges, or the selected edge. |
| `u` | Undo. |
| `U` or Ctrl+Shift+Z | Redo. |
| `+`, `-`, `0`, `f` | Zoom in, zoom out, reset the zoom, fit the graph. |
| `l` | Lay out the graph as the viewer draws it. |
| `s` | Show the pipeline settings: the name, the description, the minimum Splice version, and the run budget. |
| `j` | Show the JSON that publish writes. |
| `p` | Publish. |
| `?` | Show the list of keys. |
| Esc | Stop the current action, or clear the selection. |

You can also drag a node with the pointer. To connect two nodes, drag from
the small circle under a node to another node. The `+` under a node adds a
node after it. The editor refuses an edge that makes a cycle before Splice
checks it.

The inspector shows only the fields that apply to the node type. When you
change the type, the inspector keeps the fields that still apply. It names
the fields that it removed. For an agent node, the editor sets the minimum
Splice version to 0.3.0 when no version is set.

Depends on lists the nodes that run before the selected node, with the
payload of each edge: `summary`, `output`, or `none`. Use it to connect
nodes with the keyboard. It offers only the nodes that do not make a cycle.

For a command node, the inspector shows each argument in its own row. Under
the rows, it shows the command line exactly as it runs. For a prompt node
or an agent node, the inspector shows the prompt and its variables.

#### Publish
Publish needs a valid draft. The publish dialog shows these items:

- The library file that publish writes.
- Each command node and its exact command line.
- Each agent node: its tools and what they can reach, the permission, the
  turn limit, the budget, and the start of the prompt.
- The warnings of the topology.
- When an entry with the same name exists: a list of the nodes, edges, and
  settings that change.

Publish writes the entry only after you confirm in the dialog. When the
draft or the library changes after the dialog opens, publish stops. Open
the dialog again to see the new state.

Publish does not change the active pipeline. To use the entry, choose Use it
now in the dialog, or use `/pipeline use <name>`. Use it now refuses while a
run is active. A run uses the topology that it started with, so a change
never affects a run that is active.

