# Design revision context

A revision must retain the current plan, critique, and conversation. Otherwise,
the next design response can repeat an issue that the critic already found.

## Design epoch

A design epoch starts when Splice enters design mode. It contains:

- conversation events after that entry;
- the latest plan in that epoch; and
- the latest critique in that epoch.

A resumed session continues its current epoch. The first resumed message does
not create a new epoch.

A new design-mode entry starts a new epoch. Plans and critiques from an older
epoch do not become current state.

## Saved and live state

Splice reconstructs revision context from saved lifecycle events. The active TUI
can also provide a live plan or critique that has not reached storage.

Live state has priority over saved state. Saved state remains the default after
a restart.

This order lets the current process continue after a storage error without
pretending that the unsaved value will survive a restart.

## Consumers

The active design conversation receives the current conversation, plan, and
critique.

A new crystallization request receives the same plan and critique. The critic
also receives the prior revision data that it needs for comparison.

The orchestrator, not one design stage, assembles this context.

## Failure behavior

A malformed lifecycle event stops state reconstruction with a named error.
Splice does not discard the invalid event and continue with partial state.

If Splice cannot save a critique, the current process can retain it as live
state. A later process can reconstruct only the events that reached storage.

## User effect

After a rejected plan, revise the design in the same session and run
`/crystallize` again. The new plan keeps its family identity and increases its
revision number.

Use `/design` when you intend to start a separate design epoch.
