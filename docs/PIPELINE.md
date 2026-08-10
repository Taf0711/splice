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
- no high or critical lint finding exists; and
- no high or critical security finding exists.

An incomplete verification stage is recorded in the result. The current success
gate does not treat incomplete verification as a failure by itself.

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
| Type error | `-2` |

Other state fields remain evidence but do not change this score.

## Decision order

The monitor evaluates rules in this order:

1. Stop at the iteration limit.
2. Stop at the token budget.
3. Detect an `A, B, A, B` hash oscillation.
4. Detect any repeated current hash.
5. Request rollback when the third or later score is below the first score.
6. Request step-back analysis after three passes without score improvement.
7. Ask the user when confidence falls across three passes.
8. Continue with revised context.

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
eligible snapshot. Without worktree recovery, a rollback decision aborts.

### Step back

A three-pass plateau starts one fresh analysis request. That request receives the
recent scores, failing test names, changed files, and the plateau reason.

Its typed root-cause proposal becomes revision context for the next pass. It does
not appear as a scheduled pipeline stage.

### Ask the user

A three-pass confidence decline asks the interactive user to continue with new
guidance or abort. A headless run has no callback and aborts.

This branch does not restore a workspace snapshot before it asks the user.

### Stop

The run stops after the hard iteration or token limit. The loop also checks a
wall-time limit before each pass.

The defaults are five passes and 600 seconds. `--max-turns` replaces the pass
limit when it is positive.

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

The TUI agent loop and specification draft flow can use these features outside
the fixed pipeline.

## Usage and cost records

Each model request can report input, output, cached, cache-write, and reasoning
tokens. The result keeps per-stage latency and request attribution.

Splice uses a provider-reported cost when available. Otherwise, it can use a
known price record. An unknown cost remains unknown and never becomes zero.

The stream `final` event contains the serialized pipeline result in its `text`
field. See [Stream-JSON protocol](STREAM_JSON_PROTOCOL.md).
