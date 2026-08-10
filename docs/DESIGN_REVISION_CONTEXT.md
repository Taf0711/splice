# Design Revision Context

This document describes how design agents receive the current design plan and
critique during a revision.

## Goal

A design conversation agent must see the plan and the critique when the user
revises a rejected design. Before this fix, the critique appeared on the
screen but not in the agent request. Re-crystallization also lost that context.

## Sources

The context assembler reads three sources.

- Current epoch conversation history (session events after the latest
  `design_mode_entered`).
- The latest persisted `DesignPlan` (from `plan_crystallized`).
- The latest persisted `PlanCritique` (from `critique_recorded`).

The live TUI state can supply optional plan and critique overlays. The assembler keeps all plan and critique fields.

A resumed session continues its existing design epoch. The first resumed prompt does not create a new epoch.

## Precedence

The assembler uses this precedence.

1. Live overlays win over persisted values.
2. Persisted values are the default.
3. A new `design_mode_entered` event resets the epoch. It excludes earlier
   plans and critiques from the current epoch.

## Consumers

These paths consume the assembled context.

- The live design agent receives conversation messages as prior messages.
- The live design agent receives the plan and critique in the system prompt.
- The design crystallizer receives the plan and critique as structured input.
- The plan critic receives the previous plan and previous critique.

## Failure behavior

A malformed lifecycle event returns a named error. The live turn and crystallization stop. Nothing silently defaults.

If a `critique_recorded` write fails, the live state keeps the critique. The
assembler overlay carries it for the current process. Resume reads persisted
events only.

## Acceptance tests

- A TUI test proves the next provider request contains the full plan.
- The same test checks each must-fix issue and mitigation string.
- The test covers persisted state and a live persistence-failure overlay.
- A resume test proves the first prompt keeps the plan and the critique.
- A workflow test proves a second crystallizer request contains the current
  plan and the current critique.
- An assembler test proves the live overlay wins when persistence failed.
- An assembler test proves a new epoch resets the plan and the critique.
- An assembler test proves malformed lifecycle state returns a named error.
