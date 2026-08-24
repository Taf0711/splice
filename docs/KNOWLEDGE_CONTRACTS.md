# Knowledge-to-Contract Layer

Status: design brief. Not implemented. This document is the
implementation-ready specification for a planned architectural layer.

## The missing concept

A generalized bridge from project knowledge to deterministic
enforcement, through applicability resolution.

Splice already has both ends:

```text
PROJECT KNOWLEDGE                      EXECUTION GUARANTEES
─────────────────                      ────────────────────
AGENTS.md                              typed stages
SPLICE.md                              sandbox
skills                                 tests
memory                                 static analysis
docs                                   security checks
user instructions                      acceptance verifier
       │                                      ▲
       │                                      │
       └──────────── MISSING ─────────────────┘
```

What is missing is a system that answers:

> Given everything this project says about how work should be done,
> which rules matter for this task, and which of those rules can Splice
> guarantee rather than merely tell the model about?

The one-sentence brief:

> Implement a Knowledge-to-Contract Layer in Splice: a harness
> subsystem that resolves which project rules are applicable to a run,
> separates advisory guidance from mechanically enforceable invariants,
> compiles trusted enforceable rules into typed runtime contracts,
> gathers deterministic evidence for those contracts, and prevents
> transitions or completion when required invariants are unsatisfied.
> It must complement, not replace, skills, project instructions,
> memory, sandbox policy, or task-specific acceptance facts.

Implementation mantra:

> If knowledge can be turned into a deterministic guarantee, move that
> guarantee out of the model and into the harness.

---

## 1. Problem

Splice currently has two largely separate mechanisms.

### Knowledge/context mechanisms

These tell the model things:

```text
AGENTS.md
SPLICE.md
skills
project documentation
memory observations
conversation
repository context
```

Example:

```text
Before starting staging:
1. reset the development database
2. apply migrations
3. seed fixtures
4. then start staging
```

The model can read this and hopefully obey it.

### Harness enforcement mechanisms

These verify or constrain things outside the model:

```text
typed schemas
sandbox rules
test runner
static analyzer
security auditor
acceptance verifier
trajectory monitor
worktree rollback
```

Example:

```text
tests failed
    ↓
pipeline cannot complete
```

These are much stronger because the model cannot simply forget them.

### Missing abstraction

There is no generalized mechanism that turns:

```text
"reset DB before staging"
```

into:

```text
staging.start is illegal unless:

database.reset == passed
migrations.apply == passed
fixtures.seed == passed
```

That bridge is the Knowledge-to-Contract Layer.

Definition:

> The Knowledge-to-Contract Layer identifies project rules relevant to
> the current task, determines whether each rule is advisory or
> mechanically enforceable, compiles enforceable rules into typed
> runtime contracts, gathers deterministic evidence, and prevents
> actions or completion when required invariants are unsatisfied.

---

## 2. Core architecture

```text
                    PROJECT KNOWLEDGE
                           │
        ┌──────────────────┼──────────────────┐
        │                  │                  │
     AGENTS.md           skills             memory
     SPLICE.md           docs               config
        │                  │                  │
        └──────────────────┬──────────────────┘
                           │
                           ▼
                KNOWLEDGE NORMALIZATION
                           │
                           ▼
                 APPLICABILITY RESOLVER
                 "Does this matter now?"
                           │
             ┌─────────────┴─────────────┐
             │                           │
             ▼                           ▼
        ADVISORY RULE              ENFORCEABLE RULE
             │                           │
             ▼                           ▼
     inject bounded context       CONTRACT COMPILER
                                         │
                                         ▼
                                  RUNTIME CONTRACT
                                         │
                                         ▼
                                   EVIDENCE ENGINE
                                         │
                              ┌──────────┴──────────┐
                              │                     │
                           satisfied             violated
                              │                     │
                              ▼                     ▼
                           continue          warn/block/recover
```

---

## 3. The three first-class concepts

### A. Knowledge

What does the project expect?

Example:

```text
"Database migrations must be run before staging starts."
```

This may come from AGENTS.md, SPLICE.md, SKILL.md, project config,
repository docs, memory, user instructions, or generated design plans.
Knowledge itself does not imply enforcement.

### B. Applicability

When does the knowledge matter?

For the request "Rename parseConfig to loadConfig", the rule "Reset the
database before staging" is irrelevant. For "Apply this migration and
bring staging up", it is highly relevant.

Splice needs an explicit resolution step:

```text
task + execution plan + target paths + planned operations
+ repository state + rule selectors
        ↓
ApplicableRuleSet
```

This prevents the opposite of information minimalism: dumping every
rule into every run.

### C. Contract

If a relevant rule can be objectively checked, Splice converts an
instruction into an invariant:

```text
Instruction:
"Tests need to pass before merge."

Contract:
before_merge:
    requires:
        test_suite.status == passed
```

---

## 4. Enforcement levels

Rules have different strengths. Do not assume every project
instruction should become a hard gate.

| Level | Behavior | Example |
|---|---|---|
| `advisory` | Model guidance only | "Prefer small functions." |
| `warn` | Violation produces evidence and warning; execution continues | "Try to keep public functions documented." |
| `require` | A transition cannot happen unless evidence satisfies the requirement | "Tests must pass before completion." |
| `deny` | Action is prohibited regardless of model intent | "Never modify generated/vendor files." |

---

## 5. Discovery versus authority

Do not automatically turn arbitrary prose into hard policy. This would
create a nondeterministic policy engine.

Separate discovery from authority:

- The model may discover candidate contracts.
- Only trusted structured sources become hard requirements
  automatically.

```text
AGENTS.md          → candidate rule → advisory by default
.splice/contracts/*.yaml → trusted contract → require/deny allowed
```

Later Splice can support `splice contracts promote <candidate>` or
promotion during design review.

---

## 6. Project structure and example contract

```text
.splice/
├── config.json
├── contracts/
│   ├── staging.yaml
│   ├── migrations.yaml
│   ├── testing.yaml
│   └── generated-files.yaml
└── ...
```

Do not couple contracts directly to skills. Skills remain knowledge
packages. Contracts are enforceable project policy.

Conceptual contract shape (serialization may change):

```yaml
version: 1

id: staging.requires-fresh-database

description: >
  Staging must only start after the development database
  has been reset, migrated, and seeded.

applies_when:
  actions:
    - staging.start

enforcement: require

requires:
  - id: database-reset
    verifier:
      type: command
      command: ./scripts/reset-db --check

  - id: migrations-applied
    verifier:
      type: command
      command: ./scripts/migrate --check

  - id: fixtures-seeded
    verifier:
      type: command
      command: ./scripts/seed --check
```

---

## 7. Lifecycle boundaries

Do not invent a large semantic action language. Bind contracts to
boundaries Splice already has:

```go
type ContractTriggerKind string

const (
    TriggerBeforeStage    ContractTriggerKind = "before_stage"
    TriggerAfterStage     ContractTriggerKind = "after_stage"
    TriggerBeforeTool     ContractTriggerKind = "before_tool"
    TriggerAfterTool      ContractTriggerKind = "after_tool"
    TriggerBeforeComplete ContractTriggerKind = "before_complete"
    TriggerBeforeMerge    ContractTriggerKind = "before_merge"
)
```

V1 can start with `before_complete`, `before_merge`, and
`before_tool`. Those alone provide enormous value.

---

## 8. Typed model

```go
type EnforcementLevel string

const (
    EnforcementAdvisory EnforcementLevel = "advisory"
    EnforcementWarn     EnforcementLevel = "warn"
    EnforcementRequire  EnforcementLevel = "require"
    EnforcementDeny     EnforcementLevel = "deny"
)

type ProjectContract struct {
    ID           string
    Description  string
    Source       ContractSource
    Selectors    ContractSelectors
    Trigger      ContractTrigger
    Requirements []ContractRequirement
    Enforcement  EnforcementLevel
}

type ContractSource struct {
    Kind         string
    Path         string
    SkillID      string
    MemoryID     string
    UserApproved bool
}
```

Source matters for auditability: every applied rule records where it
came from.

---

## 9. Applicability selectors and resolver

Keep V1 selectors deterministic and small:

```go
type ContractSelectors struct {
    Paths    []string // path globs
    Stages   []string // stage names
    Tools    []string // tool names
    Commands []string // command prefixes/patterns
}
```

Resolver:

```go
ResolveApplicableContracts(
    run RunContext,
    plan ExecutionPlan,
    contracts []ProjectContract,
) ApplicableContractSet
```

Output carries reasons. Splice never silently applies mysterious
policy:

```text
staging.requires-fresh-database

applied because:
- requested operation matched "staging"
- target path matched deployment/**
```

---

## 10. Evidence is first-class

A contract is worthless unless the harness can prove whether it
passed.

```go
type EvidenceStatus string

const (
    EvidencePassed        EvidenceStatus = "passed"
    EvidenceFailed        EvidenceStatus = "failed"
    EvidenceIncomplete    EvidenceStatus = "incomplete"
    EvidenceNotApplicable EvidenceStatus = "not_applicable"
)

type ContractEvidence struct {
    ContractID    string
    RequirementID string
    Status        EvidenceStatus
    Source        string
    Command       string
    Summary       string
    Timestamp     time.Time
    ArtifactHash  string
}
```

The difference this makes:

```text
Weak:  agent claims it reset the DB
Strong:database-reset
       status: passed
       source: deterministic command verifier
       command: ./scripts/reset-db --check
       exit_code: 0
```

Evaluation flows through the existing safety substrate (tool registry,
sandbox, permissions, hooks), never raw exec:

```go
EvaluateContract(
    contract ApplicableContract,
    state RunState,
    runner ToolRunner,
) ContractEvaluation
```

---

## 11. Runtime gate and recovery

At each supported boundary:

```text
planned transition
       │
       ▼
resolve applicable contracts
       │
       ▼
collect evidence
       │
       ▼
evaluate requirements
       │
       ▼
advisory -> context only
warn     -> emit warning
require  -> block if false
deny     -> reject action
```

For `require`, produce a typed failure the trajectory system
understands:

```go
type ContractViolation struct {
    ContractID         string
    Trigger            ContractTrigger
    FailedRequirements []string
    Evidence           []ContractEvidence
}
```

Never reduce this to a generic "stage failed".

Failed contracts feed the existing recovery loop instead of always
aborting:

```text
contract violation
       ↓
revision context
       ↓
code writer
       ↓
verification reruns
```

Example revision context:

```text
Contract staging.requires-fresh-database failed:

- database reset: passed
- migrations applied: failed
- fixtures seeded: not evaluated

Required before staging.start.
```

---

## 12. Relationship to existing mechanisms

### Acceptance facts (do not replace)

Acceptance facts are task-specific ("for THIS feature, /health must
return 200"). Project contracts are repository-level invariants that
apply across unrelated tasks ("every API schema change must regenerate
clients"). They should eventually share the evidence representation.

### Sandbox (do not replace)

Sandbox answers capability/security questions ("may the agent write
/etc/passwd?"). Contracts answer semantic/project-workflow questions
("is deployment valid right now given tests have not passed?").

### Skills

Skills are knowledge packages; contracts are guarantees. One skill can
contain explanatory knowledge (context), procedures (reasoning), and
enforceable invariants (contracts). The invariant leaves the skill and
becomes a contract artifact.

### Memory

Memory observations ("this repo usually runs pnpm generate after
editing schema.graphql") become candidates, never automatic hard rules.
Future pipeline: observation → candidate → repeated evidence or
explicit approval → promoted contract. Not MVP.

---

## 13. Pipeline integration

Current flow gains contract gates:

```text
request
   ↓
classify
   ↓
build plan
   ↓
RESOLVE APPLICABLE PROJECT CONTRACTS
   ↓
focused context
   ↓
writer
   ↓
contract gate(s)
   ↓
analyze
   ↓
test
   ↓
acceptance verify
   ↓
BEFORE-COMPLETION CONTRACT GATE
   ↓
trajectory
```

Start with only `before_complete`, `before_merge`, `before_tool`.

Trajectory state gains contract counters:

```go
type ContractSummary struct {
    Passed     int
    Failed     int
    Warned     int
    Incomplete int
}
```

Success becomes:

```go
success :=
    noStageFailures &&
    noTestFailures &&
    noAcceptanceFailures &&
    noBlockingLint &&
    noBlockingSecurity &&
    noRequiredContractFailures
```

---

## 14. Persistence, versioning, visibility

Persist per run: contract id, definition hash, applicability result
and reasons, enforcement level, requirements, evidence, final status.
Contracts must be auditable: six months later, "deployment blocked"
must be explainable.

If `.splice/contracts/staging.yaml` changes between runs, old runs
remain explainable via a persisted `ContractDefinitionHash`.

TUI surface (eventual):

```text
Blocked by project contract

staging.requires-fresh-database

✓ database reset
✕ migrations applied
○ fixtures seeded

Source:
.splice/contracts/staging.yaml
```

CLI surface for MVP:

```bash
splice contracts list
splice contracts validate
splice contracts check
```

Later: `candidates`, `promote <id>`, `show`, `applicable`.

---

## 15. Initial verifier types

Keep V1 tiny:

```text
command
file_exists
file_absent
git_clean
path_unchanged
stage_result
test_result
acceptance_result
```

No CEL/Rego-style rule language in V1.

---

## 16. Worked examples

Generated code (the canonical case):

```yaml
id: graphql.generated-clients-current

applies_when:
  changed_paths:
    - "**/schema.graphql"

trigger: before_complete

enforcement: require

requires:
  - id: generated-code-current
    verifier:
      type: command
      command: make graphql-check
```

The model no longer has to remember. It cannot successfully finish
until generated code is current.

Migrations:

```yaml
id: migrations.require-tests
applies_when:
  changed_paths:
    - migrations/**
trigger: before_complete
enforcement: require
requires:
  - id: migration-tests
    verifier:
      type: command
      command: make test-migrations
```

Forbidden files:

```yaml
id: generated-files.readonly
applies_when:
  changed_paths:
    - generated/**
trigger: before_write
enforcement: deny
```

Subjective architecture stays advisory. The system must not pretend
subjective judgment is deterministic:

```yaml
id: storage.repository-pattern
applies_when:
  changed_paths:
    - internal/storage/**
enforcement: advisory
```

---

## 17. Non-negotiable invariants

1. The model never has final authority over whether a hard contract
   exists.
2. Hard contracts come from trusted typed configuration or explicit
   promotion.
3. Applicability is deterministic whenever possible.
4. Contract evidence comes from harness-visible state or deterministic
   tools whenever possible.
5. Agents cannot self-report a requirement as satisfied. Bad: writer
   says "I ran migrations". Good: migration verifier returned exit 0.
6. Unknown or incomplete evidence never silently becomes passed.
7. Every blocking decision is explainable.
8. Contract evaluation honors existing sandbox and permission rules.
9. Contracts stay bounded and relevant; never inject the whole corpus
   into every stage.
10. Fail closed for `deny`, explicitly for `require`, fail soft for
    advisory knowledge.

---

## 18. Implementation phases

1. **Typed project contracts**: schema, loader, validation, discovery
   of `.splice/contracts/*.yaml`. No LLM involved.
2. **Deterministic applicability**: path/stage/tool selectors,
   lifecycle triggers, `ApplicableContractSet`. Heavy tests.
3. **Evidence engine**: command, file_exists, git state, stage result,
   test result verifiers. All execution through existing safety and
   tool infrastructure.
4. **Completion gate**: evaluate `before_complete` contracts before
   the pipeline reports success. Best first integration; touches very
   little else.
5. **Trajectory integration**: failures become revision context,
   iteration state, quality evidence, trajectory decisions.
6. **Before-tool / before-write contracts**: stop invalid actions
   before they occur. More invasive; implement after completion
   contracts are stable.
7. **Knowledge promotion**: AGENTS.md/skills/memory → candidate
   discovery → human approval → typed contract. This is where skills
   connect to enforcement.

---

## 19. Evals and metrics

Beyond unit tests, build scenario evals:

- **Applicability precision/recall**: 20 contracts in the repo, task
  touches one subsystem, expect 2 applicable and 18 ignored with zero
  extra tool execution.
- **Hard-rule compliance**: agent attempts to finish without
  satisfying a requirement; Splice blocks completion in 100 percent
  of runs regardless of model choice.
- **Model forgetting**: the skill says to run the generator; the model
  intentionally omits it; the contract still catches it. Demonstrates
  model failure is not system failure.
- **False-positive contracts**: a task that should not trigger the
  database contract triggers nothing.
- **Evidence integrity**: the model claims it ran something without
  doing it; no evidence exists and the contract remains unsatisfied.

Metrics worth recording: applicability precision and recall, blocking
rule enforcement rate, false block rate, advisory context token cost,
verification latency, verifier failure rate, incomplete evidence rate,
recovery-after-violation rate.

Killer metric:

```text
Model followed requirement:       71%
Splice final invariant satisfied: 100%
```

That is what a harness is for.

---

## 20. What not to build

Not in any early phase:

- arbitrary AST rule DSL
- OPA/Rego clone
- LLM-generated execution graphs
- semantic ontology
- automatic hard policy extraction
- vector database
- new agent framework
- event bus rewrite

Smallest useful slice that proves the architecture:

```text
typed contracts
+ deterministic applicability
+ deterministic evidence
+ before-complete enforcement
```

Once it exists, skills, memory, project instructions, tests,
sandboxing, verification, and orchestration share one story:

```text
                    SPLICE

                    REQUEST
                       │
                       ▼
                  DESIGN/PLAN
                       │
              ┌────────┴────────┐
              │                 │
              ▼                 ▼
       PROJECT KNOWLEDGE    REPOSITORY STATE
              │                 │
              └────────┬────────┘
                       ▼
               APPLICABILITY
                       │
              ┌────────┴────────┐
              │                 │
              ▼                 ▼
         MODEL CONTEXT       CONTRACTS
              │                 │
              ▼                 ▼
          REASONING          EVIDENCE
              │                 │
              └────────┬────────┘
                       ▼
                 ORCHESTRATOR
                       │
                       ▼
                    ACTION
                       │
                       ▼
              DETERMINISTIC CHECKS
                       │
                       ▼
                 TRAJECTORY
```
