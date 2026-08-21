# Harness interface contract

This document records the typed interface contract for external harnesses.
It is the design record for the harness checkpoint (TP4). It compares two
interface shapes, records the selected design, and names what the contract can
and cannot represent.

The contract is a seam, not a plugin system. It exists so one external harness
can consume the same projection, surfaces, approvals, logs, and status that the
TUI and `splice exec` use. Do not add a second producer or a new renderer
before two real adapters need the seam.

## Goals

A harness must be able to:

- receive the same typed run events the TUI receives;
- issue typed control commands to the running agent;
- declare what it can do, without claiming authority it does not have;
- reuse the existing projection and lifecycle truth.

## Three contract types

The contract has three types.

| Type | Direction | Purpose |
| `RunEvent` | Agent to harness | One typed run lifecycle event |
| `ControlCommand` | Harness to agent | One typed control request |
| `CapabilitySet` | Harness to agent | What the harness can do |

## Interface shape comparison

### Shape A: typed envelope over existing callbacks

Shape A defines `RunEvent` as a typed envelope that wraps the existing typed
agent events. The producer is the orchestrator, which already emits
`PipelinePlanEvent` and `StageEvent`. The adapter forwards those callbacks into
the envelope. It does not re-derive lifecycle state.

`ControlCommand` is a typed struct with a kind and payload. A pure routing
function maps each command to an existing core control. The core has no
harness-specific branches.

`CapabilitySet` is a declarative struct. A capability says what the harness can
surface (plan projection, approvals, logs, status). It never grants authority.
Authority stays in the permission mode, the sandbox, and the grant store.

The shipped set carries only the bits the command gates read: approvals and
model selection. The design records additional surface capabilities (plan
projection, logs, status, session resume) as future extensions. They are not
shipped until a real adapter needs them.

### Shape B: protocol-equal wire structs

Shape B defines `RunEvent`, `ControlCommand`, and `CapabilitySet` as the wire
format itself. Every consumer, including the TUI and `splice exec`, must emit
and parse the same structs.

Shape B was rejected. It would force the TUI, which is an in-process model, to
speak a wire protocol it does not need. It would duplicate the stream-JSON and
ACP translation layers. It would couple the deterministic pipeline to a wire
contract that changes for presentation reasons.

### Rejection rationale

Shape A is selected because:

- it reuses the existing typed events instead of duplicating them;
- it keeps lifecycle truth in the orchestrator and in `pipelinePresentation`;
- it keeps capability distinct from authority;
- it prefers adapters over core conditionals;
- it is the smallest seam one external harness needs.

## What the contract reuses

| Existing seam | Role in the contract |
| `agent.PipelinePlanEvent` | Stage roster producer |
| `agent.StageEvent` | Stage lifecycle producer |
| `agent.PermissionRequest` and `PermissionDecision` | Approval surface |
| `agent.Usage` | Usage and cost events |
| `stream-json` events | Wire serialization for headless clients |
| ACP (`internal/acp`) | Existing external harness adapter |
| `schemas.RunOutcome` and `InteractionRecord` | Logs and status |
| `pipelinePresentation` | The projection both surfaces share |

## What the contract cannot represent honestly

The contract does not claim to represent:

- token-level streaming deltas (use stream-json for that);
- TUI-only interaction (mouse geometry, focus, overlays, spinner phase);
- permission authority (the grant decision stays in the core);
- session history beyond the run (the session store owns that);
- sandbox internals (floors, seccomp, worktree recovery).

A harness that needs these uses the seam that owns them. The contract does not
duplicate them.

## Adapter rule

An adapter maps the contract to a transport. ACP is one adapter. A JSON stream
is another. The core never imports an adapter. The adapter wraps the existing
`agent.Options` callbacks and routes `ControlCommand` values to the existing
controls.

A wired run with no prior permission responder denies by default. The tool
fails with a permission error. This matches the fail-closed contract of the
permission surface. The run keeps its authority; the harness only surfaces the
request.

Do not add a generic plugin registry. Add an adapter when a real transport
needs one.

The Pi bridge is the first real external consumer of this seam. The bridge
binary maps the seam to stream-json, and a Pi extension consumes that stream.
Setup, use, and capability limits are in [PI_HARNESS.md](PI_HARNESS.md).

## Validation and pairing

`RunEvent` and `ControlCommand` have a `Validate` method. `CapabilitySet`
gates commands with `Requires`, which is its validation role. A pairing test
pins each producer to its consumer. A command that a harness does not declare
in its capability set is rejected before routing.
See `internal/harness/harness_test.go`.
