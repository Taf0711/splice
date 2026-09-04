# SPEC: P1.4 Ideal-Iteration Delta (Pen "Splice TUI — Ideal Iterations (P1.4)")

**Authority:** live Pen desktop document "Splice TUI — Ideal Iterations (P1.4)"
(extracted 2026-09-03 via `pen interactive --app desktop`). This document
postdates capsule v5 and the landed gap report. Per owner rule 12b, the newer
E2E frames win where they conflict.

**Branch:** `feat/tui-workflow-surfaces` (integration; PR #26 draft = CI gate).

## What the frames specify (extracted, verbatim where normative)

### Lane 1 (design workflow) and Lane 2 (execution workflow)
Ten turns of one real session. One column, one status line, dialogs on top.
Left column = exactly what the terminal renders; commentary never renders.

### S1 — Design mode (wait dead-zone fixed) — frames jvJ9M, gEVp1
Live sidebar DURING design mode (audit finding 2: "sidebar wakes up too late"):

    SPLICE
    dev · design mode
    DECISIONS  2/4
      [+] retry idempotent methods
      [+] preserve deadlines
      [~] retry semantics — in progress
      [ ] acceptance criteria          (open, unpinned)
    OPEN FILES
      internal/http/client.go
      internal/http/retry.go
      internal/http/client_test.go
    RUN
      elapsed   5m 12s
      tokens    18.4K ↑
      stage     design · thinking

Transcript markers: "▸ Thought for 2.1s", "• Explored …", "• settled: …",
"… weighing: …", "▲ will ask you about: …".

### S2 — ask_user elevated — MD7PL (LANDED: NEEDS YOU card, [R]/[A]/[E])
No new work beyond regression-net keys already landed.

### S3 — Crystallized plan → approve — m0x0SG (LANDED: plan/critique cards)

### S4 — Executing with trajectory — aPZTh
Pipeline stage chips with counts (WRITE/ANALYZE/SECURITY/TESTS 46/48/ACCEPTANCE
4/6) — LANDED. NEW: trajectory as an inline sparkline in the sidebar body:

    trajectory
    61 ▔▔▔▔ 67 ▔▔ 72
      4 passes · 1 rollback (58 → restored) · score rising

### S5 — Regression intervention — LjQaO
Dedicated intervention card on regression:

    ⚠ REGRESSION DETECTED — splice is intervening
    This attempt reduced verified quality. Restoring pass 2 automatically.
    PASS 2 (kept)     score 67 · tests 48/50 · acceptance 5/6
    PASS 3 (rejected) score 58 · tests 46/50 · acceptance 4/6
    trajectory   61 ▔▔▔▔ 67 → restored      \  58 →
    root cause   retry classification applied after body-replay selection
    [i] inspect pass-3 diff · [s] stop run

### S6 — Failed receipt (styled) — mqEwB (LANDED: FAILED card)
Pen keys: [m] review merge-back · [r] retry from pass 3 · [k] keep worktree &
exit · [j] open raw receipt. Tree keys: [R] restore · [I] intervene · [L] logs.
Keys differ but the affordance set matches runtime truth. Keep tree keys.

### S7 — Completion receipt — dlq46 (LANDED: VERIFIED card)
Pen: [s] export · [d] open diff · [n] new task. Tree: [O]/[E]/[R]. Keep tree
keys (new task is a command, not a receipt key; no dead keys per 9b).

### Status bar — esBzN + segment frames (w0BIJA/dt7AD/UAYbi/GwrAE/s6H72/nT9R8)
"the left cluster is a projection of presentation state, not a label."
Segments (every segment optional and event-driven; the bar recomposes on each
runtime event — never one hardcoded layout per phase):

- **phase chip** — color + word driven by lifecycle phase:
  design(lime) crystallizing(amber↑) critique(amber) executing(lime)
  regression(red↑) recovering(amber↔) verified(green✓)
- **context trail** — breadcrumb of phases visited this session:
  design→plan→exec — grows as Splice context-switches; never rewinds
- **work segment** — what the current phase is doing, in its own terms:
  pass n/m · tests x/y · tasks n · decisions n/4 — swaps per phase
- **agent telemetry** — appears only when work is distributed:
  N agents · k running — fan-out/fan-in counts from runtime events
- **session meters** — stable right side: elapsed · tokens · cost

### Invariants strip — oEVcK (all hold in the tree already; regression-net)
UI projects runtime truth, no policy in render; user owns design transitions,
orchestrator owns execution transitions; nothing applies without staging;
VERIFIED only from deterministic evidence; every terminal state names the next
action and the debug path.

### MCP Manager v2 — VCeGi (LANDED: staged-add card). The manager shell
(INSTALLED/MARKETPLACE groups, / search, detail pane) is a separate surface;
out of scope for this batch unless the owner pulls it in.

## Runtime truth (verified in tree 2026-09-03)

- Decision pins: `sessions.EventDecisionPinned` events reconstruct through
  `splicerun.ReconstructDesignState(m.sessionEvents)` (design_lifecycle.go:119)
  → `state.Decisions []DecisionPinnedPayload`. LIVE source: the drained
  decision pins already refresh the transcript ledger card
  (lifecycle_cards.go:61). The sidebar module can read the SAME data.
- PASS/FAIL: no "in progress"/"will ask" states exist in DecisionPinnedPayload
  (only Statement/Detail/Revised). The S1 mock's [~] in-progress and open rows
  CANNOT be projected from runtime truth today → those two line kinds are
  DEFERRED-PENDING-RUNTIME (never invented). The settled-ledger module IS
  buildable.
- Phase trail: presentation.State.Lifecycle changes arrive per
  presentationStateMsg. The trail is derivable by observing lifecycle
  transitions in the TUI (append on CHANGE only, never rewind).
- Work segment: during execute, m.plan (done/total), pipeline counts
  (m.pipeline.presentation()), decisions n/4 (ReconstructDesignState). During
  design, decisions n and phase. Swaps per phase — matches the frame.
- Agent telemetry: len(sidebarSpecialists)+len(swarmSpawnedAgents) and the
  running subset — existing model state.
- Session meters: turnStartedAt (elapsed), usageTracker.Summary() (tokens,
  cost) — existing.
- Trajectory sparkline: state.Trajectory.PassScores + RestoreMarkers already
  in presentation.State (state.go:32). Renderable now.
- S5 regression pass-compare table: needs per-pass score/tests/acceptance as
  structured data. State has only PassScores []float64 and RestoreMarkers
  []string. The kept-vs-rejected table CANNOT be projected honestly today →
  the card keeps the current auto-reveal surface; the pass-compare table is
  DEFERRED-PENDING-RUNTIME.

## Slices (one commit each, wire-as-you-go, gates green before next)

1. **Sidebar DECISIONS module** (S1): new ContextSlot after Plan; renders the
   reconstructed ledger (settled [+] / REVISED [~] rows, count n in header).
   Absent when zero pins. Probes: renders from ReconstructDesignState data;
   absent when empty; budget drop-whole.
2. **Sidebar RUN module** (S1): elapsed (turnStartedAt), tokens (usageTracker),
   stage (phase · spinnerPhase). Only while a run is live or design mode is
   active. Probes: live values, absent when idle.
3. **Trajectory sparkline** (S4): replace the pass-list render in
   trajectory_surface.go with the inline score trail (61 ▔▔▔ 67 …), restore
   markers as "N passes · M rollbacks" summary line. Keep ASCII tier. Update
   goldens + trajectory tests.
4. **Status-bar segments** (esBzN): phase chip remap (design/crystallizing/
   critique/executing/regression/recovering/verified colors per frame),
   context trail (append-on-change), work segment (phase-appropriate counts),
   agent telemetry (only when specialists/swarm exist). Session meters already
   exist on the right. Width tiers drop segments whole.
5. **Receipt/keys + deferred ledger**: no change (tree keys stay; S5
   pass-compare + narration weighing markers recorded as
   deferred-pending-runtime in GAP_REPORT).

## Do-not-simplify
- The trail NEVER rewinds (frame RRoni: "grows as Splice context-switches").
- Segments are event-driven projections; never hardcode one layout per phase.
- Deferred surfaces stay deferred: [~] in-progress pins, weighing/will-ask
  markers, S5 pass-compare table, manager shell. No invented data.
