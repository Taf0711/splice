# Design transitions

A design session separates discussion from execution. The user keeps control of
the transitions that can create or execute a plan.

## State flow

```text
design session
  -> conversation
  -> crystallized plan
  -> plan critique
  -> approved plan
  -> execution
```

Use `/design` to enter design mode. Use `/crystallize` to create a typed plan.
Use `/approve` only after the current plan passes review.

## Direct execution

Use `/exec <prompt>` to run one prompt through the pipeline without a plan.
The run matches `splice exec` semantics: it uses auto permission mode, and the
transcript states that choice. Set `SPLICE_EXEC_MEMORY=off` before launch for
a cold run with no memory injection.

## Manual and agent requests

The design agent can request crystallization or approval through host transition
tools. The manual and agent paths use the same controller.

| Transition | Manual command | Agent request |
|---|---|---|
| Create a plan | `/crystallize` | `crystallize_design` |
| Approve a plan | `/approve` | `approve_design` |

An agent request queues a host transition. It does not bypass plan validation,
critique, persistence, sandbox policy, or user permissions.

## User authorization

The design agent can request a transition only after the user explicitly asks
for that transition.

The agent must not decide by itself that a conversation is complete. It also
must not approve a plan only because the critique has no blocker.

## Approval availability

Splice exposes approval to the design agent only when:

- a current plan exists; and
- the current critique has no required fix.

The user command follows the same checks.

## One transition per turn

A design turn can queue one transition. A second request in the same turn
returns an error and does not replace the first request.

Splice starts the transition only after the design turn completes successfully.
A failed or canceled turn does not start it.

## Create and approve in one request

The crystallization request can set `approve_if_ready` after the user asks for
both operations.

Splice approves only when:

- the design turn completed;
- the plan passed typed validation;
- the critique passed typed validation;
- the critique has no required fix; and
- lifecycle state was saved.

Any failure stops the sequence at that point.

## Revision identity

A revised plan keeps the current plan family ID. Its revision number increases.
Approval refers to the current saved revision.

This behavior keeps review history connected across several crystallization
attempts.

## Audit fields

Design lifecycle events record whether the user command or design agent
requested the transition. Older sessions without this field remain readable.

The transcript labels the request source so the user can distinguish a command
from an agent request.

## Failure behavior

Splice rejects these operations with a named error:

- a malformed transition request;
- a transition during an active run;
- approval without a current plan;
- approval with a required critique fix; and
- approval without a saved current revision.

A rejected transition does not silently create another plan or start execution.
