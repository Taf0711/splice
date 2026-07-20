# Splice Pipeline Specification

Splice's distinguishing layer is a deterministic, orchestrator-mediated,
two-phase pipeline in `internal/splice/`. This document specifies the pipeline
flow and each stage's contract. The typed structs live in
`internal/splice/schemas/` and every input/output has a `Validate()` method.

## The two-phase model

1. **Design phase.** A free-form design conversation (`design_conversation`)
   helps the user think through a change. On `/crystallize` the
   `design_crystallize` agent turns the conversation into a typed `DesignPlan`.
   On `/approve` the plan hands to the execution phase.
2. **Execution phase.** An orchestrator (`splicerun.Run` in
   `internal/splice/run.go`) classifies the request into a tier, builds a typed
   `ExecutionPlan`, and runs the tier's stages under a deterministic trajectory
   monitor.

The orchestrator is the foreman. It classifies, plans, routes, and decides what
each stage needs. Stages never pass data directly to each other; summaries flow
forward from prior stages, not raw outputs. The user's verbose original request
never propagates past the orchestrator (`DistillRequestIntent`).

## Pipeline flow

1. **Classify.** `ClassifyRequest` (deterministic) assigns one of the tiers:
   `trivial`, `light`, `substantial`, or `architectural`. (The `standard` tier
   exists in the schema but the current classifier does not produce it.)
2. **Plan.** `BuildExecutionPlan` turns the tier into a typed `ExecutionPlan`
   with a per-stage token budget. Unknown tiers fail loudly.
3. **Fulfill context.** Before a stage runs, its `ContextRequest` is fulfilled
   deterministically through Zero's tool registry (read_file, list_directory,
   grep).
4. **Run stages.** Each stage is a typed Go function in
   `internal/splice/stages/`. Model-backed stages call the configured provider;
   deterministic stages run local tools and make no provider calls.
5. **Monitor trajectory.** `ComputeIterationState` builds a state vector from
   stage outputs; `EvaluateTrajectory` scores it and decides continue,
   escalate, rollback, step back, or surface to user.
6. **Iterate or abort.** The orchestrator acts on the decision. Critical/high
   lint or security findings, or failing/errored tests, block completion.

## Stages by tier

| Tier | Stages |
|---|---|
| `trivial` | code_writer |
| `light` | code_writer, static_analyzer, test_runner |
| `standard` | code_writer, test_generator, static_analyzer, test_runner |
| `substantial` | code_writer, test_generator, static_analyzer, security_auditor, test_runner |
| `architectural` | (same as substantial) |

The design-phase stages (`design_conversation`, `design_crystallize`,
`plan_critic`) run before execution and are not tier-scheduled. `step_back` is
orchestrator-level (not a registered stage) and fires only on a trajectory
plateau.

## Stage contracts

Each model-backed stage receives `pipeline_meta.md` prepended to its own prompt
(`composeSystemPrompt`). Each stage's input/output is a typed struct with a
`Validate()` method.

### code_writer (model-backed, medium tier)

- **Role:** write or modify the code for the distilled intent.
- **Input (`CodeWriterInput`):** `intent`, `language`, `target_paths`,
  `relevant_context`, `revision_context`, `memory`.
- **Output (`CodeWriterOutput`):** `files` (a list of `FileChange`),
  `language`, `intent`, `dependencies`, `known_limitations`, `confidence`.
- **Tool:** `submit_code` returns the typed output. Missing/invalid output
  retries up to twice with corrective feedback.

### test_generator (model-backed, medium tier)

- **Role:** write tests for the code writer's changes.
- **Input (`TestGeneratorInput`):** `intent`, `language`, `target_paths`,
  `relevant_context`, `revision_context`, `memory`. The code writer's summary is
  injected into `relevant_context` by the orchestrator.
- **Output (`TestGeneratorOutput`):** test `files` and metadata, validated.

### static_analyzer (deterministic, model-free)

- **Role:** fast local quality checks (Go parser, go/format, py_compile, Ruff,
  node --check, tsc) with no provider call.
- **Output:** a typed `VerificationReport` (`passed`, `findings`, `incomplete`,
  or `not_applicable`). High/critical findings block completion.

### security_auditor (deterministic, model-free)

- **Role:** local security floor (Bandit for Python, gosec for Go, SARIF for
  JS/TS, trivy for secrets and dependencies) with no provider call.
- **Output:** a typed `VerificationReport`. High/critical security findings
  block completion.

### test_runner (deterministic, model-free)

- **Role:** run the project's test suite through the safety substrate (the
  registered `bash` tool), honoring permission mode and sandbox.
- **Output (`TestRunResults`):** per-test status (passed/failed/errored) plus
  the discovered command, persisted to memory as a `test_command` observation.

### design_conversation (model-backed, design phase)

- **Role:** free-form design conversation before any code is written. Read-only.
- **Prompt:** `design_conversation.md`. Uses `ask_user` with suggested options
  and a recommended choice for clarifying questions.

### design_crystallize (model-backed, medium tier)

- **Role:** turn the design conversation into a typed `DesignPlan` via the
  `submit_design_plan` tool.
- **Input (`DesignConversationInput`):** the conversation history.
- **Output (`DesignPlan`):** epic, requirements, and tasks with acceptance
  facts.

### plan_critic (model-backed, reasoning tier)

- **Role:** adversarial review of the crystallized `DesignPlan` before approval.
- **Input (`PlanCriticInput`):** the `DesignPlan`.
- **Output (`PlanCritique`):** issues with severities and suggested mitigations,
  via the `submit_critique` tool.

### step_back (model-backed, orchestrator-level)

- **Role:** fresh analysis on a trajectory plateau (three iterations without
  improvement). Not a registered stage.
- **Input (`StepBackReport`):** distilled intent, recent scores, failing tests,
  changed files, plateau reason.
- **Output (`StepBackAnalysis`):** hypothesized root cause and a recommended
  approach, fed back to the next code writer iteration.

## Token and cost accounting

Each model-backed stage reports a typed `StageUsage` (input, output, cached,
cache-write, and cost) that the orchestrator records in its `StageRecord` and
sums into the `PipelineResult` totals. `StageRecord` also carries per-stage
latency. The `final` stream-JSON event carries the full `PipelineResult` JSON.
Note: `StageUsage.CostUSD` is zero until a pricing source is wired for the stage
provider.
