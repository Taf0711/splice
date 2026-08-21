# Pi harness integration

Pi is an external harness that consumes a Splice pipeline run. This document
gives the setup steps, the use commands, and the capability limits. The typed
contract between Splice and a harness is in
[HARNESS_INTERFACE.md](HARNESS_INTERFACE.md).

## Parts

The integration has two parts.

| Part | Location | Role |
| `splice-pi-bridge` | `cmd/splice-pi-bridge` | Runs one pipeline. Prints stream-json events. Reads control commands on stdin. |
| Extension | `pi-adapter/splice-bridge.ts` | Spawns the bridge. Reads the events. Shows stage progress in the Pi UI. Sends cancel. |

The bridge is an adapter over the harness seam. It does not change the core.

## Event flow

The bridge emits the stream-json events in
[STREAM_JSON_PROTOCOL.md](STREAM_JSON_PROTOCOL.md). Each run ends with a
`final` event and then a `run_end` event. The `run_end.exitCode` value is
authoritative. Exit codes match `splice exec`: 0 success, 1 error, 2 usage,
3 provider, 4 incomplete, 130 interrupted.

## Setup

Build the bridge binary at the repository root.

```
go build -o splice-pi-bridge ./cmd/splice-pi-bridge
```

Start pi with the extension.

```
pi -e ./pi-adapter/splice-bridge.ts
```

For permanent install, copy `pi-adapter/splice-bridge.ts` into
`~/.pi/agent/extensions/`. The extension finds the binary when pi starts
inside this repository or a subdirectory of it. Set `SPLICE_PI_BRIDGE_BIN`
to the binary path for any other location.

## Use

Type `/splice` with a request. One command runs one pipeline.

```
/splice fix the spelling typo in hello.go
```

The widget above the editor shows the stage roster. The footer status shows
the current stage and its progress. Type `/splice-cancel` to cancel the
active run. A session shutdown also stops the child process.

## Capability limits

Read these limits before you extend the integration.

- Cancel is the only routed control. The capability set declares no approval,
  model, pause, or resume surface. Commands for those surfaces are rejected
  before routing.
- Stage progress comes from stage events alone. The extension does not
  compute total progress from the roster. The orchestrator stays the
  lifecycle authority.
- The shipped path uses the deterministic fixture provider. No live model
  provider is wired. A run without `-fixture` fails with exit code 3.
- Permission requests do not reach the extension. The bridge denies by
  default under the fail-closed permission rule.
- Text deltas, tool calls, and reasoning events arrive on the wire. This
  slice does not render them. Add rendering only with real wire data.

## Tests

The process boundary has deterministic coverage in
`internal/harness/pi/bridge_process_test.go`. The tests spawn the real
binary in fixture mode. They pin event order, terminal state, bad control
input survival, and cancel behavior. Build the binary first, or set
`SPLICE_PI_BRIDGE_BIN`. The tests skip when they cannot find the binary.

A manual smoke check loads the extension through the jiti loader that pi
uses. Run the `/splice` handler against the fixture binary in a scratch
directory. This check needs no model and no terminal UI.
