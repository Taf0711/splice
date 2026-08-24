## Current State

Splice (Go, forked from `gitlawb/zero`) is a public open-source project as of
2026-07-19. The public repo `Taf0711/splice` carries a squashed clean history
built from `4fe8f4c`; the full internal history lives in the private
`Taf0711/splice-internal` (dev repo `origin`) plus a local mirror backup at
`~/Documents/splice-backup-mirror-2026-07-18.git`. **Development moved to the
public repo on 2026-07-27.** All code work now happens on public `dev`.
`splice-internal` is a docs-plus-history archive: it keeps ROADMAP.md,
MEMORY.md, `plans/`, `docs/flug-design/`, and the full pre-publication
history. Its Go tree is frozen and already stale; do not resume code work in
it. Sensitive material still reads and writes here, never in the public repo.
The 2026-07-19 scrub-and-squash publication flow no longer applies (see the
2026-07-27 decision-log entry). `v0.2.0` is released: GitHub Release assets
for 6 platforms plus npm `@taf0711/splice` published via OIDC trusted
publishing (requires Node 24 in the workflow). See the 2026-07-20 decision-log
entry for the Track S security bundle that shipped in this release.

As of 2026-08-23, public `dev` is at `c1dcbe0`. Landed since the last
current-state note: the procrun process chokepoint reached full Tier-1 plus
Tier-2a audit coverage (stages, dtools, bash/exec tools, hooks, imageinput);
the paired-eval harness survives per-run failures and persists pairs
incrementally (`9dcd56c`); the TUI height-cache thrash fix and stable-width
counters merged from `tui/perf-hardening` (`c1dcbe0`, measured 7.8x frame
cost reduction on long sessions). The demo-ready wayfinder map decided ticket
#9 (two takes: site video plus README repair loop, both in splice-demo) and
#10 (quickstart leads with `splice exec`). The first two paired-eval runs are
logged in `plans/paired-eval-run-log-2026-08-23.md`; run 3 awaits the
trace-join telemetry fix.

Standing owner directive (2026-08-23): Splice is developed on this PC,
always. Local-first holds for workflow as well as product: all sessions,
subagents, eval runs, and fixtures live on this machine, and no development
work moves to hosted or cloud agent environments.

Later 2026-08-23 state: Eval v2 is fully drafted under `plans/eval-v2/`.
It uses a sealed private holdout, frozen training-only memory and selector,
schema-identical empty/placebo/relevant arms, repeated counterbalanced trials,
complete telemetry, explicit hidden-root denial, canonical token accounting,
locked stopping rules, and fixed-task paired-block intervals. The current
12-task suite is Development only. Run 7's 43.2 percent headline is
quarantined and cannot resolve ticket #7 or support product copy. No paid
claim run is authorized. Public `dev` is clean at `f6c2bf7`. The complete
memory-reasoning contract is in `8a4f9ae`, `e9c0bd8`, and `4f2e189`, with
review repairs in `edad633`. CI run `32675528049` passed both jobs.

As of 2026-08-16, public `dev` is at `f265ce9`. MVP Waves 1 through 3 are
complete. Real-provider TUI, plain exec, and worktree merge-back passed with
OpenRouter Kimi K3. Public `f274921` fixed the invalid full-context output cap.
Public `ee5a404` fixed CR5. Public `901d99f` completed SD15. Public `7fbda8a`
completed SD14. Public `ac07c49` completed SD16. Public `bf2809d` completed
MD3. Public `4d69ea2` completed SD17. Public `8cf01bd` completed SD11. Public
`fa1727a` completed SD12. Public `5f0cdf4` completed SD1. Public `b316ee5`
completed SD5. Public `4c90115` completed SD2. CI runs `31821825233`,
`31826231922`, `31858201582`, `31860137850`, `31861993067`, `31862382229`,
`31863728901`, `31898960035`, `31919004721`, `31919055111`, and `31919423164`
passed. Approved Batch A, Batch A2, Batch B, and Batch C are complete.

Tracks: F-Zero complete through F16 (pipeline, orchestrator, trajectory
monitor, memory sidecar, eval harness, stage events, TUI pipeline swap,
per-stage model routing, deterministic verification F14, design phase
F15 D0-D6, memory UI F16). TUI/workflow redesign CP1-CP4 (tier resolver,
onboarding pipeline step, planning-default entry, persistent plan panel)
and CP6 (Crystallize separated into its own typed agent) complete; CP6a
(2026-07-19) fixed the setup wizard pipeline-step model picker to use a
search + filtered list instead of blind Left/Right cycling, making all
four model-picking surfaces consistent. Track R (gosec, SARIF layer,
trivy) hardened the deterministic security floor. Ponytail-audit tracks
AU1-AU12 removed ~950 lines of dead or over-engineered code, all CI-green.
F11b (public docs hygiene) started 2026-07-19: public `Taf0711/splice` no
longer carries ROADMAP.md, docs/flug-design/, or flug/; public AGENTS.md
is now contributor-facing (internal keeps the full process doc).

Track PE is complete through PE7b on public `dev` at commit `1fbedd9`.
Typed routing, request attribution, priced ledger coverage, the incremental
eval collector, sample-derived accounting, benchmark v2, the built-in pipeline
runner, persisted usage estimates, honest TUI session cost, and the public eval
and protocol documentation are all live. Commit `00d93f2` then made the registry
derive prices for models the curated catalog does not carry, so session cost is
no longer inert on an off-catalog configuration. Only PE7c (full validation plus
manual real-provider acceptance) remains. The next release is v0.1.3 through a
one-shot `Release-As:` footer, because 0.2.0 is reserved for Tracks T/W/A.

## Open Questions

- Track S (security hardening) is planned in ROADMAP.md after the 2026-07-19
  audit. S1 (workspace trust UX/storage) and S2b (DenyRead default list) are
  `@needs-human` design decisions.

- Whether the stream-JSON protocol gets a first-class `stage` event type or
  keeps stage lifecycle on `reasoning` events. The dead `splice run --json`
  envelope family (RunEventType + six event structs) was deleted in AU12;
  stage lifecycle stays on reasoning markers unless a first-class stage event
  is designed fresh against `streamjson.Event`.

- Whether trivial-tier runs need an optional cheap verification pass. The
  first live Kimi K3 write changed an unrelated output string despite an
  explicit prohibition. Trivial tier runs only `code_writer`, so this drift
  shipped unverified by design. Decide whether that is an accepted product
  tradeoff before changing the tier roster.

## Next Steps

- **Memory-reasoning contract complete and reviewed.** Normal and repair
  retrieval share one preparation path; reviews are invocation-ordered;
  reconciliation is non-blocking; trace counters are post-compaction and
  cumulative across repair invocations; raw memory stays out of reviews.
  Review also fixed `dropOldestChangedFiles`. CI run `32675528049` is green.
- **Review and approve Eval v2 before code or paid execution.** Source:
  `plans/eval-v2/`. Owner decisions are the success non-inferiority margin,
  minimum useful relevant-versus-empty and relevant-versus-placebo token gains,
  separate safety limits, task distribution, model route, pilot scope, and
  spend caps. EV2-0 starts only after approval. EV2-10 is a second explicit
  owner gate for the locked preregistration and exact claim-run budget.
- **Do not use the current 12-task suite for a claim.** It remains useful for
  development and instrument smoke. Do not report Run 7's 43.2 percent as
  efficacy evidence. Frozen efficacy, stale/conflict safety, and online
  learning stay separate protocols.
- **Approved Batch A, Batch A2, Batch B, and Batch C are complete.** SD15,
  SD14, SD16, MD3, SD17, SD11, SD12, SD1, SD5, and SD2 are on `dev`. Do not
  start excluded work until the owner names the next checkpoint.
- **Do not start excluded work.** SD3, SD4, SD13, SD18, Tracks T, PC, LN, and
  TW, and all release actions require separate owner approval.
- **PE7c is the last PE checkpoint, and only its manual half remains.**
  PE7b landed in `1fbedd9`. The automated validation gate is satisfied by real
  CI: run `30301959112` passed both jobs on `00d93f2`, the current `dev` head,
  and that workflow runs the whole gate with `-race`. `git diff --check` is
  clean locally. What is left is the manual real-provider acceptance, which
  needs credentials and must stay out of CI: `splice eval validate`, then
  `eval bench` with `--json`, with `--csv-output`, and with `--agent-command`.
  Then confirm the plan's acceptance list, above all that request estimates sum
  to pipeline and task estimates and that unknown pricing shows partial or
  unavailable coverage.
- **v0.1.3 is the next release.** Cut it with a one-shot `Release-As: 0.1.3`
  commit footer, never the sticky `release-as` config key. 0.2.0 belongs to
  Tracks T/W/A.
- **v0.1.3 item 1 is FIXED in public `c1f3b78` and `887c97b` (pushed).**
  `c1f3b78` makes the crystallize handler at
  `internal/tui/model.go:2257` read `plan.Validate()` to tell a crystallizer
  failure from a critic failure, and keep a valid plan as the pending plan.
  `887c97b` corrects that commit: it cleared the critique for every error, but
  only the `critique_recorded` write failure returns a full critique, so a
  must-fix verdict was discarded and `/approve` stopped being blocked. The
  handler now keeps a critique that passes `critique.Validate()`. Ten paths
  through the handler have tests, plus a resume test. Every new case fails
  with the handler change reverted.
- **v0.1.3 item 2 is FIXED in public `c63edd0` (pushed).** `Update` skips the
  `chatLayoutGen` bump for `tea.MouseWheelMsg` only. Eleven lines. Measured 34%
  faster at 200 transcript rows and 51% faster at 20000. See the Decision Log
  entry "the scroll fix that had to be measured twice".
- **v0.1.3 item 3 is DONE and pushed. All of items 1, 2 and 3 are done, and
  the next step is item 4, the release itself.** Item 3 became Track MP in
  `ROADMAP.md`: `5d6e762` tiered pricing, `eee9f67` the cheapest-tier
  compaction cap, `8b3ad05` the embedded snapshot that replaces all 18
  `ModelCost` literals, `6bad51a` the partial-coverage display, and `1dcdf3a`
  the CI hardening. CI run `30391208668` passed both jobs on `1dcdf3a`, the
  current `dev` head. The non-hermetic `TestDefaultRegistryRealCachedSnapshot`
  was fixed first, in `887b179`. See the Decision Log entry "item 3, and the
  price that was never ours to keep".
- **`main` is 49 commits behind `dev`, and item 3 changed how pricing resolves
  at depth.** The manual real-provider acceptance matters more than usual for
  this release. Run `eval bench --json` above all, because partial coverage now
  fills `estimated_cost_usd` where the field used to be null.
- **Tracks T/W/A planned (2026-07-23, v0.3 runway).** Full plan:
  `plans/configurable-pipeline-and-review-surfaces-2026-07-23.md`. First
  checkpoint on user go-ahead: T1 (topology schema). Five `@needs-human`
  open questions have defaults recorded in plan section 11.
- **v0.1.2 RELEASED (2026-07-20).** Track S security bundle shipped to
  public. GitHub Release v0.1.2 has 12 assets (6 platforms + SHA-256),
  `@taf0711/splice@0.1.2` published to npm via OIDC. Release notes carry the
  detailed Track S security section. Public PR #9 removed the
  `release-as: 0.1.2` override and resumed normal semver. It MERGED
  2026-07-21. `release-please-config.json` on `dev` carries no `release-as`
  key, and the manifest is at `0.1.2`. No release PR is open now.
- Track DG (design-phase diagrams): DG0 (prompt-only ASCII diagrams in the
  design conversation) and DG1 (deterministic task-graph renderer in the
  plan panel) landed 2026-07-21. DG3 (crystallizer-persisted diagrams,
  `@needs-human`, AU5 re-add) is the remaining checkpoint.
- **RESOLVED (WG1, 2026-07-26).** `TestRunHonorsMaxTurnsAsIterationCap` was not
  a test-environment quirk; root cause was `internal/splice/dtools/resolve.go`
  resolving the target through symlinks but never the root. See Decision Log.
- **Track WG COMPLETE (2026-07-26).** All nine wiring-gap checkpoints from
  the dependency-graph audit landed (see Decision Log for the full
  per-checkpoint record). Headline fix: WG1's dtools workspace-root symlink
  resolver, which had silently disabled the entire deterministic security
  floor (gosec/bandit/sarif) on any symlinked workspace path and was the
  root cause of the standing macOS test failure.
- **GitHub Actions CI is GREEN again (verified 2026-07-27).** The
  2026-07-26 stop is over. CI ran from `2026-07-26T22:44Z` on `dev` and every
  run since has succeeded. Two things changed together: the billing block
  cleared, and `ci: trigger on push to dev, not just main` made `dev` pushes
  start a run at all. The accumulated WG6-WG9 commits got their real CI
  confirmation through the "Sync: catch up 24 commits" run. The local
  substitute gate is no longer necessary.
  The workflow runs the full gate on every `dev` push: `gofmt -l .`,
  `go vet ./...`, `GOOS=windows go vet ./internal/memd/...`,
  `go test -race ./...` (whole repo), `go build ./cmd/splice` plus
  Windows/Linux cross-builds, and the separate `memd` job (`go vet`,
  `go test -race`, `go build -o splice-memd .`).
  Both jobs passed on `00d93f2`, the current `dev` head, in 4m34s
  (run `30301959112`). `go test -race` is stronger than the PE plan's
  `go test -count=1`, so PE7c needs no separate local test run.
- **Upstream divergence recorded (WG5).** `internal/reasoning/` and
  `internal/reltime/` are deleted from this repo but still exist in
  upstream Zero (`gitlawb/zero`, PRs #338 and #315). A future
  `git merge upstream/main` (or equivalent) will hit a trivial "deleted by
  us, modified by them" (or clean re-add) conflict on those paths. Resolve
  by keeping the deletion - re-verify zero importers first if upstream has
  since made either package load-bearing, which is unlikely given neither
  was ever imported by this repo's own code.
- CP5 (LLM security advisor) stays tabled behind its eval contract
  (seeded-vulnerability corpus plus precision/recall thresholds); Track R
  landed the deterministic alternative.
- CP3's deferred `pipelinePromoted` toggle: re-add only if a real pain
  point surfaces.
- `ZERO_` to `SPLICE_` env-var rename (mechanical, deferred).
- Public release flow: **v0.1.2 SHIPPED.** Public PRs #5 (S0 npm name), #6
  (S1a-S4 port + CHANGELOG), #7 (patch override), and #8 (release-please
  staging) all merged 2026-07-20. Track S security-critical work is complete.
  The flow that worked, and that v0.1.3 repeats: merge the feature PRs, let
  the release-please staging PR tag the version, then dispatch
  `release-artifacts.yml` manually for the tag, because GITHUB_TOKEN does not
  chain the release event.
- Open npm question: whether to move package ownership to a
  non-identifying account (current metadata shows the personal account).
- Test gaps: TUI pipeline swap end-to-end, merge-back cancellation,
  real-provider pipeline integration (env-gated). The
  `TestRealMemdSidecarMemoryRetrieval` integration test can hang on zombie
  memd sockets; run with `-skip` or `pkill -f splice-memd` first.

### 2026-07-20: S0 landed (npm package name fix)

- Delegated S0 to a deepseek-v4-flash worker (clear-spec code-patch lane):
  `internal/update/installmethod.go:14` `@gitlawb/splice` -> `@taf0711/splice`
  + the one test asserting the value. No other live `@gitlawb/splice` refs in
  `.go`. Parent ran the gate (gofmt/vet/test green), committed `3a7ecac`,
  internal CI green. Ported to public via `git format-patch` as PR #5
  (`fix/update-npm-package-name`).
- Release strategy changed: the user is bundling all of Track S plus the
  pending public PRs into a single v0.1.2 release. No release dispatch until
  Track S is complete.

### 2026-07-20: S1a landed (workspace trust gate, closes the Critical)

- Implemented in three delegated slices (S1a-1 trust store/resolver, S1a-2a
  flags/helper, S1a-2b gate wiring) because a single worker blew kimi's 262k
  context (harness reserves ~200k output budget for toolBudget>=55). S1a-1
  used hard=45; S1a-2a/2b split the wiring to fit. Modeled on pi's project
  trust (ancestor lookup, ask/always/never, --trust/--no-trust, skip+warn not
  hard-fail, context files load regardless).
- Gate: `internal/cli/app.go` + `exec.go` resolve trust before
  registerMCPTools/hook-dispatch/plugin-scan. Untrusted -> project MCP stdio
  servers, hooks, plugins skipped; loud warning when any executable-bearing
  project config exists (`projectConfigExists` checks config.json/hooks.json/
  plugins/). Headless defaults untrusted under ask (fail-safe). `--trust`
  persists; `--no-trust` declines one run. `SPLICE_TRUST_WORKSPACE` env.
- Seams: `resolveMCPConfigTrust` sets ProjectConfigPath="" when untrusted;
  `hooks.LoadOptions.DisableProject`; `activatePlugins(...,trusted)`;
  `tui.Options.Trusted` (project commands skipped). Backward-compatible
  wrappers for tests injecting the legacy resolver.
- One inline fix: 5 tui user-command tests needed `Trusted: true` (they set
  up project commands). One inline fix: `projectConfigExists` expanded to
  check hooks.json + plugins/ too (fail-loud for hooks-only workspaces).
- Validation: gofmt/vet/build clean, cli+config+tui green, full root suite
  green in CI. Commits 36a96ff, 2bbf31c, 2e19a71, 3e5386f.
- S1b (interactive TUI prompt + /trust command) is UX, not security-critical;
  deferred. The Critical is closed by S1a alone (untrusted = skip+warn).

### 2026-07-20: S2a, S3, S4 landed (rest of the security-critical track)

- **S2a** (env scrubbing, afd904d): `secrets.ScrubChildEnv` strips credential
  env vars at all 5 child-spawn sites. Closes the env-exfil High.
- **S3** (sandbox hardening, 54036e1): TooComplex commands force a prompt;
  safe-git classifier rejects --git-dir/--work-tree/-c.
- **S4** (medium cluster, 3c9d266): dtools EvalSymlinks (deduped resolve.go),
  MCP http:// warning, seccomp fail-closed.
- All CI-green. The security-critical Track S work is complete. Remaining:
  S1b (interactive TUI trust prompt + /trust command, UX only) and S2b
  (DenyRead defaults, @needs-human, defense-in-depth only since redaction
  already covers the exfil path). Two items deferred for design judgment:
  OAuth encrypted-default (migration needed) and update http:// ban
  (loopback dev case).
- Next: port S1a-S4 .go changes to the public repo (format-patch), bundle
  with the pending public PRs (picker, docs, S0 npm-name #5) into v0.1.2.

### 2026-07-20: S1a-2c + end-to-end verification

- Closed the last gate gap: `splice mcp tools list` (extensions.go) was passing
  `projectTrusted=true` to registerMCPToolsForWorkspace, so project MCP stdio
  servers still spawned in an untrusted workspace on that code path. Now
  resolves trust like app.go/exec.go. Commit cd756da.
- E2E-verified the whole gate with a built binary against a malicious test
  workspace (`.splice/config.json` MCP stdio `sh -c 'touch RCE_PROOF'` +
  `.splice/hooks.json` beforeTool hook):
  - Untrusted (no flag, fresh store): warning printed, RCE_PROOF absent
    (spawn blocked).
  - `--trust`: no warning, RCE_PROOF created (spawn allowed), trust.json
    persisted trusted:true.
  - After persisted trust, no flag: loaded from store (no warning, spawned).
  - `--no-trust`: warning printed, spawn blocked, nothing persisted.
  - `defaultProjectTrust=always`: trusted, spawned.
- E2E-verified S2a env scrubbing: ScrubChildEnv strips OPENAI_API_KEY,
  ANTHROPIC_API_KEY, AWS_SECRET_ACCESS_KEY, and _API_KEY-suffixed vars;
  harmless vars kept.
- All CI-green. The gate is wired consistently across app.go, exec.go, and
  extensions.go. The TUI path uses skip+warn (S1b adds the interactive prompt).

### 2026-07-20: v0.1.2 released (Track S security bundle shipped)

- Public flow: PR #5 (S0 npm name) + PR #6 (S1a-S4 port + CHANGELOG) merged
  to public main. PR #7 (remove stale `release-as:0.1.1`, set one-time
  `release-as:0.1.2`) merged to unblock release-please (the stale 0.1.1
  override was no-op'ing every run since the v0.1.1 tag existed). release-please
  opened staging PR #8; I restored the detailed Track S changelog in the
  staging branch (release-please had overwritten it with a thin auto-generated
  entry from the 2 squash-merged commits). PR #8 merged -> v0.1.2 tag cut.
- Binaries: `release-artifacts.yml` dispatched manually for v0.1.2 (GITHUB_TOKEN
  doesn't chain the release event). 6 platform builds (linux/macos/windows x
  x64/arm64) + splice-memd sidecar + SHA-256 checksums uploaded; all green.
- npm: `@taf0711/splice@0.1.2` published via OIDC trusted publishing (Node 24).
- Release notes edited to the detailed Track S security section (release-please
  had regenerated the thin version on merge).
- Cleanup: PR #9 removed `release-as:0.1.2` (sticky; would pin every
  future release to 0.1.2 until removed). MERGED 2026-07-21, so this is done.
- Lesson for next release: squash-merge collapses conventional-commit history,
  so release-please can only generate thin 2-line changelogs. Either (a) use
  merge commits (not squash) to preserve the per-checkpoint commit messages,
  or (b) keep hand-editing the staging branch CHANGELOG as done here. The
  detailed changelog lives on main either way.

## Decision Log

### 2026-07-19: full security audit + adversarial triage (Track S planned)

- Ran a read-only audit of the whole repo: 8 parallel surface reviewers
  (sandbox engine, file tools/platform helpers, credentials/redaction,
  network/update/supply chain, pipeline/dtools, extensibility/MCP/hooks,
  local IPC/sessions, memd sidecar) + `govulncheck -mode=source ./...`
  (clean, 0 known vulns) + parent verification of every Critical/High claim
  against the code. Then an adversarial loop: 5 fresh-context reviewers
  attacked every finding with a default hypothesis that it was wrong.
- **What survived triage** (full verdict table in conversation; checkpoint
  plan in ROADMAP.md Track S):
  - `npmPackageName = "@gitlawb/splice"` vs published `@taf0711/splice`
    (installmethod.go:14): `applyNpmUpdate` would install a package the
    maintainer does not own. One-line fix, most urgent (S0).
  - Project MCP stdio servers spawn at startup with no consent and merged
    env (app.go -> mcp/registry.go:84 -> client.go:56). Confirmed Critical.
  - Project hooks/plugin hooks auto-run on beforeTool/afterTool with
    `os.Environ()`, no trust gate (sessionStart is never dispatched, so the
    original Critical framing downgraded to High).
  - Native sandbox is read-all/write-jail by design on all platforms
    (Landlock/bwrap/Seatbelt all read `/`); shell-string paths are never
    analyzed, so `cat ~/.ssh/id_rsa` is auto-allowed.
  - Env-based credentials (exported `OPENAI_API_KEY` etc.) are inherited by
    all child processes; config-file keys are NOT in the environment.
  - Mediums: TooComplex auto-allow, prefix-grant flag abuse +
    require_escalated, MCP http://, plugin-skill prompt injection, dtools
    symlink escape + unsandboxed spawns, keyring `-w` argv, env-controlled
    update origin with same-origin checksum.
- **Debunked by the adversarial pass (no action):** exec_command redaction
  (registry-level scrubResultSecrets covers it, registry.go:221), SARIF dtool
  LLM-reachability (never advertised to the model), npm postinstall (HTTPS +
  fail-closed), memd peer-auth/PATH/tmpdir (no marginal same-user risk),
  write-tool TOCTOU/hardlink (active same-machine adversary, out of scope),
  helper cwd resolution (exe-dir then PATH, never cwd), cross-project memory
  leakage (SQL filters by project).
- Accepted as Low/deferred: OAuth plaintext default (flip to encrypted-file
  in S4), OpenRouter missing `state` (PKCE makes it defense-in-depth),
  plaintext user messages in events.jsonl (0600, shell-history precedent).
- No code changed. Track S (S0-S4) added to ROADMAP.md; S1 and S2b are
  `@needs-human`.

### 2026-07-19: public docs hygiene (strip internal-only material from public repo)

- The public `Taf0711/splice` repo still carried internal development records
  that don't belong in a public OSS project. Opened as a separate `docs:` PR
  (branch `docs/public-hygiene`, commit `d32bf81`) independent of the picker fix.
- **Removed from public:** `ROADMAP.md` (internal checkpoint plan/decision
  rationale), `docs/flug-design/` (archived Python-era corpus referencing
  internal plans/MEMORY), `flug/` (top-level dir named after the archived
  internal prototype, held only `evals/model-presets.json`).
- **Rewrote public `AGENTS.md`** from the internal AI-development process doc
  (planner/implementer roles, checkpoint cadence, MEMORY/ROADMAP/plans workflow,
  resume-and-delegate, "user is using Splice to learn") into a contributor-
  facing guide: what Splice is, the 8 core architectural commitments, style/
  conventions, upstream discipline, file layout, build/test, docs map,
  contributing, model-agnostic note. The internal `AGENTS.md` is unchanged and
  remains the full process doc (the two now diverge by design: internal serves
  the maintainer + AI assistants, public serves outside contributors).
- **Trimmed public `CLAUDE.md`** to a clean pointer (no MEMORY/ROADMAP refs).
- **Fixed dangling refs** in `README.md` / `README_ZH.md` (dropped the
  `docs/flug-design/` reference).
- **De-pathed 3 `.go` comments** that referenced `docs/flug-design/10-...md`
  and `plans/design-phase-tui-wiring-...md`. Reworded to be path-free so the
  `.go` trees stay byte-identical between repos. Applied the same rewording to
  internal (`c1d0886`) to keep future format-patch ports conflict-free; internal
  keeps its `docs/flug-design/` and `plans/`, the comments just no longer link.
- Validated: no dangling refs anywhere in the public tree, no em-dashes in new
  text, gofmt/vet/build clean. -4710/+135 lines across 20 files.
- Workflow note: this is the F11b docs-refresh work the ROADMAP had open. Two
  public PRs now pending (picker `fix:` + docs `docs:`), independent, merge in
  any order. Release-please keys off the `fix:` for v0.1.2; the `docs:` does not
  trigger a release.

### 2026-07-19: setup wizard pipeline-step model picker (search + filtered list)

- UX bug: the Setup wizard's `setupStagePipeline` step used blind Left/Right
  model cycling (no visible option list, no search), while the immediately
  preceding `setupStageModel` step, the harness `/model` command, and the
  `/stages` wizard all use a type-to-search filtered list. It was the only
  model-picking surface in the TUI that blind-cycled.
- Fix: overview + drill-in picker. Overview (4-stage "Your pipeline" view,
  unchanged) Up/Down moves stage focus; Right opens the focused stage's model
  picker; typing in overview auto-opens the picker seeded with the char
  (matches `setupStageModel`). The picker (new) renders a search box +
  filtered list of `pipelineOptions[stage]` via the existing
  `filterStageModelOptions` helper, with Up/Down, type-to-filter, Backspace,
  Ctrl+U, Enter-commits-and-returns, Esc/Left-returns-without-commit.
  `cycleSetupPipelineModel` is deleted; `setupPipelineLeftAtTop` is simplified
  (no more cycling mutation). All four model-picking surfaces now behave the
  same way.
- Files: `internal/tui/onboarding.go` (+10 funcs, 6 modified, 1 deleted),
  `internal/tui/onboarding_test.go` (-1 blind-cycling test, +6 picker tests).
  No new schema, no protocol change, no headless-exec change. Reuses existing
  helpers (`filterStageModelOptions`, `selectableListStart`,
  `setupModelMaxVisible`, `setupOptionIndex`, `setupModelRow`/`setupModelSearchLine`
  patterns).
- Implemented by a fresh-context `openrouter/moonshotai/kimi-k2.7-code` worker
  (custom `impl-worker` agent, file/bash tools only). Delegation required two
  infra unblocks (see notes); once unblocked, the worker landed the full impl
  + tests cleanly in one pass (50 turns, hit soft wrap-up, returned a sound
  diff). Parent reviewed the diff inline.
- First ongoing `.go` change since the public squash. Ported to public via
  `git format-patch` (`.go`-only, explicit paths) on a branch + PR (not
  direct-to-main) to prove the port workflow with CI gating. MEMORY/ROADMAP
  stay internal-only (scrubbed from public).
- Delegation infra notes (session-local, NOT in the repo): the pi-subagents
  `intercomBridge` (mode `always`) injects the `intercom` tool into every
  child's required-tools list, but children register supervisor tools with
  `includeIntercomFallback: false` so `intercom` never loads there, breaking
  ALL delegation with "requested unavailable child tools: intercom". Two
  session-local fixes applied (both outside the repo, reversible): (1) wrote
  `~/.pi/agent/extensions/subagent/config.json` with `intercomBridge.mode:
  "off"` (takes effect next Pi restart; not active this session since the
  extension caches config at init); (2) patched
  `pi-subagents/src/runs/shared/subagent-prompt-runtime.ts` line 355
  `includeIntercomFallback: false` -> `true` so the session_start diagnostic
  sees `intercom` (active this session; cleared the jiti cache to force
  re-transpile). Lost on `npm` reinstall. Also: openrouter models whose
  namespace collides with a direct provider adapter (e.g. `moonshotai/...`)
  must be qualified as `openrouter/moonshotai/<model>` for the child, else Pi
  tries the direct provider and fails with "No API key found".
- Validation: no Go toolchain on this machine (CI is the gate per AGENTS.md).
  Parent self-reviewed the diff (gofmt struct alignment verified by hand, no
  em-dashes in new code, no leftover refs). CI runs build/vet/test.

### 2026-07-19: repository went public (rename + orphan-squash strategy)

- The full internal history stays private in `Taf0711/splice-internal`
  (renamed; all branches, tags, PR refs intact; dev repo `origin` re-pointed
  to it). The public `Taf0711/splice` was recreated from a single squashed
  commit of the scrubbed tree (no PR refs, no tags, no internal history).
- Scrub: MEMORY.md/plans/docs/audits removed from the public tree (kept
  locally); AGENTS.md/CLAUDE.md/.cursorrules/ROADMAP.md annotated
  "maintainer-local"; stale docs fixed; CHANGELOG reset; personal archive
  name genericized; zero .go changes (proven file-by-file vs 4fe8f4c).
- Adversarial audit caught 5 blockers pre-flight: npm 0.1.0 unpublishable-
  then-unrepublishable (deprecated instead, released as 0.1.1), OIDC publish
  already broken (setup-node registry-url short-circuit), GITHUB_TOKEN not
  chaining release events, smoke test needing public repo, release-please
  config invalid for orphan history (bootstrap-sha, manifest, release-type).
- Runtime fix: npm trusted publishing needs Node 24 (npm CLI 11.5.1+);
  Node 22 bundles npm 10.x and fails ENEEDAUTH.
- Released v0.1.1 on the public repo: 12 assets (6 platforms + checksums),
  npm publish via OIDC trusted publishing green, install smoke passed.
- Backup mirror at ~/Documents/splice-backup-mirror-2026-07-18.git.
- Local workflow note: `origin` = splice-internal (private archive);
  `public` = the public repo. Never push internal main to `public`.

### 2026-07-18: CP6 — design-phase Crystallize separation

- Implemented the last deferred checkpoint of the TUI/workflow redesign
  (`plans/tui-workflow-redesign-2026-07-16.md`). Pulled `Crystallize` out of
  the `DesignConversation` struct into its own typed agent `DesignCrystallizer`.
- **What moved:** `DesignCrystallizer` struct + `Crystallize` method +
  `designPlanToolDefinition` + `parseDesignPlan`/`decodeDesignPlan` moved to a
  new file `internal/splice/stages/design_crystallizer.go`. The crystallize
  prompt was renamed `design_conversation_crystallize.md` ->
  `design_crystallizer.md` (git mv) and fixed: it referenced
  `open_questions`/`sequence_diagrams`/`wireframes` deleted in AU5/AU6, so the
  model was told to fill fields the `submit_design_plan` tool does not accept.
  Replaced with honest guidance (record unresolved items as out_of_scope or as
  task intents).
- **What stayed:** `design_conversation.go` keeps only the chat prompt
  accessor `DesignConversationPrompt()` + its embedded prompt. The
  `DesignConversation` struct is deleted (it had only `Crystallize`).
- **Routing:** `DesignWorkflow.CrystallizeAndCritique` now calls
  `stages.DesignCrystallizer{}.Crystallize(...)` and resolves under stage name
  `design_crystallize` (was `design_conversation`). `stageTierLabels` renamed
  the entry; `reservedInactiveStageNames` renamed its entry. Onboarding
  enumerates via `StageTierLabels()`, so the rename flows automatically.
- **Architectural decision:** kept the typed `Crystallize` signature
  `(DesignCrystallizer) Crystallize(ctx, provider, opts, DesignConversationInput)
  (DesignPlan, error)` rather than making it a full `Stage` (the `Run`/
  `HarnessStageInput`/`HarnessStageOutput` interface). `PlanCritic` is a full
  `Stage` but it shoehorns design-phase I/O through `options.Plan` +
  `output.Data["plan_critic_output"]`; forcing `Crystallize` into the same
  shape would require adding a `History` field to `StageOptions` (execution-
  phase harness) for a design-phase concern. The future topology editor will
  redefine stage contracts anyway (ROADMAP future-direction note), so `Stage`
  conformance now is premature. The goal (own struct + own stage name =
  topology-node-ready + independent routing) is met without the contract churn.
- **Tier preserved** as `medium` (no semantic change; crystallization was
  `medium` before, stays `medium`). A future tier bump to `reasoning` (matching
  `plan_critic`) is a separate behavior-change decision, not taken here.
- **Clean rename, no legacy alias:** `design_conversation` -> `design_crystallize`
  everywhere (tool unreleased per F11a; no existing user configs to migrate).
  An old `stage-models.json` with a `design_conversation` entry would surface
  as an inert unknown row in `/stages` (honest: the stage was renamed).
- **Zero LLM tokens** (structural move + mechanical test renames + prompt fix).
- **Validation:** gofmt clean, vet clean, build clean, focused packages green
  (splice/stages, splice, tui, cli), full root suite green
  (-skip TestRealMemdSidecarMemoryRetrieval, 0 FAILs).
- The TUI/workflow redesign (CP1-CP6) is complete. CP5 (LLM security advisor)
  remains the only deferred checkpoint, tabled behind its eval contract.

### 2026-07-18: ponytail-audit M3, dead run-event schema family (AU12)

Deleted the six run-event envelope types and `RunEventType` (~90 lines + tests)
from `internal/splice/schemas/events.go`: `RunStartedEvent`, `RunWarningEvent`,
`StageEvent`, `ChangeSummaryEvent`, `RunCompletedEvent`, `RunFailedEvent`, and
`RunEventType` with its six constants. These described the never-built
`splice run --json` protocol from the F2 Python-era port. The shipped protocol
remains stream-json with `\x00STAGE` markers (F12a). `ChangeSummary` and
`ChangedFile` are kept (live in `run.go` trajectory monitoring). Removed
orphaned `errors` import; `fmt` stays (`ChangeSummary.Validate` uses it).

### 2026-07-18: ponytail-audit finding 2 + #9 (AU11)

Triage table (parent triage, final):

| Function | Decision | Destination |
|---|---|---|
| `renderSelectableList` | DELETE | (selectable_list_test.go deleted with it) |
| `transcriptBody` | MOVE | transcript_selection_test.go |
| `transcriptViewportStart` | MOVE | transcript_selection_test.go |
| `listCommandNames` | MOVE | commands_test.go |
| `renderMarkdownInline` | MOVE | assistant_markdown_test.go (new) |
| `overlayMouseTop` | MOVE | mouse_test.go |
| `retainedCharacters` | MOVE | render_cache_test.go |
| `completePathQuery` | REROUTE | test call sites -> `completePathQueryWithTrailingSpace(..., true)` |
| `formatCommandHelpLines` | REROUTE | test call sites -> `formatGroupedCommandHelpLines()` |
| `chatMaxScrollOffset` | REROUTE | test call sites -> `_, maxOffset := m.chatScrollMetrics()` |
| `toolBodyRendererFor` | REROUTE | test call site -> `defaultToolBodyRegistry.rendererFor(name)` |
| `likelySandboxDenied` | REROUTE | test call sites -> `sandboxDenialKind(plan, exitCode, sections...)` |

Preserved: `selectableListItem`, `selectableListOptions`, `selectableListStart`, `clampInt`, `selectableListAnchorRow`. All moved functions remain unexported. No name collisions.

- **Validation:** `gofmt -l .` empty, `go vet ./internal/tui/ ./internal/tools/` clean, `go build ./...` passes, `go test -count=1 ./internal/tui/ ./internal/tools/` passes.

### 2026-07-18: ponytail-audit findings 8/7/6 (AU10)

- **Finding 8 (spec.go duplicate):** Deleted `firstNonEmptyString` (spec.go:339-347), behaviorally identical to `firstNonEmptyCLI` (provider_setup.go:521). Updated single call site (spec.go:234) to `firstNonEmptyCLI`. Trim-first-and-compare semantics verified identical.
- **Finding 7 (cron_run.go contains):** Deleted hand-rolled `contains` (cron_run.go:91-98). Updated two call sites (cron_run.go:44, cron_run.go:115) to `slices.Contains`.
- **Finding 6 (command_prefix.go equalStringSlices):** Deleted `equalStringSlices` (command_prefix.go:505-514), zero production callers; `slices.Equal` is semantically identical including nil-vs-empty. Updated 10 call sites in loop_test.go (lines 272, 1858, 1990, 2075) and command_prefix_test.go (lines 14, 22, 33, 53, 77, 157) to `slices.Equal`.
- **Validation:** `gofmt -l .` empty, `go vet ./internal/cli/ ./internal/agent/` clean, `go build ./...` passes, `go test -count=1 ./internal/cli/ ./internal/agent/` passes.

### 2026-07-18: ponytail-audit findings 4/5 landed (AU9)

- **Finding 4 (git-output dedupe):** Three identical git-output tails (worktrees.go `gitOutput`, recovery.go `(r) gitOutput`, recovery.go `(r) gitOutputEnv`) extracted into a single `commandOutput(result CommandResult, err error) (string, error)` helper in recovery.go (Splice-owned file). Call sites became one-liners. The non-wrapping `fmt.Errorf("%s", message)` shape is preserved verbatim.
- **Finding 5 (defaultRunGit delegate):** `defaultRunGit` body replaced with `return defaultEnvRunGit(ctx, dir, nil, args...)`. The 4-line stderr-capture comment moved above `defaultEnvRunGit` (the surviving implementation). `mergeEnv(os.Environ(), nil)` preserves env inheritance; pinned by the real-git test suite.
- **Scope:** Only worktrees.go and recovery.go touched. No test files, no TUI, no splice pipeline.
- **Validation:** `gofmt -l .` empty, `go vet ./internal/worktrees/` clean, `go build ./...` passes, `go test -count=1 ./internal/worktrees/` passes (real-git suite).

### 2026-07-18: F11a — release infrastructure

- Set up the release pipeline so Splice's own versioning starts at v0.1.0.
- Deleted stale upstream Zero-era tags (v0.1.0, v0.2.0) from before the rebrand.
- Closed Release Please PR #1 (wanted v1.0.0 — wrong). Bootstrapped manifest
  at 0.0.0 so first feat: commit produces v0.1.0.
- Added `release-artifacts.yml`: cross-compiles splice + splice-memd for 6
  platform/arch combos, creates archives with SHA-256, uploads to release.
  Both pure-Go (CGO_ENABLED=0). Sandbox helpers deferred (platform-specific C).

### 2026-07-18: ponytail-audit findings 3/M1/M2 landed (AU8)

Deleted four zero-reference TUI helpers confirmed by parent-verified grep on current tree:
- `scrollableTranscriptView` (internal/tui/model.go:2926) -- dead wrapper, sibling `scrollableTranscriptLayoutView` live
- `transcriptViewportStartForLayout` (internal/tui/transcript_selection.go:1283) -- sibling `transcriptViewportStartForFrame` live
- `WatchPRState` (internal/tui/pr_status.go:153) -- `WatchPRStateContext` live
- `GetLocalDiffStats` (internal/tui/pr_status.go:241) -- `getLocalDiffStats` live with prod+test callers
Live siblings preserved: `viewLines`, `transcriptViewportStartForFrame`, `getLocalDiffStats`, `WatchPRStateContext`. No import orphans. Validated: gofmt, go vet, go build, go test all pass.

### 2026-07-18: F18 qwen3-coder catalog addition made cheapest-pick test expectations stale

Catalog commit 8b51613 added `qwen/qwen3-coder-30b-a3b-instruct` ($0.07/M input) to the OpenAI-family catalog. The stage tier resolver correctly picks it as the cheapest tool-capable model, so tests asserting `gpt-4.1-nano` ($0.10/M) as the cheapest-pick outcome were stale. Updated test fixtures in `model_routing_test.go`, `stage_tier_resolver_test.go`, and `onboarding_test.go`. Root cause was catalog data change, not a resolver bug.

### 2026-07-18: ponytail-audit finding 1 (orphaned pr-review binary)

- Deleted `cmd/splice-pr-review` (main.go, main_test.go) and
  `internal/review/` (review.go, review_test.go), -537 lines total. The
  binary was orphaned when `pr-auto-review.yml` was removed in F1; nothing
  imports `internal/review` except this binary, and no release/update/CI/npm
  machinery references it. Parent-verified before execution.
- Validation: gofmt/vet/build clean, focused packages green. The two
  pre-existing qwen3-coder catalog-drift failures on HEAD (8b51613) were
  fixed in the sibling commit (entry above); full root suite green at
  commit (skip TestRealMemdSidecarMemoryRetrieval).

### 2026-07-17: F18 — per-stage cost in eval CSV

- The user wants a token-cost eval to optimize token usage, with per-stage
  visibility. The eval harness (`splice eval bench`) already captured
  per-task token/cost totals from stream-json `usage` events, but had NO
  per-stage breakdown — you could see the total cost but not which stage
  burned the tokens.
- Key finding: the data already existed. `splicerun.Run` marshals the full
  `PipelineResult` (with per-stage `StageRecord` usage from AR8) into the
  `final` event's `text` field, and `splice exec -o stream-json` already
  emits it. The gap was purely consumer-side: the eval harness never parsed
  it.
- Bridge: `parsePipelineStagesFromStdout` in `agent_command.go` extracts the
  per-stage ledger from the `final` event. `StageBreakdown` mirrors the
  relevant `StageRecord` fields (name, status, tokens in/out/cached, cost,
  latency) WITHOUT coupling `agenteval` (Zero substrate) to
  `internal/splice/schemas` (pipeline layer) — a minimal local struct.
  `WriteBenchmarkCSV` gains a `stageBreakdown` column
  (`name:in=N,out=N,cost=F;...`). Non-pipeline agents produce empty
  `stageBreakdown` (graceful no-op).
- No protocol change, no `exec.go` change, no `splicerun` change. The data
  was already emitted; the bridge is ~40 lines of consumer-side parsing.
- New: `internal/agenteval/stage_breakdown_test.go` (5 tests: parser with
  stages, parser without final event, parser empty, formatStageBreakdown,
  populateAgentRunUsage includes stages). Updated: `agent_command.go`,
  `benchmark.go`, `benchmark_test.go` (CSV header gained a column).
- Validation: gofmt/vet/build clean, full root suite green (78 ok / 0 FAIL).
- Next: run a real eval task with OpenRouter to see actual per-stage costs.

### 2026-07-17: R3 — secret + dependency scanning via trivy (SARIF)

- Implemented the third checkpoint of the pipeline reinforcement track. A
  workspace-level `trivyCheck` that runs `trivy fs --format sarif
  --scanners vuln,secret` once on the workdir, reusing R2's shared SARIF
  parser. Covers two vulnerability classes the language lint scanners
  fundamentally miss: hardcoded credentials (any file) and known CVEs
  (dependency manifests).
- Design: trivy is workspace-level (scans lockfiles + all files), not
  per-language like the lint scanners (sarifCheck). So it's a separate
  `VerificationCheck`, not a sarifCheck map entry. Reuses the existing `sarif`
  dtool (arbitrary command runner) — no new dtool. Extracted
  `parseSarifResults`/`mapSarifFindings`/`isMissingScannerError` as shared
  package-level helpers from `sarifCheck` so both checks share one parser.
- `Required: false` (the key design decision). The F14 deferral note said
  secret/dep scanning should be opt-in. Adding trivy as `Required: true` would
  make EVERY repo without trivy installed report `VerificationIncomplete`
  (trivy is workspace-level, always runs, always required), which would mask
  actual findings (the aggregator forces `incomplete` when a required check
  is incomplete, taking precedence over `findings`). With `Required: false`,
  missing trivy shows as incomplete in the tool list (honest) but does not
  force the overall report status. Bandit/gosec stay `Required: true` (primary
  SAST for their languages); trivy is additive augmentation.
- New: `internal/splice/stages/security_trivy.go` (`trivyCheck`: runs trivy
  via the `sarif` dtool or direct binary, 30s timeout, missing -> incomplete
  non-required, parses with shared `parseSarifResults`, maps with
  `mapSarifFindings`). New: `internal/splice/stages/security_trivy_test.go`
  (3 tests: findings + severity mapping + authority, missing -> incomplete,
  no findings -> passed). Refactor: `security_sarif.go` extracted shared
  helpers. Wiring: `DefaultSecurityChecks` adds `trivyCheck{}`.
- Fixed existing test mocks: all mock `RunTool` functions in `stages_test.go`
  and `security_auditor_test.go` that errored on unhandled tools now return a
  not-installed response (so gosec/sarif/trivy degrade to incomplete, not a
  hard error). The `TestSecurityAuditorNoSourceFilesNotApplicable` test uses
  a custom check set without trivy (trivy is workspace-level, always
  applicable, so it would prevent a not_applicable report for an empty
  workspace).
- Implemented inline by the parent (small checkpoint: ~120 lines prod + ~100
  lines tests; delegation skipped after the established pattern of workers
  stalling on validation).
- Validation: gofmt clean, vet clean, build clean, splice+tui+cli green,
  e2e green, full root suite green (78 ok / 0 FAIL). Zero LLM tokens.
- The deterministic security floor now covers: SAST (Bandit/gosec/eslint via
  SARIF), secret scanning (trivy), dependency scanning (trivy). The F14
  deferral items (secret + dep scanning) are landed. CP5 (LLM advisor)
  remains tabled behind its eval contract.

### 2026-07-17: R2 — SARIF security layer (generic parser, N scanners)

- Implemented the second checkpoint of the pipeline reinforcement track
  (plans/pipeline-reinforcement-sarif-2026-07-17.md). A generic
  SARIF-parsing `VerificationCheck` adapter: one Go parser, N scanners,
  zero per-language Go code. A new language becomes one config line + the
  scanner installed.
- Context: the user asked for a dynamic layer instead of one adapter per
  language. SARIF (OASIS v2.1.0, the GitHub Code Scanning standard) is the
  industry answer: most scanners emit it (gosec, semgrep, eslint via
  @microsoft/eslint-formatter-sarif, codeql, trivy), so one generic parser +
  a language->command map replaces N adapters.
- ADDITIVE, not a rewrite. The hand-tuned Bandit (Python) and gosec (Go)
  adapters stay as proven defaults. The SARIF layer is the extensible path
  for languages they do not cover; it delivers JS/TS coverage (R2) from its
  defaults without a new Go adapter.
- New: `internal/splice/dtools/sarif.go` (`NewSarifTool`: runs an arbitrary
  scanner command via exec.CommandContext, 30s timeout, workspace-confined,
  missing binary -> "not installed or not available" so the adapter degrades
  to incomplete; takes `command`/`args`/`paths` from the tool args).
- New: `internal/splice/stages/security_sarif.go` (`sarifCheck`: a
  `map[string]sarifScanner` language->command map; `defaultSarifScanners()`
  covers javascript + typescript via `npx --no-install eslint --format
  @microsoft/eslint-formatter-sarif .`; NO go/python entries to avoid
  duplicating gosec/bandit). Derives languages present from `req.Paths` by
  extension; runs each configured scanner via `RunTool` (the safety
  substrate) or direct binary; parses the SARIF `runs[].results[]`; maps
  each result to a `VerificationFinding` (ruleId, level->severity
  error->High/warning->Medium/note->Low/none->Info, nested message.text,
  first location's artifactLocation.uri -> path, region.startLine -> line).
  Missing scanner -> incomplete (only if all configured scanners missing);
  no files for any configured language -> not_applicable.
- Wiring: `DefaultSecurityChecks()` returns
  `[banditCheck{}, gosecCheck{}, sarifCheck{scanners:
  defaultSarifScanners()}]`; `registry.go` registers `dtools.NewSarifTool`.
- Verified the SARIF v2.1.0 result schema via the OASIS spec + GitHub docs
  (runs[].results[] with ruleId, level, message.text nested under message,
  locations[].physicalLocation.artifactLocation.uri + region.startLine;
  level map error->High, warning->Medium, note->Low) before coding.
- The F14 schema is UNCHANGED; Authority="deterministic" (SARIF scanners
  are authoritative, not advisory). The trajectory/run.go/passSucceeded
  logic is unchanged; sarif findings flow through the same VerificationReport
  the aggregator produces, so high/critical sarif findings block completion.
- Robustness: when `message.text` is empty but `ruleId` is present, the
  mapper falls back to `Message = ruleId` so schema validation (which
  requires a non-empty message) does not reject the finding. Minor
  uncontracted detail, accepted on review.
- Implemented by a fresh-context `kimi-k2.7-code` worker that landed all
  production code + 5 tests but stalled before the full-suite validation
  (its environment timed out on unrelated agenteval/cli test hangs). Parent
  reviewed the diff inline (verified the command-map design, the
  toStringAny use, missing->incomplete, nested message.text, severity map,
  empty-Locations handling), fixed gofmt on the two new files, and ran the
  full gate.
- Tests: `security_sarif_test.go` (5 tests: findings + severity mapping,
  nested message.text, missing scanner -> incomplete, no applicable files ->
  not_applicable, empty Locations still a finding). Mock-based (no real
  scanner required). Existing Bandit/gosec/auditor tests stay green.
- Validation: gofmt clean, vet clean, build clean, splice+tui+cli green,
  e2e green, full root suite green (78 ok / 0 FAIL, skip
  `TestRealMemdSidecarMemoryRetrieval`). Zero LLM tokens.
- The deterministic security floor now covers Python (Bandit), Go (gosec),
  and JS/TS (SARIF/eslint), with a config-driven path for future languages.
  CP5 (LLM advisor) remains tabled behind its eval contract.

### 2026-07-17: R1 — Go security floor (gosec deterministic adapter)

- Implemented the first checkpoint of the pipeline reinforcement track
  (plans/pipeline-reinforcement-go-security-2026-07-17.md). Extended the
  deterministic security floor from Python-only (Bandit) to Go via a `gosec`
  `VerificationCheck` adapter mirroring the F14c Bandit adapter. Go repos now
  get a real security signal instead of `VerificationIncomplete`.
- CP5 review (the LLM security advisor) found the single most concrete
  pipeline weakness: the security floor was Python-only. R1 is the safe,
  deterministic alternative to CP5 — no false-positive storm, no eval, no
  blocking-semantics danger. The tool is authoritative (deterministic
  authority). Dogfoods on Splice itself (a Go repo).
- New: `internal/splice/dtools/gosec.go` (`NewGosecTool`, workspace-confined,
  missing -> "Gosec is not installed or not available", 30s timeout, mirrors
  bandit.go). New: `internal/splice/stages/security_gosec.go` (`gosecCheck`:
  filters `.go` internally, missing -> incomplete, parses `Issues[]` JSON,
  severity mapping LOW/MEDIUM/HIGH -> Low/Medium/High, line-as-string parse
  handling ranges like "12-15" -> 12).
- Refactor: `internal/splice/stages/security_auditor.go` generalized from
  Python-only to all-source: `boundedSourceFiles`/`gitChangedSourceFiles`
  (with `isSourceFile` covering .py/.go/.js/.jsx/.ts/.tsx); removed the
  "No security scanner available for non-Python files" short-circuit. Each
  check now filters by extension internally (F14b-stated intent). Bandit
  gained an internal `.py` filter (`filterPythonPaths`, behavior-preserving
  for Python repos).
- Wiring: `DefaultSecurityChecks()` returns `[banditCheck{}, gosecCheck{}]`;
  `registry.go` registers `dtools.NewGosecTool` alongside Bandit.
- Root-cause fix (the load-bearing bug): the `[]string` vs `[]any` mismatch
  in the `RunTool` args path. Both adapters called `req.RunTool(ctx, name,
  map[string]any{"paths": relPaths})` with `relPaths` as `[]string`, but the
  dtools adapters type-assert `args["paths"].([]any)` — a `[]string` fails
  that assertion, returning "paths must be an array of strings". This was a
  latent bug in BOTH adapters, never triggered for Bandit because Python
  repos short-circuited before the RunTool call. R1 surfaced it (a Go repo
  runs gosec, which hits the mismatch). Fixed via a shared `toStringAny`
  helper in `verification.go`; both adapters now convert.
- Second fix: the direct-binary missing-gosec path checked only the output
  for "not found"/"not installed", but `exec.ErrNotFound` produces no output
  (the message is in the error). Fixed to check the error string too, so a
  missing gosec binary degrades to incomplete, not a hard error.
- Behavior change (documented): a Go repo with gosec installed now gets real
  security findings (possibly blocking, since high/critical security findings
  block completion via `passSucceeded`). A Go repo WITHOUT gosec reports
  incomplete-via-gosec-check (honest, same result as before, different code
  path). The `TestSecurityAuditorNonPythonIncomplete` test was renamed and
  updated to `TestSecurityAuditorGoRepoGosecMissingIncomplete` (asserts the
  new gosec-missing summary, not the old stage short-circuit).
- Implemented by a fresh-context `kimi-k2.7-code` worker that landed ALL
  production code (both new files + the bandit filter + the auditor
  refactor + the verification/registry wiring) + the 6 new tests, but
  stalled/failed before validation. Parent interrupted, reviewed the diff
  inline (sound), and fixed two bugs the worker missed (the `[]any` mismatch
  and the missing-binary error-string check) + updated the obsolete test.
- Tests: `security_gosec_test.go` (missing -> incomplete, findings +
  severity mapping + line/range parse, non-.go -> not_applicable),
  `security_auditor_test.go` (Go-only, mixed, no-source). Existing Bandit
  tests stay green.
- Validation: gofmt clean, vet clean, build clean, splice+tui+cli green,
  e2e green, full root suite green (78 ok / 0 FAIL, skip
  `TestRealMemdSidecarMemoryRetrieval`). Zero LLM tokens.
- Next: R2 candidates (JS/TS security via eslint-plugin-security; secret /
  dependency scanning) or the two-catalog gap; user to pick. CP5 (LLM
  advisor) remains tabled behind its eval contract.

### 2026-07-17: CP3 — persistent plan panel (phase-adaptive layout, trimmed)

- Implemented the final active checkpoint of the TUI/workflow redesign
  (plans/tui-workflow-redesign-2026-07-16.md). Added one opt-in layout toggle,
  `/layout` (`planPanelPersistent`): when on + design mode + a crystallized
  plan, the `DesignPlan` (epic/requirements/tasks) renders as a bordered
  header pinned above the chat column so it survives transcript scroll during
  design revisions. Off by default; no behavior change when off.
- Ponytail call: the original plan named TWO toggles. Built ONE
  (`planPanelPersistent`) and deferred the other (`pipelinePromoted`) as
  speculative. Reasoning: the persistent plan panel fills a real gap (the
  crystallized plan scrolls away after `/crystallize`; `/plan` shows the
  agent's live `update_plan` steps via `plan_command.go`'s
  `currentPlanReader`, NOT the design plan; the sidebar PLAN section shows
  step status, not epic/requirements). `pipelinePromoted` would demote the
  live streaming chat during runs when the sidebar PIPELINE section already
  shows stage status (F12b) — cosmetic with a real cost. User confirmed the
  trim.
- `internal/tui/commands.go`: `commandLayout` kind + `/layout` command
  registration.
- `internal/tui/model.go`: `planPanelPersistent bool` field; `commandLayout`
  dispatch to `handleLayoutCommand`; `transcriptView` prepends
  `persistentPlanHeader(width)` to the pinned header (above the title bar) on
  the alt-screen scroll path.
- `internal/tui/design_mode.go`: `handleLayoutCommand` (toggle + system
  transcript notice) + `persistentPlanHeader` (renders `formatDesignPlan` of
  `m.pendingPlan` via the existing `borderedBlock` helper; inert when the
  toggle is off, design mode is off, or no pending plan).
- Reuses existing data (`m.pendingPlan *schemas.DesignPlan`) and existing
  formatter (`formatDesignPlan`, design_mode.go:240). No new schema, no
  change to the sidebar, scroll engine, `twoColumnTranscriptView`, or headless
  exec.
- Implemented inline by the parent (small checkpoint: ~50 lines prod + ~70
  lines tests). Delegation skipped deliberately — the CP2/CP4 workers
  context-exhausted on test churn; this checkpoint was small enough that
  inline was the right call.
- Tests: 5 new tests in `design_mode_test.go` (toggle on/off with notices,
  renders epic + requirements when on, inert outside design mode, inert
  without a plan, inert when toggle off).
- Validation: gofmt clean, vet clean, build clean, splice+tui+cli green,
  e2e green, full root suite green (skip
  `TestRealMemdSidecarMemoryRetrieval`). Zero LLM tokens.
- The TUI/workflow redesign (CP1-CP4) is complete. CP5 (security auditor
  augment) and CP6 (design-phase `Crystallize` separation) remain deferred to
  separate plans.

### 2026-07-17: CP4 — default entry phase = planning

- Implemented the third checkpoint of the TUI/workflow redesign
  (plans/tui-workflow-redesign-2026-07-16.md). A fresh interactive session
  (new user via onboarding, or `/new`) starts in design (planning) mode, not
  execution. The user composes -> planner -> `/crystallize` -> `/approve` ->
  `/exec`; `/exec [prompt]` is the skip-planning escape hatch.
- Key architectural fact (verified): there is no `runKind` default to flip.
  The compose dispatch (`model.go:4490`) is gated on `m.designMode` (a bool):
  true -> design conversation, false -> pipeline. The status strip
  (`view.go:modeLabel`) already rendered "design" when `designMode` is true.
  `/design` and `/exec` already toggled it. So CP4 was exactly: set
  `designMode = true` at fresh-session entry, emit `design_mode_entered`, and
  show a one-time orientation notice.
- `internal/tui/design_mode.go`: extracted `enterDesignMode(notice) model`
  helper from `handleDesignCommand` (sets flag, `ensureActiveSession`, records
  the lifecycle event, appends notice). `handleDesignCommand` is a thin caller.
- `internal/tui/model.go`: `newModel` sets `designMode = true` (line 876); the
  compose path lazily emits the event + long notice on the first turn (line
  4446), gated by a new `designNoticeShown bool` field. This avoids needing
  the session store at construction and lets `reconstructDesignState`
  (called in `handleResumeCommand`) override `designMode` for resumed sessions
  (executing/completed phase -> false; conversation/review -> true).
- `internal/tui/session.go`: `startNewSession` resets `designNoticeShown` and
  calls `enterDesignMode` so a fresh session starts in planning.
- `internal/tui/onboarding.go`: `exitSetupToChat` calls `enterDesignMode` so
  first-run users land in planning (the CP2 forward-reference).
- Behavior-change mitigation: a one-time notice ("Planning mode. Describe
  what you want to build; Splice will crystallize a plan. Type /exec <prompt>
  to run a prompt directly through the pipeline.") instead of a configurable
  setting (YAGNI). Fires once per session via `designNoticeShown`.
- Headless `splice exec` unchanged (execution-direct for CI/scripting; no
  design-mode concept there).
- The behavior change broke ~12 existing tests that assumed the default was
  execution (width-tier status line now shows "design"; `/new` now creates a
  design session rather than clearing the session id; onboarding completion
  appends the planning notice; session/cancel tests that submit bare prompts
  route to design mode). All fixed inline by setting `m.designMode = false`
  on fixtures that test the execution path, or by updating assertions to the
  new default (the `design_mode_entered` event in the sequence, the planning
  notice in the transcript, the replaced session id). The e2e
  `TestTUIPipelineEndToEndFeature` was updated to prefix its prompt with
  `/exec` (the documented escape hatch).
- Implemented by a fresh-context `kimi-k2.7-code` worker that landed all
  production code + the e2e fix + new CP4 tests but failed/exhausted before
  the test sweep. Parent interrupted, reviewed the production diff inline
  (verified the helper extraction, construction default, lazy event emit,
  `/new`/onboarding call sites, notice gate, resume-override correctness),
  and fixed the ~12 broken existing tests inline. The worker's production
  code was sound; only the test-fixture sweep to the new default was missing.
- Tests: new tests assert a fresh session's `designMode` is true, `/new`
  starts in design, `/exec` bypasses, `/design` re-enters. Existing resume
  tests stay green.
- Validation: gofmt clean, vet clean, build clean, splice+tui+cli green,
  e2e green, full root suite green (skip
  `TestRealMemdSidecarMemoryRetrieval`). Zero LLM tokens.
- Next: CP3 (phase-adaptive layout toggles), the last active checkpoint.

### 2026-07-17: CP2 — onboarding rewrite (batteries-included pipeline step)

- Implemented the second checkpoint of the TUI/workflow redesign
  (plans/tui-workflow-redesign-2026-07-16.md). A new first-run onboarding
  step, `setupStagePipeline`, sits between the model-pick and safety steps.
  It shows the user's auto-resolved pipeline (stages pre-filled by CP1's
  resolver, each editable via dropdown) and writes `stage-models.json` on
  finish so the file exists from first run.
- Prerequisite refactor: extracted a pure `ResolveStageTierModel(stageName,
  primaryProfile, registry) *ModelEntry` from `NewStageTierResolver` in
  `internal/splice/stage_tier_resolver.go`. It does all the picking (tier
  label -> custom-provider guard -> catalog resolve -> candidate filter ->
  cheapest/strongest) and builds NO provider. `NewStageTierResolver` now
  delegates to it then `buildCachedProvider`. Existing CP1 closure tests pass
  unchanged. Also exported `StageTierLabels()` so onboarding can enumerate
  model-backed stages.
- `internal/tui/onboarding.go` (+307 lines): new `setupStagePipeline` in the
  iota; `setupStages()` inserts it after `setupStageModel` in both OAuth and
  non-OAuth paths; new `setupState` fields `pipelinePicks map[string]string`
  and `pipelineOptions map[string][]stageModelOption` + `pipelineIndex`;
  `populateSetupPipelinePicks()` pre-fills from `ResolveStageTierModel`
  (fallback to primary when nil); `setupPipelineLines` render (4 model-backed
  rows + deterministic summary line); `moveSetupPipelineIndex`/
  `cycleSetupPipelineModel`/`setupPipelineLeftAtTop` key handling (Up/Down
  focus, Left/Right cycle, Left-at-top goes back); `completeSetup` writes
  `stage-models.json` via `writeSetupStageModelConfig` (reuses the wizard's
  `stageModelConfigPath` + 0o600 pattern); ready screen gains a
  `pipeline: <N> stages auto-resolved` summary row.
- Edge case (custom/local providers): `setupProviderUsesTypedModel` (= needs
  endpoint = custom-compatible) -> resolver returns nil for all stages ->
  step renders "All stages use `<model>`", no dropdowns, writes a
  default-only file (empty `Stages`). Design decision 3: state neutrally,
  do not warn.
- `internal/cli/app.go`/`setup.go`: NO change. `Save` keeps writing only
  `config.json`; the `stage-models.json` write lives in the TUI where the
  picks reside. `SetupSelection`/`SetupResult` contract unchanged.
- Resolved decisions: all 4 model-backed stages get dropdowns (not just the
  2 `/stages` exposes); `/stages` preserves the design-phase entries per its
  `reservedInactiveStageNames` contract. Entry phase is CP4, not CP2.
  Same-generation cost deferral stands (editable dropdown makes it
  self-correcting).
- Two-catalog gap avoided: CP2 sources both pre-fill and dropdown from
  `modelregistry.ListByProvider` (not `providermodeldiscovery`), so they're
  consistent. Unifying catalogs remains a separate future checkpoint.
- Implemented by a fresh-context `kimi-k2.7-code` worker that landed all
  production code + resolver tests but context-exhausted (322k tok) during
  test churn and broke `onboarding_test.go` (reverted to near-HEAD). Parent
  interrupted the worker, reviewed the production diff inline (verified the
  extraction, populate/render/keys, completeSetup write, edge-case branch,
  mouse_test fix), and wrote the 6 CP2 tests inline. The worker's production
  code was sound; only the test coverage was missing.
- Tests: 6 new tests in `onboarding_test.go` (render 4 rows, picks
  pre-filled, Up/Down focus, Left/Right cycle, completeSetup writes file
  with 4 picks + 0o600 mode, local provider default-only, ready summary)
  + resolver unit tests for `ResolveStageTierModel`.
- Validation: gofmt clean, vet clean, build clean, splice+tui+cli green,
  e2e pipeline test green, full root suite green (skip
  `TestRealMemdSidecarMemoryRetrieval`). Zero LLM tokens.
- Next: CP4 (default entry phase = planning), then CP3 (layout toggles).

### 2026-07-15: ponytail-review cleanup — shrink helix comment, drop mockup

- Ran /ponytail-review on the cosmetic-port diff and validated each finding:
  - REAL: the 8-line `helixPalette` comment was a 4× outlier vs the file's
    1-2 line-per-palette convention (dracula/nord/gruvbox/etc. all 2 lines).
    Shrunk to 3 lines (name + the non-obvious selBg-brightening rationale);
    dropped the derivable token-diff list and the provenance/approval-date
    (that lives in MEMORY/commit, not source).
  - PARTIALLY REAL (acted on): deleted the committed `cmd/mockup/index.html`
    (595 lines). The decision it extracted is in
    `plans/tui-cosmetic-port-2026-07-15.md` and all three checkpoints are
    landed, so its job is done; it duplicated all 15 palette hex values from
    `theme_palettes.go` (drift bait). Deletion is git-reversible.
  - DEBUNKED sub-claim: the review called the mockup's 3 handoff bridges
    (localStorage + download + clipboard) "redundant" — they are not; each
    covers a distinct gap (reload-persistence / agent-readback / user-paste-
    fallback) given browser↔agent share no state. Moot now that the file is
    deleted, but the record stands: that reasoning was wrong.
- `cmd/mockup/main.go` (the standalone Lipgloss palette-preview binary) is
  untracked and left in place; it is buildable (`go build ./cmd/mockup/`) and
  not referenced by CI or docs.
- Validation (end-to-end): gofmt clean, `go vet ./internal/tui/` clean,
  `go build ./...` clean (full module), full `go test -count=1 -skip
  TestRealMemdSidecarMemoryRetrieval ./...` green (the skipped test is the
  known zombie-memd hang, unrelated), memd sidecar module green. The WCAG
  contrast gate re-run clean (comment change can't affect contrast, confirmed).

### 2026-07-15: TUI cosmetic port — checkpoint 3 (arc spinner) landed

- Animated the running pipeline stage glyph with the cli-spinners `arc` cycle
  (`◜◠◝◞◡◟`) in `internal/tui/pipeline_panel.go`. Added the `arcFrames` constant
  and threaded a `phase int` parameter through `renderSection` and
  `pipelineStageGlyphAndStyle` (kept pure — no globals). The sidebar call site
  (`internal/tui/sidebar.go`) passes `m.spinnerPhase`, so the arc advances on the
  existing shared ~80ms `bubbles/v2/spinner` tick — NO new timer, no Update-loop
  change. Done stages keep `✓`, pending `○`, failed `✗`, skipped `↩`.
- Delegated the bounded finish (call-site + test updates) to a fresh-context
  `deepseek-v4-flash` worker per the APPEND_SYSTEM_GLM_5_2 routing guide (clear-
  spec impl lane). hy3:free forked the oversized parent context and hit the 262k
  limit; escalated to deepseek-v4-flash on fresh context, which made the edits
  correctly (returned no text, but the diff was exactly per spec). Parent
  reviewed the diff and ran the validation gate.
- Tests: updated `TestPipelinePanelRenderSectionGlyphs` (`●`→`◜`, added phase
  arg) and added `TestPipelineStageGlyphAdvancesWithPhase` (arc advances with
  phase; non-running statuses ignore phase).
- Zero LLM tokens, zero provider calls in the pipeline path (the spinner reuses
  the existing UI tick).
- The three-checkpoint cosmetic port is complete: helix palette (cp1), λ gutter
  (cp2), arc spinner (cp3). All CI-confirmed.

### 2026-07-15: TUI cosmetic port — checkpoint 2 (λ gutter) landed

- Checkpoint 2 shrunk to a single real change after recon: the approved
  `sidebarHeaders: "calm"` is already the TUI default (`sidebarHeader` uses
  `zeroTheme.muted.Bold(true)`), and `composerHint: true` is already the default
  (`composerPlaceholder = "describe a task for splice…"`, rendered faint when the
  input is empty). So 2b and 2c are no-ops; only 2a (prompt gutter glyph) needed
  porting.
- 2a landed per the resolved option-1 decision: user-prompt gutter `▌` → `λ` in
  `renderUserPromptStyledLine` (`internal/tui/rendering.go`) plus the
  `userPromptPrefix` width constant, AND the live composer gutter `❯ ` → `λ `
  (`input.Prompt` in `internal/tui/model.go`). The live composer caret `▌`
  (`appendStreamingCursor`, model.go:3412) stays `▌` — a block is the correct
  caret affordance. `λ` (U+03BB) and `▌`/`❯` are all 1 cell wide, so width math
  is unchanged.
- Updated 4 focused assertions: `TestUserRowRendersPromptGutter`,
  `TestTranscriptSeparatesUserPromptFromContinuation` (both `▌`→`λ`), and the two
  composer-box tests (`TestComposerBox…`, `❯`→`λ`). Other `❯` hits in the test
  suite are picker/wizard selection markers (unrelated) and were left alone.
- Zero LLM tokens, zero provider calls.
- Next: checkpoint 3 (arc running spinner reusing the existing `spinnerPhase`
  tick).

### 2026-07-15: TUI cosmetic port — checkpoint 1 (helix palette) landed

- Approved a cosmetic selection from the `cmd/mockup/index.html` approve-and-
  hand-off mockup: theme `teal:helix`, prompt glyph `λ`, calm sidebar headers,
  composer hint, arc running spinner. Four approved values (blocks bar, pipe
  separators, no reading-width clamp, no dim-completed) are already the TUI
  default and needed no port. Plan: `plans/tui-cosmetic-port-2026-07-15.md`.
- Checkpoint 1 (helix palette) landed: added `helixPalette` literal + one
  `themeRegistry` entry `{Name:"helix", Label:"Helix", IsDark:true}` in
  `internal/tui/theme_palettes.go`. The palette shares the ink/muted/faint/
  faintest ramp and diff tokens with `darkPalette` (which already clears AA), so
  only accent/promptBg/line/line2/selBg/cardRun differ. The existing
  `TestAllThemesContrastAndHierarchy` / `TestDiffWordSpanContrast` /
  `TestSelectedRowBandIsVisibleAndReadable` gated it with no new test code; it
  passed AA with zero token nudges (selBg #1a3a38 vs panel #0e0e10 clears the
  separation rule). Zero LLM tokens, zero provider calls.
- Prompt-glyph decision resolved as option 1: gutter `▌` → `λ` (historical user
  rows), live composer caret stays `▌` (a block is the right caret affordance).
- Next: checkpoint 2 (render polish: λ gutter, calm sidebar headers, composer
  placeholder hint) and checkpoint 3 (arc running spinner reusing the existing
  `spinnerPhase` tick, no new timer).

### 2026-07-13: F15 adversarial review findings

- A GPT-5.6 Sol reviewer read the F15 plan against the actual codebase and
  rejected it. Six blockers: no lifecycle/persistence contract, broken stage
  schemas (Crystallize tool fields don't match Go structs, critic mitigation
  field mismatch), `designMode bool` insufficient for four phases, `/plan`
  already exists, `runAgentWithOptions` has no design path, and the TUI should
  not own orchestration.
- The reviewer recommended D0-D6 slicing: D0 lifecycle/events/persistence,
  D1 stage contract repair, D2 read-only design conversation mode, D3
  crystallize+critic engine operation, D4 resumable design runner, D5 TUI
  execution/panel, D6 startup/project discovery.
- Key decisions: session events are authoritative (not a global file),
  engine owns orchestration (not `model.go`), design conversation uses a
  read-only tool surface, `RunDesignPlan` needs runner changes (completed-task
  input, per-task callback, acceptance fact propagation).
- Full findings documented at
  `docs/audits/2026-07-13-f15-adversarial-review-findings.md`.

### 2026-07-13: F15 design phase TUI wiring plan

- The Flug design docs (`docs/flug-design/03-design-phase.md` and
  `08-ui-architecture.md`) describe a two-phase SDLC workflow: a human-gated
  design conversation that crystallizes into a typed `DesignPlan`, runs an
  adversarial critic, and then hands off to the live execution loop.
- The execution phase is fully wired (F12b through F14). The design phase
  stages (`design_conversation`, `plan_critic`) and schemas (`DesignPlan`,
  `PlanCritique`, `AcceptanceFact`, `Task`) exist in Go but are not reachable
  from the interactive TUI. The TUI's spec-draft flow uses Zero's generic
  `agent.Run` with `submit_spec`, not Splice's own design conversation stage.
- The TUI startup mode depends on existing state, not a hard default.
  New sessions with no plan file start in design mode. New sessions with an
  existing `.splice/plans/current.json` offer to resume or start fresh.
  `splice exec` (headless) is unchanged. The user can `/exec` to bypass
  design for a direct execution prompt, or `/plan` then `/approve` for the
  full flow. `/design` re-enters design mode.
- Crystallized plans are persisted to `.splice/plans/current.json` so the
  resume-or-new prompt works across sessions. D4 updates the file with
  execution progress.
- F15 wires the design phase into the TUI in four green-to-green
  checkpoints: D1 (design conversation mode via `/design`), D2
  (crystallization via `/plan`), D3 (adversarial critic and review with
  `/approve` gate), D4 (execution handoff via `RunDesignPlan`).
- The design conversation uses `agent.Run` with `options.SystemPrompt` set to
  Splice's `design_conversation.md` prompt. Zero manages conversation history.
  Crystallization uses `DesignConversation.Crystallize()` at the typed
  boundary. This matches the Flug design principle: free exploration, typed
  handoff.
- The existing spec-draft flow is unchanged. Design mode is an additive path.
  A future checkpoint may merge them.

### 2026-07-13: F14c fast local checker profiles

- Added go/format equivalence to the Go quality adapter: after parsing,
  formats source with go/format.Source and flags unformatted files as
  low-severity GO_FORMAT findings.
- Batched Python py_compile into one process for all .py files instead of
  one process per file. Added optional Ruff check that runs only when project
  Ruff config exists and Ruff is installed. Missing Ruff records an optional
  incomplete tool run that does not make the report incomplete.
- Created quality_javascript.go: `node --check` per .js/.jsx file. Missing
  Node returns incomplete, not clean. 30-second timeout.
- Created quality_typescript.go: `node_modules/.bin/tsc --noEmit --pretty false`
  when tsconfig.json exists. Parses TS error codes. Missing local tsc returns
  incomplete. 30-second timeout.
- Updated detectLanguage in registry.go to detect TypeScript (tsconfig.json)
  before generic JavaScript (package.json).
- Sorted paths alphabetically before checks for stable fingerprints.
- Non-Python security stages now report incomplete instead of clean.
- HY 3 free completed all code and tests but exhausted its turn budget while
  updating docs. The parent finished the docs and committed.
- Zero model calls, zero added tokens. Go checks stay in-process. Python
  changes from up to 20 interpreter starts to one. JavaScript remains bounded.
  TypeScript uses one local compiler process.

### 2026-07-13: F14b typed verification report and modular check seam

- Replaced `StaticAnalyzerOutput` and `StaticIssue` with `VerificationReport`
  and `VerificationFinding` across the static analyzer, security auditor,
  trajectory monitor, orchestrator, and TUI. The data keys
  `static_analyzer_output` and `security_auditor_output` are preserved so
  trajectory extraction and persisted payloads remain recognizable.
- Added `internal/splice/stages/verification.go` with the `VerificationCheck`
  interface, `VerificationCheckRequest`/`VerificationCheckResult` types, and a
  pure `aggregateVerificationResults` function that validates tool identity,
  normalizes and sorts findings by severity then tool/path/line/rule, assigns
  stable SHA-256 fingerprints, deduplicates exact findings, and derives report
  completeness and status without calling a provider.
- Created `quality_go.go`, `quality_python.go`, and `security_bandit.go` as
  separate deterministic adapters wrapping the existing go/parser,
  py_compile, and Bandit logic. Each filters by file extension internally so
  the wrong-language check returns not-applicable rather than false findings.
- `StaticAnalyzer` and `SecurityAuditor` are now pointer types constructed via
  `NewStaticAnalyzer(...)` and `NewSecurityAuditor(...)` with default check
  sets. Running with no checks returns a named construction error. The
  production registry supplies default checks.
- Added `StageIncomplete` to `StageStatus`. After validating a stage output,
  `runPass` recognizes an incomplete `VerificationReport` and records
  `StageIncomplete` with an `incomplete` stage event. It does not trigger
  another iteration by itself.
- `IterationState` gained `VerificationIncomplete`, populated from stage
  records. `passSucceeded` now blocks on critical/high quality findings in
  addition to security findings. When findings block completion, revision
  context includes at most ten findings sorted by severity.
- Bandit unavailable becomes `incomplete`, not zero findings. No matching
  files becomes `not_applicable`, not `passed`. Unsupported language becomes
  `incomplete` and names the missing language profile.
- Four delegation attempts failed (HY 3 free x2, Kimi K2.7 Code, DeepSeek V4
  Flash) due to turn budget exhaustion, context limits, and excessive token
  churn. The parent reviewed the partial HY 3 work and completed the
  remaining migration inline.
- Runtime token overhead is zero. Report construction and fingerprinting are
  linear in the bounded finding count.

### 2026-07-13: F14a deterministic stages made model-free

- Removed `submit_analysis`, the static-analyzer prompt, optional interpretation,
  and its retry/prompt-contract coverage. Static findings now remain exactly as
  produced by deterministic checks and never call a provider.
- `static_analyzer`, `security_auditor`, and `test_runner` skip
  `StageModelResolver`, receive nil providers with empty model/effort values,
  record no model/provider attribution, and use zero token budgets. Unknown
  stages keep the model-backed default.
- `/stages` now exposes the code writer and test generator as built-in routing
  targets. Reserved ineffective entries remain loadable and survive saves;
  unknown extension rows remain editable.
- Added focused provider-panic, routing, attribution, budget-shape, budget-total,
  and wizard-preservation tests. Focused schemas, stages, orchestrator, and TUI
  suites pass locally.
- HY 3 free exhausted its turn budget after a partial patch, Kimi K2.7 Code was
  rejected for an oversized output reservation, and DeepSeek V4 Flash was
  interrupted after excessive token churn. The parent reviewed and completed
  the bounded checkpoint rather than launching another worker.
- Runtime token overhead is zero. Static-analysis findings lose one provider
  call or up to three malformed-output attempts, and static analysis no longer
  reserves 2,800 tokens per iteration.

### 2026-07-12: F14 fast deterministic verification plan approved

- Resolved the static-analyzer contract in favor of the last live Flug runtime
  and the speed requirement: static quality, security, and test stages are
  model-free. Optional LLM interpretation is removed rather than relabeled.
- F14a removes static-analysis model calls, skips provider resolution and model
  attribution for deterministic stages, gives them zero token budgets, and
  limits `/stages` to effective model-backed pipeline targets.
- F14b replaces ambiguous issue output with a typed verification report that
  distinguishes passed, findings, incomplete, and not-applicable states.
  Missing coverage is surfaced once and does not trigger an iteration that
  cannot repair the missing tool.
- F14c keeps checks local and bounded: in-process Go checks, batched Python
  checks, bounded JavaScript parsing, and a repository-local TypeScript
  compiler when present. No auto-install, remote scanner, model call, or cache
  enters the hot path.
- High/critical quality and security findings block completion and flow back as
  bounded typed evidence. Medium/low findings remain recorded and non-blocking
  by default.
- Verification implementation is adapter-based: each deterministic quality or
  security tool implements `VerificationCheck`, while a pure aggregator owns
  sorting, deduplication, fingerprints, completeness, and report status.
- A future `security_advisor` can consume the immutable `VerificationReport`
  through a separate typed model-backed stage. It may annotate or propose
  advisory findings, but cannot replace, suppress, or downgrade deterministic
  evidence. `/stages` exposes it only after real orchestrator wiring exists.
- Broader SAST, secret, and dependency scanning is deferred to measured opt-in
  adapters rather than silently adding latency or network dependencies.
- Estimated runtime token overhead is zero. Static-analysis findings save one
  normal provider call or up to three malformed-output attempts, and the plan
  removes 2,800 reserved static-analyzer tokens per iteration.

### 2026-07-13: F13 Ollama and LM Studio typed-output hardening

- Added a shared three-attempt typed-tool boundary for code writer, test
  generator, static analyzer, plan critic, and step-back. Only missing required
  tools, malformed JSON, or schema-invalid arguments retry. Transport/stream
  errors, cancellation, permission denial, and file application failures do not.
- Corrective prompts repeat the typed input and add a bounded validation defect
  plus the exact required tool. Exhaustion names the model/tool and explains
  that local runtimes require OpenAI-compatible tool calling. No cloud fallback
  exists or was added.
- Usage from all completed attempts is accumulated. `TypedOutputError` carries
  exhausted usage into failed `StageRecord`s; optional static-analysis LLM
  failure retains deterministic output while recording retry usage and emitting
  actionable activity.
- Added unit coverage for missing then malformed then valid output, usage sums,
  transport/cancellation/application no-retry rules, exhaustion messaging and
  metering, failed-record metering, and optional static-analysis fallback.
- Added an in-process OpenAI-compatible HTTP test using the real provider
  adapter with no API key. It returns plain text first and a valid tool call
  second; the real code-writer stage recovers, sends no Authorization header,
  and includes corrective feedback on attempt two.
- The local `/model` picker now tells users whether Ollama/LM Studio is
  unavailable or has no loaded models. README English and Chinese mirrors
  document tool-calling requirements, retries, and no cloud fallback.
- Valid output adds no calls. Invalid typed output adds at most two calls.
- Validation: focused Splice stages, orchestrator, provider onboarding, OpenAI
  adapter, and TUI suites passed locally; full CI run `29224764590` passed.

### 2026-07-13: F12f pipeline prompt contract tests

- Added request-level assertions for code writer, test generator, static
  analyzer, plan critic, and step-back. Each captured completion request must
  contain one system message with `pipeline_meta.md` exactly once.
- Did not add pipeline-enforcement language to `internal/agent/system_prompt.md`.
  Normal coding runs do not consume that prompt after routing to
  `splicerun.Run`; the Go call-site conditional is the enforcement boundary.
  Pipeline stage prompts already carry the accurate typed-contract context.
- No production code or stage schema changed. Valid runs incur zero additional
  tokens or calls.

### 2026-07-13: F12e real TUI pipeline feature test

- Added a deterministic Bubble Tea feature test that submits a normal prompt
  through `model.Update`, executes the real `splicerun.Run`, feeds stage and
  transcript callback messages back through `Update`, and inspects `View()`.
- The active provider is a rejecting cloud fixture while `stage-models.json`
  selects a keyless Ollama profile. The test proves the active provider is
  never called, the local profile/model reaches the provider factory, and the
  routed provider receives `submit_code`.
- The real core tool registry applies generated Go source and test files, runs
  deterministic static analysis and `go test`, and completes all three light
  tier stages. Assertions cover sidebar stage completion, formatted final
  output, raw `PipelineResult` session content, generated files, and nil TUI
  recovery authority.
- Existing `TestSpecCommandCreatesDraftReview` remains the end-to-end guard for
  the intentional spec path: `submit_spec` is advertised and `write_file` is
  absent. No production routing code changed in F12e.
- A fresh GLM-5.2 delegation exhausted its read budget without edits and was
  not resumed. The parent implemented the approved test.

### 2026-07-13: F12d shared exec/TUI stage-model routing

- Extracted stage and escalation resolver construction from CLI exec into
  `internal/splice/model_routing.go`. Providers remain lazy and cached per run.
- Each non-spec TUI prompt reloads `stage-models.json` and builds resolvers from
  the saved profiles plus the active profile fallback. Invalid config is shown
  as a transcript system warning and falls back to default routing. Spec draft
  remains on `agent.Run`, and TUI still receives no workspace recovery authority.
- Fixed a pre-existing contract defect exposed by extraction: `Resolve` returned
  the configured default with `specific=false`, but the old exec closure treated
  false as no route. Valid defaults now apply to unoverridden stages; a missing
  zero-value file remains a no-op.
- Added resolver tests for override/default/escalation selection, cache reuse,
  absent config, unknown profiles, and provider-construction errors. Added a TUI
  test that rewrites the config between two prompts and proves the provider
  factory receives the new model and effort on each run.
- Delegation attempts with Kimi, GPT-5.6, and two fresh GLM-5.2 workers failed
  before producing edits or exhausted read budgets. None were resumed. The
  parent completed and reviewed the approved checkpoint.
- Validation: Splice and TUI focused suites pass. The broad CLI suite reached
  the known `TestRealMemdSidecarMemoryRetrieval` stale-sidecar hang and will be
  rerun with that test skipped before commit.

### 2026-07-13: F12c searchable model picker and end-to-end TUI feature test

- Added type-to-search to the nested `/stages` model picker. It reuses the
  existing `commandPicker.applyQuery` ranking instead of introducing a second
  matcher. Backspace edits the query, Ctrl+U clears it, no-match state is
  explicit, and provider/effort pickers ignore printable search input.
- Added a full package-level TUI feature test following the inherited provider
  wizard test style. The test constructs `newModel`, submits `/stages` through
  `model.Update`, checks real `View()` output at overview/editor/picker stages,
  searches and confirms a deterministic model via key events, applies the
  draft, saves from the overview, then reloads `stage-models.json` from disk.
  It is network-free; only the model-option fixture is injected directly.
- A fresh GLM-5.2 implementation worker was delegated but exhausted its turn
  budget during repository reading without edits. It was not resumed; the
  parent implemented the approved bounded patch.
- Validation: gofmt clean, `git diff --check` clean, `go vet ./...` clean,
  focused stage-wizard tests green, and `go test -count=1 ./internal/tui/...`
  green.

### 2026-07-12: F12c editor rewritten to inherited Zero menu pattern

- A second manual test clarified that the first usability repair fixed focus
  rendering but preserved the wrong interaction model. The target editor still
  required Tab to traverse fields, Up/Down changed values in place, and Enter
  applied the edit instead of opening a chooser.
- Replaced the form-like editor with the inherited Zero menu contract used by
  `provider_wizard.go`, `mcp_manager.go`, and `picker.go`. The editor now has
  five rows (Provider, Model, Effort, Apply changes, Cancel); Up/Down traverses
  rows, Tab aliases Down, and Enter opens or activates the selected row.
- Provider, model, and effort each open a nested `❯` list picker where Up/Down
  moves, Enter confirms, and Esc/Left returns without changing the draft.
  Applying is the only action that mutates the in-memory stage config; overview
  `s` remains the explicit validated JSON write.
- Model choices reuse the existing saved-provider model picker source, including
  catalog and already-discovered live models, and merge the provider profile's
  configured model so valid custom/local profiles are never left with an empty
  chooser. Changing provider seeds that profile's configured model.
- Replaced obsolete free-text/focus tests with key-driven menu and picker tests
  covering traversal and wrap, picker open/confirm/cancel, draft isolation,
  apply/cancel semantics, rendering markers, active-provider fallback, override
  removal, persistence, validation, and stage rows.
- Delegation was attempted with fresh Kimi, GPT-5.6 delegate, and GLM-5.2
  workers. Kimi failed before startup due its provider output reservation,
  GPT-5.6 failed without edits, and GLM exhausted its turn budget without edits.
  The parent then implemented the already-approved contract directly rather
  than resuming or chaining the failed agents.
- Validation: gofmt clean, `git diff --check` clean, `go vet ./...` clean,
  focused stage-wizard tests green, and `go test -count=1 ./internal/tui/...`
  green.

### 2026-07-12: F12c usability repair after manual TUI test

- Manual testing disproved the initial review's claim that the `/stages` edit
  flow was usable. The shipped renderer mapped the effort row to field index
  1 instead of 2, never showed model focus correctly, and allowed text,
  Backspace, and Delete to mutate the model while provider or effort was
  focused.
- Repaired the three-row focus contract: provider=0, model=1, effort=2.
  Tab and Shift+Tab move focus, Up/Down change only the focused provider or
  effort selector, model editing keys work only on the model row, Enter
  applies, and Esc cancels. Undocumented Left/Right apply/cancel behavior was
  removed.
- Changing provider now replaces the model with that saved provider profile's
  configured model. This prevents invalid cross-provider combinations such as
  provider `openai` with model `claude-sonnet-4` and avoids forcing users to
  erase and retype the normal model choice.
- Added key-driven interaction coverage for forward/reverse focus movement,
  provider and model coupling, effort selection, model-only text/delete,
  Enter apply, Esc cancel, focus-marker rendering, and empty-model validation.
- Delegated repair attempts were bounded to the two stage-wizard files. The
  original worker exceeded its turn budget; it was not resumed after the user
  objected to reusing the same agent. A fresh delegate completed the remaining
  patch before parent diff review and validation.
- Validation: gofmt clean, `git diff --check` clean, `go vet ./...` clean,
  focused stage-wizard tests green, and `go test -count=1 ./internal/tui/...`
  green.

### 2026-07-11: F12c - Per-stage model routing TUI wizard

- Added `/stages` TUI slash command that opens an interactive overlay for
  viewing and editing `~/.config/splice/stage-models.json` (the per-stage
  model routing config AR11 made loadable and the orchestrator already
  consumes). Previously this file could only be edited by hand.
- `internal/tui/stage_model_wizard.go` (new, ~690 lines): `stageModelWizardState`
  mirrors `providerWizardState`'s shape but is simpler (no OAuth, endpoint,
  credential, or model discovery). Loads existing config via
  `schemas.LoadStageModelConfig` or seeds the default from the active
  provider profile. Overview lists default, escalation, and the 7 known
  stages (5 pipeline + 2 design, labeled "(design)"). Edit form has three
  fields (provider, model, effort) with a field cursor (Tab to cycle,
  up/down to change the focused selectable field, text to append to model).
  `d` or Backspace removes an override; `s` saves to disk (validated JSON,
  mode 0o600); Esc closes with a discard-confirmation when dirty.
- `internal/tui/stage_model_wizard_test.go` (new, ~370 lines): 11 tests
  covering seeding, load existing/load error, edit default, add/remove
  stage override, remove escalation, save writes file (mode + reload),
  save validation fails, dirty tracking, known stage rows, and a
  key-handler test proving provider cycling works through the keyboard.
- Mechanical threading across 9 TUI files: `m.stageModelWizard != nil`
  added to every `m.providerWizard != nil` guard (autocomplete, clipboard,
  composer, files_panel, model, mouse, plan_step_detail, sidebar,
  transcript_selection) and 7 key-routing dispatch sites in model.go.
- Parent review caught and fixed a critical bug: the worker's edit key
  handler had no field cursor, so up/down only cycled effort and the user
  could never change the provider profile. Tests passed because they set
  `editFields.providerCursor` directly, bypassing the key handler. Fixed
  by adding `fieldCursor` + `cycleEditField` and a key-handler test that
  proves provider cycling works. Also fixed em-dash violations (AGENTS.md
  no-em-dash rule), a footer/keybinding mismatch (`d` vs Backspace, both
  now work), and removed dead code (`editFieldCount`, `stageModelWizardStepDone`).
- Tier defaults are NOT included (the config schema has no tier field; tier
  is per-request). The wizard is stage-model config only. Changes take
  effect on the next pipeline run (documented in the footer).
- Implemented by a worker subagent (Kimi K2.7 Code for the initial
  implementation, fell back to glm-5.2 after an interrupt/resume); parent
  reviewed the diff inline, fixed the provider-cycling bug, em-dashes, and
  dead code, and re-ran full `go test -count=1 ./...` green (skipping the
  pre-existing `TestRealMemdSidecarMemoryRetrieval` which hangs on zombie
  memd processes unrelated to F12c).
- Validation: gofmt clean, `go vet ./...` clean, full
  `go test -count=1 ./...` green (with the `-skip` for the memd test).

### 2026-07-11: AR12e (final) - Release decisions (A-28, A-29, A-31)

- A-28: `package.json` engines bumped from `node >=18` to `node >=24`,
  matching `agent-browser`'s requirement. npm installs on Node 18-23 now
  fail with a clear engines error instead of silently installing a broken
  dependency.
- A-29: `package.json` `os` array no longer includes `android`.
  `scripts/postinstall.mjs` no longer maps android to a Linux binary; it
  fails with a clear message pointing to source-build. `docs/INSTALL.md`
  annotates the Termux/Android section as unsupported.
- A-31: Added `.github/workflows/release-please.yml` using
  `googleapis/release-please-action@v4` with `release-type: go`. It opens a
  release PR from conventional commits on `main`; merging it cuts a GitHub
  Release with an auto-generated changelog and tag. Binary publishing
  (cross-compile + checksum + upload) is intentionally out of scope and
  tracked separately. Also annotated `docs/PERFORMANCE.md` (Performance
  Smoke job not yet in CI) and `docs/GITHUB_ACTION.md` (action is
  source-only until releases are published).
- Implemented by a hy3:free worker subagent; parent reviewed the diff inline
  (verified the engines bump, the android removal + fail-loud path, the
  release-please config, and the doc annotations) and re-ran full
  `go test -count=1 ./...` green.
- Validation: gofmt clean, `go vet ./...` clean, full
  `go test -count=1 ./...` green.

### 2026-07-11: AR12c-2 - TUI history raw pipeline result

- `internal/tui/model.go`: the session event `content` now stores the raw
  `result.FinalAnswer` (PipelineResult JSON) instead of the formatted
  one-line summary. The transcript row `text` still uses
  `formatPipelineFinalAnswer(...)` for immediate display.
- `internal/tui/session.go`: on resume, `transcriptRowsFromSessionEvents`
  applies `formatPipelineFinalAnswer` to assistant message content, so
  resumed sessions show the formatted summary, not raw JSON.
- Test `TestResumePipelineResultFormattedOnResume`: stores a PipelineResult
  JSON as the session event content, resumes, asserts the transcript shows
  the formatted summary (not raw JSON), and verifies the stored content can
  be unmarshaled back to `schemas.PipelineResult`.
- Closes audit finding A-23 (the last non-`@needs-human` finding).
- Worker crashed before validation; parent reviewed the diff inline and
  re-ran full `go test -count=1 ./...` green.
- Validation: gofmt clean, `go vet ./...` clean, full
  `go test -count=1 ./...` green.

### 2026-07-11: A-26 - Real sidecar-to-orchestrator retrieval integration test

- Created `internal/splice/memory_integration_test.go`: a Go integration test
  that builds the real `splice-memd` binary from the `memd/` module, spawns it
  on a temp Unix socket, upserts an observation through the real HTTP handler
  + SQLite store, runs `runPass` with the real `*memd.Client` as the
  `MemoryStore`, and asserts the typed `MemoryBundle` is injected into the
  stage's `HarnessStageInput` and `selectMemory` maps it to `SelectedMemory`
  entries that flow into `CodeWriterInput.Memory`.
- Closes audit finding A-26: existing tests used mock handlers or `stubStore`,
  never the real sidecar-to-orchestrator path. The claimed Python-era file
  (`tests/integration/test_memory_sidecar_e2e.py`) never existed in the Go
  repo; this Go test replaces it.
- Skip condition: the test skips when `go build ./memd/` fails (e.g. the
  nested module is unavailable), so default CI stays green.
- Two subtests: (1) `capturingStage` verifies `MemoryBundle` injection with
  the correct `RequestingAgent` and matching observation content; (2)
  `code_writer` stage verifies `SelectedMemory` entries in the serialized
  `CodeWriterInput` JSON payload.
- Implemented by a deepseek-v4-flash worker subagent; parent reviewed the
  test inline (verified the binary build + skip logic, the real HTTP/store
  path, the `capturingStage` + `captureRequestProvider` reuse, and the
  assertion strength) and confirmed the test passes locally.
- Validation: gofmt clean, `go vet ./...` clean, full
  `go test -count=1 ./...` green.

### 2026-07-11: Trivial fixes and refactors batch

- **`internal/usage/tracker.go`**: `NewTracker` no longer panics when
  `modelregistry.DefaultRegistry()` fails. Signature changed from
  `func NewTracker(options TrackerOptions) *Tracker` to
  `func NewTracker(options TrackerOptions) (*Tracker, error)`. All callers
  updated (1 production in `internal/tui/model.go`, 4 in `tracker_test.go`).
- **`internal/tui/model.go`**: `panic(err)` for `DefaultRegistry()` failure
  replaced with `log.Fatalf` (TUI is a main-adjacent path; crashing with a
  clear message is acceptable). The `NewTracker` call's unreachable panic
  also replaced with `log.Fatalf` for consistency.
- **`Makefile`**: stale "Zero" comments fixed to "Splice"; misattributed
  AGENTS.md claim removed.
- **`README.md` + `README_ZH.md`**: `internal/memd/` status changed from
  "(in progress)" to "(complete)" (F9b is done).
- **`UPSTREAM.md`**: `internal/memd/` status changed from "(F9b, in
  progress)" to "(F9b, complete)".
- **`.cursorrules`**: created as a thin pointer to AGENTS.md (referenced by
  AGENTS.md but was missing; modeled on `CLAUDE.md`).
- **`docs/STREAM_JSON_PROTOCOL.md`**: added `step back` to the stage list;
  documented `checkpoint`, `restore`, and bare `permission` event types.
- **`SECURITY.md`**: Firecrawl pointer fixed from "see the README" to
  `internal/config/mcp_defaults.go`.
- **`docs/INSTALL.md`**: documented `ZERO_GITHUB_API` and
  `ZERO_GITHUB_BASE_URL` env vars for custom GitHub Enterprise endpoints.
- Implemented by a deepseek-v4-flash worker subagent; parent reviewed the
  diff inline (verified the `NewTracker` signature change propagated to all
  callers, replaced the remaining unreachable panic with `log.Fatalf`, and
  confirmed all docs changes are accurate) and re-ran full
  `go test -count=1 ./...` green.
- Validation: gofmt clean, `go vet ./...` clean, full
  `go test -count=1 ./...` green.

### 2026-07-11: AR12e (partial) - Packaging and release metadata

- A-04: `action.yml` passed `ZERO_REPO` and `ZERO_INSTALL_DIR` to
  `scripts/install.sh`, but the installer reads `SPLICE_REPO` and
  `SPLICE_INSTALL_DIR`. Fixed the inline env vars on the install call.
  `ZERO_VERSION` stays (the installer still reads that name; renaming is part
  of the deferred `ZERO_` env-var rename).
- A-27: `package.json` repository/homepage/bugs URLs fixed from
  `Gitlawb/zero` to `Taf0711/splice`. `package-lock.json` name fixed from
  `@gitlawb/zero` to `@taf0711/splice`.
- A-30: `.github/ISSUE_TEMPLATE/config.yml` security advisory URL fixed from
  `Gitlawb/zero` to `Taf0711/splice`.
- A-32: `.github/workflows/ci.yml` now runs `go test -race ./...` for both the
  root module and the memd sidecar, matching the Makefile's existing comment.
- `@needs-human` (NOT implemented): A-28 (Node version floor: `>=18` vs
  `agent-browser` requiring `>=24`), A-29 (Android maps to Linux binary),
  A-31 (no release workflow).
- Implemented by a deepseek-v4-flash worker subagent; parent reviewed the diff
  inline (verified the env var name mapping, the URL fixes, and the `-race`
  flag placement) and re-ran full `go test -count=1 ./...` green.
- Validation: gofmt clean, `go vet ./...` clean, full
  `go test -count=1 ./...` green.

### 2026-07-11: AR12d - memd robustness

- A-24: Added `Validate()` methods on `upsertRequest`, `searchRequest`,
  and `markReviewedRequest` in `memd/protocol.go`. The server handlers now
  call `Validate()` after JSON decode and before the store call, returning
  400 with the validation error. Rules: owner_agent non-empty, visibility
  in {private, shareable}, scope in {project, global} (default project),
  memory_type/title/content non-empty, confidence in [0,1]; query and
  requesting_agent non-empty for search; limit clamped to 100; id >= 1 for
  mark_reviewed.
- A-25: Configured the `http.Server` with ReadHeaderTimeout (10s),
  ReadTimeout (30s), WriteTimeout (30s), and MaxHeaderBytes (1 MiB). All
  body-decoding handlers wrap `r.Body` with `http.MaxBytesReader(w, r.Body,
  1<<20)`. The client `do` method now checks the status code before decoding:
  non-2xx reads the body as an error message and returns a typed error
  instead of failing with a confusing decode error.
- Tests: `memd/protocol_test.go` (new) with table-driven validation tests
  for all three request types; `memd/main_test.go` adds server-level 400
  rejection tests; `internal/memd/client_test.go` adds a non-2xx error test.
- Worker crashed mid-validation (provider error); parent fixed a test
  assertion bug (exact-match vs contains for the `markReviewedRequest` error
  message that includes the id value) and re-ran both modules green.
- Validation: gofmt clean, root and memd vet clean, memd tests green,
  internal/memd tests green, full root `go test -count=1 ./...` green.

### 2026-07-11: AR12c - TUI and protocol polish

- A-13: `sidebarHasContent()` in `internal/tui/sidebar.go` now checks
  `!m.pipeline.isEmpty()`, so the sidebar shows during pipeline runs even
  without specialists, plans, or touched files.
- A-14: `emitStageEvent` on `completed` now passes changed file paths from
  the stage output via a new `stageChangedFiles` helper in `run.go`. It
  extracts paths from `code_writer_output` and `test_generator_output`,
  deduplicates, and returns nil when neither is present. Non-completed events
  (skipped/failed/running) still pass nil.
- A-16: `internal/cli/exec_spec.go` now wires `OnReasoning`,
  `OnToolCallStart`, and `OnToolCallDelta`, matching normal exec. Spec-draft
  runs now stream reasoning and tool-call deltas to clients identically.
- A-23 deferred to AR12c-2: storing the raw `PipelineResult` JSON in session
  history instead of the formatted one-line summary touches session resume
  semantics and is a separate slice.
- Tests: `TestRunPassEmitsStageEventsWithChangedFiles` asserts the completed
  marker has 2 changed files; `TestSidebarHasContentWithPipeline` asserts
  the sidebar activates when pipeline state is non-empty.
- Implemented by a deepseek-v4-flash worker subagent; parent reviewed the diff
  inline (verified the `stageChangedFiles` helper handles both code_writer and
  test_generator, the sidebar check is additive, and the spec-draft callbacks
  match normal exec) and re-ran full `go test -count=1 ./...` green.
- Validation: gofmt clean, `go vet ./...` clean, focused and full
  `go test -count=1 ./...` green.

### 2026-07-11: AR12b - CLI correctness (flag guards, cancellation, exit codes)

- A-15: `internal/cli/exec_parse.go` now rejects `--use-spec --merge-back`
  at parse time with "--merge-back cannot be combined with --use-spec.".
  Previously the spec-draft path returned early before the merge-back
  block, silently skipping the requested merge.
- A-17: moved `runCtx, stopSignals := signalContext()` (and its
  `defer stopSignals()`) from after the worktree prepare block to before
  it, so both `deps.prepareWorktree` and `deps.mergeBackWorktree` now
  receive `runCtx` instead of `context.Background()`. Ctrl+C / SIGTERM
  now cancels worktree prepare and merge-back, not just the agent run.
- A-22: after the `switch mergeResult.Status` block in the merge-back
  path, if the status is NOT `MergeBackMerged` and NOT
  `MergeBackNoChanges`, return `exitIncomplete` (non-zero). Conflict
  and dirty-source outcomes are requested-but-unperformed merges; they
  now exit non-zero so automation that checks the exit code sees the
  merge did not happen. The worktree branch survives either way.
- Tests: `TestRunExecRejectsUseSpecWithMergeBack` (parse-time guard),
  `TestRunExecWorktreeMergeBackSkippedDirtyExitsIncomplete` (renamed
  from `...SkippedDirtyWarns`, now asserts `exitIncomplete`),
  `TestRunExecWorktreeMergeBackConflictExitsIncomplete` (new). Existing
  success and error tests unchanged.
- Implemented by a deepseek-v4-flash worker subagent; parent reviewed
  the diff inline (verified the context move preserves signal handling,
  the exit code gate is correctly placed, and no other call sites use
  `context.Background()` for worktree ops) and re-ran full
  `go test -count=1 ./...` green.
- Validation: gofmt clean, `go vet ./...` clean, focused and full
  `go test -count=1 ./...` green.

### 2026-07-11: AR12a - Canonical docs and metadata reconciliation

- `AGENTS.md` (A-34): updated the Status paragraph from "through F9a, in
  flight: F9b/F9c" to "through F9e, F10, F12a, F12b, and AR0-AR10d".
  Fixed the TUI/`agent.Run` claim to note F12b's conditional swap to
  `splicerun.Run` for non-spec-draft runs.
- `ROADMAP.md` (A-35): marked the F9 parent checkbox `[x]` since F9a-F9e
  are all complete.
- `docs/BENCHMARK.md` (A-36): added a note that `--self-correct` is inert
  under `splice exec` (deterministic pipeline) and only active in the
  TUI/`agent.Run` path.
- `docs/STREAM_JSON_PROTOCOL.md` (A-37): corrected the stage marker
  description from "a line starting with `\x00STAGE`" to "inside the
  `reasoning.delta` JSON string field".
- `scripts/install.sh` (A-38): "Install Zero" -> "Install Splice".
- `scripts/install.ps1` (A-38): "Installing Zero" -> "Installing Splice",
  "run zero" -> "run splice".
- `docs/NPM_WRAPPER_SMOKE.md` (A-38): `ZERO_INSTALL_DRY_RUN` ->
  `SPLICE_INSTALL_DRY_RUN`, `ZERO_SKIP_DOWNLOAD` ->
  `SPLICE_SKIP_DOWNLOAD`.
- `package.json` (A-27): description "Zero" -> "Splice".
- `.coderabbit.yaml` (A-33): paths `/zero` -> `/splice`, language from
  "Bun-first TypeScript" to "Go-first", test paths from `tests/**/*.ts` to
  `**/*_test.go`, TUI paths from `src/tui/**/*.tsx` to `internal/tui/**/*.go`.
- A-39 (no-em-dash rule): narrowed in AGENTS.md to apply to new and
  actively-edited text; `docs/flug-design/` archived material is exempt. No
  mechanical sweep of existing em-dashes.
- Implemented by a deepseek-v4-flash worker subagent; parent reviewed the diff
  inline and re-ran full `go test -count=1 ./...` green.
- Validation: `go test -count=1 ./...` green (no Go changes), `git diff --check`
  clean.

### 2026-07-11: AR10d - User-intervention boundary on ActionSurfaceToUser

- Added typed `SurfaceToUserAction` (`continue`/`abort`),
  `SurfaceToUserRequest` (RunID, Iteration, Reason, Evidence,
  RecentConfidences, CurrentScore, InitialScore), and
  `SurfaceToUserDecision` (Action, Message) to `internal/agent/types.go`.
- Added `OnSurfaceToUser func(ctx context.Context, req
  SurfaceToUserRequest) (SurfaceToUserDecision, error)` to `agent.Options`,
  next to `EscalationModelResolver`. When nil, the pipeline aborts with a
  clear message rather than silently retrying. Splice addition (AR10d),
  recorded in `UPSTREAM.md`.
- Orchestrator wiring in `runIterationLoop`: when
  `decision.Action == schemas.ActionSurfaceToUser`, if the callback is nil,
  return `finishAborted` with
  `"surface_to_user: <reason> (no interactive callback; aborting)"`.
  When wired, build a `SurfaceToUserRequest` (extract last 3 confidences from
  history), call the callback. `SurfaceToUserAbort` => `finishAborted` with
  `"user aborted: <message>"`. `SurfaceToUserContinue` => set revision
  context to the user's guidance, emit progress, continue. Callback errors:
  `context.Canceled` propagates; other errors `finishFailed`.
- Removed `ActionSurfaceToUser` from `isRecoveryAction` and removed the now-empty
  `isRecoveryAction` function and its call site entirely (dead code after all
  recovery actions have explicit handlers).
- Tests: 5 integration tests in `run_test.go`. The test stage triggers
  `ActionSurfaceToUser` by producing distinct content per call (avoids cycle),
  improving scores via extra passing tests (avoids plateau and regression),
  strictly decreasing confidence (0.9, 0.7, 0.5), and always 1 failing test
  (passSucceeded=false). Tests cover: nil callback aborts, continue with
  guidance, user abort, callback error (non-fatal to the pipeline as a failed
  status), and cancellation propagation.
- Bug caught during parent review: the worker's original test stage set
  `passCount = 2` at calls >= 3, which converted the failing test to passed,
  making `passSucceeded` true and completing the run at iteration 3 before
  `EvaluateTrajectory` was called. Fixed by keeping TestB always failed and
  adding extra passing tests (TestExtra0, TestExtra1) to create improving
  scores without making `passSucceeded` true.
- Implemented by a deepseek-v4-flash worker subagent (which crashed mid-run
  with a connection error before validation); parent reviewed the diff inline,
  caught and fixed the test stage bug, and re-ran full
  `go test -count=1 ./...` green.
- Validation: gofmt clean, `go vet ./...` clean, focused and full
  `go test -count=1 ./...` green.

### 2026-07-11: AR10c - Model escalation on cycle and oscillation

- Added `Escalation *StageModelConfig` (optional, `json:"escalation,omitempty"`)
  to `StageModelConfigFile` in `internal/splice/schemas/stage_model.go`, with
  validation when present. `Resolve` is unchanged (per-stage; escalation is
  resolved separately).
- Added `EscalationModelResolver func() (Provider, string, string, error)` to
  `agent.Options` in `internal/agent/types.go`, next to `StageModelResolver`.
  When nil, escalation is skipped (best-effort, non-fatal). Splice addition
  (AR10c), recorded in `UPSTREAM.md`.
- CLI wiring in `internal/cli/exec.go`: `escalationModelResolver` is built from
  `stageModelConfig.Escalation` using the same `providerProfiles` map and
  `providerCache` as `stageModelResolver`. When `Escalation` is nil, returns
  `(nil, "", "", nil)` so the orchestrator skips escalation. Wired into
  `runOptions.EscalationModelResolver`.
- Orchestrator wiring in `runIterationLoop`: tracks `escalated := false`. When
  `ActionEscalateCycleDetected` or `ActionEscalateOscillation` fires and
  `!escalated` and `options.EscalationModelResolver != nil`, calls the resolver.
  On success (non-nil provider), swaps the local `provider`, `options.Model`,
  and `options.ReasoningEffort`; sets `escalated = true`; emits progress.
  Nil resolver, nil provider, or error emits a progress note and continues
  (best-effort, non-fatal). Subsequent cycle/oscillation actions just set
  revision context (escalation fires at most once).
- Removed `ActionEscalateCycleDetected` and `ActionEscalateOscillation` from
  `isRecoveryAction` so they are handled by the new explicit block, not the
  catch-all.
- Provider precedence: `StageModelResolver` (per-stage, AR11b) still takes
  precedence over the escalated default provider for stages with explicit
  overrides. Escalation swaps the default, not per-stage config.
- Tests: validation + JSON round-trip for `Escalation` in `schemas_test.go`;
  `TestRunEscalatesOnCycle` (cycle triggers escalation at iter 2, resolver
  called once, escalated provider used for iter 3+, run aborts on max
  iterations), `TestRunEscalationNilResolverNonFatal`, and
  `TestRunEscalationErrorResolverNonFatal` in `run_test.go`. The escalation
  test records the provider received by the stage on each call and asserts
  `providers[2] == escalationProvider` so a no-op swap would fail the test
  (verified by temporarily removing the swap).
- Implemented by a deepseek-v4-flash worker subagent; parent reviewed the diff
  inline (verified the once-only guard, best-effort non-fatal paths, the
  `isRecoveryAction` change, provider-precedence correctness, and strengthened
  the escalation test to prove the provider swap actually takes effect) and
  re-ran full `go test -count=1 ./...` green.
- Validation: gofmt clean, `go vet ./...` clean, focused and full
  `go test -count=1 ./...` green.

### 2026-07-11: AR10b - Fresh step-back analysis on plateau

- Added a typed `StepBackAnalysis` schema
  (`hypothesized_root_cause`, `evidence`, `recommended_approach`,
  `confidence`) with `Validate()` to `internal/splice/schemas/agents.go`.
  It is orchestrator-level, not a pipeline stage, and does not appear in
  `StageRecord`s or `PipelineResult.Stages`.
- Added `internal/splice/stages/step_back.go` with a `StepBack(...)`
  function that makes a single-turn `submit_step_back` tool-use call via
  the existing `callToolUse` helper. System prompt
  (`prompts/step_back.md`, embedded) explains the step-back analyst role:
  diagnose root cause from a compressed report, recommend a new approach,
  do NOT write or fix code.
- `buildStepBackReport` in `run.go` constructs a bounded report from
current state: distilled intent, last 3 scores, failing test names,
  changed file paths, and the plateau reason. No full diffs, no stage
  transcripts (~500 input tokens).
- Orchestrator wiring in `runIterationLoop`: when
  `decision.Action == schemas.ActionStepBack`, instead of
  `buildRevisionContext` with full history, build the report, call
  `stages.StepBack(...)`, and set revision context to
  `"Step-back analysis: <root_cause>. Recommended approach: <approach>."`.
  The analysis replaces the iteration-history dump, so the next
code_writer
  sees a reframed problem, not a wall of numbers. Provider errors,
  validation failures, and cancellation propagate as pipeline failures
  (same contract as AR10a: stop, do not retry). A progress event
  `[step-back] root cause: <summary>` is emitted.
- Difference from AR10a: AR10a restores files to a prior snapshot
  (changes workspace state); AR10b changes the approach (changes revision
  context, keeps workspace state). They are independent.
- Integration test `TestStepBackIntegration` genuinely exercises the path:
  the plateau stage emits distinct `code_writer_output` content each call
  (so `stateHash` differs and the cycle rule does not fire) while keeping
  the test score flat (1 pass + 1 fail => score 2 every iteration), so
  the plateau rule fires `ActionStepBack` at iteration 3. The fake
  provider counts `submit_step_back` calls; the test asserts
  `stepBackCallCount >= 1`, `result.Status == "aborted"`, and abort
  reason contains "Maximum iteration count reached" (the run eventually
  hits the hard iteration limit because the score never improves after
  step-back). A previous version of the test was a false positive:
  identical output made the cycle rule fire before the plateau rule, so
  `stages.StepBack` was never actually called.
- Implemented by a kimi-k2.7-code worker subagent (AR10b test fix);
  parent reviewed the diff inline (verified distinct-hash + flat-score
  design, step-back call count assertion, no scope creep beyond
  `run_test.go`) and re-ran full `go test -count=1 ./...` green.
- Validation: gofmt clean, `go vet ./internal/splice/...` clean,
  focused and full `go test -count=1 ./...` green.

### 2026-07-10: AR10a implementation landed

- Snapshots use a temporary `GIT_INDEX_FILE` seeded from `HEAD`, stage with
  `git add -A`, write a tree, build a plumbing commit via `git commit-tree` with a
  Splice-local identity, and pin under `refs/splice/recovery/<name>/<runKey>/<iteration>`.
  The real index, `HEAD`, and workspace files are never changed during capture.
- Capture is called for iteration 0 before the first pass and after each completed
  iteration (after `ComputeScore`).
- On `ActionRollback`, `selectBestSnapshot` chooses the highest-scoring prior completed
  iteration, excluding the just-regressed iteration and iteration ≤ 0. Tie-breaking
  favors the latest iteration. Iteration 0 is a defensive fallback only when no completed
  prior snapshot exists.
- Restore verifies the current git-visible tree matches `expectedCurrentRef`, runs
  `git reset --hard <targetRef>` + `git clean -fd`, and verifies the resulting tree.
  A mismatch, cancellation, or git failure stops the pipeline and returns a named error.
- Nil `WorkspaceRecovery` plus `ActionRollback` aborts the pipeline with the message
  "rollback requires an isolated --worktree" before any further stage runs.
- Ignored files (`.gitignore`-matched) are explicitly outside the rollback set and
  remain untouched.
- No stage schema or prompt changes; no LLM or token overhead, all operations are
  local git plumbing.
- CLI seam: `iterationRecoveryForWorktree` in `internal/cli/exec.go` constructs
  `worktrees.NewIterationRecovery` only for explicit `--worktree`; in-place CLI and
  TUI pass nil. Plan mode forwards the same recovery to each task run.
- `worktrees.IterationRecovery` implements `splicerun.WorkspaceRecovery` via
  `Capture`/`Restore`. Real-git test suite covers non-mutation, tracked/untracked/included
  and ignored/excluded files, executable-mode preservation, later-untracked removal,
  mismatch refusal, cancellation, git failure, and merge-back-after-restore.
- Orchestrator integration tests force a real regression decision, verify iteration
  2 wins over regressed iteration 3, prove restore happens before iteration 4, and
  prove nil or failed restore stops before another stage runs. Capture failure and
  cancellation paths are also covered.
- Pre-commit validation is green: gofmt, diff check, full root vet, focused package
  tests, and full root tests. GitHub Actions remains the authoritative checkpoint
  gate before AR10b begins.

### 2026-07-10: AR10a implementation contract approved

- AR10a is one long green-to-green checkpoint: worktree snapshot primitives are
  not useful without the orchestrator consumer, and landing only one side would
  leave rollback misleading.
- The destructive capability is explicit. `WorkspaceRecovery` is passed as a
  Splice-owned `Run`/`RunDesignPlan` argument only when CLI exec successfully
  prepared `--worktree`; it is not added to inherited `agent.Options`, inferred
  from path shape, or enabled for the in-place TUI.
- A nil recovery plus `ActionRollback` aborts the pipeline before another stage
  runs. It never silently retries and never hard-resets the user's checkout.
- Snapshots use a temporary git index and hidden refs, leaving the worktree's
  real index, `HEAD`, and files unchanged during capture. Restore verifies the
  current git-visible tree, resets the isolated worktree to the highest-scoring
  prior completed iteration, cleans later non-ignored untracked paths, and
  verifies the result.
- Ignored files and generated caches are explicitly outside AR10a's rollback
  set and remain untouched. Session checkpoints and `FileTracker` retain their
  existing rewind/conflict roles; neither is treated as complete trajectory
  recovery.
- No stage schema or prompt changes and no LLM/token overhead. The checkpoint
  spans `internal/splice`, `internal/worktrees`, CLI/TUI call-site seams, real-git
  tests, `UPSTREAM.md`, and the normal full CI gate.

### 2026-07-10: AR11c + AR11d - Multi-model system prompt and StageRecord attribution

- Created `internal/splice/stages/prompts/pipeline_meta.md`: a short
  pipeline-level system prompt prepended to every LLM-backed stage's own
  prompt via `composeSystemPrompt(stagePrompt)`. It explains the multi-stage,
  multi-model architecture, the typed input/output contract, forward-flowing
  summaries, and the optional memory field.
- Each LLM-backed stage (code_writer, test_generator, static_analyzer,
  plan_critic) now calls `composeSystemPrompt(...)` before passing its system
  prompt to `callToolUse`.
- AR11d: `runPass` now populates `StageRecord.Model` from the resolved
  per-stage model name and `StageRecord.Provider` from
  `options.ProviderName`. These fields were already on the struct but were
  always nil; they are now non-nil when the `StageModelResolver` resolves a
  per-stage override.
- AR11 is now complete (AR11a-AR11d). Per-stage model routing is real:
  `stage-models.json` maps stage names to provider/model/effort, the
  orchestrator resolves the provider right before `stage.Run`, the system
  prompt explains the multi-model architecture, and the StageRecord captures
  which model was used. Without the config file, behavior is byte-identical.
- Implemented by Kimi K2.7 Code subagent (AR11c) with parent review;
  AR11d plumbing was done inline during the AR11b partial commit.

### 2026-07-10: AR11b - Wire per-stage provider/model/effort resolution into exec

- `agent.Options.StageModelResolver` now carries the per-stage routing hook
  added in AR11b. `internal/cli/exec.go` loads `stage-models.json` from the
  directory containing the user config, builds a lookup map over
  `resolved.Providers`, and constructs a resolver that clones the matched
  profile, overrides its model, and builds a provider via the existing
  `deps.newProvider` seam (so stored API keys are applied automatically).
- Constructed providers are cached by `provider_profile\x00model\x00reasoning_effort`
  so repeated iterations or identically configured stages do not rebuild the
  provider each time.
- Missing or invalid `stage-models.json` is non-fatal: exec logs a warning to
  stderr and continues with an empty config, leaving every stage on the default
  provider (byte-identical fallback).
- If a per-stage override references an unknown provider profile, the resolver
  returns an error; the orchestrator logs the failure and falls back to the
  default provider rather than aborting the run.
- The change is additive: without `stage-models.json` the resolver always
  returns nil and the pipeline behaves exactly as before AR11.

### 2026-07-10: AR9 - Enforce execution-plan and stage-output contracts

- Added cycle detection to `ExecutionPlan.Validate()` via DFS over the
  dependency graph (`detectCycle`), so self-dependencies and mutual cycles
  are rejected before the pipeline starts.
- `runPass` now calls `input.Validate()` after memory injection (and sets
  `bundle.RequestingAgent = stageName` proactively so a store that does not
  echo the requesting agent cannot trigger a false validation failure).
- `runPass` calls `output.Validate()` before marking a stage `StageCompleted`;
  an invalid output (empty summary, bad confidence) now records `StageFailed`
  instead of being silently accepted.
- `runExecutionPlan` calls `result.Validate()` before returning so the final
  `PipelineResult` is always structurally sound at the orchestrator boundary.
- Tests: cycle rejection (mutual + self), invalid output failing the stage,
  and existing tests pass. Implemented inline by the parent.


- Added a typed `StageUsage` struct (InputTokens, OutputTokens,
  CachedInputTokens, CacheWriteTokens, CostUSD) and a `Usage *StageUsage`
  field on `HarnessStageOutput` so LLM-backed stages report token usage through
  a single typed ledger instead of leaving `StageRecord` fields at zero.
- Added `usageFromCollected` in `stages/provider.go` to convert the
  `zeroruntime.CollectedStream.Usage` into `StageUsage`, returning nil when no
  real usage was reported so nil-memory runs stay byte-identical.
- Each LLM-backed stage (code_writer, test_generator, static_analyzer,
  plan_critic) now sets `output.Usage` from the collected stream. The
  static_analyzer threads usage from `interpretWithLLM` back to `Run`.
- `runStageWithContext` merges usage from the first (context-request) call and
  the final call so retries are accounted exactly once.
- `runPass` times each stage call and populates `StageRecord.LatencyMs`,
  `TokensInput`, `TokensOutput`, `TokensCached`, `TokensCacheWrite`, and
  `CostUSD` from `output.Usage`.
- `finishCompleted`, `finishFailed`, and `finishAborted` now sum all stage
  records into `PipelineResult.TotalTokensInput`, `TotalTokensOutput`, and
  `TotalCostUSD`.
- Known gap: `CostUSD` is always 0 because no pricing source is wired yet.
  Token counts and latency are correct and non-zero after real usage.
- Implemented inline by the parent (GPT-5.6 Sol) after the delegated child hit
  a context overflow; the work is precise metering across 8 files with no long
  tool-use loops.


- Added `schemas.SelectedMemory` and a `Memory []SelectedMemory` field with
  `json:"memory,omitempty"` to `CodeWriterInput` and `TestGeneratorInput`. The
  field is optional; Validate does not require it, and a nil or empty slice is
  omitted from the JSON payload.
- Fixed `newMemoryQuery` in `internal/splice/memory.go` to set `Scopes` to
  `["project", "global"]`, leaving `IncludePrivate` and `IncludeShareable` nil so
  the sidecar applies its default-true visibility filter. Private rows remain
  owner-only through the sidecar's requesting-agent check.
- Added `selectMemory` in `internal/splice/stages/helpers.go` to map a
  `MemoryBundle` into at most five `SelectedMemory` entries, truncating content to
  500 runes plus an ellipsis so the prompt stays bounded.
- Wired `CodeWriter.Run` and `TestGenerator.Run` to populate their typed input
  `Memory` fields from `selectMemory(input.MemoryBundle)` before marshaling the
  provider payload.
- Updated `prompts/code_writer.md` and `prompts/test_generator.md` to tell the
  model how to use the optional memory field when it is present.
- Added focused tests: `selectMemory` nil/empty handling, rune truncation,
  `CodeWriter.Run` payload presence and absence of the memory field, and a
  full-orchestrator `runPass` test proving that a stub `MemoryStore` observation
  flows into the `CodeWriterInput` consumed by the stage.
- Updated `docs/flug-design/10-structured-memory.md`, `ROADMAP.md`, and this file.
- Validation: `gofmt -l .` empty, `go vet ./internal/splice/...` clean,
  `go test -count=1 ./internal/splice/stages` passes,
  `go test -count=1 ./internal/splice` passes, and `git diff --check` is clean.

### 2026-07-10: AR6 - Register and harden deterministic Bandit execution

- Added `internal/splice/dtools/bandit.go` implementing a `tools.Tool` that runs
  Bandit via `python -m bandit -f json` on workspace-confined paths. It returns
  `StatusError` with a "not installed or not available" message when Python or
  Bandit is missing and returns a cancellation error when the context is
  canceled.
- Registered the `bandit` tool in `buildStageRegistry` when `options.Registry` is
  non-nil and does not already contain a `bandit` tool, so pipeline runs with a
  registry get a deterministic security scanner path without touching
  `internal/cli/exec.go`.
- Hardened `runBandit` in `internal/splice/stages/security_auditor.go`: an empty
  `line_range` no longer panics (the line pointer stays `&line` with zero), and
  a non-OK `RunTool` result degrades gracefully when Bandit is unavailable or
  fails the stage on permission denial.
- Added stage tests for empty `line_range`, Bandit unavailable degradation, and
  permission denial, and confirmed the existing mock-Bandit test still passes.
- Marked AR6 complete in `ROADMAP.md` and updated `MEMORY.md` Next Steps so
  AR7 is the next checkpoint.
- Validation: `gofmt -l .` empty, `go vet ./internal/splice/...` clean,
  `go test -count=1 ./internal/splice/stages` passes,
  `go test -count=1 ./internal/splice` passes, and `git diff --check` is clean.

### 2026-07-10: AR1 - Fail-loud pipeline file application with delete_file tool

- Added a scoped, prompt-gated `delete_file` tool in
  `internal/tools/delete_file.go` with `NewDeleteFileTool` and
  `NewScopedDeleteFileTool`. It rejects directories, the workspace root,
  missing paths, outside-workspace paths, and symlink traversal; forgets the
  `FileTracker` baseline after deletion; and returns a workspace-relative
  `ChangedFiles` entry with a concise summary and a removed-content diff.
- Registered `delete_file` in `CoreWriteToolsScoped`/`CoreTools`, added it to
  `MutationTargets` so rewind snapshots the deleted path, and updated the
  TUI tool display registry so `delete_file` renders as Delete/Deleting/Deleted
  with its path and diff.
- Changed `applyFileChanges` in `internal/splice/stages/code_writer.go` to
  accept the caller context and return `(schemas.FileChangeApplyResult, error)`.
  Registry-backed execution routes `create` to `write_file {path,content}`,
  `modify` to `write_file {path,content,overwrite:true}`, and `delete` to
  `delete_file {path}`. Callback errors, non-OK results, permission denials,
  cancellation, path escape, and any unapplied change return path- and
  change-type-specific errors that include tool output. The direct-filesystem
  fallback is workspace-confined, fails loudly, preserves create/modify/delete
  semantics, and never recursively deletes.
- `CodeWriter.Run` and `TestGenerator.Run` now invoke the helper for every
  requested file and propagate apply errors. They fail the stage when WorkDir is
  empty, the apply operation fails, or the number of applied changes does not
  match the number of requested changes.
- Added focused tests for the `delete_file` tool, mutation targets, TUI
  display labels, stage-level file application (create, modify, delete,
  denied permission, out-of-workspace path, cancellation, direct fallback),
  and an orchestrator test proving a denied required change records
  `schemas.StageFailed` and does not produce a completed pipeline.
- Updated `UPSTREAM.md`, `ROADMAP.md`, and `MEMORY.md` to record the new
  divergence and mark AR1 complete.
- Validation: `gofmt -l .` empty, `go vet ./...` clean,
  `go test -count=1 ./internal/tools`, `./internal/splice/stages`,
  `./internal/splice`, and `./internal/tui` all pass, and `git diff --check`
  is clean. The `internal/splice/design_runner_test.go` happy-path fixture
  was adjusted to a provider that switches repeated code_writer tasks to
  "modify" so the fail-loud create contract does not turn a multi-task
  design-plan smoke test into a false failure.
- Parent review corrections on top of the delegated patch: in registry mode
  `applyFileChanges` now delegates path confinement to the scoped tool instead
  of pre-rejecting, so `--add-dir` extra write roots keep working (regression
  caught by `TestRunAddDirDispatchForwardsGrantIntoExecScope`); a new
  `TestApplyFileChangesHonorsScopeGrantedRoot` pins that contract, including
  the ungranted fail-closed case. The direct-filesystem fallback stays
  strictly workspace-confined. The shared `execStageAwareProvider` CLI fixture
  now emits `modify` for a path it already submitted, matching a competent
  model under fail-loud create. The add-dir negative control now expects exit
  4 (incomplete): a denied required change fails the run loudly instead of
  being silently skipped, while the denied file still never lands.

### 2026-07-10: AR0 - Patch Go toolchain security pins

- Changed root `go.mod` toolchain from `go1.26.4` to `go1.26.5` while
  preserving the `go 1.25.0` language directive. `govulncheck` reported one
  reachable standard-library vulnerability under 1.26.4; clean under 1.26.5.
- Changed `memd/go.mod` to add `toolchain go1.25.12` while preserving the
  `go 1.25.0` language directive and no other changes to the module metadata.
  `govulncheck` reported 18 symbol-level standard-library findings under
  memd's pinned `go1.25.0` (no toolchain directive); clean under 1.25.12.
- Marked AR0 complete in ROADMAP.md.
- Validation: root `GOTOOLCHAIN=go1.26.5 go test -count=1 ./...` passes;
  memd `GOTOOLCHAIN=go1.25.12 go test -count=1 ./...` passes;
  `govulncheck` clean for both modules. `git diff --check` clean.
- Implemented by pi worker subagent (non-Codex lane) per delegation rules.

### 2026-07-10: Approved Track AR audit remediation plan

- Added `plans/audit-remediation-2026-07-10.md` with checkpoints AR0 through
  AR12. The first wave prioritizes toolchain security, fail-loud file changes,
  memd trust and privacy, deterministic command safety, and Bandit reliability.
- Focused Go overlay tests reproduced A-01, A-03, A-08, A-10 through A-14,
  A-19, A-20, and A-21 without modifying the working tree. Runtime probes
  reproduced first-run memd failure, empty-scope retrieval, database mode
  `0644`, action installer variable mismatch, and the Windows build failure.
- `govulncheck` found one reachable standard-library vulnerability under the
  root Go 1.26.4 toolchain and 18 symbol-level standard-library findings under
  memd Go 1.25.0. Re-scans with Go 1.26.5 and 1.25.12 were clean, establishing
  AR0's exact patch targets.
- Audit remediation precedes F12c. AR10 and AR11 are tagged `@needs-human`;
  implementers may not invent trajectory recovery or provider-routing policy.
- Delegated implementation and parallel audit work must use non-Codex smaller
  models. The parent retains planning, review, synthesis, commits, and CI gates.

### 2026-07-10: Comprehensive read-only repository audit handoff

- Preserved the full audit at
  `docs/audits/2026-07-10-full-repository-audit-handoff.md` so work can resume
  after a Pi session restart without relying on temporary delegated artifacts.
- Audit baseline: clean `d843d2f`; root format, vet, tests, selected race tests,
  nested memd validation, Linux cross-build, current CI all green. Windows
  cross-build failed at Unix-only `SysProcAttr.Setsid`. `govulncheck` was not
  installed.
- Immediate parent-confirmed concerns include false-success file application,
  current-project memd binary auto-execution, empty-scope memory retrieval plus
  no stage prompt consumption, composite-action installer env mismatch,
  first-run memd directory ordering, and database mode `0644`.
- Five delegated read-only lanes in run `r2` supplied additional static findings
  across pipeline semantics, memory, CLI/TUI/protocol, release quality, and docs.
  Their output is evidence only; focused breaking tests remain required before
  remediation.
- No product remediation or roadmap reprioritization was performed. The next
  session should approve a green-to-green remediation plan first.

### 2026-07-10: F12b - PIPELINE sidebar + conditional TUI swap to splicerun.Run

- Made the deterministic pipeline reachable from the interactive TUI.
  Previously the TUI called `agent.Run` (Zero's single-agent loop); all of
  Splice's pipeline work (stages, trajectory, budgets, memory) was
  headless-only.
- **Conditional swap:** `runAgentWithOptions` now checks
  `runOptions.specDraft`. When true, it stays on `agent.Run` (spec-draft flow
  is unchanged by design). When false, it calls `splicerun.Run` with a
  `MemoryStore` resolved via `memd.Resolve`. The nil-interface gotcha is
  handled: `var mem splicerun.MemoryStore` stays nil unless the resolved
  client is non-nil.
- **PIPELINE sidebar section:** a new `pipelinePanelState` view model in
  `internal/tui/pipeline_panel.go` (modeled on `planPanelState`) renders a
  vertical stage list with status glyphs (`✓`/`●`/`○`/`✗`/`↩`), a CURRENT
  detail block (stage name, action, progress bar), and changed-files. This
  section is added to the existing `renderContextSidebar` in `sidebar.go`
  between PLAN and FILES — additive, the existing AGENTS/PLAN/FILES/ACTIVITY
  sections are unchanged.
- **Stage marker routing:** the `OnReasoning` handler checks for the
  `\x00STAGE{...}\x00` marker (emitted by F12a's `emitStageEvent`). When
  found, it sends a `pipelineStageMarkerMsg` to the Bubble Tea update loop,\  which calls `pipeline.applyStageMarker`. Normal reasoning text falls
  through to the existing transcript handler unchanged.
- **Richer final-answer formatter:** `formatPipelineFinalAnswer` parses the
  `PipelineResult` JSON and renders `✓ completed · {tier} · {N} stages ·
  ${cost} · {tokens}k tok · {duration}s` instead of raw JSON.
- **Design principle (user directive):** additive, not replacement. The
  transcript, input bar, shortcuts, and plan panel are unchanged. Only the
  `agent.Run` call was replaced (conditionally), and the PIPELINE section was
  added to the existing sidebar.
- **Testable indirection:** the worker used `tuiSpliceRun` and
  `tuiResolveMemory` package-level variables (pointing to `splicerun.Run`
  and `memd.Resolve`) so tests can inject fakes without a real daemon.
- Delegation: implemented by gpt-5.5 via `openai-codex/gpt-5.5` (graphFile
  launch); planner reviewed the diff inline (verified the conditional swap,
  nil-interface guard, marker routing, and sidebar section placement) and
  re-ran full `go test -count=1 ./...` green.
- Validation: gofmt clean, `go vet ./...` clean, focused and full
  `go test -count=1 ./...` green.

### 2026-07-09: F12a — structured stage events via OnReasoning marker

- Extended Zero's `internal/agenteval/` harness to capture per-task
  cost/tokens/latency alongside the existing pass/fail scoring. The design
  takes the best of two harnesses: Zero's task-scoring (fixture workspaces,
  verification commands, pass/fail/blocked/error) + pi-bench's cost/token
  capture (output_tokens, cost_usd, latency, CSV output).
- **Stream-json protocol extension (prerequisite):** the `streamjson.Event`
  struct lacked `cachedInputTokens`, `cacheWriteTokens`, and
  `reasoningTokens` — only `promptTokens`/`completionTokens`/`totalTokens`
  were on the wire. Without cached/cacheWrite/reasoning,
  `modelregistry.CalculateCost` could not compute accurate cost (it would
  treat all input at full rate, overestimating cost for cache-heavy runs —
  exactly wrong for Splice). Fixed by adding three new optional fields
  (omitempty, backward-compatible) to the Event struct and populating them in
  `exec_writer.go usage()`. Documented in `STREAM_JSON_PROTOCOL.md`.
- **Harness changes:** `AgentRunResult` and `BenchmarkTaskReport` gained
  token/cost/latency fields. `BenchmarkSummary` gained `TotalCostUSD`,
  `TotalInputTokens`, `TotalOutputTokens`, `TotalCachedInputTokens`,
  `MeanCostPerTask`, `MeanCostPerPassedTask`, `MeanLatencyMs`.
  `MeanCostPerPassedTask = TotalCostUSD / PassedTasks` (cost per solved
  task, isolating efficiency from completion rate). A new
  `parseUsageFromStdout` function scans captured stdout for stream-json
  usage events and accumulates tokens across all events in the run. Cost is
  computed via `modelregistry.Registry.EstimateCost` (pricing-table lookup,
  zero LLM calls). A `WriteBenchmarkCSV` function emits per-task CSV.
- **CLI wiring:** `splice eval bench` now constructs a
  `modelregistry.DefaultRegistry()` and passes it into the harness. A new
  `--csv-output <path>` flag writes the CSV after the benchmark report.
- **Overhead:** zero on the agent run itself — usage parsing is post-hoc
  (scan captured stdout after the subprocess finishes). No extra LLM calls,
  no extra tool calls, no extra stages. Cost is a pricing-table lookup.
- **Adversarial-review findings that shaped the plan:** (1) the stream-json
  Event struct gap was found during planning review, not after implementation;
  (2) import-cycle check confirmed safe (agenteval imports modelregistry,
  zeroruntime, streamjson; none import agenteval); (3) the usage event
  payload keys are camelCase JSON tags, not Go field names — the parser
  matches the exact wire format.
- Delegation: Steps 1-3 (stream-json extension) done inline by the planner
  (glm5.2 role); Steps 4-11 (harness capture + tests + CLI) delegated to
  gpt-5.5 via `openai-codex/gpt-5.5` (graphFile launch). Planner reviewed
  the diff inline and re-ran full `go test -count=1 ./...` green.
- Validation: gofmt clean, `go vet ./...` clean, focused and full
  `go test -count=1 ./...` green.

### 2026-07-09: F9e — remaining deterministic write categories (run_config + tool_degradation)

- Completed the M7 deterministic-write set. Two new write categories land
  on top of the F9d mechanism (`extractWriteObservations` +
  `persistObservation`); no `runPass` edit was needed for the new writes.
- Category 1, run config: `runExecutionPlan` persists ONE observation at
  run start (after the registry build, before the iteration loop) when
  `mem != nil`, via a new `buildConfigObservation` in memory.go. Fields:
  `memory_type=run_config`, `topic_key=run_config` (per-project
  update-in-place, NOT per-run stacking), `owner_agent=orchestrator`,
  `visibility=shareable`, `content` is the shape only — `tier=<tier>
  stages=<comma-joined>` — and NEVER the raw `RequestIntent` (information
  minimalism, Commitment 3). `source_run_id` set, no `source_stage`.
- Category 2, tool degradation: a new `extractDegradationObservations` in
  memory.go scans a `ContextBundle.Items` for any item with `Error != nil`
  and builds one private observation per errored item
  (`memory_type=tool_degradation`,
  `topic_key=tool_degradation:<querytype>`, `owner_agent=<stageName>`,
  `visibility=private`, `content=*item.Error`, `confidence=0.5`). The
  write site is `runStageWithContext`, after `FulfillContextRequest`
  returns and `input.Context` is set, before the stage re-runs; this
  required threading `mem MemoryStore` into `runStageWithContext` as a new
  last parameter (its single call site in `runPass` already had `mem` in
  scope). The v1-deferred `get_symbol` context query is the concrete
  degradation source in-code today.
- Scope boundary (honest deferral, recorded in ROADMAP known gaps): the
  tool-not-found and permission-denied results from the
  `RegistryToolRunner` path (run.go ~590-635) are NOT persisted in F9e.
  They flow through the agent-loop tool machinery, not the orchestrator's
  `extract*` seam, so capturing them needs a write hook threaded into the
  tool runner — a larger change deliberately deferred, not silently
  dropped.
- Process note: this checkpoint took three delegation attempts. The first
  gpt-5.5 run correctly STOPPED and reverted because my spec referenced
  `plan.MaxIterations` (a field that does not exist on `ExecutionPlan`;
  the iteration cap is a runtime constant `defaultMaxIterations=5`, not a
  plan field). The corrected spec dropped the `max_iter` content
  component entirely. Two subsequent launches failed on my own
  invocation typos (a truncated `model: "g"` and a stub task body); I
  switched to launching from a graphFile, which reliably serialized the
  full model string (`openai-codex/gpt-5.5`) and full task spec. Lesson:\  the worker's stop-on-bad-spec behavior is the safety guard working as
  designed; the failures were planner input errors, not worker errors.
- Delegation: implemented by gpt-5.5 via `openai-codex/gpt-5.5` (graphFile
  launch); planner (glm5.2 role) reviewed the diff inline (verified both
  block placements: config after registry build / before iteration loop;
  degradation after context fulfill / before stage re-run; signature +
  call-site consistency) and re-ran full `go test -count=1 ./...` green.
- Tests: `run_test.go` adds `TestRunExecutionPlanPersistsConfigObservation`
  (asserts the one run_config upsert, shape content, no raw intent) and
  `TestRunStageWithContextPersistsToolDegradationObservation` (a stub
  stage returning a GET_SYMBOL context request → asserts the
  `tool_degradation:get_symbol` upsert with the real get_symbol deferred
  message; uses a nil ToolRunner since `fulfillGetSymbol` does not call
  the runner).
- Validation: gofmt clean, `go vet ./...` clean, focused and full
  `go test -count=1 ./...` green. No `MemoryRetriever` remains in any
  `.go` file.

### 2026-07-09: F9d — orchestrator deterministic writes (mechanism + discovered test command)

- Landed the deterministic write path: the orchestrator now persists
  orchestrator-observed facts to the memd sidecar via `Upsert`. This closes
  the M7 write-ordering step (deterministic writes before the deferred M8
  memory-updater agent).
- Interface evolution: `MemoryRetriever` (F9c, Search only) renamed to
  `MemoryStore` and gained `Upsert`. `*memd.Client` satisfies both methods
  implicitly, so `run.go` still never imports `internal/memd`. The `mem`
  param name is unchanged through the `Run`/`runExecutionPlan`/
  `runIterationLoop`/`runPass`/`RunDesignPlan` chain; only the type changed.
- Write mechanism in `internal/splice/memory.go`: `persistObservation`
  (non-fatal; no-op when store is nil; logs via an emit callback) and
  `extractWriteObservations(stageName, runID, workDir, output)`. `runPass`
  calls the extractor after a stage reaches `StageCompleted` (after
  `records = append`, before `priorSummaries` update, so it fires exactly
  once per successful stage and never on skipped/failed stages) and persists
  each returned observation non-fatally. Stages never write memory directly;
  the orchestrator does (Commitment 2).
- The one write in this checkpoint: the discovered test command.
  `test_runner` now surfaces `cmd` as `Data["test_command"]`; the
  orchestrator persists it as a shareable project-scoped observation
  (`memory_type=test_command`, `topic_key=test_command`,
  `owner_agent=orchestrator`, `source_run_id`/`source_stage` set,
  `confidence=1.0`). `topic_key` means the sidecar's topic_key upsert
  updates the command in place across runs rather than stacking duplicates.
- Scoping: the M7 spec names three write categories (config observations,
  discovered test command, tool-degradation events). Only the test command
  is concrete and well-grounded in the current code; the other two need
  product judgment on what's worth persisting and are deferred to F9e.
  The `extractWriteObservations` mechanism generalizes to them without
  further `runPass` edits.
- No stage consumes the injected `MemoryBundle` yet (grep-verified); these
  writes are forward-valuable corpus building for the future read path,
  not consumed in-code today. Honest framing, not a blocker.
- Delegation: implemented by gpt-5.5 via `openai-codex/gpt-5.5`; planner
  (glm5.2 role) reviewed the diff inline (verified the runPass placement:
  after StageCompleted, before priorSummaries, fires once per success) and
  re-ran full `go test -count=1 ./...` green.
- Tests: `run_test.go` `stubStore` (renamed from F9c `stubRetriever`, added
  `Upsert`) plus `TestRunPassPersistsDiscoveredTestCommand` asserting the
  full observation shape and a zero-upsert sub-case for non-test_runner
  stages. `stages_test.go` asserts `test_command` in test_runner output.
- Validation: gofmt clean, `go vet ./...` clean, focused and full
  `go test -count=1 ./...` green. No `MemoryRetriever` remains in any
  `.go` file (only in historical doc text, now annotated).

### 2026-07-09: F9c — orchestrator memory retrieval injection

- Wired the `internal/memd` sidecar client into the Splice orchestrator so
each pipeline stage receives a bounded `MemoryBundle` in its
`HarnessStageInput`. The schema field already existed (F9b-era); F9c is
the wiring.
- Seam: a nilable `MemoryStore` interface (F9c: Search only, named
  `MemoryRetriever`; F9d: renamed `MemoryStore`, added `Upsert`) in the new
  `internal/splice/memory.go`, threaded as the last param through `Run` →
  `runExecutionPlan` → `runIterationLoop` → `runPass`, and through
  `RunDesignPlan`. `*memd.Client` satisfies it implicitly, so `run.go` never
  imports `internal/memd` (import direction stays clean; the only importer
  is `internal/cli/exec.go`). The inherited `agent.Options` struct was NOT
  modified (upstream-discipline / Commitment 8).
- Injection site: in `runPass`, after the `HarnessStageInput` struct literal
  and before the stage runs, when `mem != nil` build a `MemoryQuery`
  (owner_agent=stage name, query=first 200 runes of the distilled request
  intent, project_path=work dir, limit=5; include-flags left nil so the
  sidecar applies default-true per the F9b fix) and set the returned bundle
  on the input. Memory is never load-bearing: on retrieval error, emit a
  progress note and skip injection; the run never fails because of memory.
  When `mem == nil`, nothing happens, so behavior is byte-identical to a run
  without memory.
- exec wiring: resolve the client once via `memd.Resolve(runCtx)` before the
  run. Three non-fatal cases: healthy client → pass it; `(nil, nil)` (no
  binary) → one `warning` event, pass nil; `(nil, err)` → one `warning` with
  the error, pass nil. The nil-interface gotcha (a nil `*memd.Client`
  assigned to a `MemoryStore` variable is a non-nil interface → panic)
  is handled correctly: exec declares `var memClient splicerun.MemoryStore`
  and only assigns it when the resolved client is non-nil.
- ROADMAP split: the original F9c bundled "retrieval injection AND
deterministic writes". Per the one-commit-without-"and" litmus test and
  the differing contract surface (retrieval is a read; writes are a policy
  decision needing the M7 write-set specified), F9c is now retrieval only and
  F9d is the deterministic-writes checkpoint.
- Delegation: implemented by gpt-5.5 via `openai-codex/gpt-5.5`; planner
  (glm5.2 role) reviewed the diff inline, verified the nil-interface
  handling and rune-truncation, and re-ran full `go test -count=1 ./...`
  green (83 packages, no failures).
- Tests: a stub `MemoryStore` (`stubStore`, F9c `stubRetriever` renamed
  at F9d) + `capturingStage` in `run_test.go`
  covers (a) bundle injected and query shape verified (owner=stage,
  projectPath=workDir, limit=5, rune-truncation at 200 using a multi-byte
  intent), (b) retrieval error is non-fatal and emits the skip progress, (c)
  nil retriever is byte-identical. Existing `Run`/`RunDesignPlan` call sites
  updated to pass `nil`.
- Validation: gofmt clean, `go vet ./...` clean, focused and full
  `go test -count=1 ./...` green.

### 2026-07-09: F9b — Go sidecar client in internal/memd, tests, and a MemoryQuery contract fix

- Landed the `internal/memd` sidecar client (`client.go`, previously a draft
  that built clean but had no tests and was untracked) and added
  `internal/memd/client_test.go`.
- Client surface: `Health`, `Upsert`, `Search`, `MarkReviewed`, plus `Resolve`
  (auto-spawn `splice-memd --serve` on a default socket, with
  env/PATH/dev-checkout binary resolution) and no-op degrade (returns
  `(nil, nil)` when no binary resolves, so memory is simply off).
- Contract fix shipped with the client: `schemas.MemoryQuery.IncludePrivate`
  and `IncludeShareable` changed from `bool` to `*bool` with `omitempty`. A
  zero-value plain `bool` made the client always send `include_private:false,
  include_shareable:false`; the sidecar server decodes these as `*bool`
  defaulting to TRUE when absent, and `store.Search` short-circuits on
  `!IncludePrivate && !IncludeShareable`, so a default query silently
  returned zero results. With `*bool`+`omitempty`, nil omits the field and
  the server applies its default-true, matching the Python schema and the
  server's `searchRequest`. Blast radius verified: `MemoryQuery` had exactly
  two consumers (`internal/memd/client.go` and the schemas round-trip test);
  both are pointer-transparent, and full `go test -count=1 ./...` stayed green.
- Tests are white-box (`package memd`) over a Unix-socket httptest harness.
  Initial harness used `filepath.Join(t.TempDir(), "mem.sock")`, which on
  macOS exceeded the ~104-char unix-socket address limit (SUN_LEN) for the
  longest-named subtest (`TestSearch/include_flags_nil_vs_explicit_false`),
  failing with `bind: invalid argument`. Fixed by switching
  `newTestServer` to a short `os.CreateTemp("", "memd-*.sock")` path. This
  is a path-length limit, not the sandbox TCP-bind denial that blocked the
  `internal/cli` httptest work (F7c); unix sockets never hit that denial.
- The include-flags regression subtest asserts both directions: a query with
  nil flags transmits as absent (server default true), and a query with
  `IncludePrivate: ptr(false)` transmits a non-nil pointer to false.
- Delegation note: the bulk of `client_test.go` was written by an opus
  worker delegation; the SUN_LEN fix was a separate delegation to
  gpt-5.5 via `openai-codex/gpt-5.5` (the codex subscription provider; the
  `deepseek` and `vercel-ai-gateway` providers are not configured here, and
  OpenRouter only exposes `~openai/gpt-latest` rather than a raw gpt-5.5 id).
  Planner review and full `go test ./...` re-validation were done inline.
- Validation: gofmt clean, `go vet ./...` clean, full `go test -count=1
  ./...` green.

### 2026-07-09: Docs reconciliation pass (partial F11)

- Rewrote `AGENTS.md` for the Splice/Go reality: two-layer project description
  (Zero substrate + pipeline), Go 1.25 style rules, Go-relevant file layout,
  status through F9a, upstream-discipline and memd-module rules. The
  Planner/Implementer role split, checkpoint-slicing guidance, and the
  incremental cadence carry over unchanged from the Python-era version.
- README.md and README_ZH.md: fixed the stale "F6 in progress" pipeline note
  (stages, `splicerun.Run`, and exec wiring are live), added `--plan` and
  `--worktree --merge-back` exec examples with behavior summaries, and
  extended the development package list with stages/run/design_runner,
  `internal/worktrees` MergeBack, `memd/`, and `internal/memd/`.
- UPSTREAM.md: replaced the "Planned divergences (F7)" section with landed
  divergences: exec-only call-site swap (TUI and spec-draft stay on
  `agent.Run` by decision), new exec flags, `worktrees.MergeBack`, protocol
  doc additions, and the `internal/memd` / `memd/` packages.
- docs/flug-design/03 and 11: marked as archived Python-era designs and added
  Splice-current Status notes (design runner and `--plan` for 03; F8b
  merge-back divergences, one-worktree-per-run and opt-in merge-back, for
  11), following the precedent set by doc 10's F9a note.
- Not included: `internal/memd/client.go` (F9b work in progress, untracked)
  stays out of this docs commit.
- F11 (full docs refresh + migration guide) remains open; this pass removes
  the known-stale claims but is not the F11 checkpoint.

### 2026-07-08: F9a — memd rebrand to Splice and CI coverage

- Rebranded the memd sidecar (imported from the Flug archive untouched by the
  F1 rename): module path `github.com/Taf0711/splice/memd`, binary
  `splice-memd`, env vars `SPLICE_MEMD_SOCKET` / `SPLICE_MEMD_DB`, data dir
  `~/Library/Application Support/splice` (macOS) or `$XDG_DATA_HOME/splice`
  with `~/.local/share/splice` fallback. No back-compat shims; there are no
  external users.
- Removed the committed `flug-memd` build artifact from git and ignored
  `memd/splice-memd` / `memd/flug-memd`.
- Added a dedicated `memd` CI job (vet, test, build in `memd/`); the nested
  Go module was previously invisible to the root workflow's `go test ./...`
  (only gofmt, which is file-based, covered it).
- Added a Splice-current naming note to the Status section of
  `docs/flug-design/10-structured-memory.md`; the Python-era `flug-memd`
  references in the doc body stay as archived behavior.
- Validation: memd gofmt/vet/test/build green locally; `splice-memd
  --version` prints the new name.

### 2026-07-08: F8b — worktree merge-back

- Added `worktrees.MergeBack` in `internal/worktrees/worktrees.go`: commits
  the worktree's changes on its detached HEAD, pins recovery branch
  `splice/<name>` to that commit, and merges into the source repo with
  `--no-ff`. Statuses: `merged`, `no_changes` (ancestry check via
  `merge-base --is-ancestor`, not SHA equality, so user commits made in the
  source repo mid-run do not fake a diff), `skipped_dirty` (source tree has
  uncommitted changes; never forced), `conflict` (merge aborted, tree
  restored). The recovery branch survives every non-merged case for manual
  merging.
- CLI: `splice exec --worktree --merge-back` opts in; `--merge-back` without
  `--worktree` is a usage error. Merged/no-changes report as a `text` event,
  skipped/conflict as a `warning`, and a merge infrastructure error is
  fail-loud: `merge_back_failed` error event and exit 1 (the run's work
  survives on the branch). Documented in `docs/STREAM_JSON_PROTOCOL.md`.
- Design decisions, diverging from the Python-era per-task design
  (`docs/flug-design/11-execution-worktrees.md`): one worktree per exec run
  rather than per plan task (tasks are sequential and fail-fast; per-task
  isolation adds lifecycle complexity without benefit until tasks
  parallelize), and merge-back is opt-in rather than default so inherited
  `--worktree` behavior stays byte-identical. Per-agent commit stacks (W3)
  and worktree rollback (W4) are recorded as a known gap in ROADMAP.md.
- Tests: real-git merge-back suite in `internal/worktrees/worktrees_test.go`
  (clean merge, no-changes, source-advanced no-changes, dirty skip, conflict
  abort with branch survival, invalid name) and CLI tests in
  `internal/cli/workflow_test.go` (flag guard, merge-back invocation and
  output, dirty warning, fail-loud error exit).
- Validation: gofmt clean, vet clean, focused cli/worktrees/splice suites
  green.

### 2026-07-08: F8a — design runner and splice exec --plan

- Landed the first F8 slice: `RunDesignPlan` in
  `internal/splice/design_runner.go` (topological task ordering with cycle
  detection, per-task pipeline runs through a `runExecutionPlan` seam
  extracted from `Run` in `internal/splice/run.go`, `DesignPlanResult` JSON
  as the final answer, in-band failure for cycles and task failures), plus
  `splice exec --plan <path.json>` wiring in `internal/cli/` (strict JSON
  decode rejecting unknown fields and trailing values) and plan-mode event
  documentation in `docs/STREAM_JSON_PROTOCOL.md`.
- Adversarial review (Codex, read-only) found no blockers and three
  moderates, all fixed before landing: (1) `--plan` with `--worktree` now
  loads the plan against the original workspace root before worktree
  substitution, so an uncommitted plan file still resolves; (2)
  `RunDesignPlan` no longer masks unrelated task errors as
  `context.Canceled` when cancellation races them, and wraps them with the
  task ID; (3) `--plan` now rejects `--resume` and `--fork`, since plan mode
  would silently ignore resumed session context.
- Review minors, deferred by decision: task IDs are only checked non-empty,
  so exotic IDs could produce ambiguous run IDs (revisit if plan files stop
  being self-authored); each task gets the full MaxTurns budget rather than
  a shared plan budget (intended for now, one task equals one pipeline run).
- Tests: `internal/splice/design_runner_test.go` (diamond/tie-break
  ordering, cycle in-band failure, happy path, fail-fast, generated plan id,
  mid-plan cancellation) and `internal/cli/exec_plan_test.go` (text and
  stream-json happy paths, usage errors including resume/fork, file errors,
  worktree-relative plan resolution).
- Validation: gofmt/vet clean, full `go test -count=1 ./...` green before
  the review fixes, focused splice+cli suites green after.
- The F7 push item from the previous Next Steps was already done: `dd9fb64`
  CI run succeeded on GitHub Actions 2026-07-08.

### 2026-07-08: F7a/F7d addendum — event contract, streaming event types, roadmap corrections

- F7a (before the test migration below): `splicerun.Run` gained the full
  `agent.Options` callback contract — per-stage `OnUsage`, live
  `OnToolCallStart`/`OnToolCallDelta` streaming via `CollectStreamWithOptions`,
  paired `OnToolCall`/`OnToolResult` for real tool executions, permission
  prompt/decision events matching `agent.Run`'s payloads, `MaxTurns` as the
  iteration cap.
- Superseding the F7c note below: exec no longer re-emits cumulative
  `tool_call` events for stage streams; stream-json gained additive
  `tool_call_start` / `tool_call_delta` (fragment-only) event types, with
  `tool_call` reserved for real executions paired with `tool_result`.
  Documented in `docs/STREAM_JSON_PROTOCOL.md` (F7d) alongside the pipeline
  execution model.
- Verified end-to-end 2026-07-08 with a live provider run: light-tier plan,
  3 stages, generated code compiles and its tests pass, event stream matches
  the documented contract.
- Corrected the rewritten `ROADMAP.md` (F1/F1a/F4/F5/G-series descriptions,
  `exec_spec.go` path, release status) and quarantined the unwired
  `optimizer.go` remnants out of the tree.

### 2026-07-08: F7 post-merge refactor and consistency audit

- Removed unused `internal/splice/optimizer.go` (`StageUsageMeter`, `SemanticCache`, `summarizeStageOutput` had no callers).
- Extracted shared splice helpers into `internal/splice/helpers.go` (`Ptr[T]`, `DerefString`, `CopyMapString`, `SummarizeStageOutput`) and updated run-time call sites.
- Replaced the unbounded `summarizeWorkspaceChanges` workspace walk with a bounded, git-aware implementation: when `.git` is present it uses `git status` / `git diff HEAD`; otherwise it walks the tree with file-count, per-file, and total-diff caps.
- Marked `DeferThreshold`, `Specialists`, `Skills`, `ModelSwitcher`, `SelfCorrect`, and `FileDiagnostics` as agent-loop-only / inert under `splice exec` in `internal/cli/exec.go`.
- Rewrote `ROADMAP.md` from scratch to reflect the Splice/Track F-Zero reality; archived the Python-era content to git history.
- Did not modify `internal/tui/**` or `internal/agent/**` and did not rename `ZERO_`-prefixed env vars (deferred).


### 2026-07-08: F7c exec test migration to deterministic pipeline semantics

- Migrated the failing `internal/cli` exec tests from single-turn echo/scripted providers to a shared stage-aware provider that emits valid `submit_code`, `submit_tests`, and `submit_analysis` tool calls.
- Preserved the `splice exec` pipeline seam (`splicerun.Run`) and wired exec output handling for `agent.Options.OnToolCallStart` and `OnToolCallDelta` so stage LLM tool-call streams reach JSON and stream-json clients as `tool_call` events.
- Retargeted escalation-adjacent tests: mid-run escalation remains agent-loop-only, so switch behavior is pinned at the `agent.Run` seam while pipeline exec tests assert CLI flag/usage payload behavior.
- Fixed pipeline callback/data bugs found by the migration: context cancellation now propagates to exec interruption handling, image-only prompts get a non-empty fallback intent, images are forwarded into LLM-backed stage requests, auto/unsafe prompt-gated tool decisions emit permission events, and registry-backed file application lets `write_file` enforce extra-root grants.
- Validation: `gofmt -l .` empty; `go vet ./...` clean; focused migrated CLI exec batch passed; `internal/tui` and `internal/splice/...` passed. Full `internal/cli` remains locally blocked by sandbox denial of `httptest.NewServer` bind (`listen tcp6 [::1]:0: bind: operation not permitted`), including the migrated project-config provider test before test logic runs.

### 2026-06-30: V2b (CLI/user signal for runner failure)

- Completed checkpoint V2b: `flug run` now exits 1 with a clear, user-actionable message when required stages have no configured agents.
- Generalized the pre-flight check in `cli.py::run_one_shot` from `code_writer`-only to all non-skippable stages in the plan. The check now runs before worktree creation, avoiding unnecessary setup on fast-fail.
- After `run_plan`, non-completed runs print a per-stage error message (`Stage '<name>' failed: <reason>`) in non-JSON mode, replacing the previous silent fallthrough.
- JSON mode already emitted `RunFailedEvent` for non-completed runs; verified it works correctly with the generalized pre-flight check.
- 3 new tests in `tests/unit/test_cli_commands.py`: `test_run_exits_1_when_required_stage_has_no_agent`, `test_run_exits_0_when_all_required_stages_have_agents`, `test_run_json_emits_run_failed_when_stage_unavailable`.
- All 608 tests pass. Ruff clean.
- ROADMAP.md V2b marked `[x]`.
- Remaining v0.1 blockers: G5 (memd binary packaging/fallback), cost-ratio acceptance.

### 2026-06-30: OpenAI subscription login (Codex client ID implementation)

- Implemented OpenAI subscription login via Codex CLI's built-in client_id.
- Fixed 4 bugs in `flug/security/oauth.py`: wrong authorize URL (`/authorize` → `/oauth/authorize`), wrong scopes (`openid api` → `openid profile email offline_access`), no client_id default (→ `app_EMoamEEZ73f0CkXaXp7hrann`), missing Codex-specific authorize params.
- Added `codex_tokens_exist()`, `import_codex_tokens()`, `_codex_auth_path()` to `flug/security/keys.py`.
- Added `codex_tokens_detected: bool = False` to `LoginState` in `flug/tui/view.py`.
- Auth method picker shows 3 options (API Key, Subscription, Import from Codex) when Codex tokens detected.
- Added `import_codex` + `provider_import_codex` action handlers in `flug/tui/app.py`.
- Fixed `print()` in `_loopback_flow()` to use `sys.stderr` to prevent TUI display corruption.
- 5 new tests in `tests/unit/test_tui_view.py`. All 605 tests pass. Ruff clean.
- Plan file: `plans/openai-subscription-codex-client-id.md`
- Source of truth: Pi agent's `packages/ai/src/utils/oauth/openai-codex.ts`
- Remaining: manual TTY test of subscription flow, visual rendering verification, end-to-end test.

### 2026-06-30: External reference additions (doc 09, doc 10)

- Added reference models F and G to `docs/09-agent-harness-principles.md`:
  the Fowler "build your own CLI coding agent with Pydantic-AI" article and the
  `agentic-cli` PyPI framework. Both are single-agent or framework-on-frameworks
  archetypes Flug diverges from (Commitments 2 and 7); both validate Flug's
  deterministic-first tools, fail-closed permission gate, and slash-command palette.
- Added a 4th deliberate-divergence point (framework-on-frameworks) to doc 09.
- Added 7 mapping-table rows and 2 Sources entries to doc 09.
- Added a memory comparison paragraph to `docs/10-structured-memory.md` contrasting
  agentic-cli's FAISS/RRF/forgetting approach with Flug's deterministic BM25/FTS5
  Track M sidecar; noted M8 as the relevant hook for future contradiction/forgetting.
- Raw notes in `flug/knowledge/build-your-own-agent-research.md` (gitignored).

### 2026-06-30: TUI safety gate, state correctness, and styled render path

- Completed the safety/UX hardening pass for the pyratatui TUI. Files changed: `flug/tui/view.py`, `flug/tui/app.py`, `flug/tui/theme.py`, `tests/unit/test_tui_view.py`, `tests/unit/test_tui_theme.py`.
- State correctness fixes:
  - Cancelling `/plan` or design-conversation provider calls now resets mode correctly via `cancel_active_operation()`.
  - Normal design conversation now shows a visible busy state (`BusyKind.RESPONDING`) while the provider is working, so the footer/prompt no longer look idle while input is rejected.
  - `resume_conversation()` from REVIEW now invalidates `pending_plan` and `pending_critique`, so the old reviewed plan cannot be accidentally approved after a revision.
  - `/approve` is now blocked when the critic sets `must_fix_before_execution=true`.
- Safety confirm gate (TUI-9):
  - `/approve` runs an async `preflight_reviewed_plan()` before execution.
  - Dirty repo, non-git repo, or destructive-request heuristics open a `ConfirmState` card instead of starting work.
  - Confirm choices: Enter proceeds, Esc cancels, `p` shows plan-only/dry-run unavailable or performs it if available.
  - Deterministic `has_destructive_intent()` and `execution_safety_reasons()` heuristics in `flug/tui/view.py`.
- Command palette improvements:
  - Filtering now treats the text after `/` as matching command bodies, so `/` then `p` selects `/plan`.
  - Ctrl-C closes the palette instead of being appended as filter text.
  - Palette is rendered as a bottom-anchored list using `rt.List`/`ListState` with a header + footer, reducing the "separate tab" feel.
  - Prompt line replaced by a palette-specific line while the palette is open (no duplicate cursor).
- First-run wizard affordances (wizard-owned status/footer/prompt) and local-provider readiness copy when default tiers still need a cloud key.
- Cancellation recovery: during execution, Esc cancels `run_task` and then runs `record_cancelled_execution_recovery()` to compute changed files/diff and surface them in state, so `/diff` works after cancellation.
- `/status` and `/diff` are now reachable during execution via the palette or direct `?`/`/` input while `run_task` is busy.
- `/status` now shows session state (mode, plan, permission) before a run exists instead of only "no runs".
- Tokenized/styled render foundation (TUI-11):
  - Added `StyledSegment`/`StyledLine` pure view-model types; view.py stays pyratatui-free.
  - `app.py` adds `style_for_token()` and helpers mapping semantic tokens to `rt.Style` via `theme.py`.
  - Header, rule, prompt, footer, status strip, breadcrumb, confirm card, palette, `/status`, and `/diff` now use styled channels.
  - Command palette uses native `rt.List` with highlight style.
- Status strip + breadcrumb (TUI-10, partial):
  - `StatusStrip` dataclass with mode, model, stage, permission, branch, meter, duration.
  - `phase_breadcrumb_styled_line()` renders conversation → plan → review → execute with active-stage highlight.
  - `compute_layout(width, height)` helper with `<60×18` compact fallback and breakpoint tests.
- Verification: full test suite passes; targeted TUI/view tests pass; ruff clean; py_compile clean.
- Post-fix agent-led audit was attempted but both evaluator agents hit Codex usage limits immediately and returned no findings. A manual static audit was performed instead and recorded in `flug/evals/live-test-results/tui-ux-audit-postfix-2026-06-30.md`. Deferred items are documented there.
- ROADMAP.md updated: TUI-9, TUI-10, TUI-11 checkboxes marked `[x]` with status notes.
- Next: finalize v0.1 blockers (G5 memd binary packaging/fallback, cost-ratio acceptance), then tag.

### 2026-07-01: Model selector audit and render fix

- Audited the Pi-style `/model` and `/models` implementation after manual TUI testing showed no visible overlay.
- Root cause: `scrollview_from_styled_lines()` passed `rt.Text` into `pyratatui.ScrollView.add_paragraph()`, but the API accepts `str`. Opening the model picker set `Overlay.MODEL`, then render construction raised `TypeError`, making the command appear to do nothing.
- Fixed the app render boundary to collapse `StyledLine` values to plain strings via `ScrollView.from_lines()` before entering pyratatui. This keeps pure view-model tokens while making the overlay render reliably.
- Added `tests/unit/test_tui_app.py` with a model-picker render smoke test that exercises the failing helper.
- Also reset overlay scroll position when opening `/model` or `/models`, and allowed those commands through the busy-command path so EXECUTING mode can target the active stage as planned.
- Validation: `python -m pytest tests/unit/ -q` passes locally. Ruff and py_compile pass for touched files.
- Remaining CI blocker is separate from the model selector: existing Codex/wizard mypy errors remain in `flug/tui/app.py`, and the local `STYLE_TOKEN_COLORS` addition in `flug/tui/theme.py` is still uncommitted relative to `HEAD`.

### 2026-07-01: Setup wizard audit findings

- Audited the `/setup` wizard after the model selector work. Current unit tests pass, but several interactive paths are not covered.
- Confirmed bug: for subscription-capable providers such as Anthropic and OpenAI, choosing `API Key` on the auth-method screen does not enter key-entry mode. The code calls `select_provider()` on a fresh empty `LoginState`, then returns a `provider_key_entry` action that `app.py` ignores.
- Confirmed bug: pressing Esc from the auth-method screen leaves `provider_login` set with `sub=IDLE`; the renderer treats that as API-key entry instead of returning to the provider list.
- Confirmed UX bug: tier and agent provider/model editors have no focus state. Up/Down always move models, Tab only advances the provider once/downward, and there is no way to move provider upward.
- Confirmed state mismatch: the wizard agent table ignores existing `config.stages` overrides, so models selected via `/model` do not appear as pinned in `/setup`. Saving the wizard also cannot clear old per-agent overrides when an agent is unpinned.
- Mypy still flags wizard integration errors in `flug/tui/app.py`, including the dynamic `_codex_detected` attribute and ambiguous action variable types.

### 2026-07-01: Setup wizard and model selector fixes implemented

- Completed the approved setup wizard and model selector repair plan.
- Fixed `/models` render boundary by collapsing `StyledLine` values to plain strings before creating a `ScrollView`; added `tests/unit/test_tui_app.py` coverage.
- Fixed setup wizard API-key auth flow for subscription-capable providers. Choosing API Key now enters key-entry mode for the selected provider.
- Fixed Esc from the wizard auth-method chooser so it returns to the provider list instead of falling into an idle key-entry-like state.
- Added real provider/model focus state to tier and agent editors. Tab toggles focus, and Up/Down move the focused list in both directions.
- Changed `/setup` agent rows to use `resolve_stage_target()`, so existing stage overrides from `/model` appear as pinned agent rows.
- Added `clear_stage_model_overrides()` to config persistence and wired wizard save so unpinned agents clear stale provider/model pins while preserving non-model stage settings.
- Fixed wizard/app mypy errors by adding explicit `WizardState.codex_tokens_detected` and separating action variable names for type narrowing.
- Validation: focused wizard/app/config tests pass, full unit suite passes, ruff passes, py_compile passes, and mypy passes for `flug/tui/app.py`, `flug/tui/view.py`, `flug/tui/theme.py`, and `flug/config.py`.

### 2026-07-01: Setup wizard model search

- Added `/models`-style search filtering to `/setup` tier and agent model editors.
- `WizardTierEdit` and `WizardAgentEdit` now track `model_filter_text` and expose `filtered_models`.
- Typing while the model list is focused filters the current provider's models; Backspace edits the filter; provider focus ignores text input.
- Provider changes clear the model filter and reset model selection, preventing stale filtered selections across providers.
- Wizard editor rendering now shows a `Model filter` prompt and a no-match placeholder.
- Added focused tests for tier filtering, no-match Enter safety, agent filtering, and provider-focus typing behavior.
- Validation: full unit suite passes, focused ruff/format/py_compile checks pass, and mypy passes for `flug/tui/view.py`.

### 2026-07-01: Pi-style thinking levels for tiers and stages

- Implemented provider-neutral thinking levels using Pi-agent semantics: `off`, `minimal`, `low`, `medium`, `high`, and `xhigh`.
- Added `flug/thinking.py` plus model catalog helpers for supported thinking levels, level clamping, and provider-specific level mapping.
- Added `thinking_level` to tier `ModelTarget` and per-stage `StageSettings`, including TOML persistence.
- Extended provider resolution so stage thinking overrides tier thinking, and escalation preserves the resolved thinking level.
- Threaded `thinking_level` through agent constructors, provider protocol methods, registry construction, and optimizer wrappers.
- Added OpenAI Responses request mapping to `reasoning.effort` with Pi-style off handling, and OpenRouter chat request mapping for reasoning effort on cataloged reasoning models.
- `/setup` tier and agent editors now expose `thinking/effort` selection, so the same model can be used by multiple tiers with different thinking levels.
- `/models` selected-detail text shows the current resolved thinking level, and quick model selection preserves the current resolved stage thinking level.
- Added tests for config round trips, resolution precedence, metadata clamping, provider request bodies, wrapper forwarding, and TUI thinking editing.
- Validation: full unit suite passes. Targeted ruff, format, py_compile, and mypy pass for the implementation surface. Full format check still reports unrelated pre-existing formatting in OpenAI subscription auth files.

### 2026-07-01: S1 - Normalize paths and suffix-match permission evaluation (F-S1)

- Implemented Checkpoint S1 from the full-system audit remediation plan (`plans/system-audit-remediation-2026-07-01.md`).
- Added `_matches(path, pattern, *, suffixes)` helper to `flug/tools/permissions.py`:
  - Case-insensitive matching (both path and pattern lowercased).
  - Uses `fnmatch.fnmatchcase` (not `fnmatch.fnmatch`).
  - When `suffixes=True`, tests each suffix of the path split on `/`.
  - When `suffixes=False`, tests the full path only.
- Rewrote `evaluate_path_permission` to use `_matches`:
  - User deny rules match with `suffixes=True` (catches nested sensitive files).
  - User allow rules match with `suffixes=False` (full path only, cannot widen access).
  - Built-in deny patterns match with `suffixes=True`.
- Updated `file_changes.py` to normalize paths via `target.relative_to(root).as_posix()` before permission evaluation, so `./.env` normalizes to `.env` and does not bypass the deny rule.
- Added 7 new tests in `test_file_changes.py` (nested env, git hook, case-variant .GIT, user allow with suffix behavior, structural check priority).
- Added 6 new tests in `test_permissions.py` (`_matches` suffix/full/case tests, deny-first rule-order test).
- Registered remediation track in ROADMAP.md with all S/P/O/U/R checkpoints listed; S1 marked complete.
- Validation: 34 tests pass (26 existing + 8 new), ruff clean, mypy clean.

### 2026-07-06: Full project audit and Track Q remediation plan

- Ran a full audit (functionality, roadmap progress, docs consistency, UI/UX gaps). Authored the implementation plan at `plans/audit-remediation-2026-07-06.md` (Track Q, checkpoints Q0-Q8 with finding IDs F-Q1..F-Q11). Read that file before doing any remediation work; it is the source of truth for scope and order.
- Live bug found (F-Q1): `get_oauth_tokens` raises pydantic ValidationError on malformed stored tokens. This machine's real keychain holds a junk entry (`flug`/`openai_oauth` = `{"access_token":"abc"}`), which crashes `flug auth status`, `list_auth_statuses`, and eval provider discovery (the cause of the 2 local `test_eval_harness` multi-provider failures). Fix is Q1 (tolerant read) plus a one-time keychain cleanup (F-Q2).
- Local suite at audit time: 3 failed, 684 passed, 4 skipped. Third failure is F-Q3: `run_bandit` checks bandit availability before the empty-paths early return, making `test_run_bandit_empty_paths` PATH-dependent. Fix is Q2 (reorder checks).
- Drift confirmed (F-Q4): S2 (`5423131`), S3 (`d7466c9`), P1 (`aedc6f9`), P2 (`b0fa770`), P3 (`56e7bf5`) are committed but unchecked in ROADMAP, and MEMORY has no entries for them. O2 is already satisfied by E2 (cache key includes provider and model). README still claims the TUI-9 confirm gate is unbuilt (F-Q5). AGENTS.md file layout omits ~16 existing files (F-Q6). Reconciliation is Q3.
- Uncommitted finished work in the tree (F-Q10): credential-store fallback (FileKeyStore, AutoKeyStore, FLUG_CREDENTIAL_STORE, keychain-call timeout) plus a wizard subscription-error fix. Landing it as two commits is Q0, strictly first. (Done same day, see the follow-up entry below.)
- Still-open remediation confirmed by spot-check: U1 (unknown slash commands reach the LLM, fix is Q4), U2 (8 fire-and-forget asyncio tasks in tui/app.py swallow exceptions, fix is Q5), O1 (no USD cap enforcement), P6 (per-call httpx client in the Anthropic adapter).
- v0.1 blockers remaining: G5 memd packaging decision (Q6, needs-human) and a published honest cost-ratio number (Q7, needs-human, blocked by Q1 on this machine).
- Continuity gap (F-Q9): `plans/` is gitignored, so `plans/system-audit-remediation-2026-07-01.md` (the F-S/P/O/U/R details) is missing on this machine. Decide in Q7 whether to track plans in git; recommendation is yes. (Resolved same day by the MacBook push, see below.)
- Environment note: this venv is Python 3.14.4 while docs and pyproject document 3.11+. Local green is not proof of CI green.

### 2026-07-06: Q0 landed, MacBook context pulled, F-Q9 resolved

- Pulled the MacBook push (`3a6528f..3148974`): `plans/` and `flug/knowledge/` are now tracked in git, recovering `plans/system-audit-remediation-2026-07-01.md` (full F-S/P/O/U/R specs) plus 18 other plan files; also CI fixes (Bandit config pointer, lint/typecheck fixes in recent test files, ruff format drift).
- Landed Track Q checkpoint Q0 as two commits rebased onto that push: `4c42e31` (FileKeyStore, AutoKeyStore, FLUG_CREDENTIAL_STORE=auto|file|keyring, 20s keychain-call timeout, CLI reports the actual credential store) and `fef56fa` (wizard subscription-login failure clears the login sub-overlay and surfaces the error as a wizard notice).
- Pre-commit cleanup on the Q0 diff: 2 ruff E501 wraps, 1 mypy no-any-return fix in `AutoKeyStore.get_password`, ruff format pass. Rebase conflict in `tests/unit/test_security_keys.py` resolved by keeping upstream's `list(results)` assertion plus the new FileKeyStore/AutoKeyStore tests.
- `plans/audit-remediation-2026-07-06.md` updated in place: Q0 marked done, F-Q9 marked resolved, Q4/Q5 now defer to the recovered 2026-07-01 plan's U1/U2 specs (this plan stays the current-code anchor and tie-breaker where they differ).
- Validation: focused suites green (`test_security_keys.py`, `test_cli_commands.py`), ruff + ruff format + mypy clean on touched files.
- Next: Q1 (tolerant OAuth token reads plus keychain cleanup on the WSL2 machine), then Q2, Q3 per the plan.

### 2026-07-06: M-R2 path parity and daemon spawn env

- Completed memd audit remediation checkpoint M-R2 from `plans/memd-audit-remediation-2026-07-06.md`.
- Go sidecar default socket directory now mirrors `flug.config.user_data_dir`: macOS uses `~/Library/Application Support/flug`, non-macOS uses `$XDG_DATA_HOME/flug` when set, otherwise `~/.local/share/flug`.
- Python `SidecarMemoryStore` now passes `FLUG_MEMD_SOCKET` to spawned `flug-memd --serve` processes and uses `start_new_session=True`, so custom socket paths propagate and the daemon is detached from the terminal process group.
- `_resolve_binary` now falls back to `flug-memd` on PATH after explicit, env, and dev-checkout binary resolution.
- Added focused tests for Go path parity, Python spawn kwargs, and PATH fallback resolution.
- Validation: memory client pytest passed (13 tests), ruff and format check passed, mypy passed for `flug/storage/memory.py`, Go vet and Go tests passed.
- Next: M-R3 daemon hygiene and memory doc reconciliation, then the binary-gated E2E validation gate.

### 2026-07-06: M-R3 daemon hygiene and memory docs reconciliation

- Completed memd audit remediation checkpoint M-R3 from `plans/memd-audit-remediation-2026-07-06.md`.
- `memd/server.go` now preserves live sockets, removes stale sockets only after a failed dial, chmods served sockets to 0600, and returns `errAlreadyRunning` for benign concurrent auto-starts.
- `runServer` treats `errAlreadyRunning` as a clean exit and the dead `.ready` marker is removed.
- `memd/store/store.go` now sets `PRAGMA busy_timeout=5000` alongside WAL and foreign keys.
- Added lifecycle tests for stale socket replacement, socket permissions, graceful shutdown, and already-running detection.
- Reconciled `docs/10-structured-memory.md`, `flug/knowledge/engram-reverse-engineering.md`, and `ROADMAP.md` with implemented memory behavior: UNINDEXED FTS metadata, no BM25 floor, schema defaults, protocol defaults, topic metadata refresh, daemon lifecycle, and Track M-R status.
- Validation: Go vet and Go tests passed, memory client pytest passed, full unit pytest passed, ruff and format check passed, and mypy passed for `flug/storage/memory.py`.
- Next: run the binary-gated M-R E2E validation gate, then resume the next approved remediation track.

### 2026-07-06: M-R E2E gate passed, Track M-R complete

- Ran the binary-gated E2E gate for the memd sidecar: 17 live checks through `SidecarMemoryStore` against a real daemon on a /tmp socket, all green (per-agent private isolation, shareable visibility, cross-project isolation with global-row fallthrough, topic revision, dedupe, zero-hit search, stats size, single-instance guard, socket 0600, persistence across daemon restart).
- Added `test_e2e_real_daemon_per_agent_memory` to `tests/unit/test_memory_client.py`, skipped automatically when `memd/flug-memd` is not built, so CI stays green and dev machines get real coverage.
- ROADMAP Track M-R fully checked off. Structured per-agent persistent memory now works as documented in `docs/10-structured-memory.md`.
- Session shape for the record: audit + M-R1 by the planning session, M-R2/M-R3 by a user-run implementer reviewed commit-by-commit via a repo monitor, E2E gate by the planning session.

### 2026-07-06: M-R E2E validation gate

> **Splice note (2026-07-11):** This was a Python-era entry. The claimed
> `tests/integration/test_memory_sidecar_e2e.py` never existed in the Go repo
> (it was a Python artifact that did not carry over). The real Go integration
> test that replaces it is `internal/splice/memory_integration_test.go`
> (A-26, landed 2026-07-11).

- Completed the Track M-R binary-gated E2E validation gate from `plans/memd-audit-remediation-2026-07-06.md`.
- Moved the real-daemon coverage into `tests/integration/test_memory_sidecar_e2e.py` and expanded it to cover per-agent private isolation, shareable visibility, cross-project isolation, explicit zero-hit search, persistence across client restart, persistence across daemon restart, and no-op degrade when no binary resolves.
- Updated `.github/workflows/memd.yml` so memd CI builds `memd/flug-memd`, installs the Python package, and runs the E2E test on Ubuntu and macOS.
- Local validation: Go vet and Go tests passed, sidecar E2E passed, memory client plus E2E pytest passed, full unit pytest passed, ruff and format check passed, and full mypy passed across `flug tests scripts`.
- Track M-R is complete. Next: resume the next approved remediation track from the current plan.

### 2026-07-06: Q1 tolerant OAuth token reads

- Completed Track Q checkpoint Q1 from `plans/audit-remediation-2026-07-06.md`.
- `get_oauth_tokens` now treats malformed or legacy stored OAuth token JSON as absent instead of raising, logging only the provider name and never token contents.
- Added regression tests for malformed JSON, missing required token fields (`{"access_token":"abc"}`), and `get_auth_status("openai")` surviving malformed subscription tokens.
- Removed the local junk `flug/openai_oauth` keychain entry; `flug auth status` now runs cleanly on this machine.
- Validation: focused security key and eval-harness tests passed, ruff and format check passed for touched files, and mypy passed for `flug/security/keys.py`.
- Next: Q2 deterministic empty-input handling in the Bandit runner.

### 2026-07-06: Q2 deterministic Bandit empty-input handling

- Completed Track Q checkpoint Q2 from `plans/audit-remediation-2026-07-06.md`.
- `run_bandit` now returns `([], "no files to scan")` for empty path input before checking Bandit availability, so the empty-input contract is deterministic and PATH-independent.
- Validation: focused security scan tests passed normally, restricted-PATH run passed with Bandit-dependent tests skipped, ruff and format check passed for touched files, and mypy passed for `flug/tools/security_scan.py`.
- Next: Q3 reconciliation pass for ROADMAP, MEMORY, README, and AGENTS.

### 2026-07-06: Q3 reconciliation entries for S2 through P3 and O2

- Recorded shipped remediation that had drifted from ROADMAP/MEMORY: S2 serialize OAuth refresh per provider (`5423131`), S3 loopback server survives stray requests and closes its socket (`d7466c9`), P1 Anthropic thinking plus sampling parameter gating (`aedc6f9`), P2 Anthropic cache-token cost accounting (`b0fa770`), and P3 OpenAI omits temperature for reasoning models (`56e7bf5`).
- Marked O2 complete because Track E2 already includes provider and model in the semantic stage-cache key in `flug/optimizer/cache.py`.
- Registered TUI-12, TUI-13, and TUI-14 as backlog items from `plans/audit-remediation-2026-07-06.md` F-Q11.
- Closed the historical `/models` versus `/setup` deferred item as resolved by the Pi-style model picker: `/models` opens its own overlay, while `/setup` opens the setup wizard.

### 2026-07-06: Q3 public documentation reconciliation

- Completed Track Q checkpoint Q3 from `plans/audit-remediation-2026-07-06.md`.
- Reconciled README status copy: the TUI safety confirmation gate and design phase are live, Anthropic plus OpenAI subscription login are wired, and `FLUG_CREDENTIAL_STORE=auto|file|keyring` is documented in Quick Start.
- Refreshed AGENTS.md file layout to include current provider, memory, worktree, TUI theme, deterministic tool, script, `tests/integration`, and `memd/` surfaces.
- Restored MEMORY.md `Current State`, `Open Questions`, `Next Steps`, and `Decision Log` structure.
- Next: Q4 unknown slash command rejection.

### 2026-07-06: Q4 unknown slash command rejection

- Completed Track Q checkpoint Q4 from `plans/audit-remediation-2026-07-06.md`, closing U1/F-U1.
- Added view-layer command helpers in `flug/tui/view.py`: `DISPATCH_ALIASES`, `known_commands()`, `unknown_command()`, and `command_rejection_notice()`.
- `flug/tui/app.py` now rejects unknown slash commands with a notice before they can become design conversation input, rejects argumented slash commands, and normalizes direct slash-command tokens for dispatch aliases such as `/model` and `/exit`.
- Added focused TUI view tests for alias recognition, typo detection, case handling, plain-chat passthrough, and rejection notices.
- Validation: focused TUI app/view tests passed, ruff and format check passed, and mypy passed for touched TUI files.
- Next: Q5 background task tracking and surfaced exceptions.

### 2026-07-06: Q5 TUI background task tracking

- Completed Track Q checkpoint Q5 from `plans/audit-remediation-2026-07-06.md`, closing U2/F-U2.
- Added `spawn_background_task()` in `flug/tui/app.py` to track fire-and-forget TUI coroutines, discard completed tasks, ignore cancellations safely, and surface failures through `state.notice`.
- Replaced the eight untracked background `asyncio.create_task(...)` call sites with labeled `spawn_background(...)` calls; `run_task = asyncio.create_task(...)` lifecycle calls are unchanged.
- TUI shutdown now cancels and gathers any remaining background tasks.
- Added focused tests for raised, successful, and cancelled background tasks in `tests/unit/test_tui_app.py`.
- Validation: focused TUI app/view tests passed, ruff and format check passed, and mypy passed for touched TUI files.
- Next: present Q6 and Q7 human decisions.

### 2026-07-06: Q6 memd platform-wheel packaging decision

- User chose Q6 option 2: ship platform wheels with the `flug-memd` binary instead of relying only on PATH fallback.
- Added platform-wheel packaging support: `flug/bin/flug-memd*` package data, `setup.py` binary distribution hook, bundled-binary resolution in `SidecarMemoryStore`, and `.gitignore` rules for generated binaries.
- Extended memd CI to copy the built Go binary into `flug/bin/`, build a platform wheel on Linux/macOS, install it in an isolated venv, verify bundled sidecar resolution, and upload the wheel artifact.
- Local validation: memory client tests, ruff, format check, mypy, Go tests/build/version check, and local wheel smoke passed.
- Next: run Q7 paid Anthropic live eval and publish measured cost-ratio evidence.

### 2026-07-06: Q7 blocker fix, macOS workspace symlink path normalization

- During the Q7 live eval on macOS, the Security Auditor stage failed with `ValueError: '/private/var/...' is not in the subpath of '/var/...'`. The root cause was that `SecurityAuditorHarnessAgent` used the raw, symlinked `work_dir` while `flug.tools.git_changes.changed_files` returned fully resolved absolute paths; `relative_to()` between the two failed.
- Fixed `SecurityAuditorHarnessAgent` to resolve `self.work_dir` in `__init__`.
- Confirmed that `flug.tools.git_changes` already resolves the project root before comparison.
- Added a regression test in `tests/unit/test_security_auditor_agent.py` that constructs a symlinked workspace and verifies both `work_dir` resolution and successful agent run.
- Also added OpenRouter pricing entries for `openai/gpt-4o` and `openai/gpt-4o-mini` so future Q7 runs can report actual costs instead of falling back to the $0 wildcard.
- Validation: focused file-changes, security-auditor, git-changes, pricing, and OpenRouter provider tests pass; ruff/format/mypy clean on touched files.
- Remaining Q7 blockers are provider/model limitations, not code bugs.

### 2026-07-06: Q7 partial live cost-ratio evidence published

- Ran the approved `scripts/run_evals.py --live --baseline-mode fair` Q7 evaluation against OpenRouter `openai/gpt-4o`.
- Fixed the OpenRouter structured-output incompatibility with OpenAI models by setting `strict: false` in `flug/providers/openrouter.py`; the previous `strict: true` payload failed because OpenAI requires `additionalProperties: false` on every nested schema.
- Result: 2 of 4 golden tasks completed (`typo-readme`, `parser-bug`); 2 harder tasks (`oauth-service`, `storage-layer`) generated code and deterministic-stage outputs but exhausted the trajectory iteration limit because tests never passed.
- Published the JSON artifact to `flug/evals/live-test-results/live-test-results-2026-07-06.json`.
- Published a markdown summary to `flug/evals/live-test-results/live-test-results-2026-07-06.md`.
- Updated README's cost-target bullet to cite the latest live-result file and the measured 0.54x token ratio on completed tasks.
- Updated `plans/audit-remediation-2026-07-06.md` Q7 status.
- Validation: OpenRouter provider tests pass, full unit suite planned next; ruff/format/mypy clean on touched files.
- Decision: treat this as satisfying the v0.1 blocker to publish an honest measured cost-ratio number. Further 4/4 success depends on a stronger model or relaxed acceptance criteria, not on hidden code bugs.

### 2026-07-06: Track F-Zero plan adversarially reviewed; project renamed to Splice; clone-not-fork repo strategy

- Adversarial review of `plans/flug-on-zero-implementation-2026-07-06.md` against a fresh clone of `gitlawb/zero` (main, same-day commit). Confirmed the major seam claims (Provider interface, CollectStreamWithOptions, SystemPrompt override, Tool/Safety interface, testrunner/verify/worktrees/zerogit APIs, specmode submit_spec, MIT license, Usage stream events).
- Corrections folded into the plan: (1) `agent.Run` has three call sites (tui/model.go:4915, cli/exec.go:513, cli/exec_spec.go:99), not one; the exec seam is a Q7 eval-honesty requirement. (2) `agent.Options` carries Registry/Sandbox/PermissionMode/FileTracker/Hooks; `splice.Run` must honor them or it bypasses the sandbox. (3) Zero's TUI already has a built-in `/plan`; crystallization command naming is `@needs-human` at F8. (4) `cmd/` has 8 binaries with names hardcoded in update/release code. (5) Module rename touches ~450 Go files (`github.com/Gitlawb/zero`, capital G). (6) Specialists/swarm bypass the pipeline by design. (7) Upstream merges will conflict on import lines; scripted re-rename planned. (8) Untracked `plans/*.md` must be committed before archiving.
- User decisions: no GitHub fork; clone Zero locally with full history, push to a NEW private repo `Taf0711/splice`; the project is renamed Splice (Flug = the archived Python paradigm). This repo will be renamed `Taf0711/flug_archive` (stays private). F1 in the plan has the exact sequence.
- Next: execute F1a (commit plans, rename repo to flug_archive, clone Zero to ~/Documents/splice, create Taf0711/splice, import carried-over assets).


### 2026-07-07: F1a/F1 — rebrand to Splice, archive Flug, clone Zero (backfilled)

- Committed untracked `plans/*.md` in the Python repo; renamed `Taf0711/flug` to `Taf0711/flug_archive`.
-Cloned `gitlawb/zero` with full history into `~/Documents/splice`, created private `Taf0711/splice`, pushed Zero history.
-Copied carried-over assets (`memd/`, docs, plans, eval fixtures, `AGENTS.md`, `MEMORY.md`, `ROADMAP.md`) with MIT attribution.
- Renamed module path `github.com/Gitlawb/zero` → `github.com/Taf0711/splice` across ~449 files; added `scripts/rename-module.sh` for upstream-merge re-runs.
- Renamed binaries `cmd/zero*` → `cmd/splice*`; updated hardcoded helper name lists in `internal/update/apply.go` and `internal/release/release.go`.
- Trimmed CI to minimal build/test/vet; deleted release/review/action workflows.
- Renamed user-visible strings and config/data paths (`zero` → `splice`, `.zero/` → `.splice/`).
- Validation: `go build ./cmd/splice` and local `go test ./...` pass. (CI was red until G1 due to a gofmt issue in `internal/release/release_test.go`.)

### 2026-07-07: F2 — port Pydantic schemas to Go (backfilled)

- Created `internal/splice/schemas/` with Go structs + manual `Validate()` methods for agents, plan, trajectory, design, events, and memory schemas.
- Added constraints: confidence bounds (0..1), non-empty required fields, task-graph / stage-DAG integrity, duplicate IDs, valid enums.
- Validation: `go test ./internal/splice/schemas/` passes.

### 2026-07-07: F3 — port classifier, planner, budget to Go (backfilled)

- `internal/splice/classifier.go`: deterministic keyword-based tier classification and risk-domain detection.
- `internal/splice/planner.go`: `BuildExecutionPlan`, `BuildExecutionPlanForTask`, `DistillRequestIntent`.
- `internal/splice/budget.go`: per-tier stage budget tables and token reserve logic.
- Validation: tests cover all tier shapes and budgets.

### 2026-07-07: F4 — port trajectory monitor to Go (backfilled)

- `internal/splice/trajectory.go`: `ComputeScore`, `EvaluateTrajectory` with hard-limit, budget, oscillation, cycle, regression, plateau, and confidence-collapse rules.
- `ComputeIterationState` produces deterministic state vectors from stage outputs and diff summaries; `stateHash` via SHA256 over sorted file entries.
- Validation: ported unit tests for every rule.

### 2026-07-07: F5 — deterministic context builder (backfilled)

- Initial port of `flug/orchestrator/context.py` to `internal/splice/context.go` with `ToolRunner` interface and query-type dispatch.
- Was mock-only at first; made production-grade in G5 by wiring to `tools.Registry`.

### 2026-07-07: G1 — fix gofmt, restore green CI

- **Status:** CI was red on every F1-F5 push because `internal/release/release_test.go` was mis-formatted during the F1 module/binary rename. The F1 commit message claiming "green CI" was incorrect; local `go test ./...` did pass throughout. Fixed now with `gofmt -w`.
- **Validation:** `gofmt -l .` now empty; `go vet ./...` and `go test ./...` pass; CI run green.
- **Files touched:** `internal/release/release_test.go`, `MEMORY.md`.

### 2026-07-07: G2 — fail-loud parity in the deterministic core

- `internal/splice/trajectory.go`: `typedPayloads` now returns an error on the first malformed JSON payload, naming the data key and slice index (mimics Python `model_validate`). `ComputeIterationState` now returns `(schemas.IterationState, error)` and propagates these failures.
- `EvaluateTrajectory` cycle rule no longer ignores repeated empty state hashes; Python has no such guard, so the Go implementation matches by escalating empty-hash repeats as cycles.
- `internal/splice/schemas/trajectory.go`: removed incorrect confidence bounds (0..1) on `TrajectoryDecision.CurrentScore` and `InitialScore`; scores are unbounded floats, consistent with Python.
- `internal/splice/budget.go`: `StageNamesForTier` and `BudgetForTier` return errors for unknown tiers instead of silently defaulting to `["code_writer"]`. `reserveForTier` default of 1,500 preserved.
- `internal/splice/planner.go`: `BuildExecutionPlan` and `BuildExecutionPlanForTask` propagate tier errors.
- Tests added/updated: malformed payload error in `trajectory_test.go`, empty-hash cycle parity test, unknown-tier errors in `splice_test.go`, inverted score-validation expectation in `schemas_test.go`.

### 2026-07-07: G3 — match Python rune and timestamp semantics

- `internal/splice/classifier.go`: byte-length thresholds replaced with `utf8.RuneCountInString`, matching Python's character counting.
- `internal/splice/planner.go`: `DistillRequestIntent` now slices `[]rune` and right-trims Unicode whitespace before appending `"..."`, preventing broken runes and matching Python `.rstrip()`.
- `internal/splice/trajectory.go`: `ComputeIterationState` keeps sub-second timestamp precision (`float64(time.Now().UnixNano())/1e9`) and passes caller-supplied timestamps through as `float64` without truncation.
- Tests: multi-byte emoji request confirms rune-count classification threshold; `DistillRequestIntent` output validated as valid UTF-8 with ellipsis.

### 2026-07-07: G4 — JSON round-trip coverage for all schema types

- Added `TestJSONRoundTrip` in `internal/splice/schemas/schemas_test.go` covering every struct that implements `Validate()` (54 total) across agents, design, events, memory, plan, and trajectory.
- Each case: `json.Marshal` → unmarshal into a fresh value → `Validate()` passes → `reflect.DeepEqual` matches original.
- `map[string]any` fields populated only with JSON-native values (`float64`, `string`, `bool`, nested maps/slices) to avoid `int`→`float64` round-trip asymmetry.
- Entitlement/Severity-typed map keys round-trip via `encoding/json` string-conversion rules; no extra marshaler needed.

### 2026-07-07: G5 — wire context builder to Zero's real tool registry

- Replaced the mock-only `ToolRunner` contract with `ToolResult` (OK/Output/Truncated/Meta) and added `RegistryToolRunner` wrapping `*tools.Registry`.
- `FulfillContextRequest` now maps all six query types to real Zero tools:
  - `LIST_FILES` → `list_directory` with `recursive=true`, `max_depth=5`
  - `READ_FILE` → `read_file`
  - `OUTLINE` → deterministic grep for top-level declarations
  - `SEARCH` → `grep`, with `regexp.QuoteMeta` for literal patterns
  - `FIND_SYMBOL` → word-boundary grep
  - `GET_SYMBOL` → explicit unsupported-in-v1 error item
- Context payloads are now text-only (`{"text": ...}`) matching the tool Output shape, avoiding fragile formatting parsing.
- Added `TestFulfillContextRequestAgainstRealRegistry` exercising real `read_file`, `list_directory`, and `grep` end to end.

### 2026-07-07: G6 — process backfill: MEMORY, ROADMAP, UPSTREAM

- Backfilled MEMORY.md entries for F1a through F5 (retrospective, dated 2026-07-07).
- Added Track F-Zero section to ROADMAP.md with F1–F11 status; archived Python-era plan sections retained as context.
- Created `UPSTREAM.md` documenting divergence from `gitlawb/zero` (base commit, module rename, binary renames, CI changes, system-prompt change, new `internal/splice/` packages, planned F7 call-site swaps, sync procedure, and cadence).
- Updated both implementation-plan status tables to reflect G1–G6 complete and F6 as next step.

### 2026-07-07: G5-fix — OUTLINE scoping and LIST_FILES header count

- Review of the G1-G6 implementer work found two bugs in the G5 context
  builder that shipped green because OUTLINE had no mocked unit test.
- Bug 1 (moderate): `fulfillOutline` passed the file path under the arg key
  `readToolName` (`"read_file"`) instead of `"path"`, so grep defaulted to
  `"."` and searched the whole workspace. An OUTLINE for `main.go` leaked
  `func helper()` from `lib.go`. Fixed in `context.go` by using `"path":
  *query.Path`.
- Bug 2 (minor): `countFileLines` and `trimToFileLines` counted the
  `"Contents of <dir>:"` header as a file line, inflating counts by one and
  under-returning files under `MaxResults`. Fixed by skipping lines starting
  with `"Contents of "`. The summary now counts file lines in the trimmed
  payload, so `MaxResults=2` reports `2 file lines`.
- Added `TestFulfillContextRequestOutline` (mocked, asserts grep args map
  has `path == "main.go"` and summary says `"Pattern-based outline"`) and
  `TestFulfillContextRequestListsFilesDoesNotCountHeader`.
- Strengthened `TestFulfillContextRequestAgainstRealRegistry` to assert an
  OUTLINE for `main.go` does NOT include `helper()` or `lib.go`.
- Validation: full local gate green, new tests pass, real-registry
  reproductions re-confirmed fixed.
- Next: F6 stage agents may now start; context payloads are sound.

### 2026-07-07: F6 — port stage agents to Go

- Created `internal/splice/stages/` with a `Stage` interface and `StageOptions`.
- Ported seven agents:
  - `code_writer.go` — tool-use `submit_code`, writes files, requests default context.
  - `test_generator.go` — tool-use `submit_tests`, writes test files.
  - `static_analyzer.go` — deterministic syntax checks (Go parser, Python `py_compile`), optional LLM interpretation via `submit_analysis`.
  - `security_auditor.go` — Bandit scan over changed or bounded Python files; uses `RunTool` callback so tests can mock.
  - `test_runner.go` — runs detected or explicit test commands, classifies timeout as errored.
  - `design_conversation.go` — free-form `Respond()` and structured `Crystallize()` with `submit_design_plan`.
  - `plan_critic.go` — adversarial review via `submit_critique`.
- Embedded system prompts under `prompts/` and referenced them with `//go:embed`.
- Added `stages_test.go` with mocked-provider tests for every agent.
- Validation: `go test ./internal/splice/stages/` passes; full local gate green.

### 2026-07-10: AR2 - Remove implicit project memd sidecar execution

- Changed `resolveBinary` in `internal/memd/client.go` so binary resolution order is
  exactly: explicit `SPLICE_MEMD_BIN` env var (trusted user intent, returned as-is),
  then `splice-memd` on PATH, then disabled (empty string).
- Removed the `workDir`-relative `memd/splice-memd` development-checkout fallback and
  the now-unused `workDir` parameter from `resolveBinary`, updating its only caller
  (`Resolve`) and the function doc comment.
- Updated `internal/memd/client_test.go` to cover env precedence over PATH, PATH
  fallback when the env is empty, a malicious `<cwd>/memd/splice-memd` binary being
  ignored, and the no-binary case returning empty so `Resolve` degrades to a no-op.
- Updated `docs/flug-design/10-structured-memory.md` to document the new discovery
  order and recorded the removal of the dev-checkout cwd fallback in the Splice status
  note.
- Marked AR2 complete in `ROADMAP.md` and updated `MEMORY.md` Current State/Next Steps
  so AR3 is the next checkpoint.
- Why: opening an arbitrary project directory must never auto-execute a
  repository-provided sidecar binary (audit finding A-02). Explicit env or PATH puts
  user intent between the project and execution; a cwd-relative binary did not.
- Validation: `gofmt -l .` empty, `go vet ./internal/memd/...` clean,
  `go test -count=1 ./internal/memd` passes, `rg -n 'memd/splice-memd' internal/`
  finds no code path building a cwd-relative binary, and `git diff --check` is clean.

### 2026-07-10: AR3 - Split memd spawning by platform and restore Windows builds

- Extracted platform-specific `SysProcAttr` assignment from `spawnDaemon` in
  `internal/memd/client.go` into a helper `configureSpawn(cmd *exec.Cmd)` that
  `spawnDaemon` calls before `cmd.Start()`. Removed the direct `syscall` usage
  and import from the shared file.
- Added `internal/memd/spawn_unix.go` with build tag `//go:build !windows`
  implementing `configureSpawn` with `&syscall.SysProcAttr{Setsid: true}`.
- Added `internal/memd/spawn_windows.go` with build tag `//go:build windows`
  implementing `configureSpawn` with
  `&syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}`.
- Added `TestConfigureSpawn` in `internal/memd/client_test.go` asserting the
  helper does not panic and leaves `cmd.SysProcAttr` non-nil on the current
  platform.
- Added a `Cross-vet` step running `GOOS=windows go vet ./internal/memd/...` and
  a `Cross-build` step verifying both `GOOS=windows GOARCH=amd64 go build
  ./cmd/splice` and `GOOS=linux GOARCH=amd64 go build ./cmd/splice` in the
  `build-and-test` job in `.github/workflows/ci.yml`.
- Marked AR3 complete in `ROADMAP.md` and updated `MEMORY.md` Current State/Next
  Steps so AR4 is the next checkpoint.
- Why: `Setsid` is Unix-only; referencing it in shared code broke
  `GOOS=windows go build` (audit finding A-05). Build-tagged helpers keep Unix
  session detachment and add a Windows-compatible process-group detachment
  without changing shared logic.
- Validation: `gofmt -l .` empty, `go vet ./internal/memd/...` clean,
  `go test -count=1 ./internal/memd` passes, `GOOS=windows go vet
  ./internal/memd/...` clean, `GOOS=windows GOARCH=amd64 go build
  ./internal/memd/...` compiles, `GOOS=linux GOARCH=amd64 go build ./cmd/splice`
  compiles, and `git diff --check` is clean.

### 2026-07-10: AR4 - Secure first-run memd storage

- Added `memd/secure_unix.go` with build tag `//go:build !windows` and
  `memd/secure_windows.go` with build tag `//go:build windows`. Both files
  export `ensurePrivateDir`, `setPrivateUmask`, and `tightenDBPermissions`.
  The Unix implementation creates directories at `0700`, resets the umask
  to `077` during startup, and chmods the database plus any `-wal` / `-shm`
  companions to `0600`. The Windows implementation creates the directory and
  relies on inherited ACLs; umask and chmod are no-ops.
- Updated `runServer` in `memd/server.go` to call `ensurePrivateDir` before
  opening SQLite, set a private umask until startup finishes, and tighten
  the database file permissions after `store.New` succeeds. A permission
  error during directory setup is fatal; permission tightening logs a
  warning but does not stop the server.
- Changed the socket directory creation in `listenAndServe` from `0755` to
  `0700` so the Unix domain socket lives in a user-private directory. The
  socket file itself was already chmodded to `0600`.
- Added focused tests in `memd/main_test.go` covering creating a missing
  parent directory, tightening an existing loose directory, restricting the
  database and WAL/SHM files after a write, and reopening the store after
  close. Unix-specific mode assertions are skipped on Windows.
- Updated `docs/flug-design/10-structured-memory.md`, `MEMORY.md`, and
  `ROADMAP.md` to record the AR4 behavior and mark the checkpoint complete.
- Why: first startup failed when the data directory did not exist before
  SQLite opened it (audit finding A-06), and the directory (0755), database
  (0644), and socket directory were too broad (audit finding A-07). Creating
  and tightening storage before the database opens makes the sidecar private
  by default on Unix and no worse on Windows.
- Validation: `gofmt -l .` empty, `go vet ./memd/...` clean,
  `go test -count=1 ./memd/...` passes, `GOOS=windows go vet ./memd/...`
  clean, and `git diff --check` is clean.

### 2026-07-10: AR5 - Route deterministic test execution through the safety substrate

- Added a `Meta map[string]string` field to `stages.ToolResult` so tool-specific
  metadata (in particular `exit_code`) can flow from the registry back to the
  stage.
- Updated `internal/splice/registry.go` so `adaptToolRunner` passes `res.Meta`
  through to `stages.ToolResult` and `makeRecordedCommandCallback` forwards
  `result.Meta` when emitting tool results.
- Rewrote `internal/splice/stages/test_runner.go` to route test execution
  through the registered `bash` tool when `options.RunTool` is set:
  - `shellJoin` builds a shell-safe command string by single-quote-wrapping each
    argument and escaping internal single quotes as `'\''`.
  - Calls `RunTool(ctx, "bash", {"command": shellJoin(cmd), "cwd": workDir,
    "timeout_ms": timeout * 1000})`.
  - Callback errors are returned as stage errors.
  - `exit_code` is parsed from `res.Meta["exit_code"]`, defaulting to 0; -1
    means the command could not start. Permission-denial output fails the
    stage; "timed out" output maps exit code to 124; a non-OK result with no
    exit code is treated as a generic failure (exit 1).
  - Combined tool output is placed in `Stdout`; `Stderr` stays empty.
  - `RecordCommand` wraps the `RunTool` call unchanged for observability and
    tool callback pairing.
  - When `RunTool` is nil, the previous `runCommand` fallback is preserved for
    isolated stage tests without a registry.
- Added focused `RunTool` path tests in `internal/splice/stages/stages_test.go`
  for permission denial, auto-approved pass, test failure, timeout, and
  cancellation.
- Added an orchestrator test in `internal/splice/run_test.go` proving that
  `PermissionModeAsk` with a denying permission callback for `bash` fails the
  `test_runner` stage and leaves the pipeline incomplete, while allowed
  `write_file` calls still create files.
- Updated test workspace registries in `run_test.go` to register `bash` so the
  orchestrator tests exercise the new safety-substrate path.
- Marked AR5 complete in `ROADMAP.md` and updated `MEMORY.md` Current State /
  Next Steps so AR6 is next.
- Why: audit finding A-11 showed `TestRunner.Run` executing commands via
  `exec.CommandContext` directly, bypassing permission, sandbox, tool filters,
  and cancellation. Routing through the registered `bash` tool makes test
  commands subject to the same safety substrate as every other tool call.
- Validation: `gofmt -l .` empty, `go vet ./internal/splice/...` clean,
  `go test -count=1 ./internal/splice/stages` passes,
  `go test -count=1 ./internal/splice` passes, and `git diff --check` is clean.

### 2026-07-10: AR11c - Shared multi-model system prompt

- Added `internal/splice/stages/prompts/pipeline_meta.md` with a shared system
  prompt section that explains the multi-stage, multi-model pipeline
  architecture and the typed input/output contract. The prompt is under 125
  words and contains no em dashes.
- Embedded the meta prompt in `internal/splice/stages/provider.go` as
  `pipelineMetaPrompt` and added `composeSystemPrompt(stagePrompt string)` to
  prepend it to each stage's own system prompt.
- Wrapped the system prompt for each LLM-backed stage (code_writer,
  test_generator, static_analyzer, plan_critic) with
  `composeSystemPrompt(...)` before passing it to `callToolUse`.
- Marked AR11c complete in `ROADMAP.md`; with AR11a, AR11b, and AR11d already
  landed, AR11 is now complete and F12c is unblocked.
- Updated `MEMORY.md` Current State / Next Steps so AR10a (workspace snapshot
  + git rollback) is next.
- Validation: `gofmt -l .` empty, `go vet ./internal/splice/...` clean,
  `go test -count=1 ./internal/splice/stages` passes,
  `go test -count=1 ./internal/splice` passes, and `git diff --check` is clean.

### 2026-07-13: Ponytail-audit cleanup of splice-owned code

- One-shot whole-repo over-engineering audit of `internal/splice/`,
  `internal/memd/`, and `internal/worktrees/` (inherited Zero substrate out
  of scope). Applied 9 mechanical cuts; skipped 2 deferred-feature findings
  (`DesignConversation.Run` no-op Stage conformance, `PlanCritic`/`DesignConversation`
  unwired) because they touch reserved design-phase names referenced by the
  TUI model wizard and F15, not pure over-engineering.
- Applied: deleted dead `runRecordedCombinedOutput` (zero callers); `itoa` and
  `atoiSafe` -> `strconv.Itoa`/`strconv.Atoi`; two local `contains` ->
  `slices.Contains`; `ptrFloat` -> the generic `Ptr` already in the same
  package; inlined `readFileArgKey` (ignored its param, returned a constant);
  extracted `fileChangeArraySchema()` shared by `submit_code`/`submit_tests`;
  `CopyMapString` -> `maps.Clone`; factored `stagesForTier(tier)` so
  `BuildExecutionPlan`/`BuildExecutionPlanForTask` no longer duplicate the
  tier->stages loop.
- Net: 13 files, +52/-138 = -86 lines, 0 deps added (all replacements use
  stdlib already in go.mod). No production behavior change; all call sites
  preserved.
- Validation: gofmt clean, `go vet ./internal/splice/... ./internal/memd/...
  ./internal/worktrees/` clean, `go build ./...` clean, and
  `go test -count=1 ./internal/splice/...` green.

### 2026-07-13: F15 plan revised to D0-D6, D0 implemented

- Revised the F15 plan (`plans/design-phase-tui-wiring-2026-07-13.md`) to
  adopt the D0-D6 slicing recommended by the adversarial review. The original
  D1-D4 plan was rejected for six blockers: no lifecycle/persistence contract,
  broken stage schemas, `designMode bool` insufficient, `/plan` collision, no
  design path in `runAgentWithOptions`, and TUI owning orchestration. Key
  decisions: session events are authoritative (no global file), engine owns
  orchestration, design conversation uses read-only tools, `/crystallize`
  replaces `/plan`.
- Implemented D0 (lifecycle and persistence contract): seven design lifecycle
  event types (`design_mode_entered`, `plan_crystallized`, `critique_recorded`,
  `plan_approved`, `task_started`, `task_completed`, `task_failed`) in
  `internal/sessions/store.go`; `DesignPhase` enum and `PlanRevision` type in
  `schemas/design.go`; `ReconstructDesignState` pure function plus payload
  structs in `internal/splice/design_lifecycle.go`. The function replays raw
  session events (via `ReadEvents`, not the compaction-rehydrated stream) to
  derive design state. Fork inherits (events copied), rewind clears (events
  truncated), compaction does not delete from the raw log. No TUI changes, no
  LLM calls, zero runtime overhead. 14 table-driven tests cover all phases,
  re-crystallization, reset, rewind/fork scenarios, and malformed payloads.
- Validation: gofmt clean, `go vet ./...` clean, `go build ./...` clean,
  `go test -count=1 ./internal/splice/... ./internal/sessions/...` green.

### 2026-07-13: F15 D1 - stage contract repair

- Implemented checkpoint D1 of the revised F15 plan. Repaired the broken
  stage contracts before any TUI wiring.
- `internal/splice/stages/design_conversation.go`:
  - Changed `Respond` signature to `(ctx, provider, opts StageOptions, input)`
    and added the system message (`composeSystemPrompt(designConversationSystemPrompt)`),
    ReasoningEffort, and streaming callbacks.
  - Changed `Crystallize` signature to `(ctx, provider, opts StageOptions, input)`,
    validated input, and routed the call through `callValidatedToolUse` with the
    crystallization system prompt, retries, streaming, and usage collection.
    Added `parseDesignPlan`/`decodeDesignPlan` helpers following the `StepBack`
    pattern. `plan.Source = "conversation"` is now set BEFORE `plan.Validate()`.
  - Fixed `designPlanToolDefinition` field names: `description` -> `intent`,
    `fact` -> `statement`, and added `in_scope`, `out_of_scope`, `system_design`
    to required fields so the schema matches `DesignPlan.Validate`.
  - Added exported `DesignConversationPrompt()` accessor returning the raw
    embedded design conversation prompt for TUI injection in D2.
- `internal/splice/stages/plan_critic.go`:
  - Fixed critic tool schema: `mitigation` -> `suggested_mitigation` to match
    `schemas.Critique`.
  - Added exported `ExtractPlanCritique(output)` helper so the TUI can extract
    a typed `PlanCritique` from a `HarnessStageOutput` without type-asserting
    `map[string]any`.
- `internal/splice/stages/stages_test.go`:
  - Updated existing `Respond` and `Crystallize` tests for new signatures.
  - Added captured-request tests asserting both methods prepend the pipeline
    meta system prompt.
  - Added `TestDesignConversationCrystallizeSetsSourceBeforeValidation`
    proving an empty `Source` is filled before validation.
  - Added `TestDesignConversationCrystallizeRejectsPlanMissingInScope`
    proving the required-field schema fixes are honest.
  - Added `TestDesignConversationPrompt`.
  - Added `TestExtractPlanCritique` and `TestExtractPlanCritiqueMissingKey`.
  - Added captured-request tests verifying `plan_critic` and `submit_design_plan`
    tool schemas use `suggested_mitigation`, `intent`, and `statement`.
- Validation: gofmt clean, `git diff --check` clean, `go vet ./...` clean,
  `go build ./...` clean, `go test -count=1 ./internal/splice/stages/` green.

### 2026-07-13: F15 D2 read-only design conversation mode

- Replaced `specDraft bool` in `tuiAgentRunOptions` with a typed `tuiRunKind`
  (`pipeline`/`spec_draft`/`design`). All 7 `specDraft` references in
  `runAgentWithOptions` updated: spec-draft guards use `== tuiRunSpecDraft`,
  pipeline-only guards use `== tuiRunPipeline`, the agent.Run dispatch covers
  both `spec_draft` and `design`.
- Added `/design` and `/exec` slash commands. `/design` enters design
  conversation mode, persists a `design_mode_entered` session event, and shows
  a welcome message. `/exec` leaves design mode and either shows a message
  (no args) or runs the prompt through the pipeline.
- Design mode prompts use `agent.Run` (not `splicerun.Run`) with a read-only
  cloned registry (read_file, list_directory, grep, ask_user only), the design
  conversation system prompt via `stages.DesignConversationPrompt()`, and
  `PermissionModeAsk`. No stage model resolver, no self-correction.
- Status strip `modeLabel()` returns "design" when `designMode` is true.
- Two worker deviations from the literal task packet: `handleExecCommand`
  calls `m.launchPrompt(text)` (not `m.handleSubmit(text)` which takes no
  args), and `handleDesignCommand` calls `ensureActiveSession` before
  `appendSessionEvent` so the event has a session to persist into. Both are
  correct.
- Delegated to a fresh-context Kimi K2.7 Code worker (first attempt failed:
  fork context exceeded 262k token limit; retry with `context: "fresh"`
  succeeded). Parent reviewed the full diff inline.
- Validation: gofmt clean, `go vet ./internal/tui/` clean, `go build ./...`
  clean, `go test ./internal/tui/` green (6 new design mode tests + all
  existing), `go test ./internal/splice/...` green.

### 2026-07-13: F15 D3 design workflow engine (D3a + D3b)

- D3a: `MapDesignHistory(events)` in `internal/splice/session_history.go`.
  Pure function implementing the 8-step contract: find the last
  `design_mode_entered` event, walk forward, keep only user/assistant
  `EventMessage` turns with non-empty content, include compaction summaries as
  synthetic user messages, skip everything else (ask_user, system, tool,
  permission, usage, error, all design lifecycle events). 9 tests.
- D3b: `DesignWorkflow` engine in `internal/splice/design_workflow.go`.
  `CrystallizeAndCritique` maps session events to conversation history,
  resolves per-stage providers (via optional stage model resolver, falls back
  to default), crystallizes a `DesignPlan`, persists a `plan_crystallized`
  event (with revision increment for re-crystallization), runs the adversarial
  critic, extracts the typed critique, and persists a `critique_recorded`
  event. Critic errors still return the plan (it was already persisted).
  8 tests cover success, empty history, nil resolver, per-stage resolver,
  revision increment, critic error, and resolver-error fallback.
- The engine owns orchestration (not the TUI), per the adversarial review's
  blocker #6. The TUI will call `NewDesignWorkflow(store, sessionID, planID)`
  then `CrystallizeAndCritique(...)` in D5 wiring.
- Both D3a and D3b delegated to fresh-context Kimi K2.7 Code workers. Parent
  reviewed both diffs inline: the mapper correctly excludes non-conversation
  events, the engine correctly persists events and handles the revision
  increment via `ReconstructDesignState`.
- Validation: gofmt clean, `go vet ./...` clean, `go build ./...` clean,
  `go test ./internal/splice/...` green (17 new tests + all existing).

### 2026-07-14: F15 D4 resumable design runner

- Added `RunDesignPlanWithResume` in `internal/splice/design_runner.go` with
  `RunDesignPlanOptions{PlanID, CompletedTaskIDs, OnTaskLifecycle}`. The
  unique `PlanID` replaces the old `options.SessionID` derivation (which
  collided across plans in one session). Completed tasks are skipped without
  re-execution but still added to the outcomes list. The lifecycle callback
  fires after each task, before the next starts.
- Added `BuildExecutionPlanForTaskWithFacts` in `planner.go` that extracts
  acceptance fact statements and target paths. Acceptance facts are appended
  to the task's `RequestIntent` so the code writer sees them. The existing
  `BuildExecutionPlanForTask` is now a thin wrapper (backward compatible).
- Added `TaskResult` struct and `TaskResults []TaskResult` to
  `DesignPlanResult` so the full `PipelineResult` per task is preserved (not
  just the reduced `TaskRunOutcome`). Added `TaskLifecycleCallback` type.
- Existing `RunDesignPlan` is a backward-compatible wrapper that passes
  `PlanID: options.SessionID` — the exec.go call site and all existing tests
  are unchanged.
- D4 does NOT persist task lifecycle events (task_started/task_completed/
  task_failed). That's D5's job: the TUI persists events via the callback.
- Delegated to a fresh-context Kimi K2.7 Code worker. The worker's CLI test
  run hit a 30s environment timeout; the parent reran it locally (25s, green).
  Parent reviewed the full diff inline: the resume skip logic, callback
  firing, acceptance fact propagation, and TaskResult population are all
  correct.
- Validation: gofmt clean, `go vet ./internal/splice/... ./internal/cli/...`
  clean, `go build ./...` clean, `go test ./internal/splice/...` green (5 new
  + all existing), `go test ./internal/cli/...` green, `go test ./internal/tui/...`
  green.

### 2026-07-14: F15 D5a /crystallize TUI wiring

- Added `/crystallize` slash command. It calls
  `splicerun.NewDesignWorkflow(store, sessionID, planID).CrystallizeAndCritique`
  in a goroutine, passing the session's events, provider, and cwd. The engine
  maps history, crystallizes the plan, runs the critic, and persists
  `plan_crystallized` + `critique_recorded` events.
- The result message (`crystallizeResultMsg`) displays the plan and critique
  in the transcript, sets `pendingPlan`/`pendingCritique` on the model, and
  reloads session events. If `MustFixBeforeExecution` is true, the transcript
  shows a blocked message.
- Added `pendingPlan *schemas.DesignPlan` and `pendingCritique
  *schemas.PlanCritique` model fields for D5b's `/approve` to consume.
- Stage model resolver is nil for now (engine falls back to the active
  provider); a later polish can wire `buildStageModelResolver`.
- Delegated to a fresh-context Kimi K2.7 Code worker. Parent reviewed the diff
  inline: the async pattern (beginRun + goroutine + result message), event
  reload, and transcript formatting are correct.
- Validation: gofmt clean, `go vet ./internal/tui/` clean, `go build ./...`
  clean, `go test ./internal/tui/` green.

### 2026-07-14: F15 D5b /approve command + execution handoff

- Added `/approve` slash command. It guards on pendingPlan != nil, blocks
  when MustFixBeforeExecution is true, generates a unique planID, persists a
  `plan_approved` event, then calls `RunDesignPlanWithResume` in a goroutine.
- The `OnTaskLifecycle` callback persists `task_completed`/`task_failed`
  events to the session store as each task finishes. The TUI does NOT receive
  WorkspaceRecovery (nil, same as the existing splicerun.Run TUI path).
- The `planExecutionResultMsg` handler clears run state, exits design mode,
  displays the `DesignPlanResult` JSON in the transcript, reloads session
  events, and clears pendingPlan/pendingCritique.
- 5 tests: requires pending plan, blocked while pending, must-fix blocks,
  emits result message, displays result and clears state.
- Delegated to a fresh-context Kimi K2.7 Code worker. Parent reviewed the
  diff inline: the planID ordering (generated before plan_approved event),
  lifecycle callback event persistence, and result handler are correct.
- Validation: gofmt clean, `go vet ./internal/tui/` clean, `go build ./...`
  clean, `go test ./internal/tui/` green.

### 2026-07-14: README logo refreshed

- Replaced `docs/assets/splice-logo.png` with the exported 5a wordmark from
  `Splice CLI logo design.zip`. It has a transparent background, white letters,
  and the lime splice. The existing image reference in both README mirrors
  remains unchanged.
- Verified the RGBA PNG is 760 by 360 pixels and both README files reference
  the shared asset path.

### 2026-07-14: F15 D6 startup and project discovery (F15 complete)

- Added `reconstructDesignState` method to `design_mode.go`. Called on
  `/resume` after session events are loaded, it calls
  `splicerun.ReconstructDesignState(m.sessionEvents)` and restores
  `pendingPlan`, `pendingCritique`, and `designMode` from the reconstructed
  phase. Conversation and review phases set `designMode = true`; executing
  and completed do not (the plan was approved). Malformed events degrade
  gracefully (design state off).
- `startNewSession` now clears `designMode`, `pendingPlan`, and
  `pendingCritique` so a stale plan from a previous session cannot be
  approved after `/new`.
- 5 tests: no events, conversation phase, review phase (with plan
  reconstruction), executing phase (design mode off), and /new clears state.
- F15 is now complete: D0 (lifecycle contract) through D6 (startup
  discovery). The full design phase workflow is wired: `/design` enters
  conversation mode with read-only tools, `/crystallize` calls the engine to
  produce a typed plan + adversarial critique, `/approve` executes the plan
  with resume support and per-task lifecycle callbacks, and `/resume`
  reconstructs the design state across sessions.
- Validation: gofmt clean, `go vet ./internal/tui/` clean, `go build ./...`
  clean, `go test ./internal/tui/` green (5 new + all existing),
  `go test ./internal/splice/...` green.

### 2026-07-14: F16 functional memory plan

- Researched Engram (github.com/Gentleman-Programming/engram) as the UX
  reference. Key patterns: persistent status banner (`MEM: OK 100%`), stats
  card (sessions/observations/prompts/projects), search screen with ranked
  results, recent observations list, observation detail view.
- Diagnosed the memory problem: the wiring is correct end-to-end (integration
  test passes with SPLICE_MEMD_BIN set), but binary discovery fails silently
  (resolveBinary checks SPLICE_MEMD_BIN env then PATH; neither is set by
  default), the TUI degrades with zero visibility, and there is no
  user-facing memory surface (no /memory command, no sidebar section, no
  status indicator, Client has no Stats method).
- Plan: `plans/functional-memory-tui-2026-07-14.md`. Four checkpoints: D1
  (binary discovery + Client.Stats + make install-memd), D2 (status line
  segment + transcript notice), D3 (/memory command), D4 (sidebar section).

### 2026-07-14: F16 D1 binary discovery + Client.Stats

- Added a 3rd resolution path to `resolveBinary` in `internal/memd/client.go`:
  after SPLICE_MEMD_BIN env and PATH lookup, it checks for a sibling
  `splice-memd` binary next to the running executable (`os.Executable()` dir).
  This covers `go install` and `make build` layouts. The executable's own
  directory is trusted, not the working directory — a repo cannot plant a
  binary there unless it can write to the install directory.
- Added `MemoryStats` struct + `Client.Stats(ctx)` method that calls
  `GET /stats` and decodes total observations, by-type breakdown, and DB
  size. The server already had the endpoint; the client just never called it.
- Added `make install-memd` target to the Makefile and documented the
  sidecar in `docs/INSTALL.md` (install, manual path, socket/data dir).
- The integration test (`TestRealMemdSidecarMemoryRetrieval`) now passes
  WITHOUT setting `SPLICE_MEMD_BIN` — the sibling-binary discovery finds
  the test-built memd binary automatically. This is the first time memory
  has been functionally reachable without manual env setup.
- Tests: 2 new `TestResolveBinary` sub-tests (sibling binary found, sibling
  not executable rejected) + `TestStats` (ok, server error).
- Validation: gofmt clean, `go vet ./...` clean, `go build ./...` clean,
  `go test ./internal/memd/ ./internal/splice/...` green.

### 2026-07-14: F16 D2 memory status in the TUI status line

- Added `memoryStatus` ("active"/"off"/""), `memoryCount`, and
  `memoryNoticed` fields to the TUI model. The pipeline run goroutine
  resolves the sidecar via `tuiResolveMemory` (already happening) and now
  also calls `memClient.Stats()` to get the observation count. The status
  is carried through `agentResponseMsg` and applied in the `Update` handler
  on the main thread.
- The status line renders `🧵 N` (accent color) when active, `🧵 off`
  (muted) when off, and nothing when unknown (not yet resolved). Omitted
  on tierTiny (width < 58). The thread icon fits the "splice" name.
- A one-time transcript notice fires on the first transition: "Memory
  sidecar active: N observations stored." when going active, or "Memory
  sidecar unavailable; running without memory injection." when going from
  active to off. Gated by `memoryNoticed` so it fires at most once per
  session.
- The spec_draft and design_conversation run kinds use `agent.Run` and
  don't resolve memory, so they carry `memoryStatus: ""` (unknown) — the
  display is unchanged for those paths.
- Delegated to a fresh-context Kimi K2.7 Code worker. Parent reviewed the
  diff inline: the goroutine-to-main-thread carry via `agentResponseMsg`,
  the one-time notice gate, and the status line segment are correct.
- Validation: gofmt clean, `go vet ./internal/tui/` clean, `go build ./...`
  clean, `go test ./internal/tui/` green (5 new + all existing).

### 2026-07-14: F16 D3 /memory command

- Added `/memory` slash command with three forms: bare `/memory` (stats),
  `/memory search <query>` (FTS search, 10 results), `/memory recent`
  (10 most recent observations via wildcard search).
- Each form runs in a goroutine (5s timeout) so the UI doesn't freeze
  while resolving the sidecar. Results come back as `memoryResultMsg`
  and render in the transcript.
- When the sidecar is off, the command shows "Memory sidecar not running.
  Run 'make install-memd' or set SPLICE_MEMD_BIN." — same guidance as
  the CLI warning.
- Formatting helpers: `formatMemoryStats` (total, by-type breakdown sorted
  alphabetically, DB size humanized), `formatMemorySearchResults` (query,
  count, ranked list with type badges and truncated content),
  `formatMemoryRecent` (same list format), `humanizeBytes` (B/KB/MB).
- 6 tests: stats formatting, search results, empty search, recent list,
  subcommand parsing, byte humanization.
- Delegated to a fresh-context Kimi K2.7 Code worker. Parent reviewed the
  diff inline: the goroutine pattern with `defer cancel()`, the error
  handling, and the formatting are correct.
- Validation: gofmt clean, `go vet ./internal/tui/` clean, `go build ./...`
  clean, `go test ./internal/tui/` green (6 new + all existing).

### 2026-07-14: F16 D4 memory sidebar section (F16 complete)

- Added a `🧵 MEMORY` section to the right-hand sidebar, rendered after
  PIPELINE and before FILES. Shows observation count and top 3 types by
  count (sorted descending). Absent when memory is off or unknown (the
  status line already covers those states).
- Added `memoryByType map[string]int` to the model, carried through
  `agentResponseMsg` alongside `memoryStatus`/`memoryCount`. Populated
  from `memd.MemoryStats.ByType` during the pipeline run's sidecar
  resolution.
- `sidebarHasContent` now returns true when `memoryStatus == "active"`
  so the sidebar doesn't auto-hide during a memory-active idle stretch.
- 3 new tests: sidebar lines active (count + top types), off (nil),
  no by-type (count only).
- F16 is now complete (D1-D4). Memory is functionally reachable: the
  sidecar auto-spawns via sibling-binary discovery, the status line shows
  `🧵 N` / `🧵 off`, `/memory` shows stats/search/recent, and the sidebar
  shows a compact memory section.
- Validation: gofmt clean, `go vet ./internal/tui/` clean, `go build ./...`
  clean, `go test ./internal/tui/` green (15 memory tests + all existing).

### 2026-07-15: F17 TUI cosmetic port complete; AU1 audit cleanup

- F17 (TUI cosmetic port) is complete across 4 commits: `teal:helix`
  palette (95bc5ab), `λ` user-prompt gutter glyph (1516581), `arc`
  running-stage spinner reusing the existing tick (e058a3a), and a
  ponytail cleanup that shrunk the helix comment and deleted the 595-line
  HTML mockup (a466ef8). Recon found 6 of 9 approved toggles were already
  TUI defaults (no-ops). Plan: `plans/tui-cosmetic-port-2026-07-15.md`.
- AU1 (ponytail-audit cleanup, dead code + stdlib swaps) landed: deleted
  `marshalJSON` (stages/provider.go), `NewRegistryToolRunnerWithOptions` +
  the `options` field it alone set (registry_runner.go), `RunEvent` +
  `AsRunEvent` (schemas/events.go); swapped `containsDomain` and
  `toolListContains` for `slices.Contains` (classifier.go, exec_tools.go,
  exec.go); inlined the single-use `timeNow()` wrapper (exec_writer.go).
  ~37 lines removed, zero behavior change, zero LLM tokens. Plan:
  `plans/ponytail-audit-cleanup-2026-07-15.md`; remaining checkpoints
  AU2-AU5 ordered there.
- AU1 was implemented inline for the first 4 edits then finished by a
  fresh-context `kimi-k2.7-code` worker (sensitive deterministic cuts
  deserved the reasoning lane, not deepseek-v4-flash). Parent reviewed the
  diff and re-ran the focused splice+cli tests as a second gate. Worker
  report: `.pi-subagents/artifacts/outputs/13e1f353/cp1-finish.md`.
- Validation: gofmt clean, `go vet ./...` clean, `go build ./...` green,
  full root suite green (`-skip TestRealMemdSidecarMemoryRetrieval`), memd
  module green.

### 2026-07-15: AU2 orchestrator finish-helper merge

- Merged `finishFailed` + `finishAborted` into one `finishWithReason(runID,
  plan, records, status, reason)` in `internal/splice/run.go`. Bodies were
  identical except the Status string; 16 call sites migrated (10 failed + 6
  aborted). `finishCompleted` and `abortReason` unchanged.
- Two scout-sourced AU2 items DROPPED after parent verification (recorded in
  the plan): (1) `joinShell`/`shellJoin` — `joinShell` uses `%#v` for a
  display line vs `shellJoin` does real shell escaping; consolidating changes
  observable report output, not a pure refactor. (2) `test_generator`
  context bypass — the hand-rolled context (test_generator.go:42) deliberately
  pulls only the `code_writer` prior and omits ContextBundle items; routing
  through the shared `selectRelevantContext` would change the stage's LLM
  prompt input. Both were false "duplication" — the duplication is load-bearing.
- AU2 was implemented by a fresh-context `kimi-k2.7-code` worker; parent
  re-ran the end-to-end gate (`TestTUIPipelineEndToEndFeature` + splice/tui
  focused suite) as the second gate. Worker report:
  `.pi-subagents/artifacts/outputs/b4cc5f4f/au2-finish.md`.
- Validation: `gofmt -l .` clean (whole repo, lesson from AU1), `go vet ./...`
  clean, build green, `TestTUIPipelineEndToEndFeature` green, full root suite
  green (`-skip TestRealMemdSidecarMemoryRetrieval`).

### 2026-07-15: AU3 worktrees Options.Env yagni

- Dropped the never-populated `Options.Env` field from `worktrees.Options`
  (internal/worktrees/worktrees.go). `Prepare` now passes `nil` to
  `DefaultBaseDir`; `envValue(nil, ...)` already falls through to `os.Getenv`,
  so behavior is unchanged. `DefaultBaseDir(map)` signature stays — tests call
  it directly with literal maps (test 193/210) to exercise XDG/HOME resolution
  without touching the real env.
- Three scout-sourced AU3 items DROPPED after parent verification (recorded in
  the plan): (1) `MergeBackOptions.RunGit` — dead but mirrors the used
  `Options.RunGit` test seam; kept by user decision. (2) `resolveBinary` DI
  params — 6 test fakes actively use them (client_test.go:260-323); scout was
  wrong. (3) `sha256.Sum256` -> `runID[:8]` — scout hallucinated "runID is a
  UUID"; it is `run_<timestamp>_<6hex>` (streamjson.CreateRunID); the hash does
  real uniqueness + git-ref-safety work. Cutting it would have introduced
  ref-name collisions.
- AU3 was implemented inline (3-line struct-field removal; delegation overhead
  exceeded the work). Validation: `gofmt -l .` clean, build green, worktrees
  tests green.

### 2026-07-15: AU5 design-phase dead contract fields (@needs-human, resolved)

- Deleted 5 fields from the design-phase typed contracts that the LLM was
  asked to produce during the design conversation but that nothing consumed:
  `DesignPlan.Assumptions`, `DesignPlan.OpenQuestions`,
  `DesignPlan.SequenceDiagrams`, `DesignPlan.Wireframes`, and
  `TechnicalSpec.ObservabilityHooks`. The 4 DesignPlan properties were also
  removed from the hand-written `designPlanToolDefinition` LLM tool schema
  (lockstep struct + schema edit).
- `@needs-human` decision resolved by tracing the Flug design corpus
  (`docs/flug-design/03-design-phase.md`): the corpus intended these as
  human-in-the-loop design artifacts (mermaid sequence diagrams, ASCII
  wireframes, open-questions for the reviewer) plus a `CodeWriterInput.
  technical_spec` handoff. The Go port ported the struct fields + tool
  schema but NEVER ported the consumption: the TUI design panel
  (`internal/tui/design_mode.go:201`) renders only Epic/Requirements/InScope/
  SystemDesign; `CodeWriterInput.TechnicalSpec` is a `*string` (agents.go:95),
  not `*TechnicalSpec`, so the struct never reaches code_writer; no Validate
  rule, no stage, no test, no transcript surface reads any of the 5. They were
  "collected then dropped" since the F2 schema port. The corpus marked
  promotion of these into typed sub-agents as "Not Yet Built" — carried as
  dead struct fields, not a tracked deferral.
- Cutting wins tokens (model fills 4 fewer fields per design plan) and removes
  silent scaffolding-for-later. Re-addable in ~5 lines if a future checkpoint
  wires the consumption (design-panel render, critic review, or code-writer
  handoff). The separate latent call — properly threading `TechnicalSpec`
  (the whole struct, not just the deleted `ObservabilityHooks` field) into
  code-writer — is independent and still open.
- AU5 was implemented by a fresh-context `kimi-k2.7-code` worker (lockstep
  contract edit = reasoning lane); parent re-ran the design-plan tool-call
  tests + schemas tests as the second gate. Worker report:
  `.pi-subagents/artifacts/outputs/092a1ad5/au5-finish.md`.
- Validation: `gofmt -l .` clean, `go vet ./...` clean, build green, schemas
  + stages tests green, full root suite green (`-skip
  TestRealMemdSidecarMemoryRetrieval`).
- **Ponytail-audit track COMPLETE** (AU1-AU5). Net: dead code, stdlib swaps,
  one orchestrator helper merge, one worktrees yagni field, and the 5 dead
  design-phase contract fields removed. Three false-positives dropped in AU3,
  two in AU2, five in AU4 — verify-first discipline cut the scout's estimate
  by more than half on three consecutive checkpoints.

### 2026-07-16: AU6 planned + user-configurable pipeline direction recorded

- Traced the remaining half-wired `TechnicalSpec` seam end to end after AU5.
  Findings (all grep-verified): `technical_spec` was NEVER in the
  `designPlanToolDefinition` tool schema, so the design conversation cannot
  produce one; no authored/ingested path constructs a `DesignPlan` anywhere
  (those `Source` values are set only in tests); `CodeWriterInput.TechnicalSpec`
  (`*string`) has zero writers and zero readers; the sub-structs (`Entity`,
  `Endpoint`, `ComponentSpec`, `FilePlan`, `TestRequirement`) have no consumer
  outside `TechnicalSpec` itself. The `TechnicalSpec.Validate()` is thorough
  but unreachable in every live path. Corrects an earlier in-session claim
  that the conversation "can crystallize" a spec; it cannot.
- Decision: cut the whole cluster (AU6) rather than wire it. Wiring would be
  a real feature (tool schema + orchestrator threading + code-writer prompt +
  eval) with no consumer asking for it. User confirmed.
- User's future direction recorded in ROADMAP ("Future direction:
  user-configurable pipeline"): the current pipeline is the default, not the
  final shape; a later local web GUI (network-topology style editor) will let
  users change stage models, add/remove stages, and register custom agents.
  Prerequisite: reinforce the default pipeline first. This direction reinforces
  the AU6 cut (stage contracts will be defined by the editor, so a hardwired
  spec handoff is legacy either way) but the cut is justified on today's
  dead-code evidence alone.
- AU6 worker contract appended to `plans/ponytail-audit-cleanup-2026-07-15.md`:
  exact cut list (design.go structs + validator + DesignPlan field/nil-check,
  agents.go field, schemas_test entry, code_writer.md prompt phrase), keep
  list (`Source` enum), re-verification greps, validation commands. ~100
  lines, zero behavior change. Ready to delegate to a kimi-k2.7-code worker.
- Also fixed stale ROADMAP checkboxes: AU1 marked done, AU4 marked skipped
  with the false-positive rationale.

### 2026-07-16: AU6 — cut the dead TechnicalSpec cluster (complete)

- Cut the entire TechnicalSpec cluster, the last dormant half-wired seam from
  AU5's neighborhood. Deleted from `internal/splice/schemas/design.go`: the
  `Entity`, `Endpoint`, `ComponentSpec`, `FilePlan`, `TestRequirement`,
  `TechnicalSpec` structs and `TechnicalSpec.Validate()`; the
  `DesignPlan.TechnicalSpec` field; and the `if d.TechnicalSpec != nil {...}`
  block in `DesignPlan.Validate()`. Deleted `CodeWriterInput.TechnicalSpec`
  (`*string`, `agents.go:95`), the `TechnicalSpec{...}` entry from the
  `TestJSONRoundTrip` Validate table (`schemas_test.go`), and the stale
  "technical spec" phrase from `prompts/code_writer.md` (now references only
  revision context). Kept the `Source` enum (authored/conversation/ingested) —
  harmless and plausibly returns with the configurable-pipeline direction.
- ~100 lines removed (107 deletions, 9 insertions where the 9 are gofmt
  re-aligning the DesignPlan struct columns after the longest-typed field was
  removed). Zero LLM tokens, zero behavior change.
- Parent verification (grep-confirmed before delegating): `technical_spec` was
  never in the design conversation LLM tool schema, so the model cannot produce
  one; no authored/ingested/CLI/TUI/file path constructs a DesignPlan with a
  non-nil TechnicalSpec; the sub-structs had no consumer outside TechnicalSpec;
  the validator was unreachable in every live path. Corrects an earlier
  in-session claim that the conversation "can crystallize" a spec; it cannot.
- Implemented by a fresh-context `kimi-k2.7-code` worker; parent re-verified
  on-disk (only 4 expected files, zero residual refs, clean Validate seam,
  gofmt clean, build + schemas + stages tests green) as the second gate.
  Worker report: `.pi-subagents/artifacts/outputs/d345907a/au6-finish.md`.
- Decision reinforced by the user-configurable pipeline direction (ROADMAP
  "Future direction"): stage contracts will be defined by the pipeline editor,
  so a hardwired spec handoff in the default pipeline is legacy either way.
  Cut justified on today's dead-code evidence alone.
- **Ponytail-audit track COMPLETE** (AU1-AU6). Full tally: ~160 lines
  removed across the track, zero behavior change, zero LLM tokens. Scout
  estimates consistently halved by verify-first: AU2 22->10, AU3 13->3,
  AU4 60->14 (skipped), AU5 13->9, AU6 ~100 (all confirmed dead, no
  false positives this round).

### 2026-07-16: post-AU TUI surface fixes + GPT-5.6 catalog

Three small commits after the AU track, each addressing a "the TUI doesn't
reflect what Splice is" gap surfaced while reviewing the live build.

- `3bad0c9` Helix is now the default theme (was Zero's darkPalette). The F17
  cosmetic port added helixPalette to the registry but left the package-level
  `zeroTheme = buildTheme(darkPalette)`, so a fresh user got Zero's colors.
  Changed the initial var + `applyTheme`'s auto-resolution: `auto` on a dark
  background now resolves to `defaultDarkTheme` ("helix"), not `themeDark`
  ("dark"/Lime). Explicit "dark" still gives Lime (preserved as selectable).
  `TestApplyThemeResolution` updated to the new contract.
- `f68b960` Footer shows "executing" phase during pipeline runs. Before,
  the status line was `● ask` whether idle or mid-execution (indistinguishable).
  Now: idle -> `● ask`, design -> `● design`, executing -> `● executing · ask`
  (permission mode demotes to a muted suffix so the safety signal survives
  during test_runner). Phase granularity only (sidebar owns per-stage); the
  `designMode` check precedes `pending` so `/crystallize` stays "design" (no
  flicker). Extracted `permissionModeLabel()` for the suffix. Added
  `TestStatusLineExecutingPhase`.
  - Lesson: I implemented this without an explicit user "go" after treating a
    design clarification as agreement. User corrected: ask for confirmation
    before implementing, and delegate the slice. Both rules held for the
    GPT-5.6 commit that followed (delegated to kimi-k2.7-code worker).
- `12d28eb` Added the GPT-5.6 model family to both catalogs. The curated
  catalogs stopped at gpt-4.1 (modelregistry) / gpt-5.5 (providermodelcatalog),
  so gpt-5.6-sol was invisible in the onboarding picker and unknown to the
  cost/capability registry. Added gpt-5.6-sol ($5/$30, 1.05M ctx, flagship),
  gpt-5.6-terra ($2.50/$15, balanced), gpt-5.6-luna ($1/$6, cost-effective)
  to modelregistry/catalog.go (with vision/reasoning/JSON-mode/long-context/
  prompt-cache capabilities + upgrade chain luna->terra->sol) and to the
  chatgpt block in providermodelcatalog/catalog.go. Pricing verified against
  platform.openai.com/docs/pricing + developers.openai.com gpt-5.6-sol page.
  Reasoning efforts auto-derive from the gpt-5 name prefix.

### Open design threads (none started; all need a dated plan + adversarial review)

1. TUI/workflow redesign (umbrella): batteries-included model resolver
   (heuristic downgrade b, tier-label stage contract replacing hardcoded
   model IDs), onboarding rewrite (Option C-batteries-included: user picks
   ceiling, resolver pre-fills per-stage downgrades, editable, lands in
   planning phase), phase-adaptive layout (idle/design/executing, chat
   primary by default, pipeline-promoted + persistent plan panel as
   options), default entry phase = planning.
2. Security auditor redesign: augment (keep deterministic Bandit/gosec floor,
   add LLM security-engineer stage for gaps). Needs an eval. Moves
   security_auditor out of reservedInactiveStageNames into /stages.
3. Design-phase designer separation: pull Crystallize out of the
   DesignConversation struct into its own typed agent. Bounded checkpoint.
4. Configurable pipeline / topology editor: recorded in ROADMAP "Future
   direction", not scheduled. Prerequisite: reinforce the default pipeline
   (thread 1).

### Deferred small items

- OpenRouter providermodelcatalog block still has only 5 entries (no GPT-5.x).
  User skipped expanding it; available later. Verified OpenRouter is NOT
  coding-only (~345 text models, plus image/audio/embedding/video); there's
  no coding filter from their API, so an "all coding models" list would need
  name-heuristic matching and would still go stale.
- Two AU4 cuts remain available: inline writeExecToolList, merge
  parseExecDepth/parseExecMaxTurns into parseExecInt. ~14 lines, low priority.

### 2026-07-16: CP1 — batteries-included tier resolver (TUI/workflow redesign)

- Implemented the foundation checkpoint of the TUI/workflow redesign plan
  (plans/tui-workflow-redesign-2026-07-16.md). Stages stop hardcoding model
  IDs and use tier labels; a new resolver picks a cost-conscious model per
  stage from the user's provider family, returning a real provider (not a
  model string — CompletionRequest has no Model field).
- The resolver composes with the existing StageModelResolver as a middle
  layer: explicit stage-models.json override -> tier fallback -> primary.
  stage-models.json remains the user's explicit override path.
- New file internal/splice/stage_tier_resolver.go: stageTierLabels map
  (code_writer/test_generator/design_conversation -> "medium", plan_critic
  -> "reasoning"), NewStageTierResolver, effectiveInputRate (tier-aware
  cost for Cost.Tiers models like Gemini), providerCacheKey helper (shared
  cache, no duplicate builds).
- Algorithm: check tier label -> custom-provider guard (OpenAICompatible/
  AnthropicCompat -> nil) -> resolve primary in catalog (unknown -> nil) ->
  for "reasoning" pick strongest in-family (Option B, user chose: critic
  escalates to the strongest model, cost optimization deferred) -> for
  "medium" pick cheapest tool-capable in-family -> build+cache provider or
  nil (fall back to primary).
- model_routing.go: BuildStageModelResolvers gained a TierResolverConfig
  param (nil registry = disabled = pre-CP1 behavior); the stageResolver
  closure composes the three layers.
- Two call sites updated: exec.go (headless) + model.go (TUI, which already
  carried modelCatalog modelregistry.Registry at line 86).
- design_mode.go: handleCrystallizeCommand now passes the composed resolver
  to CrystallizeAndCritique (was nil — design-phase stages never saw the
  resolver). Added m.stageModelResolver field.
- Stage code: 4 hardcoded-ID sites -> tier labels. step_back unchanged
  (orchestrator-level, not in the tier map).
- schemas/stage_model.go: fixed misleading comment on LoadStageModelConfig
  (it returns zero-value Default when no file exists, not "populated from
  caller's profile").
- Two adversarial passes preceded implementation: pass 1 found 5 BLOCKING
  (resolver must return provider not string; "strongest" invalid tier;
  plan_critic can't go in BudgetForTier; design-phase not wired; ModelOverride
  pre-seeding). Pass 2 found 2 BLOCKING (model.go call site missed;
  design_mode.go passes nil) + 3 MAJOR + 1 false positive (Default-shadowing
  was wrong — verified the loader returns zero-value). All folded in.
- Behavior change (documented): existing users with no stage-models.json
  override now get the tier-resolved model (e.g. gpt-4.1-nano for code_writer
  when primary is gpt-5.6-sol) instead of the primary for every stage. This
  is the intended cost-conscious default and fixes the Anthropic-assumption
  hardcoded defaults. StageRecord.Model attribution shifts to the resolved
  model. stage-models.json overrides remain authoritative.
- Note: the resolver picks the CHEAPEST model in the family, which may be an
  older generation (gpt-4.1-nano, not gpt-5.6-luna). Constraining to
  same-generation models is the deferred "cost optimization" discussion.
- Implemented by fresh-context kimi-k2.7-code worker; parent re-verified
  on-disk (gofmt, build, 9/9 resolver sub-tests + cache test, splice+tui+cli
  green, end-to-end pipeline test green, full root suite green ~8min) as the
  second gate. Worker report: .pi-subagents/artifacts/outputs/bde4c84f/.
- CP3 (phase-adaptive layout, persistent plan panel) complete and
  CI-confirmed. The TUI/workflow redesign (CP1-CP4) is complete. CP5/CP6
  remain deferred to separate plans.

### 2026-07-21: Track DG0+DG1 design-phase diagrams

- User feature request: mermaid-equivalent architecture previews in the TUI
  during the design phase, default-on, shaped over several rounds into a
  deterministic-first design. A fresh-context adversarial reviewer (16
  findings) attacked the original plan (typed Diagram schema + default-on
  background preview stage + crystallizer persistence) and found 2 blockers
  (`Task.TargetPaths` is never populated so a file-tree adapter has no data;
  the D3 schema change forgot the `designPlanToolDefinition` lockstep edit)
  plus high-severity misreadings (no "light" tier in stage_tier_resolver;
  design agents are standalone typed agents, not registry.go stages; the
  transcript is append-only so "update a block in place" does not exist;
  background preview cost is O(turns^2), not ~1k/turn; the plan collided
  unacknowledged with AU5, which deleted `SequenceDiagrams`/`Wireframes` on
  2026-07-15 for being collected-then-dropped).
- Resolution: split the feature by where determinism is possible. DG0:
  conversation-phase diagrams are prompt-only, the model draws ASCII in
  fenced blocks (the Claude Code approach; the terminal renders the block
  preformatted, so no parser/renderer/schema). DG1: post-crystallize, the
  plan already carries a DAG (`Tasks[].DependsOn`), so
  `schemas.Diagram` + `RenderDiagram` + `TaskGraphFromPlan` render it
  deterministically at zero tokens, surfaced only in the View-time
  `persistentPlanHeader` (never static transcript rows, which rewrap and
  mangle box art), with an indented-list fallback below 58 columns.
- DG3 (crystallizer-persisted diagrams) stays `@needs-human`: it is the
  AU5-sanctioned re-add path ("re-addable if a future checkpoint wires the
  consumption") and must do the full lockstep edit and own the
  per-crystallization token cost.
- Same session, effort escape hatch: user on glm-5.2 (custom endpoint) saw
  `/effort` show only "auto". Root cause: the TUI gated effort controls on
  the catalog's per-model effort list, while the exec path already passed
  effort through verbatim for unknown models (`forwardedReasoningEffort`).
  Fix: unknown-to-catalog models may now force low/medium/high via /effort,
  Ctrl+T, and the picker, with an "unverified, forwarded as-is" warning;
  known non-reasoning models (gpt-4.1) keep the strict refusal, and the
  model-switch reset only applies to known targets so a forced effort
  survives unknown-to-unknown switches. Remaining caveat: whether an
  endpoint honors `reasoning_effort` for GLM-class models is endpoint
  behavior; verify with one prompt.
- Follow-up: effort persistence + setup-wizard step. `ProviderProfile.
  ReasoningEffort` (camel+snake decode, mergeProfile support) persists the
  preference; the provider wizard gained an effort step between Model and
  Done (auto + per-model options via `reasoningEffortOptionsForModel`:
  catalog or name-fallback efforts, forced ring for unknown models, step
  skipped for known non-reasoning models); the TUI seeds
  `Options.ReasoningEffort` from the resolved profile at startup and
  headless exec falls back to the profile value when --reasoning-effort is
  absent (spec effort still overrides, forwardedReasoningEffort stays the
  final gate). Implemented by a fresh-context kimi-k2.7-code worker after
  two lane failures (fork-context parroting, then a provider connection
  error; fresh context + resume fixed both). Parent reordered one helper
  (name-fallback before forced ring) to match /effort picker semantics.
  First k2.7-code worker run: lane is viable but needed fresh context and
  one resume.
- DG1 implemented by a fresh-context worker; parent reviewed the diff and
  re-ran the gate (gofmt/vet clean, splice+tui suites green except the
  pre-existing `TestRunHonorsMaxTurnsAsIterationCap` failure, verified on
  clean HEAD via stash). Renderer note: convergence-note attribution can
  misorder which parent is "also" depended on in multi-parent cases; every
  parent is still named, cosmetic only.

### 2026-07-23: Tracks T/W/A planned (configurable pipeline + native review surfaces, v0.2)

- Extended design discussion (planner role) turned the 2026-07-16
  "user-configurable pipeline" future direction into a full plan:
  `plans/configurable-pipeline-and-review-surfaces-2026-07-23.md`. Target
  v0.2.x, long runway. 20 decisions recorded in the plan (the premise);
  five `@needs-human` open questions with defaults (section 11 of the plan).
- Shape: three tracks on one shared foundation. **Track T**: the pipeline
  becomes a user-editable topology (nodes, typed edges with info-flow
  payloads: summary/output/none, per-node model/effort/budget/capabilities,
  per-tier activation). The default pipeline becomes an embedded topology in
  the same schema, proven by a dogfood equivalence test (compiled default ==
  today's StageNamesForTier/BudgetForTier for every tier). Custom `prompt`
  and `command` node types; command nodes run only through the sandboxed
  RunTool registry. Shareable topology files: user library, trust-gated
  project `.splice/pipeline.json` (S1a machinery), import-time y/N consent
  listing command nodes. Six-layer config cascade; additive-only compat
  contract; `splice config describe` with per-value origin. Cost rule: tier
  envelope caps every run unless the topology declares `budget.total`.
- **Track W**: one lazy local HTTP+SSE server (stdlib only, 127.0.0.1:0,
  token URLs, go:embed, zero runtime JS deps, no build step). Serves both
  other tracks; the TUI stays the center of gravity and headless never
  blocks on a browser. `agent.Options.EventSink` is the thin seam feeding
  the SSE hub (same precedent as StageModelResolver).
- **Track A**: native plannotator-style review. `/annotate` opens the
  crystallized DesignPlan in a browser surface; feedback is a typed
  AnnotationFeedback schema anchored to task/requirement IDs (not text
  offsets, so feedback is pre-routed and plan revisions are structurally
  diffable) and returns to the design conversation via
  DesignWorkflow.ReviseWithFeedback. `/review` (A4) does the same for a
  run's diff before merge-back. User-invoked only, never auto-opening.
- UI direction the user pushed hard on: smooth and lightweight, plannotator
  feels janky. Answer is a written UI performance contract (plan section 7):
  server-rendered HTML, zero-build vanilla JS islands, SSE class-flips + CSS
  transitions, <100KB assets, CSP self, keyboard-first. Astro evaluated and
  declined (right islands pattern, wrong toolchain/content model for a Go
  binary serving runtime-dynamic surfaces); graduation path if vanilla
  hurts is TypeScript via esbuild's Go API at go-generate time.
- Key architectural findings from grounding (why the track is smaller than
  it looks): cost accounting, model routing, memory retrieval, and stage
  events are already name-generic (AR8/F12a/F18); the rigidity is six
  name-keyed spots (StageNamesForTier/stageBudgets, stageOptions PullContext
  check, isModelFreeStage, buildStageRegistry map, trajectory verification
  assumptions, TUI enumerations). ExecutionStage.DependsOn is already
  validated (cycle detection) but unused; design_runner.topologicalOrder is
  the in-repo scheduling precedent.
- No code changed. ROADMAP "Future direction" section replaced by the
  Tracks T/W/A section pointing at the plan. Checkpoints start only on user
  go-ahead, T1 (topology schema) first.

### 2026-07-23: adversarial review loop on the T/W/A plan (plan deepened, no code)

- Ran the established adversarial pattern on the new plan: 5 fresh-context
  kimi-k2.7-code reviewers, one per attack surface (schema/compile,
  executor/trajectory, safety+web security, W/A mechanics, config/cascade).
  First launch failed on model resolution (`kimi-k2.7-code` unqualified;
  `openrouter/moonshotai/kimi-k2.7-code` required, same lesson as 2026-07-19).
  3 of 5 then blew kimi's 262k context (reserved output budget); retry with
  `toolBudget hard=30` + explicit grep-not-read instructions landed all 3.
- **Delegation routing (user-set, 2026-07-23):** the kimi lane is for
  planning and reviewing deep dives (adversarial loops, big plan/spec
  passes). Smaller plans and orchestration stay inline with the parent
  (glm-5.2). Worker implementation lanes are unchanged.
- **Branch strategy (user-set, 2026-07-23):** all Tracks T/W/A
  implementation lands on a single long-lived feature branch, `tracks-twa`,
  not on `main`. Each checkpoint commits and pushes to `tracks-twa` and
  waits for the branch CI run as its gate; the user tests from the branch
  between checkpoints. Merge to `main` happens once, when the user is
  satisfied, ahead of v0.2. Docs-only process commits (plan, ROADMAP,
  MEMORY) keep landing directly on `main`.
- Parent verified every blocker/high claim against the code before accepting.
  Verified TRUE: `Requirements []string` has no IDs (design.go:227);
  `ComputeIterationState` hardcodes output keys and `stateHash` hashes only
  code-writer files (trajectory.go:109,232), so verification-only graphs
  false-trip the cycle detector; `passSucceeded` already succeeds
  verification-free graphs (run.go:542); `commands.go:42-44` has a raw-exec
  fallback when `RecordCommand` is nil (no env scrubbing);
  `test_generator.go:43` reads `PriorSummaries["code_writer"]` by name;
  `stageChangedFiles` is key-hardcoded; `/crystallize` and `/approve`
  generate different fresh planIDs (design_mode.go:113,183) so Revision
  never increments.
- Plan deepened in place (sections 4-8, 11-13): v1 anchors restricted to
  task/epic/plan; dogfood equivalence relation defined (DependsOn excluded);
  envelope fully-allocation rule; tier-filter compile rules; orphan rule
  dropped; explicit ModelFree capability; canonical `verification_report`
  key + state-hash fallback (T8 core); load-time single-pass notice;
  additive `HarnessStageOutput.ChangedFiles`; (name,iteration) stage keys;
  full local-security table in section 7 (Host-header check, one-time URL
  token -> SameSite cookie, Origin checks, action tokens, session scoping,
  idle timeout); command nodes use one fixed shell tool + raw-exec fallback
  closed; import client hardened; SSE redaction; A1 grew the plan-family
  fix; browser->TUI actions are ID-validated tea.Msgs via RuntimeMessageSink;
  /review diff source pinned to PipelineResult.ChangedFiles + git diff with
  the worktree/non-worktree split; editor staging-file + publish; exact
  model-resolution ladder; config describe bounded key set.
- Rejected after verification: "any custom node violates the envelope"
  (intended cost-cap behavior, now documented); EventSink "blocker" (plan
  always said the field is new; wording clarified); orphan-node rule
  redefinition (dropped entirely, DAG check suffices).
- One new open question added (plan section 11.6): typed Requirements for
  requirement anchors, deferred past v0.2 by default (DG3 lockstep lesson).
- No code changed. Commit is docs-only.

### 2026-07-24: brand assets from FINAL 11c design (Splice Logo.dc.html)

- Implemented the locked logo design (braid icon, two strands single twist;
  ink #141414 / paper #faf9f6, rust #b5502f accent; Space Grotesk 700 wordmark)
  as SVG assets in docs/assets/: splice-icon.svg, splice-icon-inverted.svg,
  splice-logo.svg, splice-logo-inverted.svg, splice-glyph.svg (LOCKED gv2
  workflow glyph). SVGs are the source of truth; paths copied verbatim from
  the design file.
- Regenerated docs/assets/splice-logo.png (1120x395, transparent) from the new
  design at the same path so existing hotlinks resolve to current brand.
- README.md + README_ZH.md header now uses <picture> with
  splice-logo-inverted.svg for prefers-color-scheme: dark.
- Installed Space Grotesk Bold (OFL) to ~/Library/Fonts for PNG rasterization;
  remove with `rm ~/Library/Fonts/SpaceGrotesk-Bold.ttf` if unwanted.
- Deferred: rust-accent lockup variant (13c, marked optional in the design);
  TUI prompt glyph change (F17 chose λ deliberately; the design's gv2 rust
  prompt glyph is a separate UX decision). Public repo port pending user call.

### 2026-07-24: gutter glyph λ -> ∞ (braid single-twist approximation)

- Replaced the user-prompt gutter glyph with the closest terminal char to the
  LOCKED gv2 single-twist workflow glyph: ∞ (U+221E). The gv2 glyph is an
  opened infinity; ∞ is its closed form and the best widely-supported read.
  Rejected: ⋈ bowtie (tofu in Menlo, poor font support), ∝ proportional-to
  (closest topology but reads as the letter alpha at 1-cell size), ✕ (reads
  as close/multiply). Color stays theme accent (zeroTheme.userPrompt), which
  maps the design's rust prompt-glyph intent to each theme's accent.
- Spots: model.go input.Prompt, rendering.go userPromptPrefix + the two
  Render calls, and the 4 λ assertions in rendering_lime_test.go. Both glyphs
  are width-1, so no layout change. Full internal/tui suite green locally
  (go1.26.5 present on this machine; the old no-toolchain note was stale).
- Not committed; pending user call together with the logo asset commit.

### 2026-07-24: exact braid glyph as a one-glyph PUA font (opt-in)

- The user wanted the actual gv2 single twist in the gutter, not the ∞
  approximation. Investigated the three paths: Braille mosaic (1-row is an
  unreadable blob; 2-row reads well but needs a 2-row gutter layout change and
  Braille is tofu in Menlo), terminal image protocols (bubbletea-hostile, no),
  and a one-glyph font at a PUA codepoint (the Nerd Fonts mechanism). Built
  the third.
- `scripts/build-glyph-font.py` (fontTools): converts the exact gv2 bezier
  strokes to filled outlines (sampled offset curves + round caps, two
  contours, nonzero winding) and emits `SpliceGlyph.ttf` with one glyph at
  U+F8FE. Verified end-to-end on macOS: a Swift/Core Text probe shows the run
  for U+F8FE substitutes SpliceGlyph-Regular while Menlo renders the rest, so
  terminal font fallback needs no terminal-font change. Font shipped at
  `docs/assets/SpliceGlyph.ttf`.
- TUI wiring: `userPromptGlyph` package var in rendering.go, default ∞, env
  `SPLICE_BRAND_GLYPH=1` switches to U+F8FE. Opt-in by design: without the
  font the codepoint is tofu. userPromptPrefix became a var derived from the
  glyph. New test TestUserPromptGlyphBrandOverride (env resolution, styled
  render, width stays 1). Full internal/tui suite green.
- Docs: INSTALL.md gained a "Brand glyph font (optional)" section.
- Caveats recorded: U+F8FE can collide with a Nerd-patched font's MDI slot
  (rebuild with a different codepoint if seen); Linux fontconfig/Windows
  DirectWrite fallback are the same mechanism but untested here; WebKit does
  NOT fall back for PUA (the qlmanage HTML test showed tofu, Core Text is the
  authoritative terminal path).
- Not committed; pending user call with the logo assets and the ∞ gutter.

### 2026-07-24: brand glyph packed into the binary (splice font install + auto-detect)

- The user asked whether the glyph font can pack with the package. Answer:
  embedded in the Go binary. New `internal/brand` package: go:embed of
  SpliceGlyph.ttf (single source, the docs/assets copy was deleted),
  `Glyph` const (U+F8FE), platform per-user `FontDir` (~/Library/Fonts,
  XDG/~/.local/share/fonts, %LOCALAPPDATA%\Microsoft\Windows\Fonts, all
  admin-free), `FontInstalled` (one Stat), `InstallFont` (write + best-effort
  fc-cache on Linux).
- New CLI command `splice font install` (internal/cli/font.go + app.go case +
  help line). Explicit user action, no npm postinstall (writing to font dirs
  from postinstall is the malware-shaped pattern the security culture avoids).
  The npm wrapper and release archives carry the font for free since it is
  inside the binary.
- TUI: `resolveUserPromptGlyph` is now three-way: SPLICE_BRAND_GLYPH=1 forces
  the braid, =0 forces ∞, unset auto-detects via `brand.FontInstalled()`. The
  env var is an override, not a requirement.
- Test-coupling fix: package-init auto-detect made 4 existing render tests
  depend on the host's font state (they failed on this machine once the font
  was installed). Added `pinUserGlyph(t, glyph)` helper in
  rendering_lime_test.go; the 4 tests pin ∞. TestUserPromptGlyphBrandOverride
  now covers default/auto-detect/force-off/force-on via a temp HOME.
- Validation: gofmt/vet clean, brand+cli+tui full packages green, built
  binary `./splice font install` verified end-to-end. From now on this
  machine shows the braid gutter with no env var.
- Not committed; pending user call with the rest of the brand batch.

### 2026-07-24: Ghostty font discovery requires monospace metadata

- The gutter rendered glitched for the user. Root cause: the user's terminal
  is Ghostty 1.3.1, whose font discovery only surfaces MONOSPACE fonts
  (`ghostty +list-fonts` showed 5 system mono families; even a valid
  proportional font in ~/Library/Fonts was invisible). The v1 SpliceGlyph was
  not monospace-marked (unequal advances, isFixedPitch=0), so Ghostty never
  discovered it and U+F8FE rendered as garbage. The earlier Core Text probe
  passed because NSTextView/CT uses classic fallback; Ghostty does its own
  discovery. Terminal-specific renderers must be tested, not assumed.
- Fix (scripts/build-glyph-font.py v2): equal 1000-unit advances for every
  glyph, post.isFixedPitch=1, monospace Panose (bProportion=9), an empty
  space glyph (discovery probes basic Latin), braid scaled to fill the em.
  `ghostty +list-fonts` now lists "Splice Glyph". Font reinstalled, binary
  rebuilt, brand tests green.
- Fallback lever if auto-fallback still fails: Ghostty's documented
  `font-codepoint-map = U+F8FE=Splice Glyph` in ~/.config/ghostty/config.
  Font discovery runs at Ghostty app start, so a restart is required after
  install.
- Glyph centering fix (v3): the v2 braid's ink center sat at y=500 per 1000
  upm, floating ~200 units above where text symbols live. Measured Menlo's
  infinity sign (ink center y~307 per 1000 upm; typical symbol centers are
  300-400) and remapped the braid's ink center to y=320 (YOFF=-80 with S=8;
  braid ink x 112..888, y 76..564). Also set OS/2 sxHeight=547,
  sCapHeight=729 (Menlo-normalized) so terminal fallback scaling matches the
  primary font. Verified with a same-baseline PIL render against Menlo's
  infinity sign before shipping. Lesson: do not center glyphs in the em;
  center them on the primary font's symbol ink center.
- Remote-boundary guard: the glyph renders from a font on the machine that
  runs the TERMINAL, so `FontInstalled` is meaningless over SSH or inside
  WSL (the font file would sit on the server/Linux side while the client
  renders). `resolveUserPromptGlyph` now defaults to ∞ when SSH_TTY,
  SSH_CONNECTION, or WSL_DISTRO_NAME is set; SPLICE_BRAND_GLYPH=1 still
  forces the braid for clients that have the font. VS Code's integrated
  terminal (browser font stack) is a known-weak renderer for PUA
  codepoints; documented with the platform matrix in INSTALL.md.

### 2026-07-24: braid-glyph machinery removed; gutter stays ∞

- The user decided the whole custom-font path is not worth it: deleted
  `internal/brand/` (embedded font + install/detect), `splice font install`
  (cli/font.go + app.go case + help line), `scripts/build-glyph-font.py`,
  the INSTALL.md brand-font section, the SSH/WSL guard + auto-detect in
  rendering.go, the pinUserGlyph helper + override test, and the installed
  font at ~/Library/Fonts/SpliceGlyph.ttf. The gutter is unconditionally ∞
  (the λ->∞ swap from earlier today survives; const userPromptPrefix = "∞  ").
- Kept deliberately: the logo asset set + README <picture> (separate,
  approved workstream), docs/assets/splice-glyph.svg (unused but part of the
  design's locked asset kit), and the user's own startup.go welcome tile
  (the zero-install, works-everywhere braid surface).
- Retained knowledge (MEMORY entries above stand as the record): Ghostty
  discovery requires monospace metadata; PUA rendering needs a locally
  installed font everywhere; terminal renderers must be tested, not assumed;
  center glyph ink on the primary font's symbol center, not the em.
- Validation: gofmt/vet clean, cli+tui full packages green, binary rebuilt.

### 2026-07-24: PX0 landed (TUI perf instrumentation)

- First checkpoint of Track PX. `internal/tui/perf_metrics.go`: 512-slot ring
  buffers for View()/Update() durations, always-on (one time.Now + slot write
  per call), no lock (bubbletea v2.0.7 eventLoop is single-goroutine,
  re-verified). Hooks are two deferred lines in model.go View()/Update().
- `/debug` (command_views.go debugText) gained three sections: Frames
  (view/update p50/p95/max/mean over the retained ring), Render cache (the
  renderCacheStats that were collected since the cache existed but never
  surfaced: hits/misses/hit rate/evictions/skipped-oversized), Transcript
  (rows, flushed frontier, alt-screen, sidebar).
- New perf_metrics_test.go: percentile math, ring wrap (retains last 512,
  count keeps full total), debugText section presence. Full tui suite green.
- Uncommitted with the rest of today's batch; commit decision is the user's.

### 2026-07-24: PX1 landed (settled alt-screen transcript snapshot)

- Delegated to a fresh-context `impl-worker` subagent (created because the
  builtin worker required `contact_supervisor` which the intercom bridge
  failed to load in children; the custom agent has file tools only and
  escalates by returning). Full self-contained spec: the four review-closed
  holes, the five in-place mutation sites, the design principle, the
  acceptance gate. Worker hit its turn budget (90 turns) but landed the
  complete diff before aborting.
- Parent verification (not trusting the worker's claim): gofmt/vet/build clean,
  full tui suite green (18.1s). All four holes confirmed in the diff and
  correctly wired: transcriptRenderInteraction (set at transcript_selection.go:237,
  get at :190), collapseRepeatedStatusCard invalidation (model.go:2442),
  frontier/width guard (flush.go:128), alt-screen no-op removed (m.flushed
  advances, all reads mode-agnostic). startup.go untouched by the worker.
- Benchmark (the proof): BenchmarkIssue561SettledAltScreen at 50/500/5000
  turns: 792 / 787 / 822 ns/op (flat across 100x row count, 0 allocs). Before
  the cache this grew linearly.
- 13 files changed (329 insertions, 78 deletions). The fast path in
  transcriptBodyItems (transcript_selection.go:245) returns the cached
  settled items directly in steady state; the builder (line 292) prepends
  the cache and builds only the live tail during streaming.
- Uncommitted with the rest of today's batch.

### 2026-07-24: PX2 landed (reasoning delta coalescing)

- Delegated to impl-worker with a precise spec: extend the textCoalescer to
  batch agentReasoningMsg the same 16ms window as agentTextMsg, preserving
  arrival order. Worker landed a clean single-buffer + kind-tag design
  (coalesce.go +52/-24, coalesce_test.go +96). Parent verified: gofmt/vet/
  build clean, all 9 coalescer tests pass (5 original + 4 new), full tui
  suite green (15.9s). The implementation matches the spec exactly: a kind
  or runID switch flushes first; drainAndForwardLocked forwards the right
  message type. Consumer side (model.go) unchanged.
- Track PX active work is complete: PX0 (instrumentation), PX1 (settled
  snapshot), PX2 (reasoning coalescing) all landed. PX3 (conditional
  scroll-path study) is not scheduled unless /debug data shows scroll-specific
  spikes after PX1+PX2. The two verified root causes of the thinking jank are
  both addressed.

### 2026-07-24: Track PX added to ROADMAP (frame-consistency principle)

- The user contributed a tip: "redrawing the transcript fully fixes TUI
  scroll jitter." Research (Bubble Tea v2 releases/discussions, Ratatui
  rendering docs) grounds and refines it: the established pattern is
  "compose the COMPLETE frame in memory every tick; the renderer cell-diffs
  and flushes with synchronized output (Mode 2026, default-on in bubbletea
  v2.0.7, supported by Ghostty)." Jitter = inconsistent or dropped frames,
  not full redraws. This converges with the PX1 settled-snapshot plan: the
  cache is what makes complete frames cheap enough to sustain at scroll
  speed. Recorded as Track PX's design principle in ROADMAP.md.
- Track PX written into ROADMAP.md: PX0 (instrumentation via /debug +
  renderCacheStats surfacing), PX1 (settled alt-screen snapshot, manual
  port of Zero d74ceb1 with the three review-closed holes:
  transcriptRenderInteraction, frontier/width-not-count invalidation, six
  mutation sites incl. collapseRepeatedStatusCard, m.flushed audit),
  PX2 (coalesce agentReasoningMsg), PX3 (conditional scroll-path study).
  Root causes ranked by evidence: O(n) frame cost (code-verified),
  uncoalesced reasoning deltas (code-verified), scroll throttles
  (uninvestigated, PX0 decides).

### 2026-07-24: PX3 instrumentation — per-trigger frame tagging (gate made readable)

- PX3 is conditional ("only if PX0 data still shows scroll-specific spikes
  after PX1"). Two blockers to proceeding as an implementation checkpoint:
  (1) the gate was not readable — perf_metrics recorded View()/Update()
  durations but did NOT tag frames by the triggering message kind, so /debug
  could not distinguish a scroll-burst frame from a streaming frame; (2) a
  static audit found no smoking gun (chatScrollOffset is not part of the
  settled-cache invalidation key, so scrolling in steady state hits the PX1
  fast path; mouse throttle 15ms, drag-edge glide 70ms, joinColumns/viewLines
  are O(viewport) not O(transcript)).
- User chose the instrumentation path: make the gate measurable, change no
  scroll behavior. This is that slice.
- internal/tui/perf_metrics.go: refactored the loose view/update ring fields
  into a durationRing type (reuses percentiles(); same nearest-rank math),
  and added a byTag map[string]*frameTagStats. tagForMsg classifies each
  tea.Msg into a small fixed set: mouse_wheel, mouse_motion, edge_scroll,
  key, window, agent_text, agent_reasoning, tool_stream, other. Update sets
  perf.lastTag at its top (single-goroutine eventLoop, no lock); View's
  deferred record reads it so a frame's view cost is attributed to the
  message that triggered it. recordView/recordUpdate signatures are
  unchanged (one new line at the top of model.Update).
- /debug (command_views.go): new "Frames by trigger (view p95 / max, worst
  first)" section via frameByTriggerLines. Worst view-p95 sorts first so the
  trigger behind the slowest frames is at the top. Update stats omitted when
  zero to keep idle triggers quiet.
- Tests: existing percentile/wrap/debugText tests updated to the
  durationRing constructor; new TestPerfByTriggerTagsFrames proves a scroll
  trigger and a streaming trigger land in separate buckets with separate
  p95/max and that mouse_wheel sorts first; new TestTagForMsgCoversScroll-
  AndStreaming pins the classifier. gofmt/vet/build clean, full tui suite
  green (13.9s).
- Next: the user scrolls a long session, opens /debug, and reads whether
  mouse_wheel / edge_scroll view p95/max dwarfs agent_reasoning. If yes,
  PX3 becomes a real implementation checkpoint with a named target; if no,
  PX3 stays closed (the conditional honestly unmet) and Track PX is done.
- Uncommitted with the rest of today's batch.

### 2026-07-25: PX3 gate read — condition unmet, Track PX closes

- The user ran a session (78 rows, alt-screen, active run) and read /debug:
  overall view p50 3.7ms / p95 6.4ms / max 29.3ms / mean 2.6ms; render cache
  99.5% hits.
- The gate was "scroll-specific spikes after PX1." The data shows the
  opposite: mouse_wheel view p95 3.1ms / max 4.8ms (n=361) is the
  SECOND-LOWEST of any trigger, well under the overall p95 6.4ms. Scroll is
  cheap; PX1's settled snapshot works. PX3-impl is NOT warranted.
- Residual jank candidates (NOT scroll, separate investigations if pursued):
  (1) `other` bucket max 29.3ms (n=2375, 75% of frames — periodic spinner/
  blink/flush ticks; the one frame over the 16.7ms budget); (2) `key` update
  max 33.3ms (view stayed 5.3ms — a single expensive keypress, input lag not
  a frame drop). Both are occasional outliers against a healthy p95 6.4ms.
- Track PX is complete: PX0, PX1, PX2, PX3-inst landed; PX3-impl honestly
  unmet. No scroll-path code changed.

### 2026-07-25: PX3 reopened — scroll lag root cause is the throttle drops wheel events

- Correction to the 2026-07-25 "Track PX closes" entry: that closure was
  premature. It substituted the narrow scroll gate (unmet) for the track's
  actual mission (the jank symptom). The user reported the real symptom:
  "slight lag and/or scrolling feels slow when scrolling up and down."
- Root cause (the disproof of my "scroll is cheap" read): the aggregate
  mouse_wheel p95 3.1ms measured only RENDERED frames. The mouse throttle
  (mouse_filter.go) dropped every MouseWheelMsg arriving <15ms after the
  last. On a trackpad, momentum scroll sends events every ~4-8ms, so the
  filter dropped the majority of scroll input before it reached Update.
  Dropped deltas are gone (no accumulation). Result: the transcript moved
  in 5-line jerks with gaps; scroll felt laggy/slow despite each rendered
  frame being cheap. The data and the symptom were both honest; they
  measured different axes (frame cost vs input fidelity).
- Fix (one-line behavior change): stop throttling wheel, keep throttling
  motion. MouseMotionMsg is idempotent (latest hover position wins), safe
  to drop. MouseWheelMsg carries a discrete scroll delta, lossy to drop.
  The throttle's stated purpose ("bounds the redraw rate") is already
  handled by the terminal's synchronized-output frame pacing (Mode 2026,
  default in bubbletea v2.0.7); the app-level throttle was double-pacing
  AND lossy on the wrong event type.
- Files: internal/tui/mouse_filter.go (case drops MouseWheelMsg, keeps
  MouseMotionMsg; comment explains), mouse_filter_test.go (renamed test
  to TestMouseEventFilterThrottlesMotionNotWheel; wheel always passes,
  motion clock is now independent of wheel since wheel no longer calls
  now()), model.go (stale "mouse-event throttle" comment clarified: motion
  only, wheel unthrottled).
- Risk (recorded, the user tests feel): with the throttle removed, a
  trackpad swipe that sends many wheel events moves 5 lines each
  (chatWheelScrollLines=5), so a fast swipe could jump far. If that surfaces
  as too-fast/jumpy, the next step is step-size tuning (reduce the constant
  or honor a wheel magnitude if the terminal protocol carries one; it
  does not today, MouseWheelMsg is {X,Y,Button,Mod} with no delta). This
  checkpoint does NOT touch step size; it only stops starving input.
- Validation: gofmt/vet/build clean, full tui suite green (16.4s). Feel is
  the user's test (not verifiable headless); shipped for subjective review.

### 2026-07-26: design conversation gets real prior-turn messages (Option B)

- Symptom: in a long interactive design conversation, asking the agent to go
  deeper on something discussed earlier lost the earlier detail, all in the
  same session. Root cause was NOT crystallize (which reads raw events with
  full content via MapDesignHistory). It was the live agent path: each TUI
  turn calls agent.Run fresh, and the only inter-turn memory was the text
  block built by sessions.FormatExecPrompt, which (a) truncates every prior
  message to 500 bytes (summarizePayload/truncateUTF8), (b) caps to the last
  80 events (maxPromptContextEvents), and (c) flattens turns into
  "- #N message: <snippet>" text lines instead of real messages. So the model
  literally never received the full earlier turns.
- Fix (Option B, scoped to design-conversation runs only): feed prior
  user/assistant turns as real zeroruntime.Message entries to agent.Run.
  - internal/agent/types.go: Options.PriorMessages []zeroruntime.Message
    (nil = byte-identical to the prior single-prompt seeding).
  - internal/agent/loop.go: Run seeds [system] + priorMessages + [user(prompt,
    images)] when set ; hypa -c "falls back to SeedMessagesWithImages when nil.
  - internal/tui/model.go: tuiAgentRunOptions.priorMessages threaded to
    options.PriorMessages" ; hypa -c "launchPrompt's design branch passes the raw
    current prompt plus designPriorMessages(m.sessionEvents) instead of the
    truncated sessionPrompt block. rawPrompt captured before the
    prompt=agentPrompt reassign so the pipeline/spec-draft paths are
    unchanged (they still use the truncated block; out of scope).
  - internal/tui/design_mode.go: designPriorMessages reuses
    splicerun.MapDesignHistory (same epoch semantics as crystallize), drops
    the just-appended current-user event, returns real messages. First turn
    of an epoch returns nil (byte-identical).
- The agent loop's existing proactive compaction (maybeCompact, driven by
  options.ContextWindow which the TUI already sets for all run kinds) bounds
  the now-full history, so no hardcoded byte/event cap is needed.
- Tests: TestRunSeedsPriorMessages + TestRunWithoutPriorMessagesUnchanged
  (agent loop seeding order + byte-identical nil path)" ; hypa -c "TestDesignRunPassesFullPriorContent (tui, 1200-byte prior assistant turn
  exceeding the old 500 cap, asserts the full content reaches the provider).
- Pipeline and spec-draft paths are unchanged. sessions.FormatExecPrompt and
  MapDesignHistory are untouched (still used by headless exec and crystallize).
- Validation: gofmt empty, vet clean, agent/tui/sessions/config/cli green.
  Pre-existing unrelated internal/splice TestRunHonorsMaxTurnsAsIterationCap
  failure persists on clean HEAD" ; hypa -c "out of scope.

### 2026-07-26: design conversation gets real prior-turn messages (Option B)

- Symptom: in a long interactive design conversation, asking the agent to go
  deeper on something discussed earlier lost the earlier detail, all in the
  same session. Root cause was NOT crystallize (which reads raw events with
  full content via MapDesignHistory). It was the live agent path: each TUI
  turn calls agent.Run fresh, and the only inter-turn memory was the text
  block built by sessions.FormatExecPrompt, which (a) truncates every prior
  message to 500 bytes (summarizePayload/truncateUTF8), (b) caps to the last
  80 events (maxPromptContextEvents), and (c) flattens turns into text lines
  instead of real messages. So the model never received the full earlier turns.
- Fix (Option B, scoped to design-conversation runs only): feed prior
  user/assistant turns as real zeroruntime.Message entries to agent.Run.
  - internal/agent/types.go: Options.PriorMessages []zeroruntime.Message
    (nil = byte-identical to the prior single-prompt seeding).
  - internal/agent/loop.go: Run seeds [system] + priorMessages + [user(prompt,
    images)] when set; falls back to SeedMessagesWithImages when nil.
  - internal/tui/model.go: tuiAgentRunOptions.priorMessages threaded to
    options.PriorMessages; launchPrompt design branch passes the raw current
    prompt plus designPriorMessages(m.sessionEvents) instead of the truncated
    sessionPrompt block. rawPrompt captured before the prompt=agentPrompt
    reassign so pipeline/spec-draft paths stay unchanged.
  - internal/tui/design_mode.go: designPriorMessages reuses
    splicerun.MapDesignHistory (same epoch semantics as crystallize), drops
    the just-appended current-user event, returns real messages. First turn
    of an epoch returns nil (byte-identical).
- The agent loop existing proactive compaction (maybeCompact, driven by
  options.ContextWindow which the TUI sets for all run kinds) bounds the
  now-full history, so no hardcoded byte/event cap is needed.
- Tests: TestRunSeedsPriorMessages + TestRunWithoutPriorMessagesUnchanged
  (agent loop seeding order + byte-identical nil path);
  TestDesignRunPassesFullPriorContent (tui, 1200-byte prior assistant turn
  exceeding the old 500 cap, asserts the full content reaches the provider).
- Pipeline and spec-draft paths are unchanged. sessions.FormatExecPrompt and
  MapDesignHistory are untouched (still used by headless exec and crystallize).
- Validation: gofmt empty, vet clean, agent/tui/sessions/config/cli green.
  Pre-existing unrelated internal/splice TestRunHonorsMaxTurnsAsIterationCap
  failure persists on clean HEAD; out of scope.

### 2026-07-26: configurable pi-style compaction knobs, default-on

- Context: Splice already had a complete compaction system (proactive
  maybeCompact at 0.7 of ContextWindow, reactive recover, /compact command,
  EventCompaction persisted + rehydrated, 200k fallback window for unknown
  models). It was on by default but the trigger ratio and preserve count were
  hardcoded; tuning meant recompiling. Researched pi's compaction
  (pi.dev/docs/latest/compaction): trigger contextTokens > window - reserve,
  keepRecent 20k, reserve 16k, structured summary, split-turn handling,
  extension hooks, branch summarization. Splice matches the standard hybrid
  pattern; the gap was configurability.
- Fix (Checkpoint 1, additive): expose pi-style knobs in config, default-on,
  zero/nil reproduces existing behavior byte-for-byte.
  - internal/config/types.go: CompactionConfig{Enabled *bool, ReserveTokens
    int, KeepRecentTokens int} with EnabledOrDefault (nil = on, tri-state like
    Recaps). Added to FileConfig + MarshalJSON + UnmarshalJSON + ResolvedConfig
    so it survives save/load/merge.
  - internal/config/resolver.go: mergeCompactionConfig threaded into user and
    project merge paths.
  - internal/agent/types.go: Options.CompactionReserveTokens +
    Options.CompactionKeepRecentTokens (zero = existing behavior).
  - internal/agent/compaction.go: compactionState carries reserve/keepRecent;
    newCompactionState computes threshold as window - reserve (clamped to
    half-window) when reserve set, else keeps the 0.7 ratio; preserveLastFromTokens
    walks backward by token budget; CompactionOptions.KeepRecentTokens threaded
    through both maybeCompact and recover.
  - internal/cli/app.go: resolved.Compaction into tui.Options + AgentOptions
    numeric fields.
  - internal/tui/model.go + options.go: compaction on the model; runAgentWithOptions
    gates ContextWindow on EnabledOrDefault (0 when disabled makes maybeCompact
    and recover strict no-ops), threads reserve + keepRecent. Added
    captureRunOptions test seam (nil-safe, mirrors captureRunImages).
- Defaults match pi: enabled=true, reserveTokens=16384, keepRecentTokens=20000.
  Omit the block (or Enabled unset) and the built-in 0.7 ratio / 6-message
  preserve stays (byte-identical). Set enabled=false to disable.
- Config block in ~/.config/splice/config.json or .splice/config.json:
  {"compaction":{"enabled":true,"reserveTokens":16384,"keepRecentTokens":20000}}
- mergeCompactionConfig is in mergeProjectConfig too, so a cloned project can
  tune compaction. It is a UX/perf preference, not a security boundary, so this
  matches pi. Revert to user-config-only if that ever needs to change.
- Known gap (intentional, scoped out): headless internal/cli/exec.go still uses
  its own ContextWindow logic and does not read compaction config yet.
  Interactive TUI is fully wired; extending exec is a follow-up checkpoint.
- Tests: reserve threshold math, half-window clamp, default-ratio-unchanged,
  keepRecent preserve, config defaults/explicit/round-trip, disabled-sets-
  ContextWindow-zero gate, enabled-passes-reserve-and-keep.
- Validation: gofmt empty, vet clean, agent/tui/config/cli/sessions green.
  Pre-existing unrelated internal/splice TestRunHonorsMaxTurnsAsIterationCap
  failure persists on clean HEAD; out of scope.

### 2026-07-26: plan_critic category enum fix (crystallize genuinely broken on GLM-5.2)

- Symptom (real, user-reported after the "crystallize works" verification):
  `Crystallization failed: model "z-ai/glm-5.2" failed typed output after 3
  attempts: required tool "submit_critique" ... critiques[0]: invalid category`.
  The crystallizer itself succeeded; the plan_critic stage died after 3 retries.
- Root cause: `Critique.Validate()` enforces six category values (scalability,
  security, maintainability, complexity, operability, correctness) but the
  `submit_critique` tool schema declared `category` as a bare `{"type":
  "string"}` (severity had an enum; category did not), and the plan_critic
  prompt never named the taxonomy. The model had NO source for the valid
  values, so GLM-5.2 emitted a reasonable but invalid category (e.g.
  "performance") and burned all retries. The machine contract under-specified
  what the runtime validator enforced.
- Prior live verification (same session, throwaway harness, since removed):
  DesignCrystallizer passed 4/4 on GLM-5.2 first attempt. The failure was
  specific to plan_critic's category field.
- Fix (one contract, one source of truth):
  - internal/splice/schemas/design.go: added exported CritiqueCategories()
    returning the six values; Critique.Validate() now checks against it.
  - internal/splice/stages/plan_critic.go: the category field in
    planCriticToolDefinition() now declares `"enum":
    schemas.CritiqueCategories()` so the model sees the valid values in the
    tool schema.
  - internal/splice/stages/prompts/plan_critic.md: names the six categories
    and forbids inventing others.
- Test: TestPlanCriticCategoryEnumMatchesValidator in stages_test.go locks the
  schema enum to the validator set (both directions: every enum value passes
  Validate, every validator category is in the enum), and asserts
  "performance" is rejected, so the two cannot drift apart again.
- Live verification: ran the fixed plan_critic against GLM-5.2 via OpenRouter
  3 times. 3/3 succeeded on the first attempt with valid categories
  (correctness, maintainability, operability, security, scalability,
  complexity). Throwaway harness removed after.
- Validation: gofmt empty, vet clean, stages and schemas suites green. The
  pre-existing internal/splice TestRunHonorsMaxTurnsAsIterationCap failure is
  unchanged on clean HEAD and out of scope.

### 2026-07-26: two user-reported fixes — forced typed tool calls, thinking persistence

- Issue 1 (tooling): user reported typed-stage tool calls fail and diagnosed it
  as a request-creation fault, not a model fault. Verified: no adapter ever
  emitted tool_choice, so crystallize/critic/writer/test-gen tool calls were
  voluntary; a prose answer stranded the retry loop with "model did not call
  <tool>". Fix (commit e84d73c): zeroruntime.CompletionRequest.ToolChoice,
  mapped natively per adapter (OpenAI function, Anthropic tool, Gemini
  functionCallingConfig ANY), set in stages.callToolUse. Empty is
  byte-identical. Verified live: GLM-5.2 honors the forced call over streaming
  (curl), and a full DesignCrystallizer run with forcing wired in succeeded.
- Issue 2 (thinking display): user sees live thinking but it "doesn't write
  through and disappears"; wants pi's behavior. Verified: reasoning deltas DO
  arrive from GLM-5.2 at any effort (live diagnostic), the whole display chain
  works, but there was no reasoning session event type, so thinking never
  persisted and vanished on /resume. Pi persists thinking as ThinkingContent
  in its session JSONL. Fix (commit d7aef2c): EventReasoning session event
  persisted per flushed segment from flushReasoning, rehydrated into collapsed
  rowReasoning rows on resume. Generic machinery carries the new type
  (rehydration clones unknown types; compaction preview has a default).
- Delegation note: kimi-k2.7-code on OpenRouter started reserving ~205k output
  tokens regardless of thinking level, overflowing its 262k window for child
  runs; reasoning is mandatory on that endpoint so :off fails. Also lowered
  the impl-worker agent's configured thinking from high to low
  (~/.agents/code-analysis.impl-worker.md) since spec-driven implementation
  does not need a large thinking budget. Switched the worker lane to
  z-ai/glm-5.2, which completed both checkpoints cleanly.
- Validation: gofmt empty, vet clean, zeroruntime/providers/sessions/tui/
  stages/schemas suites green. Pre-existing internal/splice
  TestRunHonorsMaxTurnsAsIterationCap unchanged on clean HEAD, out of scope.

### 2026-07-26: WG1 - dtools workspace root symlink resolution (Track WG starts)

- Context: a directed dependency graph over all 1,182 first-party Go files
  (graphify, local `graphify-out/` analysis, not checked in), cross-checked
  against both execution paths, surfaced wiring gaps invisible to a green
  build and green CI. First and most severe: `TestRunHonorsMaxTurnsAsIterationCap`
  had been carried as "pre-existing, out of scope, macOS-only" across five
  prior log entries. It is not a test-environment quirk.
- Root cause: `internal/splice/dtools/resolve.go` resolved the *target* path
  through `filepath.EvalSymlinks` but never the *root* (only `filepath.Abs`).
  `filepath.Rel(unresolvedRoot, resolvedTarget)` then compares two different
  namespaces whenever the root traverses a symlink (macOS `/var ->
  private/var`, so `t.TempDir()` and `/tmp` both trigger it), producing
  `../../../private/var/...` and rejecting every file as "escapes workspace".
  Reproduced empirically and traced end to end: `security_auditor` -> gosec
  check -> `resolveWorkspacePath` rejection -> `stages/security_gosec.go:67`
  default branch turns the non-OK tool result into a stage-killing error ->
  `run.go:168` returns `"failed"` instead of reaching the abort path at
  `run.go:304`. Blast radius is the whole deterministic security floor, not
  one check: `DefaultSecurityChecks()` is `{bandit, gosec, sarif, trivy}`, and
  bandit/gosec/sarif all route through the same shared resolver.
  `internal/tools/workspace.go:43` already resolves both root and target
  symmetrically for every Zero read tool; `dtools/resolve.go` was the lone
  asymmetric resolver in the repo.
- Why CI never caught it: both GitHub Actions jobs run `ubuntu-latest`, where
  `/tmp` is a real directory (no symlink), so root and target already share a
  namespace and the bug cannot reproduce there. A green Actions run was never
  evidence this path worked; it only proved Linux was unaffected.
- Fix: `filepath.EvalSymlinks` on `root` immediately after `filepath.Abs`,
  mirroring `internal/tools/workspace.go:43`. One-line change in
  `internal/splice/dtools/resolve.go`, fixes all three callers
  (`gosec.go:70`, `bandit.go:71`, `sarif.go:97`) at once.
- Why this does not weaken the path-escape guard: the existing check compared
  a resolved target against an *unresolved* root, which is a namespace
  mismatch, not a stricter containment test. Resolving both sides puts them
  in one namespace; a symlink genuinely outside the workspace still resolves
  outside the resolved root and is still rejected. Proof: the pre-existing
  negative test `TestResolveWorkspacePathRejectsSymlinkOutsideWorkspace`
  stays green unmodified, and a new negative test
  (`TestResolveWorkspacePathRejectsSymlinkOutsideSymlinkedRoot`) proves
  containment holds even when the root itself is reached through a symlink
  layer, closing the gap the old test could not see (it passed before for
  the wrong reason: the unresolved-root bug happened to still reject it).
- Tests: extended `internal/splice/dtools/resolve_test.go` with
  `TestResolveWorkspacePathAcceptsFileThroughSymlinkedRoot` (positive) and
  `TestResolveWorkspacePathRejectsSymlinkOutsideSymlinkedRoot` (negative),
  both built via an explicit `os.Symlink` helper rather than relying on the
  platform temp dir being symlinked, so they reproduce the regression on
  Linux CI too, not only on this macOS machine.
- Verified in this checkpoint (not a separate slice - a checkpoint with no
  diff is not a checkpoint): `TestRunHonorsMaxTurnsAsIterationCap` now passes.
- Validation: gofmt empty, vet clean,
  internal/splice/... and internal/tools/ suites green.
- Track WG (wiring gaps) opened in ROADMAP.md. Remaining checkpoints: WG2
  compaction on `exec --spec`, WG3 mark the pipeline `ContextWindow` inert,
  WG4 `EventTaskStarted` emitter plus `TaskRunStatus` vocabulary alignment,
  WG5 delete dead `internal/reasoning`/`internal/reltime`, WG6 prune unused
  `buildStageRegistry` params, WG7 document inert pipeline exec options, WG8
  document the Windows specialist process-leak limitation, WG9 fix a stale
  doctor connectivity message.

### 2026-07-26: WG2 - exec_spec honors compaction config

- Context: `internal/cli/exec_spec.go` backs `splice exec --spec`, the only
  headless entry point that runs a real `agent.Run` loop (the default
  `splice exec` path runs `splicerun.Run` instead, see WG3). It built
  `agent.Options` with `ContextWindow` from `resolveAgentContextWindow` but
  never read `resolved.Compaction`, so a user's `{"compaction": {...}}`
  config had an effect in the interactive TUI (`tui/model.go:4816-4823`)
  and no effect on `exec --spec`, silently.
- Fix: `specDraftCompactionOptions(cfg config.CompactionConfig,
  resolvedContextWindow int) (contextWindow, reserveTokens,
  keepRecentTokens int)`, a pure helper in `internal/cli/exec_spec.go`
  mirroring the TUI gate exactly - enabled (default) passes the resolved
  context window and the configured reserve/keep-recent knobs through;
  disabled forces `ContextWindow` to 0, matching
  `agent.Options.ContextWindow`'s documented "0 disables compaction
  entirely" contract. Wired into the `agent.Run` call at
  `exec_spec.go:115-119`.
- Chose a pure helper over a `captureRunOptions`-style test seam (which
  `internal/tui` uses): `internal/cli` has no equivalent seam today and the
  composite literal is passed directly to `agent.Run`, so introducing one
  would be new scaffolding for a single call site. The helper is
  independently testable without one.
- Tests: `TestSpecDraftCompactionOptions` in `exec_spec_test.go`, a 3-case
  table (unset/enabled, explicit reserve+keep, disabled) mirroring the
  TUI's `TestCompactionDisabledConfigSetsContextWindowZero` /
  `TestCompactionEnabledConfigPassesReserveAndKeep` assertions.
- Validation: gofmt empty, vet clean, internal/cli and internal/agent
  suites green.

### 2026-07-26: WG3 - mark the pipeline ContextWindow inert

- Context: `internal/cli/exec.go:604`'s pipeline `runOptions` (feeds only
  `splicerun.Run` / `splicerun.RunDesignPlan`, confirmed via
  `grep -n runOptions exec.go`, no other consumer) computed `ContextWindow`
  via `resolveAgentContextWindow`, which on a registry miss makes a live
  provider-discovery network round trip (`discoveredModelContextWindow`,
  5s timeout). `internal/splice` reads `Options.ContextWindow` zero times
  anywhere in its tree - the value was paid for and discarded on every
  headless pipeline run with an uncatalogued model.
- Fix: dropped the computed assignment, replaced with a comment matching the
  five existing "is agent-loop only... inert under splice exec" comments
  already in the same struct literal (DeferThreshold, Specialists, Skills,
  ModelSwitcher, SelfCorrect, FileDiagnostics).
- Test: `TestPipelineNeverReadsContextWindow` (exec_test.go) walks
  `internal/splice` and fails if any non-test file references
  `ContextWindow`, so the comment's claim cannot silently go stale if a
  future change threads compaction into the pipeline. Verified the test is
  actually falsifiable: temporarily appended a `ContextWindow` reference to
  `internal/splice/run.go`, confirmed the test failed with the expected
  message, reverted via `git checkout --`.
- `resolveAgentContextWindow` itself is unchanged and still used by
  `exec_spec.go` (WG2) - only this one call site was dead.
- Validation: gofmt empty, vet clean, internal/cli suite green
  (includes the new invariant test, passing and proven falsifiable).

### 2026-07-26: WG4 - EventTaskStarted emitter, TaskRunStatus vocabulary alignment (@needs-human)

- Context: the one architectural checkpoint in Track WG (changes a typed
  schema contract, commitment #1). `EventTaskStarted`
  (`sessions/store.go:51`) had a full contract - payload struct
  (`design_lifecycle.go`), a decoder arm setting `"running"` - and zero
  producers anywhere. Separately, that decoder built a
  `schemas.TaskRunOutcome{Status: "running"}`, and `TaskRunOutcome.Validate()`
  rejects anything but completed/failed/aborted: the decoder was
  constructing an implicitly-invalid value. `schemas.TaskRunStatus`, the
  type whose own doc comment says "per-task execution status... including
  in-flight states," had zero non-test usages anywhere and a drifted
  vocabulary (`pending, running, succeeded, failed` vs `TaskRunOutcome`'s
  `completed, failed, aborted`) - the same drifted-taxonomy class of bug as
  the plan_critic category fix earlier in the week, just never triggered
  because nothing wrote it.
- User decisions (recorded before implementation): emit the event rather
  than delete the contract; keep and align `TaskRunStatus` rather than
  delete it or leave the drift.
- Fix, four files:
  - `schemas/design.go`: `TaskRunStatus.Status` aligned to
    `pending, running, completed, failed, aborted` (drops `succeeded`).
    Added `TaskStartCallback func(task Task, runID string)` next to the
    existing `TaskLifecycleCallback`, matching that convention rather than
    an inline func field.
  - `design_runner.go`: `RunDesignPlanOptions.OnTaskStart
    schemas.TaskStartCallback` (additive; `OnTaskLifecycle` untouched, so
    every existing caller keeps compiling unchanged). Invoked right after
    the "Starting task N/M" progress emit and before
    `BuildExecutionPlanForTaskWithFacts`/`runExecutionPlan` - i.e. after the
    `completedSet[task.ID]` skip-check, so resume-skipped tasks correctly
    never fire it. `runID` was already computed earlier in the loop.
  - `design_lifecycle.go`: `DesignState.TaskOutcomes` changed from
    `map[string]schemas.TaskRunOutcome` to `map[string]schemas.TaskRunStatus`
    - the only type that can legally hold `"running"`. All three decoder
    construction sites (`task_started`/`task_completed`/`task_failed`)
    switched from `TaskRunOutcome{RunID: t.RunID, ...}` to
    `TaskRunStatus{RunID: &t.RunID, ...}` (pointer field). The terminal-check
    loop at the end of `ReconstructDesignState` reads `.Status`, a plain
    string on both types, so it needed no change.
  - `tui/design_mode.go`: new `onTaskStart` callback persists
    `EventTaskStarted` via `store.AppendEvent`, wired into
    `RunDesignPlanOptions.OnTaskStart` alongside the existing
    `onTaskLifecycle` wiring.
  - `splice exec --plan` (`exec.go:723`, `splicerun.RunDesignPlan`) still
    passes no lifecycle callbacks at all, same as before this checkpoint -
    it persists no task_started/completed/failed events. Not a regression;
    recorded as a known gap, not fixed here (would need threading a session
    store into a headless exec path that does not have one today).
- Effect: a design-plan run interrupted mid-task (crash, terminal close,
  kill) now reconstructs on resume/replay with that task's status as
  `"running"` instead of the task being absent from `TaskOutcomes`
  (indistinguishable from a task that never started at all).
- Tests: `TestReconstructTaskStartedStaysRunning`
  (design_lifecycle_test.go) - a started-then-nothing-after replay yields
  phase Executing, t1 status "running" with the RunID pointer correctly
  set, and t2 (no event at all) absent, not "running". Two runner tests
  (design_runner_test.go): `OnTaskStartFires` - fires once per dispatched
  task in order, not for a task in `CompletedTaskIDs`; `OnTaskStartNilSafe`
  - a nil callback does not panic. Schemas: added a `TaskRunStatus{Status:
  "running"}` case to the existing JSON round-trip table, plus a dedicated
  vocabulary-alignment subtest asserting all five aligned statuses validate
  and the old drifted `"succeeded"` is now rejected.
- Validation: gofmt empty, vet clean, full `go build ./...` and
  `go vet ./...` clean repo-wide (this touched schemas/splice/tui, which
  many packages depend on). internal/splice/..., internal/tui/,
  internal/sessions/ suites green.

### 2026-07-26: WG5 - delete dead internal/reasoning and internal/reltime

- Context: the dependency-graph audit found two packages inherited from
  upstream Zero with zero importers anywhere in this repo's commit
  history. `internal/reasoning` (495 lines: `capability.go`, `catalog.go`,
  an embedded `modelsdev_snapshot.json`, `catalog_test.go`) duplicates
  `internal/modelregistry`, which is the actually-used implementation.
  `internal/reltime` (65 lines, one exported `RelTime` function) is
  explicitly abandoned refactoring scaffolding per its own comment ("the
  ORIGINAL implementation, kept verbatim so characterization tests can be
  pinned against it before refactoring") - and no characterization tests
  were ever written.
- The risk this closes is not dead code by itself but false confidence:
  `internal/reasoning`'s nine tests (`TestGroundTruthOpenAI`,
  `TestGroundTruthAnthropic`, `TestCoversZeroShippedReasoningModels`, and
  others) ran green in every CI run against code the binary never calls,
  which is exactly the shape that lets a contributor "fix" reasoning
  capabilities there, watch tests pass, and change nothing about the
  shipped binary.
- Verified zero importers immediately before deletion (not just from the
  earlier audit): `grep -rn 'splice/internal/reasoning\|splice/internal/reltime'`
  across every `.go` file outside the two packages themselves returned
  nothing.
- Deleted both packages entirely (`git rm -r`).
- Upstream Zero still carries both (PRs #338, #315) - this is a deliberate
  divergence, not an oversight. See the Current State note above for the
  future-merge-conflict consequence.
- Validation: gofmt empty. Full-suite validation rather than focused, per
  the plan's judgment that a deletion's blast radius is unclear: `go build
  ./...`, `go vet ./...`, and `go test -count=1 ./...` all green across
  every package in the root module (no package broke), plus `go build
  ./... && go test -count=1 ./...` green in the separate `memd/` module.

### 2026-07-26: WG6 - prune buildStageRegistry params, cache detectLanguage

- Context: `buildStageRegistry` (`internal/splice/registry.go`) took
  `provider agent.Provider` and `runner ToolRunner` params it never used
  (`_ = provider`, `_ = runner`) and computed
  `language := detectLanguage(workDir)` only to `_ = language` it - the
  audit's original finding. Investigating it surfaced the real, larger
  issue: `detectLanguage` is called via `stageOptions` once per stage per
  iteration (not "twice per build" as first estimated from a shallow read)
  and does a full `filepath.WalkDir` of the workspace whenever no
  `go.mod`/`tsconfig.json`/`package.json` marker exists, i.e. on every
  Python target. A multi-stage, multi-iteration run re-walks the same
  unchanged directory repeatedly.
- Fix, two parts:
  1. `buildStageRegistry(options agent.Options, workDir string)` - dropped
     the two unused params and the dead `language`/`_ =` lines entirely.
     Its one call site (`run.go:74`) updated; `provider`/`runner` are still
     used elsewhere in that function so removing them from this one call
     is safe.
  2. Considered threading a computed `language` value through the full call
     chain (`runExecutionPlan` -> `runIterationLoop` -> `runPass` ->
     `runStageWithContext` -> `stageOptions`, plus the separate step_back
     call site at run.go:226) to compute it once per run. Rejected: that
     touches 5 function signatures and ~20 direct test call sites
     (`run_test.go`, `recovery_test.go`, `memory_integration_test.go` all
     call `runPass`/`runIterationLoop`/`runStageWithContext` directly) for
     what the plan review itself called the track's lowest-value slice.
     Instead added a small mutex-guarded single-entry cache inside
     `detectLanguage` (`languageCacheMu`/`languageCacheDir`/`languageCacheVal`),
     with `detectLanguageUncached` as the pure original logic underneath.
     Marked `ponytail:` naming the ceiling: single-entry, not keyed per
     workDir, so a future caller interleaving stages across multiple
     concurrent distinct workspaces (e.g. a daemon) would thrash it back to
     a walk per call; upgrade to a small bounded map if that happens.
     Verified no `t.Parallel()` anywhere in the package and no goroutine
     dispatch of `splicerun.Run`/`RunDesignPlan` in the codebase, so the
     mutex is defensive, not covering a live concurrent-caller today.
  3. Caught and fixed a bug in the first draft before committing: an
     uninitialized cache (`languageCacheDir == ""`, the zero value) would
     falsely "hit" on the very first call made with `workDir == ""`,
     returning `""` instead of the correct `"python"` default. Added an
     explicit `languageCacheValid bool` to distinguish "never computed"
     from "computed for the empty-string workDir".
- New `internal/splice/registry_test.go` - no test file existed for this
  component before. Covers: all five stage names present in the built
  registry; bandit/gosec/sarif register exactly once into a shared
  `*tools.Registry` across two `buildStageRegistry` calls (the Get-before-Register
  guard at registry.go:37-46 must not double-register or panic); marker-file
  detection for go.mod/tsconfig.json/package.json via the uncached function;
  empty-workDir defaults to python; and cache correctness - same workDir
  returns the stale cached value after its marker file is deleted (proves
  memoization), a different workDir is not sticky to the first one's cached
  value.
- Validation: gofmt empty, `go build ./...` and `go vet ./...` clean
  repo-wide, `go test -count=1 ./internal/splice/...` green, and
  `go test -count=1 -race ./internal/splice/` clean on the new shared
  mutable state.

### 2026-07-26: WG7 - document inert pipeline exec options

- Context: `internal/cli/exec.go:604-660`'s pipeline `runOptions` sets six
  fields the deterministic pipeline never uses (`Specialists`, `Skills`,
  `DeferThreshold`, `ModelSwitcher`, `SelfCorrect`, `FileDiagnostics`),
  each with an honest in-code "is inert under splice exec" comment, plus
  compaction (WG3). ROADMAP's "Deferred / known gaps" documents only
  mid-run escalation, and ROADMAP does not ship in the public repo
  (F11b, 2026-07-19), so a user reading only `docs/` saw none of this.
- Fix: `docs/PIPELINE.md` gained an "Options that do not apply under
  `splice exec`" section (after "Stage contracts", before "Token and cost
  accounting") - a table naming each option and why the pipeline ignores
  it, plus a short note that this is by design, not a bug.
- Sharper finding than the audit originally stated: verified
  `--allow-escalation` and `--self-correct` have NO effect under any
  `splice exec` invocation today, not just the default pipeline path.
  `exec.go` wires `ModelSwitcher`/`SelfCorrect` only into `runOptions`,
  which feeds only `splicerun.Run`/`RunDesignPlan` (confirmed: `grep -n
  runOptions exec.go` shows exactly those two consumers). `exec_spec.go` -
  the one headless path that runs a real `agent.Run` loop
  (`splice exec --spec`) - was grepped exhaustively for
  `correct|escalat|switcher` (case-insensitive) and matches nothing. Only
  the interactive TUI wires these flags to a real run. Documented that
  precisely rather than the looser "inert on the pipeline path" framing
  the original audit used.
- Added a one-line "(interactive TUI only; see docs/PIPELINE.md)" pointer
  under both `--allow-escalation` and `--self-correct` in the `exec --help`
  text (`internal/cli/app.go:1387-1390`).
- Validation: gofmt empty, vet clean, `TestRunExecHelpDocumentsM1Flags`
  (loose substring assertions, unaffected by the added line) and the full
  internal/cli suite green.

### 2026-07-26: GitHub Actions blocked by billing; WG6 push discovered it

- WG6's push (`14d9f30`) was the first commit this session not confirmed
  green by real CI before the next checkpoint started (a cadence slip:
  WG5's CI was checked, but WG6 was pushed without first checking WG5's
  result landed clean - it had, but the process gap is worth naming). When
  checked, WG6's CI run failed in 9 seconds with no test output: GitHub's
  annotation read "The job was not started because recent account payments
  have failed or your spending limit needs to be increased." Confirmed via
  `gh run view` on both jobs (Build & Test, memd sidecar) - neither
  started. This is an account-level billing block, not a code or workflow
  problem.
- User decision: run the CI workflow's exact steps locally as the
  checkpoint gate instead of waiting on GitHub Actions, until billing is
  resolved. Read `.github/workflows/ci.yml` and replicated both jobs
  verbatim rather than approximating: `gofmt -l .`, `go vet ./...`,
  `GOOS=windows go vet ./internal/memd/...`, `go test -race ./...` (every
  package, not the focused subset used earlier in this track),
  `go build ./cmd/splice`, `GOOS=windows/linux GOARCH=amd64 go build
  ./cmd/splice`; then in `memd/`: `go vet ./...`, `go test -race ./...`,
  `go build -o splice-memd .`.
- Ran the full sequence once at WG6's committed state (retroactive
  verification, since it had already been pushed unverified): every step
  green, including `-race` across all ~75 packages in the root module.
  Build artifacts (`splice`, `splice.exe`, `splice-memd`) removed after
  each local run so they do not get committed by accident.
- WG7 committed locally, the full sequence re-run against it (with a
  forced non-cached `-race` pass on the touched `internal/cli` package
  specifically), confirmed green, then WG6+WG7 pushed together.
- This substitution is explicitly temporary. See the Current State note
  above: once billing is fixed, get a real CI confirmation on the
  accumulated WG6-WG9 commits rather than treating local runs as
  permanent proof.

### 2026-07-26: WG8 - document the Windows specialist grandchild limitation

- Context: `internal/specialist/exec_proc_windows.go`'s
  `hardenSpecialistChild` kills only the direct child process on
  cancel/timeout, because Windows has no equivalent to a POSIX process
  group. The Unix counterpart (`exec_proc_unix.go`) sets `Setpgid` and, on
  cancel, signals the whole process group (`syscall.Kill(-pid, SIGKILL)`).
  Already marked M6 in an honest in-code comment; absent from user-facing
  docs while the release ships Windows binaries.
- Fix: docs-only. Added a "Process Lifecycle On Cancel Or Timeout" section
  to `docs/SPECIALISTS.md` after "Background State" (a distinct concern -
  process lifecycle, not persisted state) explaining the Unix/Windows
  asymmetry in plain terms and naming `WaitDelay` (2s) as a hang-guard for
  Splice itself, not a fix for the leaked grandchild. Added a
  `@needs-human` entry to ROADMAP's "Deferred / known gaps" naming the
  fix (Windows Job Object, `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`) and its
  precondition (cannot be verified from macOS without a Windows CI runner).
- No code changed; only docs. Validation: `gofmt -l .` empty (confirms no
  stray `.go` edits).

### 2026-07-26: WG9 - fix the stale doctor connectivity message

- Context: `doctor.go:236`'s fallback message read "Connectivity probing
  is not wired in the Go doctor backend yet." The original audit called
  this "always warns" and later "reachable only when a TUI embedder
  leaves the prober nil." Neither was quite right; traced precisely
  before fixing.
- `connectivityCheck` has four branches, gated on `enabled` (the
  `Connectivity` option) and whether a `ProviderHealth` result was
  supplied. The stale-message branch fires only when `enabled=true`,
  the profile is valid, the model resolves, AND `health == nil` - i.e. a
  caller asked for a connectivity check but never ran one.
- Traced both real callers of `doctor.Run`: `internal/cli/observability.go`
  (`splice doctor --connectivity`) always calls
  `deps.probeProviderHealth(...)` and sets `health` whenever
  `options.connectivity && config.HasProviderProfile(provider)`; the TUI's
  `doctorOptions` (`internal/tui/model.go:901-925`) does the same whenever
  `connectivity && m.probeProviderHealth != nil && HasProviderProfile(...)`.
  `m.probeProviderHealth` is threaded from `internal/cli/app.go:826`, which
  is always `deps.probeProviderHealth`, normalized to the non-nil default
  `providerhealth.Probe` at `app.go:505-506` when unset. Grepped the whole
  tree for `tui.Options{` construction outside tests: only `app.go`
  constructs it in production. Conclusion: this branch is **unreachable
  from the shipped CLI or TUI today**. It is a caller-bug guard, not a
  real "backend unbuilt" condition - `providerhealth.Probe` is wired and
  working; a caller that sets `Connectivity: true` without running it is
  the actual bug, if one ever exists.
- Fix: corrected the message to name the real condition ("Connectivity
  was requested, but no provider health probe result was supplied.")
  instead of the false "not wired... yet" claim, with an in-code comment
  explaining why the branch should not normally fire.
- Test: `TestRunReportConnectivityRequestedWithoutProbeResult` in
  doctor_test.go - no test previously exercised this exact branch
  (`Connectivity: true` + `ProviderHealth: nil` + a valid, resolving
  profile), which is how the stale claim went unnoticed through every
  prior audit pass. Asserts the message no longer contains "not wired"
  and does contain the corrected condition.
- Validation: gofmt empty, vet clean, internal/doctor suite green.
- **Track WG complete.** All nine checkpoints landed. See the Current
  State note above for the GitHub Actions billing caveat covering
  WG6-WG9's verification method.

### 2026-07-26: planned first-class pipeline evals and honest accounting

- Wrote
  `plans/first-class-pipeline-evals-and-cost-accounting-2026-07-26.md`.
  It supersedes the original `plans/f10-eval-harness.md`, which assumed a
  manually supplied external command and one matrix model for all pricing.
  Marked the old plan as historical so future work does not follow it.
- Default decision: `splice eval bench` without `--agent-command` will
  start the current executable as `splice --no-trust exec ...
  --output-format stream-json`. This reuses production config, provider,
  model routing, registry, sandbox, permission, memory, and event setup.
  Fixture-owned project extensions remain disabled by the trust policy.
- `--agent-command` remains the external-agent override. A benchmark model
  is the requested primary model, not proof that every stage used it.
- Accounting decision: one record per provider stream is authoritative.
  Records include streams whose providers omit usage. Stage totals group
  the same records; auxiliary calls remain in pipeline and task totals.
- Cost decision: calculate each request once with its actual model. Keep
  explicit priced, unpriced, and error states plus aggregate coverage.
  Persist the estimate source and date. Never use numeric zero for unknown
  pricing.
- Found a critical capture gap during review: `CommandAgentRunner` stores
  only the first 8 MiB, so post-run parsing can lose tail usage and final
  events. PE4 moves JSONL parsing into a bounded incremental collector.
- Corrected the cost contract after review: cached-read and cache-write
  tokens are disjoint input subsets. Uncached input is input minus both.
  Malformed subset counts and excessive reasoning must fail instead of
  being clamped.
- Resolver decision: replace positional resolver returns with one typed
  provider/model selection. After successful trajectory escalation, later
  LLM-backed stages bypass normal routing and use the escalated selection.
- Plan sequence: PE1 typed selection and complete tokens; PE2 raw request
  ledger; PE3 structured pricing; PE4 incremental eval accounting and
  benchmark v2; PE5 built-in runner; PE6 TUI and historical usage parity;
  PE7 CSV, docs, and acceptance.
- Existing OpenAI-compatible SSE, explicit workspace trust, and WSL2
  sandbox-test defects are preconditions. They are separate from PE scope.
- Two independent reviewer launches still exited with the known unavailable
  `intercom` configuration error. Both returned useful inline findings.
  The plan now includes their critical corrections: disjoint cache-write
  pricing, missing-usage request records, incremental JSONL parsing, all
  resolver call sites, effective escalation precedence, structured pipeline
  request counts, and the benchmark contract bump at the shape change.
- Updated `ROADMAP.md` with pending Track PE. No public repository file was
  changed during this planning checkpoint.

### 2026-07-26: PE preconditions and PE1 implemented locally

- Fixed the existing OpenAI-compatible exec acceptance test. It now opts into
  project trust and isolates `XDG_CONFIG_HOME` and `XDG_DATA_HOME`. The old
  test loaded the developer's real stage model routes, called a real remote
  provider, and hung despite its local fake server.
- Fixed the sandbox adapter expectation on WSL. A Linux host without the helper
  now expects the explicit WSL fallback when WSL is detected, and the generic
  unavailable backend elsewhere.
- Added `agent.ModelSelection` and typed stage and escalation resolver function
  types. Every resolver now returns provider, provider profile name, model, and
  reasoning effort as one value.
- Cross-provider stage records now use the routed provider profile. Tier routes
  carry the primary profile name. Design fallback carries the complete primary
  selection.
- Successful trajectory escalation now disables normal stage routing for later
  iterations. Tests prove later calls and records use the escalated provider
  and model.
- Added reasoning tokens to `StageUsage` and `StageRecord`. Removed the unwired
  `StageUsage.CostUSD`. `PipelineResult` now totals cached-input, cache-write,
  and reasoning tokens without adding subsets to input or output.
- Fixed a review-discovered accounting bug in `runStageWithContext`: a failed
  context retry previously discarded usage from the first request. A metered
  wrapper now preserves and merges all five token dimensions across failure.
- Added exact successful context-retry, failed context-retry, typed-output
  retry, cross-provider, escalation, and pipeline-total assertions.
- Validation is green: full `internal/splice/...`, `internal/agent/...`,
  `internal/cli`, `internal/sandbox`, and `internal/tui` tests; focused vet;
  `go build ./...`; gofmt; and `git diff --check`.
- This work is still uncommitted in the public repo. PE2 has not started. The
  pre-existing TUI viewport changes remain in the same working tree and were
  not reverted or committed.

### 2026-07-26: PE2 usage attribution implemented locally

- Added `CollectOptions.OnUsageResult`. It fires exactly once when a provider
  stream ends and states whether the provider reported usage. Legacy `OnUsage`
  still fires only when usage exists.
- Added `agent.AttributedUsage` and `Options.OnAttributedUsage`. Pipeline stage
  options emit provider profile, model, stage, iteration, normalized usage, and
  the usage-reported flag. Legacy and attributed callbacks are mutually
  exclusive, so one provider stream cannot double emit.
- Context retries reuse the attributed stage options and emit one callback per
  provider stream. Step-back uses stage `step_back` with the active selection.
- Stream JSON usage events gained typed `usageReported`, `stage`, `iteration`,
  and `usageSequence` fields. The false usage-reported value uses a pointer and
  remains present on the wire.
- Exec assigns one monotonic sequence per attributed callback. Stream and
  session usage events persist the same provider, model, stage, iteration,
  reported state, and sequence. A parent review caught and removed a duplicate
  stream event: the first worker version called the legacy writer before the
  attributed stream writer.
- Added focused collector, stage-option, context-retry, stream round-trip, and
  exec stream/session-correlation tests. The exec test also isolates the user
  config so real stage routes cannot contaminate it.
- Used the project `pi-subagents.impl-worker` on the lower-cost
  `openrouter/xiaomi/mimo-v2.5` model for the first implementation pass. It
  exceeded its configured turn budget after completing the patch. Parent
  review applied the duplicate-event fix and stronger tests.
- Created the project read-only `splice.cheap-reviewer` agent on the same cheap
  model. It has only `read` and `bash`, no intercom dependency, and completed an
  independent PE2 review without the built-in reviewer's extension failure.
- Simplified the plan after implementation: PE2 emits attributed events only.
  PE3 creates the ledger when pricing makes it useful. This avoids dead private
  state between checkpoints.
- Validation is green: `internal/zeroruntime`, `internal/streamjson`,
  `internal/splice`, and `internal/cli` tests; focused vet; `go build ./...`;
  gofmt; and `git diff --check`. PE2 remains uncommitted with PE1.

### 2026-07-26: PE3 request pricing implemented locally

- Added strict token validation at the runtime, usage tracker, model cost, stage
  record, and pipeline request record boundaries. Cached reads and cache writes
  must be disjoint input subsets. Reasoning must be an output subset. Invalid
  counts no longer clamp.
- Added `agent.UsageCostEstimate` and a registry-backed cost estimator. A known
  model with reported zero usage has a priced zero pointer. Missing usage,
  model, registry, or rates is unpriced. Malformed reported usage is an error.
- Added one serial request ledger at the orchestrator boundary. It assigns the
  sequence, normalizes usage, calls the estimator once, stores the request,
  enriches the attributed callback, and preserves the legacy callback fallback.
- Added `PipelineUsageRecord`, explicit request cost states, estimate source and
  date, request counts, and complete, partial, unavailable, and not-applicable
  aggregate coverage. Pipeline totals now come only from request records.
- Stage token and cost fields now group request records by stage and iteration.
  Auxiliary `step_back` requests affect pipeline totals without a synthetic
  stage record. Context and typed-output retries stay separate request records.
- Added a stage integrity check between stage-reported usage and grouped request
  usage. Code-writer and test-generator post-stream errors now preserve their
  consumed usage, including file-application failures.
- Added a typed provider usage-error event. Anthropic and Gemini normalization
  errors no longer disappear as missing usage. They become one reported request
  with `costStatus: error` and do not invoke the estimator.
- Stream JSON and session events now carry a priced-zero-safe `costUsd`, status,
  estimate flag, provenance, source, date, reason, routed identity, and all token
  dimensions. Exec uses the orchestrator sequence. It does not price again.
- The TUI installs the same estimator for pipeline runs, persists enriched
  events, sends live usage with the routed model, and keeps the response usage
  slice for stale or cancelled-run recovery. Other run kinds keep legacy usage.
- The lower-cost `openrouter/xiaomi/mimo-v2.5` implementation workers timed out
  after producing PE3a and PE3b patches. Parent review removed dead code, fixed
  malformed-state handling, strengthened validation and parity tests, and
  reduced duplicate event logic.
- Independent review found three defects: missing rates were errors instead of
  unpriced, provider normalization errors disappeared as missing usage, and the
  stage integrity check was absent. All three were fixed. A focused reviewer
  follow-up verified the corrections.
- Validation is green: the complete root `go test ./... -count=1`, the separate
  `memd` test suite, the PE3 vet gate, `go build ./...`, gofmt, and
  `git diff --check`. PE1 through PE3 remained uncommitted with the unrelated
  pre-existing TUI viewport work preserved at this point.

### 2026-07-26: PE1 through PE3 published; ponytail review recorded

- Published the complete validated public working tree to `dev` in commit
  `7440a8e`. This includes PE1 through PE3, the acceptance repairs, and the
  transcript viewport and scroll-pinning work developed in the same session.
- Recorded the post-implementation complexity review in the PE plan. It lists
  nine cleanup opportunities with an estimated reduction of 450 lines.
- No cleanup changed the published behavior. Triage the list before PE4 so
  simplification and benchmark contract changes remain separate.

### 2026-07-27: splice-internal becomes a docs-plus-history archive

- Decision: public `Taf0711/splice` is now the single source of truth for code.
  All code work happens on its `dev` branch.
- `splice-internal` keeps ROADMAP.md, MEMORY.md, `plans/`, `docs/flug-design/`,
  and the full pre-publication history. Its Go tree is frozen.
- That tree is already stale. PE1 through PE3 landed on public `dev` at
  `7440a8e` and never came back. `internal/splice/run.go` is 1240 lines here
  against 1517 lines publicly. `PipelineUsageRecord` and `ModelSelection` do
  not exist here at all. Treat internal `.go` files as historical, not current.
- The two repos share no git ancestry. Public was re-rooted at `480083e`, so
  `git merge-base` between them is empty. No merge path exists. Any future
  content movement is a manual copy, not a push or a merge.
- Fixed a git defect found the same day: internal `dev` tracked `public/main`
  instead of its own `origin`, and had never been pushed anywhere. It is now
  pushed to `origin/dev` (695 commits) and tracks it. The branch was
  local-only until then, with no remote backup.
- Unchanged: the sensitive-docs rule. Roadmap, memory, plans, and internal
  notes read and write here.

### 2026-07-27: Track PE cleanup, PE4, PE5, PE6, and PE7a landed

- Nine checkpoints on public `dev`, each gated locally then confirmed by CI:
  CP-A `7389a45`, CP-B `7829e1e`, PE4a `15dc68e`, PE4b `9e137e0`,
  PE4c `eed860d`, PE5 `4ff0a2d`, PE6a `0215c28`, PE6b `0de1c81`,
  PE7a `a740bb7`.
- **CI is no longer blocked.** The 2026-07-26 billing note is stale. Runs
  resumed and every checkpoint above passed in roughly 4 to 5 minutes.
- **Delegation model**: every implementation slice went to `gpt-5.6-luna` at
  high reasoning effort through the codex plugin. The parent wrote the specs,
  reviewed the diffs, ran the gates, and committed. Getting luna working needed
  a fix: three codex installs existed (nvm, homebrew, the Codex.app bundle) and
  the model needs CLI 0.145.0 or newer. `codex update` upgraded only nvm, but
  the plugin broker spawns the homebrew copy, so delegation kept failing with a
  400 while a direct `codex exec` succeeded. The homebrew copy is now removed
  and only the nvm install remains. Leave `/Applications/Codex.app/.../codex`
  alone; it is app-internal and the config points three things at it.
- **Cleanup item 5 dropped after review.** `chatLayoutGen++` fires on every
  handled message, so the viewport cache never outlives a frame, which does
  make five keys at `model.go:3145` redundant. But `model.go:2971` takes
  `width` as an argument and derives `height` from caller-supplied header and
  footer, and three call sites share the cache per event. About eight lines
  against a stale-render risk was a poor trade.
- **PE4a defect caught in review, before commit.** The collector returned an
  error from `Write` while wired through `io.MultiWriter`, which stops at the
  first failing writer. os/exec then aborts its copier and closes the pipe. A
  standalone reproduction confirmed `signal: broken pipe` with the diagnostic
  capture cut to 1040 of about 40005 bytes. One oversized JSONL line would have
  killed the agent and destroyed the diagnostics, which is worse than the
  truncation PE4a exists to fix. The collector now records the error, discards
  the line to the next newline, and resumes. It never breaks the pipe.
  The unit test passed while the integration was broken, because it exercised
  the collector alone. The retest goes through `CommandAgentRunner.Run`.
- **PE4b fixed a regression PE4a introduced.** `UsageSample.CostUSD` had been
  flattened from `*float64` to `float64`, which collapsed a priced zero and an
  unknown price into the same value.
- **PE4c alias trap.** The old `meanCostPerPassedTask` computed
  `TotalCostUSD / PassedTasks`, so it maps to `campaignEstimatedCostPerPass`.
  The similarly named `meanEstimatedCostOfPassedTasks` uses a different
  numerator. Verified numerically, not by trusting the tests.
  `StageBreakdown` gained iteration, provider, and cost status beyond plan item
  12, so PE7 could add CSV columns without forcing a v3.
- **PE6 was not a missing feature.** The TUI already rendered a cumulative
  session cost at `view.go:354`. The number was wrong: costs were re-priced
  from the live registry on every read, and `session_controls.go` discarded a
  record whose pricing failed, so a session with mixed pricing showed a
  confident total that omitted part of the work. Records are now retained on
  every path, including both error returns, so tokens survive when the cost is
  unknown. The status line shows `cost partial`, `cost unavailable`, or
  `cost n/a` instead of a misleading total. A genuine priced zero still renders.
- **Three report contracts now version independently**: `splice.cli.eval.v1`,
  `splice.agenteval.benchmark.v2`, `splice.agenteval.report.v1`.
- **The CSV rename is breaking.** `model` became `requestedModel`, `costUSD`
  became `estimatedCostUSD`, and `modelsUsed` is new. An unknown cost is an
  empty top-level cell and `cost=unknown` inside the packed stage cell. Never
  zero.
- Remaining: PE7b public docs, PE7c full validation and manual real-provider
  acceptance.

### 2026-07-27: v0.1.3 release scope and the version decision

- The user wants v0.1.3 to carry TUI fixes, session cost accumulation, the eval
  harness work, and the crystallize plan handoff fix.
- **0.2.0 is reserved** for Tracks T/W/A, the user-configurable stage and
  workflow GUI, which changes much of the logic and has not started. Only its
  prerequisites exist. So this release must be 0.1.3 even though `dev` carries
  `feat:` commits that would make release-please compute 0.2.0.
- **Use a `Release-As: 0.1.3` commit footer**, which is a one-shot override.
  Do NOT re-add `release-as` to `release-please-config.json`. That setting is
  sticky, it pinned every release to 0.1.2, and PR #9 existed only to remove it.
- Release path: merge `dev` into `main`, let release-please open its staging PR,
  merge that to cut the tag, then dispatch `release-artifacts.yml` by hand
  because GITHUB_TOKEN does not chain the release event.
- **"Handoff" means crystallize, not `internal/swarm/`.** The chain is the
  `/design` conversation, then `/crystallize` calling `CrystallizeAndCritique`,
  which produces a `DesignPlan`, persists `plan_crystallized`, and runs
  `PlanCritic`, then `RunDesignPlanWithResume` executing each task as its own
  pipeline run.
- **Blocked on repro steps** for the TUI defects and the crystallize handoff
  defect. Neither is specified well enough to root-cause, and both have sibling
  callers where a symptom-level patch would leave the real cause in place.

### 2026-07-27: the session cost feature is inert on this user's config

- **Found while reviewing PE6b, not by a test.** The user asked when cost would
  fail to show. The answer is always, for their current setup.
- `~/.config/splice/config.json` configures two models: `gpt-5.5` and
  `z-ai/glm-5.2`. Neither exists in `internal/modelregistry/catalog.go`, which
  is a hand-curated list of about 18 entries (the OpenAI 5.6 and 4.x families,
  three Anthropic models, three Gemini models, plus `moonshotai/kimi-k3` and
  `qwen/qwen3-coder-30b-a3b-instruct`).
- Verified there is no escape hatch. `Registry.Get` is an exact map lookup and
  `normalizePattern` only lowercases and trims, so `gpt-5.5` cannot resolve to
  `gpt-5.6-*`. `applyModelsDevOverrides` iterates existing entries only, so the
  models.dev snapshot refreshes prices for curated models but never adds one.
- Consequence: every request is unpriced, coverage is always `unavailable`, and
  the v0.1.3 session-cost feature does nothing for this user. Not degraded,
  inert.
- **This nearly shipped as a silent failure.** The proposed display change
  (tokens only when coverage is unavailable) would have rendered the inert
  state as normal-looking output, indistinguishable from the feature working.
- **RESOLVED the same day in `00d93f2`. No manual rates were needed.** See the
  next entry. Do not add hand-written rates to `catalog.go`.
- The `~$0.42` partial-coverage display change is agreed but deliberately NOT
  landed. Revisit now that pricing resolves.

### 2026-07-27: model pricing was a wiring gap, not missing data

- `00d93f2` on public `dev`, CI green. Both of the user's models now price:
  `z-ai/glm-5.2` at 0.6692/2.1032 and `gpt-5.5` at 5/30.
- **The data was always on disk.** Splice fetches and caches
  `https://models.dev/api.json` on every start. A probe against the real cache
  showed 172 providers, 341 OpenRouter models, and
  `openrouter/z-ai/glm-5.2 in=0.6692` already parsed into memory, while
  `Registry.Get("z-ai/glm-5.2")` returned false. Roughly 5700 models were loaded
  and 18 curated entries were allowed to read from them.
- Two causes: `modelsDevSlugs` returned nil for every provider kind except
  anthropic, openai, and google, so `ProviderOpenAICompatible` never attempted a
  lookup; and `applyModelsDevOverrides` iterated the curated slice, so it could
  refresh a known price but never add an unknown model.
- **Provider scoping is mandatory, not a nicety.** The same model id carries
  very different prices: `z-ai/glm-5.2` is 0.6692 under openrouter, 1.2 under
  crossmodel, and **0 under nvidia**. A first-match lookup would price a paid
  model at zero. Derived entries are keyed to an explicit provider key, and
  without provider context a model stays unpriced rather than guessing.
- **Two defects were caught in review before this landed, neither visible in the
  implementation's own tests.** First, one malformed upstream record
  (`~x-ai/grok-latest`, max output greater than context) made `DefaultRegistry`
  return an error, leaving callers with no models at all including curated ones;
  82 of 5751 models.dev records have that shape, so derived records now skip
  individually while curated ones keep strict validation. Second, all six
  production call sites still used the zero-argument `DefaultRegistry()`, so
  nothing was ever added and the feature was inert.
  **CORRECTION (2026-07-28): that second claim is wrong, and so is this
  commit's message.** `00d93f2` fixed the call site in the `/model` picker
  (`internal/tui/command_center.go`) and MISSED `internal/tui/model.go:766`,
  which builds the catalog that feeds the usage tracker, the compaction cap,
  and stage routing. The TUI therefore priced nothing on a derived model for
  another day, while the picker listed the very model the tracker could not
  price. Fixed in `2c3089b`. See the entry "the TUI never saw a derived model".
- Correct research conclusion: **genai-prices is not needed.** An earlier
  WebFetch summary wrongly reported models.dev as having 15 providers and no
  OpenRouter key. Reading the real 3.2 MB cache showed 172 providers including
  `openrouter`. Trust the cached file, not a summarized fetch of it.
- Still open in this area: tiered pricing. `openai/gpt-5.5` doubles above 272k
  context via `tiers` and `context_over_200k`, and `modelsDevModel` parses only
  flat cost fields, so long-context requests under-price silently.

### 2026-07-27: crystallize root cause, and the TUI scroll cause

Both found by reading real session data and code, not from a bug report.

**Crystallize discards a plan that succeeded.** `CrystallizeAndCritique` runs
the crystallizer, persists `plan_crystallized`, and THEN runs `PlanCritic`. When
the critic errors it returns an error for the whole call, and the TUI handler at
`internal/tui/model.go:2257` early-returns on `msg.err != nil`, so
`m.pendingPlan` is never set. `/approve` then reports "No pending plan" for a
plan that is on disk.

Session evidence: 4 sessions hold a real `plan_crystallized` event, **0 hold a
`critique_recorded` event, and 0 ever reached `plan_approved` or
`task_started`**. The critic has failed every observed time. `ExtractPlanCritique`
requires a structured `plan_critic_output`, which off-catalog models may not
produce reliably.

The message also lies: it says "Crystallization failed" when the crystallizer
succeeded. That wrong message is why the user believed the agent could not call
the tooling.

Fix: set `pendingPlan` whenever the plan itself is valid, treat a failed
critique as "no critique available" rather than "no plan", and name the stage
that actually failed. The existing approve guard already permits a nil critique.

**The agent cannot crystallize on request, by design.** The design conversation
registry allows only `read_file`, `list_directory`, `grep`, `ask_user`. No
crystallize tool exists, yet `design_conversation.md:3` tells the model
crystallization "happens separately", so the model offers and cannot deliver.
Adding a tool that CALLS the stage would violate the orchestrator boundary, but a
tool that REQUESTS the harness crystallize mirrors `ask_user`, which already
routes a tool call through `options.OnAskUser` at `internal/agent/loop.go:1691`.
The user wants both entry points with the user as the final gate, and the offer
gated deterministically so it stays contextual.

**TUI scroll degrades with transcript size.** `chatLayoutGen++` runs
unconditionally for every handled message at `internal/tui/model.go:1107`, and
mouse wheel ticks are messages. `measureTranscriptBodyItems` walks every
transcript item. So each wheel tick invalidates the viewport cache and rebuilds
spans across the whole transcript, and the cost grows linearly with size.

This is the ponytail cleanup item 5 that was dropped earlier the same day. The
analysis correctly observed the cache "never outlives a frame" and then treated
that as evidence the extra keys were redundant, instead of recognizing that the
cache barely works. The small safe diff was chosen over the performance bug.

**Design-mode state machine defects, after an adversarial pass.** The canonical
`DesignPhase` (None, Conversation, Review, Executing, Completed) is derived
correctly by `ReconstructDesignState` but consulted in only one place, on
resume. Every command guard reads in-memory proxies instead.

- D1 survives: `/approve` has no phase or mode guard, and
  `EventPlanApproved` does not clear `state.Plan`, so a resumed executed session
  can re-approve and re-run completed work.
- D2 narrowed: a failed TASK returns a nil error and cleans up correctly. Only
  infrastructure errors hit the early return.
- D3 survives with reduced severity: three independent `planID` mints leave
  revision permanently at 1, but **nothing reads the revision number**, so this
  is bookkeeping only.
- D4 REFUTED as first stated: resume never keyed on `planID`. It keys on
  `runOpts.CompletedTaskIDs`, which **no caller anywhere passes**, so
  `RunDesignPlanWithResume` always starts from task zero. Since no plan has ever
  executed, this is dead flexibility; delete the parameter or leave it, do not
  spend a checkpoint wiring it.

**Hang, unconfirmed.** Real design sessions die from provider stream failures
(3 connection resets, 1 timeout) with no retry anywhere in the streaming path,
and `DefaultStreamIdleTimeout` is 5 minutes, so a stalled stream looks frozen
for minutes. Crystallize is the most exposed call. But slash commands are never
persisted as session events, so there is no direct trace. Confirm by checking
whether a hang eventually surfaces `provider stream error`.

### 2026-07-27: agreed priority order for v0.1.3

1. Crystallize plan discard, plus the wrong "Crystallization failed" message.
2. TUI scroll: stop re-measuring the whole transcript per wheel tick.
3. Setup wizard cost display, strip `ModelCost` literals from `catalog.go`,
   tiered pricing.
4. Cut v0.1.3: merge `dev` into `main`, one-shot `Release-As: 0.1.3` footer,
   merge the release-please PR, then dispatch `release-artifacts.yml` by hand.

Deferred by decision: D4 resume wiring, D3 revision correlation, the agent-side
`request_crystallize` tool (wanted, but after the discard fix).

### 2026-07-27: item 1 landed, and a test that CI can never fail

- **Item 1 is fixed in public `c1f3b78`** (local commit on `dev`, not pushed).
  The handler reads `plan.Validate()` to separate a crystallizer failure from a
  critic failure. A valid plan stays pending with a nil critique, which the
  approve guard already permits, and the message names the critique.
- **`887c97b` repairs a safety defect that `c1f3b78` introduced.** The first
  fix cleared the critique on every error. That is too broad.
  `CrystallizeAndCritique` returns an empty critique when the critic STAGE
  fails, but it returns the FULL critique when only the `critique_recorded`
  write fails (`internal/splice/design_workflow.go:150`). A discarded critique
  loses its must-fix verdict, and the approve guard blocks only when a critique
  is present, so `/approve` would run work that the critic rejected. The
  handler now keeps a critique that passes `critique.Validate()`, which
  requires an overall assessment and so separates a real critique from the
  empty one. A kept must-fix critique blocks approve. A critique that did not
  save is now reported as not saved, not as a critique that failed, which
  avoids repeating the misleading-message half of the original bug.
  **Lesson: the first fix traded a discarded plan for a bypassed safety gate.**
  A narrow repair to an error path needs the whole error contract read first,
  not just the path in the bug report.
- **The resume path never had this defect.**
  `internal/splice/design_lifecycle.go:107` handles `plan_crystallized` by
  setting phase Review, the plan, and a nil critique. So `/resume` always
  restored a plan that the live path dropped. It now has a test.
- **Codex code was good. Codex verification was not, three times out of
  three.** In all three delegated tasks it reported
  `TestStartNewSessionResetsState` and `TestCompleteSetupLandsInDesignMode` as
  unrelated pre-existing failures. None of it reproduces: the full TUI package
  passes every run, in isolation and under `-race`. On the third task Codex
  said `git stash` was blocked by a read-only `.git`, so it could not have
  checked the clean tree that it claimed to check. Root cause is its sandbox,
  which needs a writable `GOCACHE`. **Accept Codex diffs, re-run Codex test
  claims.** Give it the code work and verify the results in the real tree.
- **Codex tested the mechanism, not the symptom.** Its cases asserted that
  `pendingPlan` gets set. None drove `/approve`, which is the actual bug
  report. A symptom-level test was added. Both new tests were then confirmed to
  fail with the handler change reverted, so they are real regression cover.
- **`TestDefaultRegistryRealCachedSnapshot` is non-hermetic and CI can never
  fail it.** It skips when the models.dev cache is absent
  (`modelsdev_test.go:370`), and CI runners have no cache, so it always skips
  there. On a developer machine that ran Splice, the cache exists and the test
  hard-asserts `0.6692 / 2.1032 / 0.12428` for `openrouter z-ai/glm-5.2`.
  Upstream prices already moved: two local runs seconds apart read `0.7182`
  then `0.7378`. So the test pins live upstream pricing, fails for anyone with
  a cache, and stays green in CI forever. Fold this into item 3, which already
  strips `ModelCost` literals from `catalog.go`. Assert shape and provenance,
  not upstream rates.

### 2026-07-28: the scroll fix that had to be measured twice

- **Item 2 is fixed in public `c63edd0`** (local commit on `dev`, not pushed).
  `Update` skips the `chatLayoutGen` bump for `tea.MouseWheelMsg` only.
  Eleven lines.
- **The first attempt was correct and still wrong.** It captured a full
  layout signature before and after `updateModel` and bumped only on a change.
  The signature RENDERED the header and footer, including `footerView` with the
  pinned plan panel, twice per message. Measured: **24% slower at 200 rows** to
  gain 11% at 20000. It was rejected. It also added 236 lines, a new model
  field, a new invariant to maintain at four in-place row write sites, and a
  stubbed clock inside signature capture.
- **The lesson: a performance fix without a measurement is a guess.** The first
  attempt passed every correctness check and would have shipped a regression at
  realistic transcript sizes. What caught it was benchmarking the SMALL case,
  not the big one the bug report named. Always measure the common case, because
  that is the one a fix for the extreme case tends to hurt.
- **Why the narrow fix is safe.** The viewport cache at `model.go:3171` already
  compares width, source length, flush frontier, detailed mode, and subchat
  mode. So the generation key uniquely protects height, footer line count, and
  a change to the last row. A wheel tick changes none of them. Wheel handling
  moves wizard, picker, suggestion, and composer cursor positions, which are
  selection indices and not line counts. The jump-to-bottom hint shares a row
  that is always written, so it is one line whether or not the hint shows.
  `composerDescriptionHint` needs exactly one suggestion at index zero, so a
  wheel cannot toggle it.
- Measured before and after on the same machine, 100 iterations over wheel
  `Update` plus `View`: 200 rows `353192 -> 234669` ns/op, 2000 rows
  `687052 -> 366618`, 8000 rows `1655769 -> 833564`, 20000 rows
  `3541441 -> 1723502`. Faster at every size.
- **The plugin runtime fixed Codex's reporting.** Through the raw `codex exec`
  CLI, Codex called the same two TUI tests pre-existing failures four times,
  and they never reproduced. Run through `/codex:rescue` and the plugin job
  runtime, with real repo access instead of a sandbox with a read-only
  `GOCACHE` and `.git`, it correctly attributed the same failures to its own
  environment. **Prefer `/codex:rescue` over raw `codex exec` for anything
  where the verification result matters.**
- **Give a delegated performance task an explicit acceptance gate.** The second
  attempt got the baseline numbers and the rule that it must beat them at every
  size, including the small one. It reported honestly against that gate. The
  first attempt had no gate and reported success on a regression.

### 2026-07-28: item 3, and the price that was never ours to keep

Item 3 is done and pushed as Track MP. The work is in `ROADMAP.md`; this entry
holds what the commits do not show.

- **models.dev is accurate; it lags.** An early reading of the OpenRouter data
  looked like a systematic error, because `z-ai/glm-5.2` moved by a uniform
  scalar across input, output and cache read. That reading was WRONG. Comparing
  both sources at the same instant, **312 of 317 OpenRouter models agree to the
  digit**. Five disagree, each by its own factor and in both directions:
  `qwen/qwen3-coder-next` +63.6%, `nvidia/nemotron-3-ultra` -16.7%,
  `google/gemma-4-26b` +7.1%, `qwen/qwen3.5-35b-a3b` -6.7%, `z-ai/glm-5.2`
  +4.2%. That is a scrape lag, not a method difference. A uniform scalar inside
  ONE model only means the provider re-priced it and scaled its rates together.
  It carries no information about currency. **Lesson: a pattern in one sample
  is not a mechanism. Check whether it holds across the population before
  naming a cause.**
- **glm-5.2 drifted 14.9% in about a day** (0.6692 to 0.7686), through three
  quotes. That is why the pinned-price test broke CI, and why a 7-day cache
  tolerance under-reports on a model that is re-pricing.
- **Stripping prices killed stage routing, silently.**
  `stage_tier_resolver.go` ranks models by input rate, and `effectiveInputRate`
  returns `+Inf` when unpriced. With no prices every candidate compared equal,
  every branch skipped every candidate, and each stage fell back to the primary
  model. A change made to improve cost ACCURACY would have made runs cost MORE.
  Seven tests caught it. **Lesson: price data is not only a display input. Grep
  for every consumer before removing a field, not only the one the ticket
  names.**
- **The user's proposal beat the planned fix.** A rank table was about to be
  added so the resolver could order models without prices. Shipping the
  snapshot removed the need for it. It also repaired `modelsDevMaxAge`, whose
  comment justified dropping a stale cache because "the curated catalog is
  likely fresher" — untrue the moment the catalog held no prices. **Lesson:
  when a fix needs a new fallback, ask first whether the gap should exist.**
- **A shipped snapshot is not the same as a kept one.** Keeping the last disk
  cache does nothing on a first run, where no cache has ever existed. Only an
  embedded file fixes that.
- **`SourceLastVerified` used to lie.** Derived entries stamped the hardcoded
  catalog constant `2026-06-04` on prices fetched minutes earlier, under the
  provenance work of `0de1c81` and `0215c28`. It now reports the date of the
  source that supplied the price.
- **The adversarial pass earned its cost.** It was told the removed curated
  tier guard was "value-neutral for gemini", which was true that day, and it
  attacked the case where upstream stops matching instead. It also found that a
  tier omitting `cache_read` bills cached tokens at the full input rate: real
  in the live data, 5 models, 3 of them on openrouter, up to a 10.7x overcharge
  on `openrouter/google/gemini-3.1-pro-preview`.
- **CI tests were never offline.** `internal/cli` starts a background refresh
  at `exec.go:156` and `app.go:624`, so every CI run made a real request to
  models.dev. That is what broke CI on `c63edd0`; only the test was repaired at
  the time, so every run since raced the same way and won. `1dcdf3a` sets
  `ZERO_DISABLE_MODELS_FETCH` on the test step. Six tests had to clear the
  variable first, which also fixed a suite that failed for anybody who had it
  set in the shell.
- **The refresh workflow is inert until v0.1.3 reaches `main`.** GitHub runs a
  schedule, and accepts a dispatch, from the default branch only. `gh workflow
  run` returns 404 today. The workflow is pinned to `dev` with `ref: dev` and
  `--base dev`, so once it is on `main` the schedule fires from main's copy but
  operates on `dev`. Its shell logic is proved against the live endpoint: 173
  providers, 3.28 MB, change detected, gzip round-trip clean. The
  `gh pr create` path is unproven. Watch the first real run.
- **Delegation note.** Codex reported "targeted tests pass" for the catalog
  strip while 8 tests failed in packages its sandbox could not run. The tests
  it could not run were the ones that mattered. **Always re-run the whole gate
  locally, and give a delegated change a tripwire it must not edit** — the
  fixed skip count of 28 and the untouched routing assertions are what proved
  these changes were sound rather than merely green.

### 2026-07-28: the TUI never saw a derived model

- **Found by the user asking "is the TUI wired properly?" and refusing a
  guess.** Everything in Track MP was correct in the CLI and dead in the TUI
  for the configured model. A TUI test plan had already been written on the
  assumption that the display worked; running it would have shown "cost
  unavailable" at every step and pointed the blame at the new pricing code.
- **The defect.** `internal/tui/model.go:766` built the catalog with a
  zero-argument `DefaultRegistry()`. `applyModelsDevOverridesWithStats` returns
  early on an empty provider profile, BEFORE the overlay check, so no cache
  state and no overlay flag could ever put a derived entry in that registry.
  The catalog was assigned once and never rebuilt, and it feeds the usage
  tracker, `modelContextWindow` (the compaction cap) and `ResolveStageTierModel`
  (stage routing).
- **Proved with the real binary, not by reading.** The same `splice models`
  command lists `z-ai/glm-5.2` when the provider profile resolves and does not
  list it when it does not. The active profile here is openrouter with
  `z-ai/glm-5.2`, a derived entry, so in the TUI every record was unpriced and
  coverage read "unavailable".
- **Two things had to move together.** `usage.NewTracker` copies the registry
  by VALUE, so rebuilding the catalog does not reach the tracker. The fix adds
  `Tracker.SetRegistry` and calls it from a rebuild helper, which runs at the
  four places that reassign `m.providerProfile`: the provider picker twice, the
  setup wizard, and onboarding. Fixing only the construction site would have
  worked at startup and decayed at the first provider switch.
- **The obvious fix for the sibling sites was the wrong one.** The picker, both
  reasoning-effort paths and the model list also called `DefaultRegistry()`.
  Passing them a profile would have been correct and slow: the comment at
  `internal/tui/session_controls.go:178` warns that the call rebuilds the whole
  registry and must not reach the render path, and `effortForcingAllowed` has
  three call sites. They now read `m.modelCatalog`, which fixes the same bug and
  REMOVES a rebuild instead of making it heavier.
- **Why it survived a whole day of pricing work.** No test pinned the tracker
  resolving a derived model. The `/model` picker listing `glm-5.2` looked like
  proof the wiring worked, because `00d93f2` had fixed the picker.
  `TestTUIUsageTrackerPricesDerivedModel` now pins it.
- **Lesson: verify the surface the user actually uses.** Every check that day
  ran through the CLI or the unit tests. Both were green while the TUI, the
  only interface the user runs, was inert. **A display function reads correctly
  and still shows nothing if the data feeding it is empty. Trace the producer,
  not only the formatter.**

## 2026-08-03 — the 0.1.4 review sweep: what six unrelated bugs had in common

Twenty-seven commits closing five external review reports. The individual fixes
are in `ROADMAP.md` Track RR. What is worth keeping is why they were the same
bug.

- **One shape, six instances: produced, sometimes validated, never consumed.**
  The usage payload wrote `stage`/`provider`/`iteration` and the reader struct
  declared none of them, so `encoding/json` discarded them silently. The
  orchestrator wrote the stage roster into every stage input and no stage
  forwarded it to a model, while the prompt in the same commit told stages to
  use it. `selectRelevantContext` read prior summaries for two keys no tier
  roster ever contains. `xhigh` and `max` were enum members three adapters
  dropped. Acceptance criteria carried runnable commands that the planner
  flattened to text. `TestRunResults.Tests` was overwritten by one synthetic row
  on every live path. Each shipped green, because a producer and a consumer that
  never agree still compile and still pass tests written against either half.
- **The guard that generalises is a pairing test.** One test that collects the
  keys a writer emits, reflects the reader's tags, and fails on any key that is
  neither read nor listed as deliberately unread with a reason. Coverage does
  not find this class; every one of these lived in covered code.
- **A narrowing fix reads exactly like a fix.** `8590900` wrapped the exec core
  re-registration in `WithoutEmptyBackedTools`, which turned "plugin skills are
  always clobbered" into "clobbered when default-dir skills also exist". The
  symptom got rarer and therefore harder to attribute. Whenever a change makes a
  failure conditional rather than impossible, say so.
- **Fixing the test can hide the bug.** The race detector flagged a counter in
  the acceptance verifier's own test. Adding a mutex made CI green and left the
  defect — a timed-out command was abandoned, so two commands could sit inside
  the tool runner at once, and that path goes through the permission gate and
  the command ledger. Proven by removing the real fix and watching the mutexed
  test still pass. **When the detector points at a test, ask what the production
  code did to make the test racy.**
- **Local validation ran the wrong command for three days.** CI runs
  `go test -race ./...`; every local gate ran without `-race`. Three commits
  landed red and nobody looked at the runs. **Read the CI workflow before
  trusting a local green.**
- **Measure before capping.** Rendering acceptance criteria capped each task at
  three, which looked reasonable and concealed 11 verification commands across
  6 tasks in the real plans on this machine — inside the commit whose only job
  was showing commands before approval. The rule had to split along the line
  that mattered: a criterion carrying a command is always shown, and only
  criteria that run nothing are capped.
- **A test can pass for the wrong reason.** The first attempt at pinning the
  no-overlap property passed without the fix, because a cancelled command
  returns too quickly for the window to open. The test now holds a cancelled
  command open briefly, the way a real process does. **A regression test that
  cannot be made to fail is not yet a test** — mutate the fix away and watch it
  go red, and make sure the mutation still compiles, because a build failure is
  not a red test.
- **The spec can be the thing that is wrong.** Instructing the escape-sequence
  strip at `genericCardBody` closed the generic path and left `bash`, `exec`,
  `grep` and `diff` — the highest-risk renderers — untouched. The agent reported
  the gap rather than silently widening scope; the fix belonged one level up,
  where every renderer normalizes its detail. Ask which callers a fix location
  actually covers.

## 2026-08-03 — hardening a default deleted a live credential file

Track CR. The change was right and the mechanism was wrong, and the wrong
mechanism destroyed real data on the developer's machine before anything was
committed. The details are in `ROADMAP.md`; the parts worth carrying forward:

- **A read must never mutate a credential.** Migration was put on the store's
  load path, so `codexAccountForKey` — a library accessor that builds a
  default-path store to read one account claim — moved and deleted the live
  plaintext token file. Running the test suite was enough to trigger it. The
  pattern the spec pointed at,
  `config.MigratePlaintextProviderKeys`, is called explicitly at
  `internal/cli/app.go:687` and is never a side effect of reading. **Lazy
  migration is convenient because it needs no wiring; that is the same reason
  it fires from places nobody audited.**
- **The spec caused it.** It said "migrate on first load" while citing a
  reference that does the opposite. Naming the mechanism ("an explicit call at
  startup, as app.go:687 does") would have prevented the whole incident.
  Describing a pattern and then contradicting it in the instruction is worse
  than citing nothing.
- **When a default gets safer, audit what the old one was quietly protecting.**
  Plaintext-by-default made a default-path store in production code harmless.
  Hardening the default is what armed it. Any call site that relied on the weak
  default being cheap becomes a live wire the moment it is not.
- **A red proof that passes may mean the mutation missed, not that the code is
  safe.** The fail-safe test was mutated by moving a file removal — and passed,
  because the insertion sat after an early return that the failure path takes.
  Only a removal placed *before* the verification made it fail. When a mutation
  does not turn a test red, first ask whether the mutated line executes on the
  path under test.
- **Guard the real thing, not just the temp dir.** The deletion was caught by
  hashing `~/.config/splice/*` before and after the suite, not by any test.
  Every later run in this session compared those hashes on both sides. **A test
  suite that can reach a user's real credentials is a defect regardless of
  whether it currently corrupts them** — the isolation now lives in
  `deps.migrateOAuth` being nil-able, which is why a dozen test files changed
  alongside a small production diff.

## 2026-08-13 — the audit that found the fork's own fossil, and where the direction settled

A full read of the orchestration, trajectory, memd, and worktree layers, plus
a delegated real-git end-to-end verification of the worktree subsystem.
Findings and decisions live in `plans/audit-and-direction-2026-08-13.md`;
tracks WL, SD, PC, LN opened in `ROADMAP.md`. What is worth keeping:

- **The frame is dynamic; the policy is fixed.** The roster-driven loop, typed
  `Stage` interface, and config-driven model resolution make the next *stage*
  cheap. The tier tables, budgets, trajectory weights, ordered rule chain,
  closed action enum, and stage-name-keyed switches make the next *policy*
  expensive. Every strategic feature discussed this session (configurable
  topology, plan composer, learning) needs policy-as-data first; that is
  Track SD, and it is zero behavior change.
- **The RR defect shape came back.** `StageBudget.ModelTier` is set and
  validated but read by no resolution path; `ExecutionStage.DependsOn` is
  produced and never consumed; `IterationState.TypeErrors` is declared and
  always 0. Produced, sometimes validated, never consumed — three more
  instances, found by tracing reads rather than trusting names. The pairing
  test idea from the 0.1.4 sweep would have caught all three.
- **The strategy session re-derived Track T.** The "user-defined pipelines as
  JSON topology" path was independently reasoned into existence before
  checking the roadmap, where it has been approved and specified since
  2026-07-23. Process note: read the roadmap before strategizing. The genuinely
  new half is the second author: an LLM plan composer emitting the same
  validated schema (Track PC), with re-planning as a trajectory action. The
  LLM compiles plans; it never orchestrates at runtime.
- **Learning got a shape that fits the product instead of fighting it.** The
  Bitter Lesson tension dissolves once the deterministic layer is understood
  as the reward function rather than the hand-crafted knowledge: verifiers
  give ground truth most harnesses lack, and learning replaces the hardcoded
  policy, not the substrate. Ladder: traces (LN1) -> counting (LN2/LN3) ->
  retrieval exemplars (PC3) -> training (LN4, deferred). memd's schema was
  built for this (provenance, confidence, dedupe) and stores three trivial
  memory types and zero outcomes; the warehouse is fine, the inventory is the
  gap.
- **The worktree subsystem is the best-engineered code in the repo and the
  least exposed.** Verified end to end against real git: plumbing-level
  snapshot/restore, ancestry-aware merge-back, honest conflict handling, all
  statuses correct. But no removal code exists, this machine had 690 stale
  worktrees (12 GB), most created in unexplained pairs one second apart
  (WL3), zero recovery refs have ever been created in the real repo, and the
  TUI never gets rollback because it deliberately passes nil recovery. The
  flagship anti-death-spiral behavior is headless-opt-in only; the fix
  direction is TUI runs *inside* worktrees, not destructive recovery in a
  live checkout.
- Public-repo state at session end: three uncommitted pieces on `dev`
  (specialist tool-call visibility + review hardening, the MapDesignHistory
  ask_user fix, and their tests), pending commit split and the CONTRIBUTING
  TUI capture.

## 2026-08-13 (2) — the TUI wiring audit: the 5-iteration cap that never applies

A follow-up read of the TUI pipeline wiring against headless exec, plus the
token/wall accounting. Recorded as plan-doc section 7; SD6-SD8 opened in
Track SD. What is worth keeping:

- **The TUI runs the full pipeline by default.** Empty `runKind` is
  `tuiRunPipeline`; every normal prompt goes through `splicerun.Run`. TUI
  extras: self-correct LSP diagnostics. TUI gaps: no worktree, nil recovery
  on both the plain and design-plan paths, so rollback aborts in the primary
  interface (as designed, documented).
- **A reused knob silently displaced a designed limit.** `run.go` treats
  `options.MaxTurns` as the pipeline iteration cap ("closest equivalent
  turn"). But MaxTurns is the agent-loop tool-turn budget (default 50,
  ceiling 500, `/turns`-adjustable) and is never 0, so
  `defaultMaxIterations = 5` is dead code on every real path. Worse, the two
  limits disagree: trajectory hard-limit fires at 50 while stage-failure
  recovery retries stop at 5. Lesson shape: when a new subsystem borrows an
  existing field for a "closest equivalent" meaning, the old default becomes
  the new behavior with no diff anywhere.
- **The budget abort, not the trajectory envelope, actually ends runs.**
  `tokensConsumed()` sums Input + Output + Cached, but cached input is a
  documented subset of input (`uncached = Input - Cached - CacheWrite`), so
  prompt caching makes the budget fire early; billed reasoning tokens are not
  counted at all. With tier budgets around 28k total, termination today is
  "budget kills the run early", often after one or two passes. A second
  instance of the day of "produced and validated but wrong at the consumer":
  the usage fields were defined precisely in `zeroruntime/types.go` and
  summed loosely in `trajectory.go`.
- **The wall clock is a between-iterations check only.** One pass (up to 6
  stages at 120s timeouts) can overrun the 600s budget unchecked; with the
  50-pass cap above, the worst-case TUI run is far past the intended
  envelope.

## 2026-08-13 (3) — routing optimizes price; the system measures speed and throws it away

A read of the routing path and TPS handling. Plan-doc section 8; SD9 opened,
LN1 extended. What is worth keeping:

- **Routing's only signal is price.** The tier fallback picks the cheapest
  tool-capable model in the provider family ("medium") or the most expensive
  reasoning-capable one ("reasoning"). No speed field exists in the catalog,
  and models.dev does not publish one to ingest.
- **Speed is measured and discarded.** Every StageRecord carries LatencyMs;
  the TUI computes TTFT and session TPS for /context; none of it reaches the
  usage ledger, memd, or routing. Third recurrence of the RR shape in one
  day: produced, never consumed.
- **The fixed 120s stage timeout is a routing failure mode.** With 8,192-token
  output budgets, a full-budget generation needs ~68 tok/s to finish inside
  the timeout. The tier resolver can select a model slower than that, and the
  stage then fails by timeout, which the trajectory monitor treats as a
  quality failure and retries. Cheap fix (SD9): scale the timeout by output
  budget and expected TPS, floor 120s.
- **Speed-aware routing waits for traces.** Routing on measured per-model TPS
  (LN2+) fits the token-honest commitment; routing on vendor-claimed TPS
  would not.

## 2026-08-13 (4) — the independent memd audit, and the customization seam

Two more audits landed. Plan-doc sections 9 and 10; Track MD opened (MD1-MD5),
SD10 added, Track T gained a standing consequence. What is worth keeping:

- **A second pair of eyes with repro numbers beats a code read.** The reviewer
  session confirmed five memd defects, two of them race/runtime classes a
  static read would never prove (concurrent cold-start SQLITE_BUSY 4/4;
  macOS bind:EEXIST on redundant daemon). Three were independently
  spot-checked against the code before recording: the pragma order is wrong
  in one line (journal_mode before busy_timeout), the topic-key comment
  contradicts its own WHERE clause (claims memory_type is identity; the
  lookup omits it), and search validation checks only non-empty.
- **Memory identity is cwd, and worktrees make that wrong.** project_path =
  workDir means every --worktree run writes a separate namespace. The stable
  key (RepoRoot) already exists in worktrees.Result. MD1 blocks nothing today
  but silently fragments the trace corpus LN1 is supposed to build.
- **The customization substrate is mature; the missing piece is the seam to
  the pipeline.** Tools, skills, MCP, plugins, hooks, user commands all work
  in the agent loop; pipeline stages see exactly one forced typed tool plus
  deterministic context requests (correctly). The holes: hooks never fire in
  the pipeline tool path (SD10), skills are single-root and stage-invisible,
  and plugin ToolExtensions (trust-gated manifest tools) are the natural but
  unwired vehicle for Track T command nodes.
- **No cache-correctness defect was found in the uncommitted TUI tool-call
  work** (reviewer audit), which clears one more gate for the pending commits.

## 2026-08-13 (5) — the seam is the bug factory

A deep dive into the Zero/Splice engine boundary, prompted by the owner
suspecting systemic gaps. Plan-doc section 11; SD11-SD13 filed. What is worth
keeping:

- **The meta-problem has a nameable shape: one options bag, two engines, no
  enforcement.** splicerun.Run takes the agent loop's agent.Options and
  consumes a shifting subset. Every field can silently no-op or silently
  change meaning. MaxTurns, the filter bypass, the hooks bypass, and the dead
  SelfCorrect wiring are one bug, not four.
- **The pipeline runner reimplements the loop's permission flow and has
  already drifted.** ~80 inlined lines; hooks, tool filters, progress,
  output snapshots, and inline diagnostics all absent. Hand-copied policy
  diverges; it is a corollary of the pairing-test lesson from the RR sweep.
- **The TUI wires SelfCorrect/FileDiagnostics specifically for pipeline runs;
  only the agent loop consumes them.** Fifth produced-never-consumed
  instance in one day.
- **Stage events are NUL-delimited magic strings inside reasoning deltas,
  and that is the documented headless protocol.** streamjson has typed
  events for everything else. SD13 files the typed-event fix with a one-release
  shim.
- **Providers and file application are fine.** agent.Provider is a type alias
  of zeroruntime.Provider; stages write through the sandboxed registry path.
  Not everything at the seam is broken — the contract surface is.
- The fix that kills the class (SD11): a narrow pipeline run config built
  explicitly at the seam, plus the reflect-and-pair test so the next
  40-field drift fails CI instead of shipping.

## 2026-08-14 - MVP Wave 1 landed and passed CI

Wave 1 moved three focused commits to public `dev`:

- `6df95c5` adds bounded specialist tool-call history, safe detail rendering,
  distinct completed-card cache keys, and resume persistence. The review found
  one production gap: `specialist_start` omitted `status`, so resume showed an
  interrupted specialist as an error. The commit now stores `running` and
  tests the production payload.
- `a4e257e` maps persisted `ask_user` questions to an assistant turn and maps
  `ask_user_answers` to a user turn. A focused review found no blocker.
- `2b11f28` fixes the CI gate without changing runtime behavior. The trusted
  write test rejected every permission decision. Linux correctly emitted an
  audit decision for the test runner's `bash` call. The test now rejects only
  write-tool permission events.

A VHS capture exercised the actual specialist card renderer. It showed the
completed state, the 12-call total, the bounded eight-call tail, and the
`+4 earlier` fold line. Local formatting, vet, root tests, memd tests, the
full race suite, and the Linux Docker race reproduction passed. GitHub Actions
run `31761416355` passed both jobs on `2b11f28`.

The next work is Wave 2 in the owner-approved order: MD2, SD7, SD6, SD8,
SD9, WL1, WL2. Keep SD8 and SD9 minimal. Do not fold in SD11-SD12 while the
execution-envelope work is in progress.

## 2026-08-14 (2) - MD2 concurrent memd startup

MD2 landed in public `0ae201f`. The regression test reproduced the old
failure on its first round: four simultaneous `Store.New` calls returned
`SQLITE_BUSY` while no busy handler existed.

Putting `busy_timeout` before `journal_mode=WAL` was necessary but not
sufficient. The pinned modernc driver returns SQLITE_BUSY from the WAL switch
without using the busy handler. `Store.New` now retries only primary or
extended SQLITE_BUSY codes. The retry has ten 25 ms gaps. Other errors still
fail immediately.

`TestConcurrentOpen` runs four barrier-coordinated cold openers over eight
fresh databases. Thirty repeated test runs passed. The memd race suite passed.
An eight-round real-daemon probe always retained a healthy server. GitHub
Actions run `31762862788` passed both jobs. SD7 is next.

## 2026-08-14 (3) - SD7 token budget accounting

SD7 landed in public `5c39db2`. `tokensConsumed()` now sums only total input
and total output. It does not add cache-read, cache-write, or reasoning
subsets. The previous ROADMAP text said reasoning was omitted. That statement
conflicted with the normalized usage contract, where reasoning is part of
output, so this entry corrects it instead of adding another double count.

The updated state test was red on the old code with `got 36, want 33`. It
includes all three subsets and now passes. The focused test, the full splice
package race suite, formatting, and the diff check passed. GitHub Actions run
`31763551557` passed both jobs. SD6 is next.

## 2026-08-14 (4) - SD6 pipeline pass limit

SD6 landed in public `6a2f27c`. `runIterationLoop` always uses
`defaultMaxIterations` for pipeline passes. `agent.Options.MaxTurns` no longer
changes the pass cap. Stage-failure recovery and trajectory checks use the
same local cap.

The red proof showed `MaxTurns=1` stopped after one pass and `MaxTurns=3`
stopped changing failures after three calls. Both now reach the fixed
five-pass cap. Tests also preserve terminal failure detail after the expected
second-pass repeated-failure stop. `docs/PIPELINE.md` now states that
`--max-turns` does not change the pipeline pass limit.

The full root build, vet, and test suite passed. The splice, CLI, and TUI race
suites passed. The memd module passed. A fresh reviewer found no blocker and
confirmed that the idempotent `modify` fixture is valid. GitHub Actions run
`31765161488` passed both jobs. SD8 is next.

## 2026-08-14 (5) - SD8 in-pass wall checks

SD8 landed in public `6d6a407`. The run derives one absolute wall deadline.
`runPass` checks it before each stage and returns a typed sentinel when it has
expired. The orchestrator maps that sentinel to an aborted `wall time exceeded`
result and keeps records from stages that completed earlier in the same pass.
Parent cancellation is checked first.

The red test passed an expired deadline to a two-stage pass. The old code ran
both stages and returned nil. The new code starts neither stage and returns the
sentinel. The public pipeline guide now states that wall checks occur before
each pass and stage.

The full root build, vet, and test suite passed. The splice, CLI, and TUI race
suites passed. The memd module passed. A fresh reviewer found no blocker after
the parent fixed pass-boundary cancellation precedence. GitHub Actions run
`31766787474` passed both jobs. One in-flight stage can still cross the wall
deadline until SD9 clamps stage contexts. SD9 is next.

## 2026-08-14 (6) - SD9 active-stage wall deadline

SD9 landed in public `8eb1561`. Each planned stage receives a child context
with the absolute pipeline wall deadline. A live-parent wall expiry returns
the existing wall sentinel even when a provider maps the child deadline to
`context.Canceled`. Parent cancellation or an earlier parent deadline still
wins. Stage timers are released after each call.

The enforcement-point check corrected the old roadmap claim. The 120-second
`StageOptions.TimeoutSeconds` value applies to deterministic subprocess stages.
`code_writer` and `test_generator` call the provider directly and never read
that value. They did not have the claimed 120-second LLM timeout. The patch did
not add a speculative expected-TPS constant or a new token-scaled timeout.

The red test started a stage that blocked on its context. Before the fix, it
waited for the two-second parent timeout. It now stops at the 50 ms wall
context and returns the wall sentinel while the parent remains live. The full
root build, vet, and test suite passed. The splice, CLI, and TUI race suites
passed. The memd module passed. A fresh reviewer found no blocker; the parent
fixed its provider-cancellation classification finding before the final gate.
GitHub Actions run `31768162053` passed both jobs. WL1 is next.

## 2026-08-14 (7) - WL1 merged worktree cleanup

WL1 landed in public `871dc41`. `worktrees.Remove` calls `git worktree remove`
without force. It validates the source and worktree paths, reuses the shared
git result parser, and names the worktree path on failure. It does not delete
merge-back branches or recovery refs.

`splice exec --worktree --merge-back` calls removal once after `merged` or
`no_changes`. It never removes after a merge error, conflict, skipped dirty
source, failed run, incomplete run, interrupted run, or plain worktree run.
A cleanup error warns with the retained path but keeps the successful exit.

Real-git tests prove that clean removal unregisters the worktree and removes
its directory. A dirty untracked file makes removal fail and survives intact.
CLI policy tests cover each status and the warning path. The full root build,
vet, and test suite passed. The worktrees and CLI race suites passed. The memd
module passed. A fresh reviewer found no blocker. GitHub Actions run
`31770363110` passed both jobs. WL2 is next.

## 2026-08-14 (8) - WL2 bounded worktree pruning

WL2 landed in public `efcd1a5`. `splice worktrees prune` scans only registered
direct children of the repository's Splice directory. It removes a candidate
only when it is unlocked, a real directory, clean, and its HEAD remains
reachable from source HEAD or a `splice/*` branch. It reports and keeps every
managed candidate that fails a per-candidate check. Top-level discovery errors
fail the command. The sweep never uses age, force, `RemoveAll`, raw `git
worktree prune`, or ref deletion.

`Prepare` now runs the same sweep and excludes its requested target. New
worktrees use atomic `git worktree add --lock`. Reused worktrees lock before
the sweep. Exec unlocks on all exit paths and before WL1 cleanup. A manual
`splice worktrees prepare` stays locked until `git worktree unlock <path>`.
This native Git lock closes the active-clean-worktree race found during review.

Review also disproved an assumption in WL1: plain `git worktree remove`
deletes ignored files without force. `Remove` now rechecks tracked, untracked,
and ignored files. The new red test proves an ignored file survives a refused
removal. Symlinked targets and managed directories are refused. Ancestry checks
now distinguish Git exit 1 from command failures.

Real-git tests cover source-reachable and splice-branch-reachable removal,
dirty, unreachable, locked, ignored, symlink, outside-managed, Prepare-sweep,
and unlock cases. CLI tests cover text, JSON, flags, redaction, errors, and
unlock-before-cleanup order. A scratch CLI smoke observed lock, skip, unlock,
and removal. Build, vet, full root tests, full root race tests, memd tests,
formatting, and diff checks passed. DeepSeek v4 Flash 0731 independently found
no blocker after the safety fixes. GitHub Actions run `31810110002` passed both
jobs. MVP Wave 2 is complete. Wave 3 is next.

Residual risk: SIGKILL can leave a native Git lock until manual unlock. Old
worktrees from binaries before WL2 have no active lock. An unlocked worktree
must remain inactive during a sweep; this is now an explicit ownership rule.

## 2026-08-13 (6) — TUI worktree execution planned; six seams found before any code

Track TW opened (TW1-TW5); source of truth
plans/tui-worktree-execution-2026-08-13.md. What is worth keeping:

- **The slogan "just wire the existing machinery" was half true.** Prepare,
  IterationRecovery, and MergeBack are verified and reusable. But the deep
  dive surfaced six seams headless exec never had: sessions match workspaces
  by exact cwd, memory keys on cwd (MD1 again), fresh worktrees lack ignored
  files (no node_modules, no .env, broke test_runner in theory before it ever
  ran), sandbox trust anchors to workspace path, the session-long LSP manager
  would diagnose the wrong tree, and MergeBack's clean-source requirement
  collides with interactive reality where trees are usually dirty. Every one
  is a would-be bug found by reading instead of by a user.
- **git's own primitives keep being the answer.** Live-session protection is
  `git worktree lock --reason`, not a new lockfile format; WL2's sweep skips
  locked worktrees. Check the platform before inventing it.
- **skipped_dirty is the common case in interactive use, not the edge.** The
  merge UX is designed around the preserved-branch outcome, with merge as the
  happy path, not the reverse.
- **Supersession is recorded in the plan doc**: the audit's "consequence for
  Track T/A" note underestimated the surface; plans get promoted to tracks
  when they grow seams.

## 2026-08-14 (9) - ask_user Esc cancellation

Wave 3's Esc checkpoint landed in public `110a64a`. One Esc on any active
`ask_user` tab now clears the composer and calls the existing `cancelRun`
path. It never calls the answer callback or submits committed partial answers.
The questionnaire footer states `esc cancel run` in each state.

A provider-backed test runs the agent loop into a real `ask_user` tool call.
It proves Esc cancels the run context, releases the blocked loop, and prevents
a second provider turn. Picker, free-text, partial-answer, and stale cancel
confirmation tests cover the model state. The captured TUI footer is in
`/tmp/splice-wave3-ask-user-esc.gif` and its final PNG is next to it.

Local validation passed `go build ./...`, `go vet ./...`, full root tests, the
full TUI race suite, memd tests, formatting, and diff checks. DeepSeek v4 Flash
0731 found no implementation blocker. Its context-unblocking test gap was
closed before the checkpoint. GitHub Actions run `31813231935` passed both
jobs.

Real-provider acceptance remains blocked. The exact safe probes were:

- `/tmp/splice-wave3-smoke providers list --json`: `openrouter` and `chatgpt`
  both reported `apiKeySet: false`.
- `/tmp/splice-wave3-smoke-detect providers detect --json`: no local runtime.
- `/tmp/splice-wave3-smoke-doctor doctor --json`: exit 3 because the active
  `openrouter` profile has no credential. Config, Go, and sandbox checks
  passed.

Do not run the TUI, exec, or worktree provider smoke with fabricated or exposed
credentials. Resume Wave 3 when an approved provider credential or local
runtime is available.

## 2026-08-14 — the charter landed, and two first-run bugs proved it early

Big direction day. The owner dropped a full design charter
(`plans/SPLICE_ADAPTIVE_HARNESS_ARCHITECTURE.md`, 191 sections: workflow
topology as data, durable project knowledge via "Veritas", uncertainty-driven
synthesis, trajectory-derived learning, cost-aware orchestration). Adopted
with three amendments in `plans/adaptive-harness-adoption-2026-08-14.md`:
local-first stands (Veritas optional, memd built-in); a canonical
phase-to-track map so nothing gets built twice (SD=P0, T=P1-2, PC=P5,
LN=P9, MD=identity prerequisites); implementers read the adoption record,
not the raw charter. ROADMAP.md gained a North Star reading-order section.
The charter is convergent with 2026-08-13's decisions, which is the strongest
signal it is the right shape: an independent expansion arrived at the same
architecture.

What is worth keeping:

- **The reads line up for once.** The charter's Phase 0 assumptions (explicit
  abstractions, stable identity) are exactly what the SD/MD tracks found
  broken. Nothing about the vision contradicts the audit findings; the audits
  priced the vision's foundation.
- **First-run auth was broken in two stacked ways, found by watching a real
  first run.** (1) `splice auth openrouter` mints and PRINTS a key; nothing
  persists it (OAuthMintsKey by design), so doctor's "run splice auth" advice
  completed a flow that configured nothing. (2) Setup's ready screen read the
  in-memory paste buffer ("saved API key") while the save path wrote nothing:
  no inline key, no apiKeyStored marker, no keychain apikey: entry. Confirmed
  contributor: `mergeProfile` never copies `APIKeyStored` — the L17
  ParseThinkTags comment documents the same drop one field over. Fix
  assigned to the executing session with red-test-first. Unblock route:
  inline apiKey in config.json (resolution precedence guarantees it wins).
- **The kimi-k3 blocker was the charter's cost thesis failing on the wire.**
  Registry entry says maxOutput=1M (context echo), factory forwards it
  verbatim as max_tokens, OpenRouter rejects, pipeline retries and dies.
  Declared budgets never bounded the request. The fix keeps the registry
  metadata and omits the unusable cap at the request boundary. The SD item to
  wire stage `OutputMax` to `max_tokens` closes the remaining class.
- **Two external sessions now collaborate through intercom on verified
  facts.** veritas-cache adopted the exact-match-only contract because of our
  StateHash landmine; their Phase 6 bench doubles as our first external
  trajectory-robustness measurement. The owner running multi-session dev
  with a shared findings document worked; the documents did the
  coordination.

## 2026-08-14 (10) - Wave 3 real-provider acceptance

Wave 3 completed against OpenRouter Kimi K3. The safe prerequisite probe
`splice doctor --connectivity --json` passed provider config, model, and HTTP
connectivity without printing a credential.

The first plain exec exposed a load-bearing defect. Kimi K3's live registry
metadata reports a 1,048,576-token output capability and the same total context
window. The factory sent that full value as `max_completion_tokens` beside a
nonempty prompt and tools. OpenRouter rejected all three attempts, and the
pipeline exited 4 as incomplete.

Public `f274921` fixes the provider boundary. A registry output capability at
or above the total context no longer becomes a wire cap. OpenAI-compatible
requests omit it; other provider families use their existing defaults. Valid
smaller caps remain unchanged. The registry metadata remains truthful. A red
factory request test captured the invalid wire value before the fix. Full
local tests, provider and registry race tests, build, vet, memd tests, and an
independent DeepSeek review passed. CI run `31821825233` passed both jobs.

The same live plain exec then completed the trivial `code_writer` in one
iteration. It reported 1,422 input tokens, 221 output tokens, complete pricing,
and no budget abort. The output log is
`/tmp/splice-wave3-plain-fixed.jsonl`.

The worktree command used `--worktree smoke1 --merge-back` from a clean source.
It completed in one iteration and reported `merged splice/smoke1`. The source
advanced to merge commit `ee88c1c`. The `splice/smoke1` branch remains, while
the managed worktree directory was removed. Usage attribution reported 1,399
input tokens, 699 output tokens, and 423 reasoning tokens. The output log is
`/tmp/splice-wave3-worktree.jsonl`.

The real-provider TUI trusted only the temporary fixture, read `main.go`, and
answered that it prints `Hello, world!` with a trailing newline. It touched no
files. The capture is `/tmp/splice-wave3-tui-live.gif` with a final PNG beside
it.

Adverse observation: both write runs changed `fmt.Println("hi")` to
`fmt.Println("Hello, world!")`. The second prompt explicitly prohibited other
line changes. Trivial tier runs only `code_writer`, so no verification stage
challenged the drift. The harness lifecycle and accounting passed; the model's
instruction fidelity did not. This is now an open product tradeoff, not a
silently green claim.

Wave 3 also exposed CR5. The npm `0.1.4` setup flow changed the selected model
but persisted no pasted OpenRouter credential or `APIKeyStored` marker. The
next checkpoint must reproduce this on `dev` before fixing it. SD14 separately
records that `StageBudget.OutputMax` is measured after inference but never
bounds the provider request.

## 2026-08-14 (2) — the veritas-cache bench ran; the spiral moved house

The sibling project's three-arm control-loop experiment (baseline pass-through,
static semantic serving, ld3 judged serving) produced the first external
measurement of the trajectory monitor under cached-response pressure. What is
worth keeping:

- **Cache hits that synthesize usage disable the token-budget brake.** The
  static arm served 49/50 requests from one entry, never tripped the budget,
  and spiraled to the iteration cap with 7x the tool calls of baseline. The
  monitor's cost brake assumes usage accrues per call; any free-response path
  (cache, future local model, mocked provider) defeats it. No-progress braking
  must not depend on tokens alone.
- **Thrash evades cycle detection.** StateHash kept moving without files being
  written: the model produced varying-but-useless typed outputs each pass.
  Cycle and oscillation rules only fire on identical hashes, so a thrashing
  model's only effective brakes are the budget and the hard limit. Real gap
  for the charter's evidence plane; consider a "no file movement across N
  iterations" signal alongside hash equality.
- **Zero escalation markers in every arm**, consistent with the once-per-run
  escalation flag and with rule order (budget and hard limit are checked
  before cycle/oscillation). Abort-mode analysis, not escalation counts, is
  the right measurement surface, as predicted.
- **Our abort path does not lose usage data**: finishWithReason returns stage
  records and applyRequestLedger runs on every non-error exit (run.go:236-243).
  Their empty breakdowns were zero-completed-stages, not lost accounting.
- Follow-up designed with them: a write-forcing task whose verifier requires
  on-disk change, making identical-vs-varying outputs a controlled variable
  and exercising StateHash directly.

## 2026-08-14 (11) - CR5 setup credential marker

CR5 is complete in public `ee5a404`. A red integration test seeded an existing
OpenRouter profile with `apiKeyStored: false`. It then ran the real setup save
path with a synthetic key. Before the fix, the model changed and the temporary
encrypted store held the key, but the marker stayed false.

The defect was in the existing-profile write. `SecureProviderProfile` set the
marker after the store write. `UpsertProvider` then used the general profile
merge, which did not copy the marker. The next process obeyed the intentional
marker gate and refused to load the stored key.

`UpsertProvider` now promotes `APIKeyStored` only for the explicit provider
write. The general profile merge remains unchanged. This avoids a wider fix
that could let project config reactivate a stale stored credential. No real
credential or owner configuration was read during the reproduction.

The focused test passed after the fix. Full root tests, full root race tests,
build, vet, memd tests, formatting, and `git diff --check` passed. An
independent DeepSeek review found no blocker. CI run `31826231922` passed both
jobs. CR5 closes the last approved MVP blocker; no next checkpoint is approved.

## 2026-08-14 (3) — run 3: the monitor fires in the wild, and cycles have two parents

The corrected bench (dev ee5a404, stream-json) confirmed the full picture:
cycle detection fires exactly once per run in every arm, and the "no
escalation provider configured" no-op is now observed outside the test suite.
Two things worth keeping:

- **Cycle detection cannot separate cache-induced cycles from task-induced
  ones.** Their write-forcing task manufactures StateHash cycles with no
  cache at all: a competent model repeats the same correct edit while the
  verifier keeps failing, and identical file states result. The monitor's
  "cycle" label conflates model-thrash with environment-stuck. Filed as SD16
  (verifier failure signature in cycle evidence). Their bench-side fix is a
  verifier that forces different writes per iteration, so identical states
  can only come from byte-identical served responses.
- **The budget-brake defeat is real and survives the corrected binary**:
  static 50 iterations to hard limit vs baseline 8 to budget abort (6.25x).
  This is the bench's solid result for both writeups.
- Their judged serving mode (ld3) behaved as designed on this traffic:
  21 hits served, 16/16 judgments correct, budget-brake preserved. The
  exact-only contract for Splice remains the rule for generation stages;
  judged serving is viable where the envelope is preserved.

## 2026-08-14 (12) - SD15 trajectory guidance

SD15 is complete in public `901d99f`. The TUI now installs
`OnSurfaceToUser` and reuses the existing `ask_user` questionnaire. It shows
the trajectory reason, evidence, and recent confidence values. Nonempty text
continues with that exact text as the next revision context. Empty input and
cancellation abort. Headless exec remains unchanged and aborts when the
callback is nil.

The trajectory rule no longer claims that a rollback or retry occurred. It
states only that confidence decreased across the last three iterations. The
rule order and thresholds did not change.

Run tests prove that guidance reaches the next iteration and that abort returns
`user aborted`. TUI tests cover visible evidence, trimmed guidance, empty
input, and cancellation. Long questions now wrap instead of truncating the
evidence. Full root tests, root race tests, build, vet, memd tests, formatting,
and an independent DeepSeek review passed. CI run `31858201582` passed both
jobs. The TUI capture is `/tmp/splice-sd15-surface-to-user.gif`.

## 2026-08-14 (13) - SD14 stage output caps

SD14 is complete in public `7fbda8a`. A model-backed stage now sends its
`StageBudget.OutputMax` as `CompletionRequest.MaxOutputTokens`. OpenAI,
Anthropic, and Gemini use the smaller positive value from the request cap and
the configured provider maximum. A zero request cap keeps the provider
behavior. The `f274921` registry clamp is unchanged.

The cap remains stable across typed-output retries. An 8,192 code-writer budget
now produces an 8,192 request cap instead of the Kimi registry maximum. Stages
with a zero output budget send no override. Codex remains unchanged by the
approved contract.

Anthropic thinking cannot raise `max_tokens` above the stage cap. It shrinks
when enough space remains, and it turns off when the minimum thinking budget
plus the reserved response cannot fit. Parent review corrected the boundary
case below that combined minimum. Provider and run tests cover the cap
precedence, retry behavior, zero path, and thinking conflict.

Full root tests, root race tests, build, vet, memd tests, formatting, and an
independent Grok review passed. CI run `31860137850` passed both jobs.

## 2026-08-14 (14) - SD16 cycle attribution

SD16 is complete in public `ac07c49`. The cycle rule still fires on a repeated
state hash. It now also records the current and previous verification failure
signatures. The signature uses failing tests, errored tests, failing acceptance
facts, and the high plus critical lint and security counts.

When those signatures match, the reason states that the environment or verifier
may be stuck. When they differ, the reason states that model thrash is more
likely. The action, rule order, and cycle threshold did not change.

Focused trajectory tests, full `internal/splice` tests, a race run, build, vet,
memd tests, and formatting passed. CI run `31861993067` passed both jobs.

## 2026-08-14 (15) - MD3 search control rejection

MD3 is complete in public `bf2809d`. `searchRequest.Validate` now rejects NUL
and other C0 controls. Tab and newline stay valid. `/search` returns HTTP 400
instead of sending the query to FTS5, where a NUL produced an unterminated
string and HTTP 500.

Protocol tests cover NUL, another C0 byte, and allowed whitespace. The HTTP
test proves a NUL query is 400. memd tests and formatting passed. CI run
`31862382229` passed both jobs. Approved Batch A is now complete.

## 2026-08-15 — the pass-gate forensics: one bogus HIGH can loop a green run forever

The veritas-cache bench hit abort_hard_limit on every passable task and did
the forensics: static_analyzer reported exactly one HIGH (PY_COMPILE) every
iteration while tests passed, and passSucceeded gates on
LintIssuesBySeverity[high] > 0 (run.go:790), so a single constant finding
forces endless iteration. Verified from code:

- PY_COMPILE is hardcoded SeverityHigh (quality_python.go:110-118) and fires
  on ANY py_compile failure — syntax, missing interpreter, unreadable path,
  or sandbox/env failure — with the raw compiler output as the message.
- Prime suspect: py_compile WRITES .pyc into __pycache__ as a side effect.
  The analyzer runs through the sandboxed bash tool; pytest tolerates
  cache-write failures silently, py_compile does not. A denied write produces
  exactly the observed signature: constant HIGH, green tests, content-
  indifferent. Awaiting their tool_result output to confirm.
- Observability gap this exposed: the full VerificationReport (with finding
  messages) lives only in the in-memory stage output; the durable record
  carries only the summary. The actual error text was recoverable only from
  the tool_result event in stream-json. Finding details (first N messages)
  should reach the stage event detail or StageRecord.
- If confirmed, the fix shape: compile with an owned cache dir (py_compile
  cfile to a temp location) or bytecode-write suppression, so a read-only
  quality check has no write side-effect inside the sandbox.

The live tool_result later proved a simpler cause: the quality checks sent a
command array to bash. That is SD17, now fixed. The pycache write theory is
still open as a later risk, not the Batch A2 defect.

## 2026-08-15 (2) - SD17 quality bash argv

SD17 is complete in public `4d69ea2`. The four quality-check bash sites now
pass `shellJoin(command)` instead of a raw `[]string`. That is the same helper
the test runner already used. Live bench evidence was
`Error: Invalid arguments for bash: command must be a string`. That rejection
produced a constant HIGH `PY_COMPILE` and blocked `passSucceeded` on every
Python project.

The mock-boundary lesson is the second half of the fix. Quality tests now wrap
bash through a schema-enforcing fake that rejects a non-string command with the
same error text. A dedicated test proves the fake would have caught the old
array shape. A real `tools.NewBashTool` smoke compiles a broken Python file and
must not emit the schema error.

Full root tests, root race tests, build, vet, memd tests, and formatting
passed. CI run `31863728901` passed both jobs.

## 2026-08-15 (2) — run 4: cross-task contamination, and the scoping hook we already emit

The corrected bench (SD17 binary) confirmed passable tasks now pass, and
produced the second failure mode of semantic serving for agent traffic: one
cached entry served 19 hits across DIFFERENT tasks; task 3's code_writer
received task 1's response and edited the wrong fixture's file with full
confidence, no error, until the cap. What is worth keeping:

- **Wrong-context serving is worse than malformed serving** because it fails
  invisibly. The exact-only contract for agent generation stages is now
  grounded in two observed failure modes (envelope stripping, cross-task
  contamination), not caution.
- **Splice already emits the tenant signal**: prompt_cache_key =
  sessionID:stageName is on the wire for OpenAI-compatible providers
  (internal/providers/openai/provider.go:528). A cache key that includes it
  prevents cross-run/task serving; within one session, task identity is not
  on the wire (noted gap, acceptable today).
- **The incentive is real**: static serving cut upstream prompt tokens 93% in
  their bench. Exact-match serving found zero hits on novel-task traffic; the
  paying traffic classes are retries and repeated workflows, which is where a
  savings measure should look next.
- For the charter: this is the prompt-injection/section-132 trust-separation
  principle observed in the wild — context from one task must never leak into
  another task's stage. Evidence planes and knowledge scopes (project,
  session, task) are not niceties; they are the failure boundary.

## 2026-08-15 (3) — run 5: scoped serving is correct, and the budget brake is confirmed dead thrice

Run 5 (deepseek, SD17 binary, prompt_cache_key scoping live): all four arms
passed both passable tasks; scoped semantic serving cut upstream prompt
tokens 51% with zero contamination. The tenant hook works. What is worth
keeping:

- **prompt_cache_key scoping validated**: sessionID:stageName in the cache
  key is sufficient isolation for per-run agent traffic. Semantic serving
  with that scoping is correct for same-session same-stage retries.
- **Budget-brake defeat is not a contamination artifact**: scoped static
  still exited cycle tasks on abort_hard_limit while every other arm exited
  on abort_budget. Correctness-safe is not economics-safe. Filed as SD18
  (progress-based brake: workspace-change or step-count signals that do not
  depend on provider-reported usage).
- **ld3 served zero hits on agent traffic**: its evidence bar never clears
  within a 4-10 request run, so on single runs it is exact-only in practice.
  For Splice, the exact-only contract loses nothing against judged serving on
  this traffic shape.

## 2026-08-15 (4) — Batch B dispatched, and the modular direction is now a standing rule

The owner's grilling answers resolved the queue: the current system must be
fully functional (Batch B re-justified as feature completion — tool filters
and hooks are broken in pipeline runs, verified at exec.go:727 vs the
pipeline runner), stages become self-describing environments with real tool
access, and the codebase leans modular. What is worth keeping:

- **The design conversation already proves the stage pattern the owner
  wants**: full agent loop (tools, skills, MCP) plus a typed submit tool.
  Generalizing that to every pipeline stage inside a declared envelope is the
  Batch E shape; no new runtime invention needed.
- **Shared workspace per run, worktrees per parallel stage group.** Per-stage
  worktrees buy nothing for sequential stages and cost the node_modules
  problem every time; parallelism is when per-stage workspaces pay.
- **Batch B spec, as dispatched**: Commit 1 SD11 (PipelineRunConfig +
  constructor in the splice layer, reflection pairing test classifying every
  agent.Options field as consumed or ignored-with-reason); Commit 2 SD12
  (shared policy-execution helpers, additive exports only in internal/agent,
  pipeline runner's inlined permission flow deleted, filters/hooks/RunOptions
  gaps wired). Acceptance is user-visible: disabled bash must fail loudly in
  a pipeline run, a beforeTool hook must fire for a pipeline tool call.
- **Module convention added to the ROADMAP standing rules**: one package,
  small exported seam, own tests, registration point, docs section; applied
  opportunistically, no mass restructure. SD2 Capabilities() is the reference
  shape for stages.
- Research verdict on the seam (for the record): filters and hooks silently
  unenforced in pipeline runs (functionality bugs, now being fixed), stage
  markers work and are stripped before session persistence, SelfCorrect is
  dead on the pipeline path, no direct perf cost from the seam itself.

## 2026-08-15 (5) - Batch B: SD11 and SD12

Batch B is complete. SD11 landed as `8cf01bd`. The pipeline now converts
`agent.Options` into `PipelineRunConfig` at the public Run and design-plan
boundaries. A reflection test fails CI when a new field is unclassified.

SD11 CI `31897503717` failed on `TestPipelineNeverReadsContextWindow`. That
guard treated the ignore-map key `ContextWindow` as a field read. SD12
tightened the search to `.ContextWindow`.

SD12 landed as `fa1727a`. Shared helpers in `internal/agent/tool_policy.go`
cover filter denial, beforeTool/afterTool dispatch, hook feedback, Task
progress, and `tools.RunOptions` construction. The pipeline runner still owns
its auto and spec-draft grant path. Disabled bash now fails with
`DenialFiltered`. A beforeTool hook fires on a pipeline `read_file` call.
The pairing test now classifies Hooks, EnabledTools, DisabledTools,
OnToolProgress, OnToolOutput, and FileDiagnostics as consumed.

Full root tests, race tests, build, vet, memd tests, and formatting passed.
CI run `31898960035` passed both jobs.

## 2026-08-15 (5) — correction: run 4's contamination was truncation-collapsed embeddings

The veritas-cache session disclosed that their tokenizer truncated embeddings
at 128 head tokens, so every request embedded as the shared harness system
prompt: sim=1.0 hits in runs 1-6 were "same prefix", not "same intent", and
run 4's cross-task contamination flowed through the collapse (the embeddings
never saw task text). Their fix: embed the last user message. Honest
re-assessment of the shared record:

- SURVIVES: the budget-brake defeat (usage accounting, unrelated to
  embeddings; replicated in three runs incl. scoped run 5); cycle detection
  firing and the escalation-no-op; the SD17 bash-argv fix and its wild proof;
  run 5's scoped-serving correctness result.
- WEAKENED: run 4 as evidence for "similar prompts naturally contaminate."
  The failure MODE (wrong-context serving is invisible and dangerous) is
  still real and the exact-only contract for generation stages still stands,
  but the strength of run 4's evidence is reduced: contamination that day was
  manufactured by pathological embeddings, not by inherent prompt similarity.
  The corrected claim: wrong-context serving is possible and silent, and
  embedding quality + tenant scoping are what stand between it and production.
- NEW (second harness, independent): a Pi agent session under static serving
  looped 3,466 identical write calls at ~7 req/s with zero upstream cost.
  The free-retry loop is now observed in two harnesses. General form, now
  stated twice in the record: a semantic cache removes the cost gradient that
  normally slows a stuck loop. This is the SD18 motivation verbatim.
- Process note: the correction came from the bench owner, unprompted, with
  the artifact identified. The shared record is better for it; record
  corrections with the same prominence as results.

## 2026-08-15 (6) — bench arc closes: scoped serving is safe, tail embeddings are the subtle part

The veritas-cache bench closed its arc. The durable results, all
cross-verified against our code where applicable:

- **Tail embedding, not last-USER-message embedding.** Agent sessions hold
  one constant last user message while turns arrive as tool results, so
  last-user embedding still collapses every turn to one vector (the Pi
  free-loop persisted). Embedding the last message of ANY role fixed it: 2
  bounded hits, all tasks green. Anyone building cache keys over agent
  traffic hits this; it is now in the record.
- **Run 8**: scoped static and scoped ld3s both cut upstream prompt tokens
  71% with 2/2 passable tasks green. scoped-ld3s served 2 bounded hits vs
  static's 8 unbounded; per-entry judged serving has served zero for three
  runs running on this traffic shape (cold start is structural, not a bug).
- **The safety-net framing, now demonstrated on two harnesses**: without
  caching, stuck loops hit budget aborts; naive semantic caching removes the
  cost gradient and they spin free (Splice: 5-7x work amplification; Pi:
  3,466 identical writes at 7 req/s). Scoped + tail-embedded serving removes
  the demonstrated hazard. Splice's residual risk is filed as SD18
  (progress-based brake independent of provider-reported usage).
- Their closing design law: learn by stage-shape, serve by session scope; on
  agent traffic, per-entry adaptive serving converges to per-entry behavior
  only where entries repeat.

## 2026-08-16 - Batch C: SD1, SD5, SD2

Batch C is complete on public `dev`.

SD1 landed as `5f0cdf4`. `StageBudget.ModelTier` is gone. Zero versus both-
positive budget shape remains. Design-task `recommended_model_tier` stayed.
CI run `31919004721` passed.

SD5 landed as `b316ee5`. `ExecutionStage.DependsOn` and the unused stage DAG
validator are gone. Design-task `DependsOn` stayed. CI run `31919055111`
passed.

SD2 landed as `4c90115`. Registry stages declare `Capabilities()` for model-
free, memory, context, and timeout. The four name-keyed switches are gone.
`extractWriteObservations` still keys `test_runner` observations by name. A
pairing test matches the legacy outcomes. CI run `31919423164` passed.

## 2026-08-16 (2) - SD1 follow-up: design tier fossils

Public `7f67099` closed the rest of the SD1 fossil family. The crystallizer
schema no longer advertises `recommended_tier` or `recommended_model_tier`.
`Task.EstimatedTier` is gone. Session persistence uses ordinary JSON unmarshal,
so an old `estimated_tier` value is ignored and the classifier still chooses
the task tier. Tests cover schema absence and ignored persisted payloads. CI
run `31920589697` passed.

## 2026-08-16 (3) - Batch C2 review minors

Public `095b1ac` closed the four review minors from Batches A-C. All-zero
cycle signatures no longer claim a stuck verifier. Auto and spec-draft grant
events now report `PermissionGranted=true`. The reverse `agentOptions()` copy
has a sentinel pairing test. `RegistryToolRunner` stays a read-only context
runner and does not apply SD12 filters. `docs/PIPELINE.md` now states filter
and hook propagation plus stage output caps. CI run `31921814741` passed.

## 2026-08-16 (4) - SD3 trajectory rule table

Public `a200452` converted `EvaluateTrajectory` from an if-chain into an
ordered named rule slice. First non-nil decision still wins. Existing
trajectory tests stayed unchanged. A pairing test pins the seven-rule order.
CI run `31922234601` passed.

## 2026-08-16 (5) - SD4 signal extractors

Public `c0a6991` replaced hardcoded payload keys with `trajectoryExtractors`.
A pairing test requires every registry stage to claim an extractor key or sit
in `trajectoryIrrelevantStages`. `IterationState.TypeErrors` is gone. The
score no longer subtracts a dead type-error term. CI run `31922546244`
passed.

## 2026-08-16 (6) - SD18 no-progress brake

Public `9dccad0` added a progress brake that does not depend on provider
usage. Three empty workspace passes request one step-back. A later empty
stretch aborts. Existing rollback, plateau, and confidence fixtures now write
files so they stay distinct. The rule table is now eight rules. CI run
`31924117395` passed.

## 2026-08-16 (7) - SD13 typed stage events

Public `5b4f2ee` added `streamjson.EventStage` and `agent.OnStageEvent`.
Live TUI and exec consume the typed event. The old `\x00STAGE` marker remains
as a one-release compatibility shim. CI run `31924835596` passed.

## 2026-08-16 (8) - SD18 no-progress brake

Public `9dccad0` added a progress brake that does not depend on provider
usage. Three empty workspace passes request one step-back. A later empty
stretch aborts. Existing rollback, plateau, and confidence fixtures now write
files so they stay distinct. The rule table is now eight rules. CI run
`31924117395` passed.

## 2026-08-16 (13) - reject-reason tap

Public `55b7958` (owner-approved Lane 1 item 2) shows one follow-up ask_user
question after Reject at the worktree review: "Why are you rejecting?" with
wrong_approach / still_failing / changed_mind / other. Esc or empty yields
unspecified and still removes. The decision and reason land on a
`worktree_review` session event for the run_outcome trace. Accept and Keep
never see the reason prompt. Removal flow unchanged. CI run `31973024465`
passed.

## 2026-08-16 (12) - TW2 TUI iteration recovery wiring

Public `f03d163` (owner-approved Lane 1 item 1) passes
`worktrees.NewIterationRecovery(prepared)` at the `tuiSpliceRun` call when a
TUI pipeline run executes inside a worktree. Live-checkout fallback keeps
nil, so a rollback trajectory decision still aborts with "rollback requires
an isolated --worktree". Design and spec-draft runs are unchanged. CI run
`31972452523` passed.

## 2026-08-16 (11) - TW3 worktree-reject demo tape

Public `0efd91e` (owner-approved TW3) adds `scripts/tui-worktree-reject.tape`,
`scripts/tui-worktree-reject-setup.sh`, and `docs/assets/tui-worktree-reject.gif`.
`SPLICE_TUI_DEMO=worktree-reject` swaps only `tuiSpliceRun` so the tape can
type `/exec` without a live model. Unset, the TUI is unchanged. Worktree
prepare and the Accept/Reject/Keep review stay on the real path. The GIF is
18 seconds and shows the pipeline, a step-back, and the real review prompt.
CI run `31957101861` passed. Re-cut in `a27d8da` (CI `31971161314`) to 10
seconds with the full reject outcome in-frame: Reject selected, worktree
removed, and the branch-survives notice. The demo env is keychain-free
(`SPLICE_CRED_STORAGE=encrypted-file`, `SPLICE_OAUTH_STORAGE=file`, inline
apiKey) so macOS never prompts during the recording.

## 2026-08-16 (10) - TW2 TUI worktree review surface

Public `1ba00dd` (owner-approved TW2; internal ROADMAP TW3) offers Accept,
Reject, or Keep after a TUI pipeline run inside a worktree. Accept merges
and removes. Reject pins `splice/<name>` then force-removes, so work
survives on the branch. Keep prints the path. A dirty main checkout hides
Accept and refuses merge. Esc means keep. The worktree lock is held until
the decision and released on every exit. Dirty check runs in the run
goroutine, not the UI update path. CI run `31954292809` passed.

## 2026-08-16 (9) - TW1 TUI worktree pipeline runs

Public `f265ce9` runs TUI pipeline turns in an isolated locked worktree.
Design and spec-draft stay in the live checkout. Prepare failure or an
explicit `worktrees.enabled=false` falls back with a one-line notice that
rollback is unavailable. Tests cover cwd swap, lock lifetime, fallback, and
the worktree chip. CI run `31928290030` passed.

## 2026-08-17 (11) - LN design grilling complete, consolidated doc

Nine-question grilling of the LN1 trace design is done; every decision
recorded in plans/adaptive-harness-learning-design-2026-08-17.md. Headlines:
trace-only test split, self-contained traces, three-state verdict with audit
hooks, six-tuple bucket key with floors and frozen-bucket invalidation, stage
input metadata, typed weighted interventions with a permission floor,
two-tier epistemics (store proposes, PE harness disposes). New decisions
beyond the grilling: LN4 learned topology (proposals-only, gate-protected,
last in build order), the adaptive-tooling ratchet (envelope, narrow
downward, escalate to expand), the five-wall stage sandbox model, the
cross-project transfer policy (model priors + user profile, consume and
contribute switched separately, priors always weak), regression-first testing
with fabricated corpora and guard-first fixtures, test-tampering as a
trajectory red flag, sampled red-capability checks, and the Veritas contract
(optional, x-cache headers traced, harness bypass skips read AND write).
LN1 must not ship before the Q7/Q8 fields are in the schema: traces are
append-only and missing fields never backfill. Build order: MD1, LN1, LN2,
PC3, LN3, LN4, PE.

## 2026-08-17 (12) - MD1 repo-root memory identity

Public `6e5208d` (owner-approved MD1; CI `32040532971`) splits memory identity
from execution cwd. agent.Options and PipelineRunConfig gain ProjectRoot
(empty = derive from Cwd, no behavior change for existing callers).
runExecutionPlan keys all four memory call sites to ProjectRoot via
memoryProjectRoot(); tools, summarizeWorkspaceChanges, and the stage registry
keep the worktree path. TUI and exec --worktree set ProjectRoot to
preparedWorktree.RepoRoot. Forward copy PipelineConfigFromAgentOptions
carries it; reverse copy is pinned out by
TestPipelineAgentOptionsDoesNotReverseCopyProjectRoot with a non-empty
sentinel. memory_identity_test.go proves two runPass calls with different
worktree paths but one RepoRoot produce identical ProjectPath values.
Reviewed by splice session 019ff87f before acceptance. This unblocks LN1
(traces now land under the stable project) and TW4.

## 2026-08-17 (13) - Regression discipline in AGENTS.md

Public `8b5487d` (owner-directed; CI `32041430785`) adds a Regression
Discipline section to AGENTS.md: guards get the most adversarial fixtures,
learning logic is tested with fabricated corpora in table tests, invariants
get property tests, producers pair with consumers, integration mocks at the
provider seam, and slow behavioral checks stay out of CI. The section opens
with the standing rule: every feature lands with a regression net that pins
its failure modes, not only its happy path.

## 2026-08-17 (14) - LN1 run_outcome traces shipped

Public `60ddf34` + follow-up `ed1256f` (owner-approved LN1; CI `32044673642`,
`32045227460`). schemas/trace.go: RunOutcome (embedded plan, iterations with
the Q2 preexisting/authored test split, TracedStage input metadata, memory
record, typed weighted interventions) plus VerdictRecord as a SEPARATE
append-only record; RunOutcome carries no verdict field, absence means
unknown. memd gains run_traces (write-once, ON CONFLICT DO NOTHING) and
verdicts (append-only, latest-wins) tables with /trace/upsert, /trace/verdict,
/trace/query. Write path builds and validates the trace after the ledger;
validate failure fails loudly, store failure warns and never fails the run.
Verdicts: TUI accept=kept, reject=rejected+reason, keep/Esc=none; exec
merged=kept(sha+branch), else none; sessionless exec merges warn instead of
silently dropping. Two reviewer rulings: ephemeral verdict skip accepted
(run ID threading deferred to owner), review decision NOT mirrored into
Interventions (final; VerdictRecord is the higher-fidelity carrier).
Regression net per the new AGENTS.md discipline, including
TestBuildRunOutcomeCoversEveryField (producer-consumer pairing). Traces now
accrue on every pipeline run with the full Q7/Q8 field set. Next: LN2
(budget fitter) or PC3 (exemplar retrieval).

## 2026-08-17 (15) - LN2 learned budgets shipped

Public `8610484` (owner-approved LN2; CI `32047530317`). New internal/splice/learn
package (pure, no LLM): FitBudget over the six-tuple BucketKey (repo, stage,
prompt hash, model, tool fingerprint, topology hash) + memory status. p80
percentiles, floor 20 with loud refusal, survivorship guard (budget aborts
raise the fit; matches "abort_budget:" reason prefix), sanity clamp
[0.5x, 2.0x] of static default, legacy-bucket refusal for empty key fields.
Schema additive v1: TracedStage.PromptHash, RunOutcome.ToolFingerprint,
TopologyHash, BudgetProvenance. This also closes the LN1 reviewer-noted gap:
bucket key fields are now captured at trace-WRITE time (query-time hashing
would re-key history on prompt edits). Topology hash = stage names only,
which fully covers today's edge-free ExecutionStage; Track T must extend it
when real edges land. Kept-rate rollback guard deliberately deferred (verdict
corpus too sparse). Reviewed by 019ff87f: fitter math, wiring order (fits
before accumulator so the trace embeds fitted budgets), abort-reason string
match, and the full 13-test regression net all verified. Traces accrue;
buckets fill; the fitter declines loudly until floors cross.

## 2026-08-17 (16) - PC3 exemplars + three-state memory status

Public `084211e` (PC3; owner-approved) and `1e372f8` (memory status fix), CI
`32063395849` green over the stack. PC3: kept-run exemplars join stage memory
bundles (top 3 by bm25, deterministic score gate at -1.0, 400 chars each and
1200 total competing for the existing bundle budget, distillate fields only,
run_id provenance, repo-scoped, verdict=kept INNER join, InputMeta gains
ExemplarItems). memd: intent column + FTS5 + migration, Verdict filter on
/trace/query. Memory status: resolve error or mid-run search failure now
records "unavailable" (warm arm no longer polluted by failed-warm runs);
deliberate cold stays "off"; LN2 needs no change (exact-status match).
Live-verified by 019ff87f before acceptance: real run wrote a failed-status
trace with correct repo root, key fields, and "budget not calibrated: 0/20"
provenance; TUI boot/chat/sidebar/error-row smoke clean. Audit noted:
iterations array is null on stage-failure runs (abort reason + stage records
carry the failure shape; LN3 note, not a bug).

## 2026-08-18 (17) - PE paired eval harness shipped

Public `e8fdf28` (owner-approved PE v1; CI `32066585897`). splice eval pe drives
a held-out taskset in paired arms: cold (memory off) vs warm (memory on, tasks
run in order against one shared repo copy so accrual IS the condition), fresh
copies per arm, deterministic session ids, traces collected from memd, task
success decided by each task's shell check (never LLM judgment). Lexicographic
decider: evidence floor 10 pairs, depth-guard tolerance 0 (any success drop =
regression), 10% cost margin, burden gate, tie-goes-cold, zero-success-cold
division guard pinned inconclusive, gate trail in the report. exec gains
--memory=on|off. Veritas bypass seam documented (constant marked where the
cache flag lands). Interpretation accepted: PE is a MODE of the existing
splice eval command (splice eval pe), not a new top-level command. Reviewed
by 019ff87f (decider gates, arm structure, knob, verdict strings). This
closes the adaptive cycle: traces accrue, LN2 calibrates above floors, PC3
injects kept knowledge, and PE manufactures controlled evidence to prove or
disprove the value. Remaining: LN3 (data-gated), LN4 (last), then the
operational queue (ephemeral run-ID threading, kept-rate rollback guard when
the verdict corpus matures, Track T).

## 2026-08-18 (18) - Learning surface, preflight, and the memory-notice latent bug

Public `3fab286`, `46b2c7b`, `f039b9c` (owner-approved; CI `32097650306` and
`32144391789`). Learning surface: memoryStatusSegment renders "unavailable";
transitions into off/unavailable emit their own system rows. LATENT BUG FIXED:
the transition branches sat under the memoryNoticed flag, so once the
session-start "memory active" notice fired, a mid-session memory death was
silent forever - the exact failure the three-state work existed to surface.
PC3 now emits "exemplars: N from kept runs" when injection fires (silent at
zero). Preflight (internal/splice/preflight.go): pure advisory checks run
after plan build - permission mode per stage tool ("may prompt mid-run" /
"will be denied"), enabled beforeTool hooks that could intercept splice.*,
and provider tool-calling capability from the static catalog (never a live
probe). Emits warnings via OnReasoning and continues: user machinery stays
authoritative. Reviewer follow-up landed as `f039b9c`:
TestStageSpliceToolsKeysAreRealStages pins the hand-maintained stage-tool map
to the registry so renames/deletions fail CI; the uncovered direction (a new
stage calling splice.shell without a map entry) is documented in a comment
naming stages.Capabilities as the future home. Substrate-risk framing for the
record: contract drift (classification tests) and merge discipline
(rehydration safety) are standing guards, not open work; user machinery and
provider capability are now diagnosed before spend instead of mid-run.

## 2026-08-18 (19) - Roadmap reconciliation and next-step spec

Docs-only session (no code). Reconciles ROADMAP against shipped work and
records the next-step spec from the 2026-08-18 status assessment. Three
stale checkboxes fixed: PC3 (`084211e`), LN1 (`60ddf34` + `ed1256f`), LN2
(`8610484`) were still `[ ]` though shipped; now `[x]` with CI numbers. The
adaptive loop is closed: MD1 -> LN1 -> LN2 -> PC3 -> PE v1 all land, plus the
learning surface, preflight, and memory-notice latent-bug fix (entry 18).

Next-step spec, in dependency order:
1. TW4 (session identity: origin_cwd, picker labeling, resume from main repo).
   Unblocked: MD1 shipped 6e5208d. The only bounded, unblocked shippable item.
2. LN3 (trajectory weights + learned skip policies). Data-gated, not
   code-gated: SD3/SD4 (its code dep) already shipped. Blocker: the verdict
   corpus is empty (fitter floor 20 samples per six-tuple bucket; live proof
   logged "0/20"). Remedy: run PE (evidence floor 10 pairs) and real runs to
   fill buckets. Wall-clock, not engineering.
3. Operational queue. Ephemeral run-ID threading: owner-deferred twice, needs
   an owner decision. Kept-rate rollback guard: same verdict-corpus data gate.
4. Track T (configurable pipeline) then PC1/PC2. Planned v0.3 runway, lands on
   tracks-twa, checkpoints start only on owner go-ahead. Not started.
5. LN4: deferred last (needs thousands of (plan, outcome) pairs + PE gate).

Blockers, one line each: LN3 and the rollback guard wait on verdict-corpus
maturity; Track T / PC / LN4 wait on owner go-ahead; run-ID threading waits on
the owner decision. Public repo is green (build, vet, test, gofmt) and in sync
with origin/dev.

## 2026-08-18 (20) - Track T/W/A checkpoints filed

Docs-only. The v0.3 runway (configurable pipeline + native review surfaces,
plans/configurable-pipeline-and-review-surfaces-2026-07-23.md) had a runway
order but no per-checkpoint items in ROADMAP. Transcribed plan section 8 into
18 concrete `[ ]` checkpoints: T1-T8 (topology core, zero behavior change
until T5/T6), W1-W3 (web foundation), A1-A5 (annotate/review, A5 optional),
T9-T10 (pipeline viewer + editor). Each carries its scope, the section-4
contract it realizes, and its `go test` gate. Runway order unchanged:
T1-T8 -> W1-W3 -> A1-A2 -> T9 -> A4/A3 -> T10 -> A5. Nothing started; each
checkpoint still requires owner go-ahead and lands on tracks-twa.

## 2026-08-18 (21) - Track LX filed; LN4 re-decided to learned topology

Docs-only. The 08-17 learning-design doc decided components beyond the
LN1-LN4 ladder that had no checkpoint home. Filed a new Track LX (learning
extensions), gated on the LN3 + PE verdict corpus: LX1 cross-project transfer
(two channels, consume/contribute switches, weak priors, loud provenance),
LX2 adaptive tooling the ratchet (envelope ceiling + learned narrowing +
escalation as traced intervention), LX3 measurement dashboard as the living
rollback guard (tokens down + kept-rate down = revert), LX4 red-capability
testing (failing-test red flag; red-cap authored-test sampling). Also fixed
the stale LN4 wording: the 08-17 session re-decided LN4 as learned topology
(harness proposes a pipeline diff, user-approved, dead last), superseding the
earlier "train a composer/classifier" text. Training local models (offline,
user-invoked) and the Veritas cache (owned by semantic-cache; splice only
shipped the X-Veritas-Bypass header, d8dc747) stay deferred/external.

## 2026-08-18 (22) - TW4 session identity shipped (CI 32162579962 green)

Public `b537c96` (owner-approved TW4). Session identity is no longer an exact
cwd match: Metadata/CreateInput gain OriginCwd (json originCwd), persisted at
create and inherited on fork/child. exec --worktree records the pre-swap
source repo root as origin while execution Cwd stays the worktree. The TUI
matcher (sessionWorkspaceMatch) accepts Cwd OR OriginCwd, with the empty-origin
fallback correctly not widening every plain session; the picker labels
worktree sessions wt:. TUI worktree mode keeps session Cwd at the main repo
(the worktree is per-run options.Cwd), so no TUI-side origin capture is
needed; resume re-enters via the persisted toggle with no double-prepare.
ROADMAP TW4a/b/c marked shipped; TW5 split into TW5a (seed) / TW5b (trust spike).

## 2026-08-18 (23) - T/W/A retargeted to v0.3

Version correction. v0.2.0 shipped 2026-08-10 (manifest 0.2.0, tag v0.2.0),
so the configurable-pipeline + review-surfaces runway (Tracks T/W/A) is the
v0.3 target, not v0.2. Updated the live trackers (ROADMAP T/W/A section,
MEMORY Current State, entries 19/20) from v0.2 runway to v0.3 runway. The
dated plan file (configurable-pipeline-and-review-surfaces-2026-07-23.md) still
reads v0.2.x in its header; left as a historical record.

## 2026-08-18 (24) - Track LL local LLM integration filed

Deep-dive + plan filed as `plans/local-llm-integration-2026-08-18.md` and Track
LL in ROADMAP. Verdict: local LLM support is strong at the transport layer
(ollama + lmstudio first-class Local providers, auto-detect, no-key onboarding,
context discovery, heavy quirk-handling) and weak at capability metadata.
Key findings verified in code: the wizard probes installed-model ids then
discards them (only the catalog default is offered); local models get
tool-calling by default (withBaseCapabilities) with no live probe, so preflight
cannot report a real capability miss; only context length is live-probed (the
/api/show capabilities array is discarded); reasoning_effort is plumbed through
OpenAI-compat but never emitted for local models (empty ReasoningEfforts). Track
LL: LL1 live capability discovery, LL2 installed-model picker, LL3 reasoning
effort mapping, LL4 deterministic weak-model tool narrowing (the floor LX2
refines, not a duplicate), LL5 optional hardware-fit hint.

## 2026-08-18 (25) - Track LL sequencing decided; LL1+LL2 dispatched

Ordered Track LL relative to the v0.3 runway. LL is independent of Track T/W/A
(discovery/provider vs executor/web), so it neither blocks nor is blocked.
Internal chain: LL1 foundation (LL2 pairs, LL3/LL4 depend on its probe), LL5
optional/last. Order: LL1+LL2 first, then LL3, LL4 (coordinate with Track T's
T4 if running; no hard dependency), then LL5; Track T/W/A proceeds in parallel
on tracks-twa at owner go-ahead. Recorded in plan section 8. LL1+LL2 specced
and dispatched to worker 01a00916.

## 2026-08-19 (26) - TW5, LL1, LL2 shipped green

TW5 (worktree seed manifest + trust-inheritance spike) confirmed green:
commit d1ac44c, CI 32191731114. Track TW is now complete (TW1-TW5).

LL1 + LL2 shipped: commit 7c661bd, CI 32210468650. LL1 added
DiscoverOllamaCapabilities (/api/show capabilities + template),
DiscoverOllamaTags (/api/tags), and Registry.OverlayCapabilities, which
re-registers under id/api-model/aliases so Get/Resolve/ResolveWithFallback all
see the overlay. LL2 added DetectedLocalRuntime.InstalledModelIDs and promoted
an installed id to its full registry entry in resolveModelSwitchTarget.

Review finding worth keeping: the overlay is additive-only and
withBaseCapabilities still grants tool-calling to every entry, so a probed
no-tool-calling model still reads as tool-capable and preflight still cannot
report a miss. LL1 delivered the discovery plus the registry seam, not the
miss detection. LL4 now carries the negative signal and the TUI overlay (LL1
wired the headless exec path only). Verified register() only errors on a real
id conflict, so the swallowed re-register errors are safe, not a silent no-op.

Next in Track LL: LL3 (local reasoning-effort mapping).

## 2026-08-19 (27) - LL3 shipped green

LL3 shipped: commit b89494f, CI 32213788355. OllamaCapabilities gains Reasoning
from the /api/show "thinking" capability, and Registry.OverlayReasoningEfforts
mirrors OverlayCapabilities (additive-only, unknown-model no-op, re-registers
under id/api-model/aliases). Reuses the single LL1 probe, still gated to
CatalogID == "ollama".

The real value was a latent bug, not the stated scope. A local reasoning model
carried no ReasoningEfforts, so EffectiveReasoningEffort returned none and the
pre-existing forwardedReasoningEffort suppressed the value: an explicit
--reasoning-effort on a local reasoning model was silently discarded. LL3 opens
that gate by populating the entry; forwardedReasoningEffort and the wire format
were untouched.

Stays opt-in: forwardedReasoningEffort returns empty when the user requested no
effort, so runs that do not ask are unchanged.

Decided (plan open question 1): map onto existing low/medium/high, no
think-on/think-off value, and no raw think field on the request body (native
/api/chat parameter; Splice talks /v1/chat/completions).

Track LL now LL1-LL3 done. Next: LL4 (weak-model tool narrowing), which also
carries LL1's negative capability signal and the TUI overlay.

## 2026-08-19 (28) - LL4 shipped green; Track LL complete except LL5

LL4 shipped: commit 5013996, CI 32252292395. OllamaCapabilities.Reported (true
only when /api/show carried a NON-EMPTY capabilities array) plus
Registry.RemoveCapability, the surgical counterpart to OverlayCapabilities.
Removal fires only on Reported && !ToolCall, at the existing exec probe site and
in the TUI via an injected func matching the context-window seam. preflight.go
unchanged: its check was always correct, nothing could make its condition true.

The tri-state was the whole point. An absent capabilities array (older Ollama,
custom Modelfile) is indistinguishable from a real negative on a plain bool, so
a naive negative overlay would strip tool-calling from every model on an older
daemon and break working setups.

Blast radius of a wrong negative, verified during review rather than assumed:
stage_tier_resolver.go returns early for ProviderKindOpenAICompatible, which is
what localOpenAI produces, so the tool-calling filters at :71/:92 are
unreachable for local models and pipeline model selection is untouched. The
remaining effects are the intended preflight warn, a hidden "tools" badge
(picker.go:560), and the model dropping out of the onboarding candidate list
(onboarding.go:981) where it stays reachable by typing. No refusal, no failed
run. The worker had not flagged the resolver or onboarding consumers.

Re-scoped LL4: tool-set narrowing deliberately NOT built. A model that cannot
tool-call is not helped by fewer tools, and narrowing weak-but-tool-capable
models needs a weakness signal the capability list does not carry. That is LX2's
learned ratchet. No param-size heuristic added.

Track LL: LL1-LL4 done, LL5 (hardware-fit hint) optional and unstarted. Next
decision point is the v0.3 runway (Track T on tracks-twa).

## 2026-08-19 (29) - Adaptive harness direction captured + audited

The external design conversation (ChatGPT, "Agent Runtime Security
Infrastructure", share 6a85bf14) converged on a direction that supersedes the
WorkflowSpec/DAG roadmap: progressive autonomy through learned, scope-specific
authority. Five pillars: evidence ledger (runtime_events, stage_invocations,
stage_interactions; run_traces stays forensic), stages as addressable agents
with a messaging protocol (revision_request re-enters a stage with focused
context = local repair loop), epistemic authority (Beta posteriors per
scope_key, UNKNOWN first-class, freshness decay), deterministic projections in
learn/ (stage_authority, stage_priors, collaboration_priors, topology_priors
keyed by topology_FAMILY not exact hash), and a decision layer
(DecisionSnapshot + policy engine + DIRECT_PEER routing that skips the
orchestrator LLM for verified low-risk repairs).

Captured + audited in plans/adaptive-harness-direction-2026-08-19.md. Audit
verdict: direction is right and fits the repo spine, but weakest where most
load-bearing. Blockers: A1 authority math asserted not derived (spec before
schema), A2 scope_key reproduces the LN2 0/20 exact-bucket stall (must be
broad-first priors, not fallback-from-specific), C bootstrap problem (need a
supervised-attempt earning path or authority can never leave zero). Highs:
message-loop termination, secrets discipline for event payloads, memd
degraded-mode, dual deterministic decision layers (trajectory rules vs policy
router) need a boundary, message invocations must charge run budgets. Tracking
gaps: foundation fixes (sandbox bypass, trajectory doc/behavior, persisted
decisions, incremental events) are checkpoints in NO filed track (sandbox fix
is only a side effect in T5); Track T (topology-first) conflicts with this
direction (messaging/evidence-first) and needs a deliberate re-order or
re-scope. Recommended ordering: foundation -> evidence + authority math spec
-> messaging with termination -> topology. Veritas still unbuilt; authority
coverage numbers depend on it and that dependency is invisible in all plans.

## 2026-08-19 (30) - Track FND foundation phase filed; items routed

Owner direction: clear and solidify the foundation before any adaptive-harness
feature work ("clear the area before building"). Filed Track FND ahead of Track
T/W/A in ROADMAP: F1 sandbox bypass (pulled out of T5 where it was only a side
effect), F2 trajectory doc/behavior mismatch, F3 persist trajectory decisions,
F4 incremental event writes, F5 authority math spec (blocks schema work),
F6 single key-derivation function (loop-closure invariant), F7 broad-first
priors, F8 north-star metric (interventions/task + tokens/task without
kept-rate drop). Routed in from other tracks: RR17 (usage double-fire),
MD4 (topic-key identity), MD5 (socket hygiene), S2c/S2d (sandbox substrate),
PE7c (validate the measurement instrument). Deliberately left in place: RR13,
RR14, RR16, RR18, LN3/LN4, LX1/LX2/LX4. Also reconciled two stale checkboxes:
MD1 shipped 6e5208d (CI 32040532971); F14 parent (F14a/b/c shipped 2026-07-13).
Done criterion for FND is fixed: eight F items plus routed items, no additions
without a named failure they prevent (foundation creep is the track failure
mode).

## 2026-08-19 (31) - Demo priority: Track DM repair-loop slice filed

Owner context: at an AI startup event, the attention signal was surface-led -
a seamless, beautiful, legible UX is what gets noticed, and the owner needs to
show the adaptive concepts WORKING in a sandboxed, demoed, truncated view for
founders/CEOs. Reconciled answer (glm-5.2): the repair-loop vertical slice is
simultaneously the correct first architecture step and the best demo surface,
so no reorder conflict. Order: FND rubble (F1-F4) -> repair-loop slice with
the event contract -> beautiful read-only live view of that slice -> learning
stays shadow until real events accumulate. The slice must be REAL (actual
re-entry, actual events); only the planted trap is scripted, or the demo is
vaporware. Also noted: the external conversation produced three different
"firsts" across its length (Workflow IR in the charter, DesignPlan scheduler
at the roadmap review, stage semantics in the final answer); the settled order
here is FND -> stage semantics via the DM slice -> TUI projection -> learning.
Filed Track DM (DM1 typed StageMessage with one kind, DM2 bounded re-entry in
run.go max 2 repairs charged to run budget, DM3 incremental interaction
events, DM4 TUI interaction card in the existing pipeline panel, DM5 fixture
repo with the DeleteUser/AuditService planted trap running under --worktree).
Done criterion: live loop on the fixture, in the TUI, in a worktree, events
in the trace.

## 2026-08-19 (32) - Website decision: wait, README is the interim surface

Owner asked whether to host a marketing site now. Decision: wait. The
messaging gate is the demo: copy today either repeats the old fixed-pipeline
thesis (rewritten in two weeks) or claims the adaptive thesis unproven (vapor
an engineer checks by cloning). Stake the domain now (owner-side). The interim
site is the GitHub README: after DM lands, a 30-60s recorded terminal run of
the repair loop goes at the top and serves as the marketing surface for the
founder/PM audience. Revisit a single-page site next week, post-demo. W1-W3
(product pipeline viewer) is unaffected and stays on its own track. Relayed to
the worker explicitly so no site work starts there. Also shipped today:
DM1 (0a6e638) and F4 (74873a9), CI 32283467819 green; DM2 dispatched and
in flight (GO given 17:46 UTC).

## 2026-08-19 (33) - splice-site state confirmed; site paused with clean base

splice-site session (01a01b2d) reports: nothing running (no agents, no
deploy). Committed draft is safe as the future base: full static site, landing
+ 20 docs routes, night-notebook design system, install switcher, 7-scene
scroll story, launch prep ab0ff0f (404/500, sitemap, robots); domain named in
sitemap is spliceagent.dev. Uncommitted spine (direction fold: Now live / Next
destination / Aim north star, /roadmap/ page, PRODUCT.md + DESIGN.md, dated
2026-08-15) paused in place. It wrote PAUSED.md so no session resumes
build/host. Two self-confirmations of the wait logic: it had already blocked
its own demo-video redo because it wants an honest <video> of the pipeline
(same asset the README recorded run will be), and it flagged that the landing
still leads with the fixed-pipeline thesis ("A pipeline, not a chat") with
learning hedged as destination — the exact copy trap the wait prevents
shipping.

## 2026-08-19 (34) - DM4 shipped; live fixture run smoked out real bugs

DM4 (TUI message cards) shipped 195ab88, CI 32292135967. The first live
integration run of the fixture caught things before Saturday: (1) a
pre-existing ledger bug that aborts runs: stage usage reasoning tokens
(~2x request usage) diverges because validation-failed attempts inside the
typed-output retry loop consume tokens without being recorded in the request
ledger; applyRequestLedger correctly kills the run loudly. Root-cause hunt
dispatched to the worker as top priority. (2) The fixture task needs wording
that keeps the model from rewriting users.go wholesale (a full-file rewrite
removed audit structures -> build failure -> trap never fires). Pre-tuning
note added. (3) Environment skew caught: the running splice-memd was an old
npm-installed binary with the pre-F4 write-once UpsertTrace; partial writes
would win and final traces would stick at "running" forever. Swapped to a dev
build; before demo, memd must run the matching build.

## 2026-08-20 (35) - Ledger bug root-caused and fixed (cb639c5)

The demo-blocking usage-ledger mismatch is fixed, committed, CI-green, and
confirmed live (0 ledger errors across a full fixture run). The worker's first
diagnosis (addUsage double-summing aliases) was real but not the live failure;
its Effective* change is kept as defensive hardening. The true mechanism:
the stage summed RAW per-attempt usage via addUsage, while the request ledger
normalizes each attempt (NormalizeUsage) and ZEROES un-normalizable reports
(reasoning > output, produced by providers that report completion excluding
reasoning). An un-normalized attempt counted in the stage total but was zeroed
in the ledger, so applyRequestLedger aborted with a reasoning-token mismatch.
Fix: callValidatedToolUse now sums normalizedAttemptUsage (the ledger-recorded
value) per attempt, so both sides agree by construction. Regressions:
TestValidatedToolUseUnnormalizedReasoningMatchesLedger (reproduces the exact
live divergence) and the worker's TestValidatedToolUseUsageMatchesStreamCallbacks.
usageFromCollected reads only canonical Input/Output tokens, so dropping the
alias accumulation in addUsage is safe. CI 32336687798.

Two remaining live-run findings for the Saturday reliability pass (NOT the
ledger bug): (a) the code_writer stage did not receive file CONTENTS, only a
directory listing (model said so verbatim), so it reconstructed users.go from
scratch and oscillated 110->90->131 lines -> "wall time exceeded" after 6
stage records; (b) the test-generator stage collided with the fixture's
existing users_test.go (write_file error: already exists). Both are context-
fulfillment / fixture-shape issues, not usage accounting.

## 2026-08-20 (36) - Cost optimization remains a Splice hypothesis

The Adaptive Auto-Harness paper and the harness-engineering article refine the
adaptation and evaluation method. They do not prove that adaptive runs become
cheaper. The paper optimizes task reward, reports solver resources only as
diagnostics, and omits persisted evolver tokens plus some orchestration cost.
Splice owns the additional hypothesis: comparable warm-project work should use
fewer tokens, less amortized total cost, less latency, fewer retries, and fewer
interventions per kept successful task without a correctness regression.

Progressive autonomy and cost optimization are now separate measurement axes.
Less user supervision cannot stand in for lower internal inference. Cost now
has three named layers: operational cost, learning/evaluation cost, and
amortized total cost. Management burden is a separate weighted-intervention
measure. Policy promotion stays lexicographic: evidence floor, deterministic
success and kept-rate tolerance, amortized cost margin, management burden, then
keep the incumbent when results are inconclusive. A material success gain with
higher cost is a capability tradeoff, not a cost win, and needs owner approval.

The measurement work split into LX3a and LX3b. LX3a is an observational
baseline dashboard and can start when its existing trace and PE inputs are
trustworthy. It changes no policy. LX3b remains behind the LN3 and PE verdict
corpus and owns paired fixed-policy versus adaptive-policy promotion and
rollback. The DM repair-loop demo proves bounded autonomous recovery only.
A later repair-enabled versus repair-disabled pair must support any cost claim.
ROADMAP also caught up with DM4 shipped and corrected DM1/DM2/DM5 wording to
match current public dev behavior.

## 2026-08-23 (37) - Eval instrument hardened twice by its own failures; demo decisions locked

The demo-ready wayfinder map moved from planning into execution. Owner
decisions recorded: ticket #9 resolves to two takes, both filmed in
splice-demo (a ~35s site video on the architectural tier and a separate 30-60s
README repair-loop recording); ticket #10 resolves to a quickstart that leads
with `splice exec` and stream-json, keeps the bridge binary name, and ships
one minimal non-Pi sample consumer. The splice-demo fixture was reset clean to
`82d8f9c` with owner approval.

The paired eval (#7) ran twice and taught more than a clean run would have.
Run 1 crashed at task 10 of 24: the harness treated one `abort_budget` exec
failure as fatal and discarded all completed pairs. Fix `9dcd56c` makes a
failed exec mark that arm failed (with verbatim error and partial tokens) and
persists each pair to pe-pairs.jsonl as it completes. Run 2 finished all 12
pairs but its mechanical CONCLUSIVE verdict ("100.0% fewer tokens") was an
artifact: cost telemetry silently returned zeros because collectTrace joined
traces on the raw arm path while macOS records the symlink-resolved path
(/var vs /private/var). Honest status after run 2: success direction is
positive (warm 8/12 vs cold 6/12, zero warm regressions), cost gate is
UNMEASURED. A trace-join fix plus explicit missing-telemetry warnings ships
before run 3.

Two product findings feed the staging-refinement track. First, the classifier
cannot emit TierStandard at all: trivial-keyword or short requests go trivial,
substantial-keyword or long requests go substantial, architectural keywords go
architectural, everything else lands light. The five-tier budget table implies
a band that no request can reach. Second, a reasoning flagship doing one
repair loop exhausts the light tier's output budget (run 1's abort was exactly
this), so light-tier budgets are tuned for models that do not reason in the
open. Budgets stayed frozen through the eval to keep the memory question
clean; both findings are queued for the refinement slice after run 3.

Model routing note for evals: OpenRouter credits are exhausted; evals run on
OpenAI gpt-5.6-sol through the chatgpt provider profile. Note that
stage-models.json overrides the --model flag for pipeline stage calls; pass
--model gpt-5.6-sol explicitly so the flag matches what actually runs.

## 2026-08-23 (38) - Eval v2 replaces the quarantined paired claim path

The failed synthesis workflow was recovered from four completed local research
artifacts. Primary evaluation methods, memory and retrieval controls, current
Splice seams, and the owner's project workflow are now synthesized under
`plans/eval-v2/`: protocol, threat model, efficacy and safety preregistration
templates, PC-local runbook, research basis, and index.

The causal design is fixed at three schema-identical frozen arms: empty, hard
placebo, and relevant. All arms trace identically. Observation and exemplar
writes are disabled. The fixed primary sequence is relevant-versus-empty
success non-inferiority, relevant-versus-empty net token improvement, then
relevant-versus-placebo content improvement. This prevents a harmful placebo
from producing support when relevant memory does not beat empty memory.
Stale/conflicting safety has its own locked limits and verdict. Online
adaptation remains separate. Memory dispositions stay diagnostic and never
substitute for deterministic outcomes.

The current 12-task suite is permanently Development only because task checks,
budgets, prompts, telemetry, isolation, and the memory contract changed after
result inspection. Run 7's 43.2 percent figure is quarantined. It cannot close
ticket #7, change `PRODUCT.md`, or appear in public copy. Eval v2 needs a sealed
PC-local no-remote holdout, a training-only corpus and selector frozen before
task selection, randomized counterbalanced repeats, complete telemetry, locked
routes and hashes, OS-level hidden-root denial, a sanitized child environment,
preregistered exclusions and stopping, task-quality audit, fixed-task
paired-block intervals, and independent review.

Filed Track EV2 in `ROADMAP.md`. EV2-D0 is complete. EV2-0 through EV2-9 build
and validate the instrument. EV2-10 is an explicit owner gate for the exact
success margin, two useful token thresholds, safety limits, sample, model
route, maximum next-call reserve, and hard spend cap. EV2-11 is the first
possible Level-1 internal claim run. EV2-12 requires a
second holdout and model or provider before a quantitative public claim.

Two fresh-context reviews initially returned NO-SHIP. The first found ten
specific defects: a placebo could create support without beating empty, hidden
roots were readable under the current macOS shell policy, the safety gate had
no decision rule, holdout retrieval could be curated, the bootstrap and token
formula were ambiguous, and four runbook or identity defects remained. The
revision added a relevant-versus-empty gate, OS-level read denial, a separate
safety preregistration, corpus-before-task ordering, an exact fixed-task
paired-block estimator, canonical `Usage.TotalTokens()` accounting, `--serve`,
ID mapping, destination archive verification, and a pre-call spend reserve.
The bounded re-review found two remaining ambiguities in the token estimator
and archive copy. Both are now explicit: ratio of equal-task arm means, and a
relative-path manifest verified after copy.

The strongest remaining objection is cost. The protocol does not answer it by
weakening evidence after results appear. A development-only variance pilot sets
the sample and expected spend. If the powered design exceeds the owner's cap,
the claim remains untested.

## 2026-08-24 (39) - Reasoning-memory contract reviewed, repaired, and CI-green

The three planned implementation commits landed on public `dev`: `8a4f9ae`
adds schemas and the deterministic `memoryreason` module; `e9c0bd8` adds the
paired prompt/tool contract and two-layer decode; `4f2e189` sends normal and
repair invocations through one preparation path and traces post-compaction
reviews. No inference run occurred.

Two independent review axes initially failed the slice. The standards review
found a hand-written integer formatter and unused exported method. The spec
review found four contract gaps: memory counters mixed latest-item counts with
cumulative characters across repair invocations; reasoning-stage input
validators did not validate stable IDs or duplicates; an omitted disposition
array did not count as a parse issue; and the known changed-file compactor
selected keys from the summary map. It also found that the tests fabricated a
review merge but did not prove full repair retrieval plus two reviews.

Repair `edad633` closes those gaps. It uses `strconv.FormatInt`, removes the
unused method, validates delivered memory before provider calls, counts
missing/null arrays without retry, aggregates item and character counters per
invocation, preserves a review on the fallback merge path, fixes independent
changed-file key selection, and adds full repair retrieval/review/counter
coverage. The focused contract suites, full root tests, vet, build, and the
separate memd module passed locally.

The first CI run exposed one unrelated portability defect from an earlier eval
commit: a repo-root test required the macOS `/var` alias on Linux. `f6c2bf7`
makes the test assert the actual raw-versus-canonical rule on both systems.
CI run `32675528049` then passed Build & Test and the memd sidecar job. Public
`dev` is clean and matches `origin/dev`.

This completes the memory prerequisite only. No claim-bearing eval is now
licensed. EV2-0 still requires owner approval of the protocol direction, and
EV2-10 remains the exact preregistration and spend gate.

## 2026-08-24 (41) - EV2-1 and EV2-2 shipped green; demo-readiness slice landed

Three implementation checkpoints and one demo slice shipped on public dev,
each reviewed by the planner session with independent adversarial probes
outside the repo.

EV2-1 durable identity (`0962e757`, CI 32756229083): deterministic
Latin-square schedule generation from PCG(seed, FNV1a(experiment-id)),
an append-only idempotent trial journal with rank-ordered statuses, and
MissingTrials as the resume primitive. Probes confirmed block balance,
determinism, and crash-replay idempotence. Spec committed as 28594fb2.

EV2-2 isolated environment (`0974877a`, CI 32760689531): workspace layout
builder with sha256-pinned binary staging and clean-source gate, explicit
trial reset that preserves sibling journals, four-shape hidden-root deny
rules, deny-by-default child environment allowlist, and preflight schemas
requiring a complete denied probe matrix. Probes confirmed symlink-escape
denial and /var-/private/var alias equality. Spec committed as 4cea7386.
Wiring notes for the runner: consume or drop BuildDenyRuleSet; add an
explicit tool-class parameter to DenyRuleSet.Check; pin the git binary
from manifest toolchain versions.

Demo-readiness (`77af0bca` timeout 600s default; `9304d5ea` skip .pi and
.pi-subagents in workspaceindex), CI 32758708342. The repair-loop fixture
was revalidated live on current dev after these fixes: one take,
chatgpt/gpt-5.6-sol route, trap-test failure produced typed revision
evidence, one bounded code-writer re-entry, tests passed, exit 0;
6,894 input / 2,134 output / 430 reasoning tokens across 3 requests,
about $0.10. The public repo also gained the Knowledge-to-Contract layer
design brief (20ac5f3) as a Planned doc.

Next: EV2-3 frozen memory policy. Demo remains gated only on a fresh GIF
capture with scrubbed terminal chrome.
