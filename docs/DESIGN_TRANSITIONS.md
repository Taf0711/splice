# Design Transitions

This document describes how a design plan is crystallized and approved. The
user can run the commands. The design agent can also call tools that queue the
same transitions.

## State flow

A design session moves through these phases.

```text
design_mode_entered
    -> conversation
    -> plan_crystallized (crystallize_design or /crystallize)
    -> critique_recorded
    -> plan_approved (approve_design or /approve)
    -> execution
```

## Dual entry points

Each transition has two ways to start.

| Transition | Manual | Agent tool | Permission |
|------------|--------|------------|------------|
| Crystallize | `/crystallize` | `crystallize_design` | Allow |
| Approve | `/approve` | `approve_design` | Allow |

Both paths converge on one typed controller. The controller validates the
design state and begins the run. The manual command and the agent tool share
the same checks, critic, sandbox, and permission gates.

The tools only queue a host transition. They do not run the crystallizer,
critic, or executor. That is why their permission is Allow.

## Authorization rule

The design agent may call a transition tool only when the current user
explicitly asked for that transition.

The tool description and the design conversation prompt state this rule. The
agent must not decide on its own to crystallize or approve.

## approve_design exposure

The `approve_design` tool is exposed only when both are true.

- A current plan exists.
- The current critique does not block execution.

The `crystallize_design` tool is always exposed in design mode.

## One transition per turn

One design turn can queue one transition.

A second transition tool call in the same turn returns a useful error. It does
not replace the first request.

The transition begins after the design turn finishes successfully. A failed
design turn does not run the transition.

This design has no nested provider run and no session race.

## approve_if_ready rule

`crystallize_design` accepts an optional `approve_if_ready` boolean.

Approval is scheduled only when all are true.

- The design turn succeeded.
- The crystallizer produced a valid plan.
- The critic produced a valid critique.
- No critique requires a fix.

A must-fix critique, a crystallizer error, a critic error, and a persistence
error all stop the flow at the point of failure. Approval is not scheduled.

## Source audit

Each lifecycle event records who requested the transition.

- `plan_crystallized` carries `source`.
- `critique_recorded` carries `source`.
- `plan_approved` carries `source`.

The value is `manual` or `agent`. The field is optional for backward
compatibility. Older sessions decode without the field.

The plan agent is labeled as an agent request in the transcript. A manual
command is labeled as a user slash command.

## Plan revision reuse

The controller reuses the current plan family ID across re-crystallizations.
The Revision counter increments instead of restarting.

Approval persists the current reconstructed PlanID. It does not create a new
unrelated ID. Approval fails loudly when no persisted current revision exists.

## Failure rules

- A malformed transition request returns a named error and does nothing.
- A transition during an active run is rejected.
- An approval with no current plan is rejected.
- An approval with a must-fix critique is rejected.
- Approval with no persisted current revision fails loudly.