# EVAL-V0 F — Product Journeys (Golden User Suite)

Deliverable 41.F: five golden user journeys covering the handoff section
28 minimum: fresh setup, TUI task, headless task, session reopen, and
cognition reuse. Each journey is a runnable script plus a machine-checkable
expectation file, so the suite can run on macOS now and Linux once the
bubblewrap CI path (PR #24) is exercised per-release.

## Design constraints (handoff section 28/29)

- Journeys run from PRISTINE state: a temp XDG_STATE_HOME, a temp
  workspace, no residual provider state beyond the test credential.
- Only normal install/readme-level guidance: no maintainer knowledge.
- The verifier is external to the agent: journey scripts assert on
  artifacts (exit codes, session files, memory sidecar contents, stream-json
  output), never on the agent's self-reported success (section 17).
- A journey PASSES only if every expectation holds; any failure is a
  product blocker (section 29).

## The five journeys

### J1 — fresh-install-and-configure
Script: `j1_fresh_setup.sh`
- Fresh XDG_STATE_HOME + empty config; runs `splice doctor`.
- EXPECT: doctor runs to completion (exit 0), reports the runtime,
  config file paths, and provider state WITHOUT leaking any key material
  to stdout; `splice config` exits 0 and its JSON output parses.

### J2 — headless-task-exec
Script: `j2_headless_task.sh`
- In the pristine fixture workspace: `splice exec --output-format
  stream-json "add a healthz handler to the service"` (or equivalent
  fixture task from taskset-v0).
- EXPECT: stream-json output parses line-by-line as valid JSON; a final
  result event with status; the workspace diff contains the change; the
  hidden verifier (taskset check command) passes against the resulting
  workspace.

### J3 — tui-launch-and-task
Script: `j3_tui_launch.sh` (vhs tape driving the real TUI)
- Boots the TUI in a pty (120x40), types a prompt, waits for the pipeline
  strip, quits cleanly.
- EXPECT: the tape completes without a crash; the transcript pane rendered
  the pipeline strip (ASCII tier); the process exits 0; no panic text
  appears in the captured output.

### J4 — session-reopen
Script: `j4_session_reopen.sh`
- J2's session id is captured from the stream-json output; a second
  `splice exec --resume <id>` continues the conversation with a follow-up
  prompt.
- EXPECT: resume exits 0, the stream-json transcript contains the prior
  context (the earlier task's final answer is referenced or the session
  lineage shows two entries via `splice sessions`).

### J5 — cognition-reuse
Script: `j5_cognition_reuse.sh`
- Runs the PRECURSOR task from a cognition family (memory ON), captures
  the sidecar observation; then runs the TARGET task twice: once with the
  seeded memory (warm), once with `SPLICE_EXEC_MEMORY=off` (cold).
- EXPECT: the warm run's stream-json trace records a direct hit
  (`memory_lookup_mode: direct` in the stage meta); the cold run records
  `search` mode; both verifiers pass (warm success >= cold success is the
  §39 gate, asserted across rollouts by the harness, not by one journey).

## Runner contract

`run_journeys.sh [J1|J2|J3|J4|J5|all]`:
- Allocates a fresh temp state root per journey.
- Runs the journey script; captures stdout/stderr/exit code.
- Validates the expectation file (jq-checked JSON assertions).
- Writes `journey-results.json` (one row per journey: status, duration,
  failure classification per section 33: setup failure vs agent failure
  vs infrastructure failure).

## What is intentionally NOT in V0-F

- Real external users (section 29) — needs the automated suite green
  first; recruiting is an owner action.
- Linux matrix — CI sandbox work (PR #24) makes it possible but the
  runner wiring is separate.
- The 30-50 journey scale — V2 per section 44.

## Known dependencies

- J1 needs a provider credential in the environment (the journeys do not
  create accounts; the owner's credential is expected, matching how the
  TUI e2e ran).
- J2/J4/J5 run the real agent and therefore need a provider config. A
  fresh journey state has none by design, and splice exec correctly
  refuses to run without one (exit 3, fail-loud - verified live). The
  scripts accept `SPLICE_JOURNEY_CONFIG` (path to a config.json copied
  into the journey's config dir; secrets never print). Without it they
  exit 3 and the runner records `infrastructure_skipped`: the run is
  visible in journey-results.json but does not fail the suite, because
  the journey never executed. J1 stays fully credential-free by design
  (doctor/config must work pre-setup).
- J3 needs tmux (any pty driver); the tape follows the existing
  demo-repair-loop.tape conventions. The full pipeline-strip assertion
  needs a live provider run and belongs to the per-release live smoke.
- J5 needs the memory sidecar binary built (`splice-memd`).
