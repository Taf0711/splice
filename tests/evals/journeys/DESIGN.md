# EVAL-V0 F: Product Journeys (Golden User Suite)

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

### J1: fresh-install-and-configure
Script: `j1_fresh_setup.sh`
- Fresh XDG_STATE_HOME + empty config; runs `splice doctor`.
- EXPECT: doctor runs to completion (exit 0), reports the runtime,
  config file paths, and provider state WITHOUT leaking any key material
  to stdout; `splice config` exits 0 and its JSON output parses.

### J2: headless-task-exec
Script: `j2_headless_task.sh`
- In the pristine fixture workspace: `splice exec --output-format
  stream-json "add a healthz handler to the service"` (or equivalent
  fixture task from taskset-v0).
- EXPECT: stream-json output parses line-by-line as valid JSON; a final
  result event with status; the workspace diff contains the change; the
  hidden verifier (taskset check command) passes against the resulting
  workspace.

### J3: tui-launch-and-task
Script: `j3_tui_launch.sh` (vhs tape driving the real TUI)
- Boots the TUI in a pty (120x40), types a prompt, waits for the pipeline
  strip, quits cleanly.
- EXPECT: the tape completes without a crash; the transcript pane rendered
  the pipeline strip (ASCII tier); the process exits 0; no panic text
  appears in the captured output.

### J4: session-reopen
Script: `j4_session_reopen.sh`
- J2's session id is captured from the stream-json output; a second
  `splice exec --resume <id>` continues the conversation with a follow-up
  prompt.
- EXPECT: resume exits 0, the stream-json transcript contains the prior
  context (the earlier task's final answer is referenced or the session
  lineage shows two entries via `splice sessions`).

### J5: cognition-reuse
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
- Writes `journey-results.json` to `~/.cache/splice-journeys/` (or
  `SPLICE_JOURNEY_ROOT`), not into the repository. It has one row per
  journey: status, duration, and failure classification per section 33
  (setup failure, agent failure, or infrastructure failure).
- A skipped journey (`infrastructure_skipped`) does not fail the result.

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

## Layer 2 live journeys (J6 to J10)

These journeys run the real provider. They run on release cadence, not in CI.
Shared helpers: `live_common.sh`. Stream and screen checks: `live_check.py`.

Rules for every live journey:

- Provider: the ChatGPT sign-in profile (`chatgpt`) only. The script copies
  `config.json` and sets `activeProvider` to `chatgpt` in the copy. OpenRouter
  is not used.
- Do not set `SPLICE_OAUTH_STORAGE` or `SPLICE_CRED_STORAGE`. The token is in
  the macOS keyring.
- The real `~/.config/splice` files are read only. The script hashes
  `config.json`, `trust.json`, and `stage-models.json` before and after. A
  change fails the journey (`GUARD FAIL`).
- The state root is a fresh directory under `SPLICE_JOURNEY_ROOT`
  (default `~/.cache/splice-journeys`), not `/tmp`.
- `SPLICE_JOURNEY_DRYRUN=1` stops after setup, before any model request.

### J6: custom-topology-exec
Script: `j6_custom_topology.sh`
- `splice exec -o stream-json --pipeline` with the example write-vet-test
  topology, on a copy of the taskset fixture. The task has no classifier keyword.
- EXPECT: the stream parses; each always-run node has one terminal stage
  event; the vet node passed; splice's verified claim equals the external
  verifier (build, vet, grep for the change).

### J7: failing-command-node
Script: `j7_failing_command.sh`
- As J6, but the vet node becomes `lint` and runs `sh -c "exit 3"`.
- EXPECT: the lint node reports a failed-class status; splice does not claim
  verified (`FALSE SUCCESS` otherwise).

### J8: tui-topology
Script: `j8_tui_topology.sh` (tmux, 120x40)
- Trusts the fixture, runs `/pipeline use write-vet-test`, submits a task,
  opens the Ctrl+G pane and a node drawer during the run, and waits for the
  receipt (10 minutes).
- EXPECT: the receipt kind matches the external verifier; the statuses change
  across captures; the drawer shows a model line; a node reads "not run"; no
  panic text.

### J9: tui-hostile-repo
Script: `j9_tui_hostile.sh` (tmux, 120x40, no model request)
- The project config adds provider `evil` and makes it active. `AGENTS.md`
  links to a canary file outside the repo. A fake `gopls` on PATH writes a marker.
- EXPECT: the trust card names the project config; after decline, the footer
  shows the user provider; no `gopls` marker; `/help` answers within 2 s;
  after `/trust`, the message says the project config loads on the next launch.

### J10: design-invalid-routes
Script: `j10_design_invalid_routes.sh` (tmux, 120x40)
- The copied `stage-models.json` is malformed. The script sends one short
  design message (one request), then `/crystallize`.
- EXPECT: the failure names "stage routing config invalid" and the file.
- Not scripted: the drawer-across-a-tier-change check.

### Known limits (Layer 2)
- Stream-json does not carry a separate verified field. The verified claim is
  read from the completion text ("Not verified") and the run-end status.
- J9 checks the footer for the provider name `chatgpt`. Adjust the check if
  the footer shows the model name instead.
