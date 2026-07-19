# UI Architecture

## Why This Is Its Own Doc

Most agentic CLIs blur the line between engine and UI. They wire prompts,
spinners, and panels directly into orchestration code. That works until
someone wants a different front end (a richer TUI, a web dashboard, an RPC
client, a TypeScript wrapper) and discovers the engine is welded to the
terminal.

Flug treats the UI as a replaceable layer from day one. The engine
(`flug/orchestrator/`, `flug/providers/`, `flug/storage/`, `flug/security/`,
`flug/agents/`, `flug/optimizer/`, `flug/schemas/`, `flug/tools/`) stays
terminal-agnostic. Two UI layers sit on top of it, and only those layers
import a terminal rendering library:

- `flug/tui/` imports `pyratatui` (ratatui rendering). This is the native,
  primary front end.
- `flug/ui/` imports `rich`. This is the fallback front end.

This doc captures that contract, the brand kit, the exact screen layout, the
state model, and the surfaces the TUI renders. The mockups in
`docs/design/assets/*.png` and the handoff brief in
`docs/design/tui-design-brief.md` are the visual source of truth this doc
tracks.

## Native UI Is the TUI; Rich Is the Fallback

Decision (2026-06-26, user directive): the `pyratatui` TUI is the native,
primary UI for **every** interactive surface, not just the running session.
Bare `flug` launches the TUI. The first-run setup wizard, `/login` provider
auth, masked key entry, confirmations, the command palette, and the help
overlay are all native pyratatui surfaces. None of them hand off to Rich from
inside the TUI.

`flug/ui/` (Rich) is now formally a fallback-only layer. It renders when:

- stdout is not a TTY (`flug run`, pipes, CI),
- the user passes `--json` (structured event stream, no styled UI), or
- pyratatui cannot load (unusual platform or dependency failure).

This does not violate the "UI is a replaceable layer" commitment. The engine
still has no opinion about how it is rendered; both UI layers consume the same
engine API and the same `on_stage_event` / `StageRecord` callbacks. "Native =
TUI" is front-end primacy, not engine coupling. The historical phrasing "only
`flug/ui/` imports a terminal library" is superseded by the two-layer rule
above: the engine imports neither `rich` nor `pyratatui`.

## Visual Identity

Flug's mascot is inspired by Dr. Flug from the show Villainous: a paper bag
worn over the head with goggles strapped on top and a child-style airplane
drawn on the front. The German word "Flug" means "flight," so the airplane
motif also fits the project name. The airplane (`✈`) is the throughline of the
whole UI: it is the logo, the active-progress spinner anchor, and the prompt
glyph.

### Icon primitives

Three repeating shapes, used at every size:

| Primitive | Glyph(s) | Role |
|---|---|---|
| Goggles | `◉═◉` | Top of the paper bag |
| Paper bag | `╭ ╮ ╰ ╯ ─ │` | Rounded box drawing |
| Airplane | `✈` | Front of the bag, also the active progress glyph |

#### Banner (large)

Used on `flug` startup and in the setup wizard welcome step.

```
   ╭──◉═◉──╮
   │       │
   │   ✈   │
   │       │
   ╰───────╯
```

#### Compact (header)

Used inline next to panel titles or section labels, and as the wizard step
badge.

```
╭◉═◉╮
│ ✈ │
╰───╯
```

#### Glyph (status)

Single airplane character used as the prompt prefix, the in-flight progress
indicator, and the lead glyph on info messages.

```
✈
```

### Color palette (truecolor hex)

Restrained on purpose. Two accents carry the identity: sky blue is the flight
motif, amber is the paper bag.

| Name | Hex | Use |
|---|---|---|
| Sky blue | `#7DD3FC` | Flight motif: banner text, panel borders, prompt glyph, info |
| Amber | `#FCD34D` | Paper bag: bag walls, panel titles, accent highlights, brand wordmark |
| Slate | `#94A3B8` | Muted/secondary text, dividers, inactive breadcrumb labels |
| Success green | `#86EFAC` | `✓` success |
| Warning amber | `#FBBF24` | `↩` skip, `↻` retry, warnings |
| Error red | `#FCA5A5` | `✗` failure |
| Info indigo | `#A5B4FC` | info accents |
| Dim slate | `#475569` | deep-dim hints and disabled rows |

Assume a dark (near-black) terminal by default and also support a light
terminal. Do not rely on a specific background fill: terminals own the
background, so color only the foreground (and selection highlight) and design
for both dark and light.

### Status glyph set (the only progress glyphs)

| State | Glyph | Style |
|---|---|---|
| pending | `·` | slate (muted) |
| running | `✈` | sky blue (info) |
| success | `✓` | green |
| failed | `✗` | red |
| skipped | `↩` | amber (warn) |
| retry | `↻` | amber (warn) |

The running glyph deliberately matches the icon glyph. A pipeline in flight
literally shows airplanes against rounded panels, reinforcing the visual
identity without extra animation.

### Spinner

Animated braille frames cycle for in-flight work:

```
⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏
```

The running task header and the actively running stage row swap their static
`✈` for the current spinner frame so the screen visibly breathes. The status
strip and the empty-state idle marker use the filled ring `◉` / `◎`.

### Shape language

All containers use rounded box-drawing corners (`╭ ╮ ╰ ╯`) to echo the paper
bag. Rules and dividers are a single dim `─` line spanning the width. Keep it
clean and airy: lots of negative space, thin lines, no heavy borders, no drop
shadows. Overlays are full-region swaps, not floating windows.

### ASCII and NO_COLOR fallbacks

When `NO_COLOR` is set or the terminal cannot render the glyph set, every
non-ASCII glyph degrades to a stable ASCII token and color is dropped:

| Styled | ASCII fallback |
|---|---|
| `✓` | `OK` |
| `✗` | `ERR` |
| `⠹` (spinner) | `..` |
| `✈` | `*` |
| `→` | `->` |
| box drawing | `+ - |` |

The brand kit (semantic style tokens, the glyph set, spinner frames, banner
constants, and these fallbacks) is centralized in `flug/tui/theme.py` so the
TUI, the Rich layer, and `flug/ui/icon.py` all source one definition and
cannot drift apart.

## Terminal Constraints

The TUI is rendered by ratatui in a real terminal. These bound what is
buildable:

- **Monospace grid.** Everything snaps to a fixed-width character cell grid
  (columns by rows). A "pixel" is one character cell.
- **No images, vectors, or gradients.** Color is per-cell foreground and
  background only. Rounded corners are box-drawing characters, not
  anti-aliased curves.
- **Truecolor (24-bit) is available**, so the hex palette is fine, but it is
  used sparingly because terminals vary.
- **Limited glyph set.** Stick to ASCII plus the box-drawing set plus the
  specific glyphs above (`✈ ◉ ◎ ═ · ✓ ✗ ↩ ↻ → ⠋…⠏ █ ░`). Avoid emoji and
  exotic Unicode; they break monospace alignment.
- **Responsive width, two breakpoints.** Narrow (< 100 columns) is a single
  column. Wide (>= 100 columns) splits the transcript 55% / 45%. Below roughly
  60 columns by 18 rows the TUI shows a minimal fallback layout. The width
  threshold lives as `WIDE_THRESHOLD` in `flug/tui/app.py`.
- **Variable height, content scrolls.** Design for a scrollable transcript
  region, not a fixed canvas.
- **One full-screen alt-screen app.** When running, Flug owns the whole
  terminal. Overlays are full-region takeovers.

## Layer Separation

```
┌────────────────────────────────────────────────────────────────────┐
│ flug.cli                                                           │
│   parses arguments                                                 │
│   picks a front end (TUI on a TTY, Rich otherwise)                 │
└──────────────────────┬─────────────────────────────────────────────┘
          ┌────────────┼─────────────────────────────┐
          ▼            ▼                              ▼
   ┌─────────────┐ ┌─────────────┐            ┌──────────────────────┐
   │ flug.tui    │ │ flug.ui     │            │ flug.* (engine)      │
   │ pyratatui   │ │ rich        │            │ schemas, providers,  │
   │ native UI   │ │ fallback    │            │ orchestrator,        │
   │ view + app  │ │ banner,     │            │ security, storage,   │
   │ wizard,     │ │ prompts,    │            │ optimizer, agents,   │
   │ overlays    │ │ progress    │            │ tools                │
   └─────────────┘ └─────────────┘            └──────────────────────┘
```

Hard rules:

1. **Only the UI layers import a terminal library.** `flug/tui/` imports
   `pyratatui`; `flug/ui/` imports `rich`. Anywhere else, importing either is
   a CI failure waiting to happen and will be rejected in review. The engine
   imports neither.
2. **No business logic in either UI layer.** UI code only formats data passed
   in. Decisions about provider selection, key storage, or pipeline planning
   never live here.
3. **No secrets in styled output.** API keys, tokens, and passwords must not
   flow through any UI helper. The masked key-entry field never echoes its
   value, and `flug/security/keys.py` exposes only `AuthStatus` objects, which
   are safe to render verbatim.
4. **Non-TTY fallback always works.** When stdout is not a terminal, the CLI
   uses the Rich layer, which degrades to plain text. CLI smoke tests in
   `ci.yml` rely on this.

### TUI internal split

Inside `flug/tui/`, the same separation repeats one level down so the logic is
testable without a terminal:

- `flug/tui/view.py` is pure and imports no `pyratatui`. It maps engine
  objects (conversation turns, `DesignPlan`, `StageRecord`, run summaries) into
  a renderable view model: header text, transcript blocks, stage rows, overlay
  contents, prompt and hint lines, and the wizard state. All of it is unit
  tested.
- `flug/tui/app.py` is the only module that imports `pyratatui`. It owns the
  event loop, raw-mode key handling, the responsive layout split, and turns the
  view model into ratatui widgets. It is imported lazily so that non-TTY paths
  never load pyratatui. Its render paths are hand-verified on a TTY, not in CI.

The same discipline keeps logic in pure functions and `WizardState` so that
checkpoints land green in CI while only the thin pyratatui render glue stays
hand-verified.

## Component Map

| Module | Role |
|---|---|
| `flug/tui/theme.py` | Semantic style tokens, glyph set, spinner frames, banner constants, ASCII/NO_COLOR fallbacks. The single brand-kit source. |
| `flug/tui/view.py` | Pure, testable view model: header, transcript blocks, overlays, prompt/hints, `Mode`, `Overlay`, `WizardState`. |
| `flug/tui/app.py` | The only `pyratatui` importer: event loop, key handling, layout, ratatui widgets. |
| `flug/ui/theme.py` | Rich theme derived from the same palette. |
| `flug/ui/icon.py` | BANNER, COMPACT, GLYPH constants and styled factories. |
| `flug/ui/console.py` | Shared `UIConsole`, banner, tables, panels, status helpers (fallback path). |
| `flug/ui/prompts.py` | `select_option`, `confirm`, `prompt_secret` (non-TTY fallback flows). |
| `flug/ui/progress.py` | `StageTracker` driving Rich `Live` (fallback path). |

## Session State Model

The TUI separates the session **phase** from transient **overlays**. They are
two independent enums in `view.py`, not one flat enum.

### Mode (session phase)

| Mode | Meaning |
|---|---|
| `conversation` | Free-form design chat. The default. |
| `crystallizing` | Brief busy state: distilling chat into a typed plan, then running the critic. |
| `review` | Shows the crystallized plan plus critique; awaiting `/approve` or revision. |
| `executing` | Running the approved plan's tasks one at a time with live stages. |

The header status marker tracks this: `◉ idle`, `◉ conversation`, `✈ running`,
`◉ review`.

### Overlay (transient surface)

An overlay is layered on top of whatever phase is active and replaces the
transcript region full-width. It does not change the phase.

| Overlay | Trigger | Contents |
|---|---|---|
| `none` | default | the transcript |
| `status` | `/status` | stage board: every completed task's stages plus the live task |
| `diff` | `/diff` | last run's changed-file list plus a bounded unified diff |
| `command` | `/` | the slash-command palette |
| `help` | `?` or `/help` | full keymap and command list |
| `login` | `/login` | provider picker and masked key entry |

`Overlay` replaces the older `show_status` / `show_diff` booleans with a single
`overlay` field and one render dispatch. Any non-scroll key closes a passive
overlay (`status`, `diff`, `help`); interactive overlays (`command`, `login`)
own their keys until dismissed with `Esc`.

The first-run setup wizard is neither a `Mode` nor an `Overlay`: it is its own
top-level full-screen state that precedes or replaces a session, driven by a
pure `WizardState`.

## Current TUI Layout

The session screen is a vertical stack of six regions, top to bottom.

| Region | Height | Contents |
|---|---|---|
| **Header** | 1 row | `✈ flug` wordmark, a divider rule, and a right-aligned status marker. |
| **Breadcrumb / progress** | 1 to 2 rows | The `design → plan → code → tests` phase rail (each labeled with its status glyph), and while executing a `[████░░] 3/5 stages` gauge. |
| **Transcript** | fills remaining | Scrollable session content. Splits 55/45 in wide mode. |
| **Rule** | 1 row | A dim full-width `─` divider. |
| **Prompt** | 1 row | Idle: `› {typed text}_`. Busy: `{spinner} {status line}`. |
| **Hints** | 1 row | Dim, mode-aware keybinding hints. |

### Phase breadcrumb rail

A horizontal rail of the four pipeline phases connected by dim rules, each
prefixed with its status glyph and styled by state:

```
✓ design ── ✓ plan ── ✈ code ── ✗ static ── · tests
```

The active phase is highlighted (amber fill on the running label), completed
phases use `✓`, a failed earlier stage shows `✗` with a one-line note
(`✗ static_analyzer failed earlier · /status for details`), and pending phases
are dim with `·`.

### Transcript content blocks

The transcript is built from stacked blocks, in order:

1. **Notice** (optional): a one-line banner prefixed `! `, for example a
   git-safety warning.
2. **Empty-state hint** (only before the first message): a centered compact
   banner over `describe what you'd like to build / or type /help to see
   available commands`.
3. **Conversation lines**: `you` and `flug` turns, label in the left gutter,
   wrapped body to the right, separated by thin dim rules.
4. **Plan block** (after crystallize): task, numbered steps with sub-bullets,
   and an estimate line (`estimated: 24 lines added, 0 lines modified`).
5. **Completed turn blocks** (one per finished task): a `✓ task n/m` header,
   its stage rows with metadata, and a changed-files list.
6. **Live run block** (while a task executes): a spinner header, a text
   progress bar with percent and elapsed clock, the run path, and per-stage
   rows. The active stage shows an elapsed clock and an indented activity
   stream of real harness events (`→ fetching project context`).

### Stage row anatomy

```
{glyph} {stage_name}   {detail} · {duration} · {cost} · {tokens}
```

- `glyph` is the status glyph, or the current spinner frame if actively
  running.
- The trailing metadata appears only once a stage has data
  (`wrote limiter.py · 2.1s · $0.02 · 3.4k tok`).
- The active stage gets an elapsed `{n}s` and up to about five lines of
  `→ activity` underneath.

### Narrow vs wide

Narrow (< 100 columns) stacks everything in one column. Wide (>= 100 columns)
splits the transcript: the left 55% holds the conversation, plan, and finished
turns; the right 45% holds a bordered `activity` pane with the live run block
(blank when idle). PageUp/PageDown/Home/End scroll the left pane in wide mode;
the right pane follows the bottom. Below roughly 60 by 18 the TUI collapses to
a minimal single-column fallback.

## Overlays in Detail

### `/status` (stage board)

A bordered board titled `/status pipeline status` listing every stage across
turns with `status`, `duration`, and a short note column, plus a footer line
of aggregate counts (`1 running · 2 done · 1 failed · 3 pending`) and
`esc close`.

### `/diff` (review)

A bordered `/diff file changes` panel: a header count (`2 files changed +26
-3`), a selectable file list with per-file badges (`[new file]`, `[modified]`)
and add/remove counts, then a bounded unified diff with `-`/`+` lines colored
red and green. Footer: `↑↓ scroll · esc close`.

## New Surfaces

These four surfaces are the active TUI build track (see `ROADMAP.md`, Track
V-TUI). Each is a native pyratatui surface.

### Command palette (`Overlay.COMMAND`)

Typing `/` at the start of the prompt opens a rounded container anchored to the
prompt line. It lists commands and filters as the user types (`/st` ->
`/status`). `↑/↓` moves the highlight, `Tab` completes, `Enter` runs, `Esc`
closes. Each row is a command name plus a short description, with a type
affordance (action, view, settings).

Command scope (default): real now are `/plan`, `/approve`, `/status`, `/diff`,
`/login`, `/model` (alias `/models`), `/help`, and `/quit`. Shown as labeled
"coming soon" rows until their backing work lands: `/reject`, `/log`,
`/config`.

```
› /st
╭──────────────────────────────────────────────────╮
│ ▸ /status   open the stage status board     view │
│   /plan     crystallize the conversation   action │
╰──────────────────────────────────────────────────╯
```

### `/login` (`Overlay.LOGIN`)

An in-app flow to configure a provider's credentials, native to the TUI (no
Rich handoff). Steps: pick a provider, see its current auth status, then enter
a masked key (cloud) or just enable it (Ollama).

The provider picker shows status badges sourced from
`list_auth_statuses()`: `✓ configured`, `· not set`, `↩ via env`, `local`. Key
entry is an in-TUI masked field (`API key: ••••••••••••`) that never echoes the
value; it validates shape with `validate_key_shape` and stores via
`set_api_key` (OS keychain), then shows a success or error state.

Provider auth model:

| Provider | Auth | Notes |
|---|---|---|
| Anthropic | API key (`sk-ant-…`) | cloud |
| OpenAI | API key (`sk-…`) | cloud |
| Google | API key | cloud |
| Ollama | none | local; just enable |

### ESC = interrupt

`Esc` no longer quits. Its behavior is contextual:

- **Running a task or agent call:** `Esc` cancels it, shows a brief
  `⏹ interrupted` notice, and returns to the prompt.
- **Idle with text typed:** `Esc` clears the input line.
- **Idle, empty input:** `Esc` does nothing (no accidental exit).
- **Quit** moves to `Ctrl+C` and `/quit`.

Hint copy across all modes replaces "esc exit" with "esc interrupt" and
surfaces the new quit affordance, for example
`executing · test_generator running · esc interrupt · ^C quit`.

Clean interruption depends on an engine change (`@needs-human`): `run_plan` and
`run_design_plan` must cancel cooperatively so an asyncio cancel yields a
finalized `aborted` / `interrupted` run artifact, not a corrupt one. The TUI
interrupt is built only after that engine work lands.

### First-run setup wizard (primary deliverable)

On first launch after install (no config, no auth), or via `/model`, a
full-screen, multi-step, keyboard-driven wizard configures which model and
provider each pipeline agent uses. It is the canonical first-run flow on a TTY;
the Rich `run_init_wizard` is the non-TTY fallback. Both write through
`config.py` helpers and `security/keys.py`, so the engine contract is
unchanged.

The wizard chrome is the compact banner plus a `setup  step N of M` header rail
and a footer hint line.

**Step 1, Welcome.** The large banner, one line about what setup does, and
"Press Enter to begin."

**Step 2, Auth method and providers.** Choose the auth method (`API key` or
`subscription`), then enable and authenticate providers. Each provider row
shows a checkbox, a status badge (`[configured]`, `[not set]`, `[local]`), and
its available models. Key entry reuses the `/login` masked-field pattern.
`space` toggles, `enter` configures, `tab` advances, `esc` goes back. Footer
note: at least one cloud provider is required for AI-powered stages; Ollama
handles any tier if a local model is running.

Subscription login is the one `@needs-human` item: only Anthropic and OpenAI
plausibly have a safe official flow, so v1 ships API key end-to-end and shows
`subscription` as a visible-but-disabled "coming soon" option until an official
OAuth or device-code flow is chosen.

**Step 3, Tier defaults.** A table of the five capability tiers with columns
`tier`, `provider`, `model`, and `token cost`. `↑/↓` selects a tier, `←/→`
changes its model. Writes `model_tiers`.

| Tier | Default provider / model |
|---|---|
| `nano` | anthropic / claude-haiku-4-5 |
| `small` | anthropic / claude-haiku-4-5 |
| `medium` | anthropic / claude-sonnet-4-5 |
| `large` | anthropic / claude-sonnet-4-5 |
| `reasoning` | openai / o3-mini |

**Step 4, Per-agent overrides (advanced, optional).** A table of the four
model-calling agents. Each row shows its resolved model (from its tier) and
lets the user pin a specific provider/model just for that agent; untouched rows
stay on their tier default. `space` pins, `enter` picks, deterministic agents
are greyed out. Writes `stages[agent]`.

| Agent (stage) | Default tier | What it does |
|---|---|---|
| `design_conversation` | medium | Drives the design chat and crystallizes the plan |
| `plan_critic` | large | Adversarially reviews the crystallized plan |
| `code_writer` | medium | Writes and edits real source files |
| `test_generator` | medium | Generates tests |

The deterministic agents (`static_analyzer`, `security_auditor`,
`test_runner`) use no model and never appear in the wizard.

**Step 5, Summary and save.** A resolved view of every agent and its effective
provider/model, pinned versus tier-default visually distinguished. Save writes
the config and keychain entries, then the wizard lands in the conversation
screen.

## Stage Progress

`StageTracker` lives in `flug/ui/progress.py` and renders pipeline progress on
the Rich fallback path. In the TUI, the same stage states drive the breadcrumb
rail and live run block via the view model. Stages move through six states,
each with a fixed glyph and color:

| State | Glyph | Style |
|---|---|---|
| pending | `·` | flug.muted |
| running | `✈` | flug.info |
| success | `✓` | flug.success |
| failed | `✗` | flug.error |
| skipped | `↩` | flug.warn |
| retry | `↻` | flug.warn |

The UI never decides what counts as success or failure; the engine does, and
both UI layers render the state they are handed.

## Non-TTY Behavior

When stdout is not a terminal, the CLI uses the Rich layer, which strips ANSI
codes and collapses layout gracefully. The CLI checks `is_tty` before starting
any animated display so CI runners do not produce flicker or wasted bytes.
Plain-text fallbacks (`BANNER`, `COMPACT`, the ASCII glyph tokens) live beside
their styled counterparts; CI smoke tests assert against the plain constants.

## Testing Strategy

- Keep all decision logic in `flug/tui/view.py` and `WizardState` so it is unit
  tested without a terminal. The `flug/tui/app.py` render glue is hand-verified
  on a TTY and stays out of CI assertions.
- For the Rich path, use `Console(record=True)` via `reset_console_for_tests`
  and assert key substrings (panel titles, glyphs, command names) rather than
  exact ANSI escapes.
- Assert that the icon motif (`◉═◉`, `✈`, `╭ ╮ ╰ ╯`) appears in every size
  variant. This is the canary that prevents drift between sizes.
- Assert the ASCII/NO_COLOR fallbacks resolve correctly from `theme.py`.
- Add responsive layout tests at 60x20, 80x24, 100x30, and 120x40 so the
  narrow/wide split and the sub-60 fallback stay correct.
- Validate that no key material leaks into rendered output by feeding the UI
  fake `AuthStatus` objects with secret-looking strings and asserting they
  never appear in recorded output.

## What This Layer Does Not Do

- It does not run async pipeline tasks (it observes them via callbacks).
- It does not call providers.
- It does not read or write secrets directly; it triggers
  `flug/security/keys.py` and renders only `AuthStatus`.
- It does not make planning or provider-selection decisions.
- It does not own engine cancellation semantics; ESC-interrupt depends on the
  engine making `run_plan` cancellation-cooperative first.

## Future Front Ends

The same engine should be drivable by:

- The `pyratatui` TUI, the native default front end (this doc).
- The Rich layer, the non-TTY and fallback front end.
- A TypeScript wrapper that embeds the Python core via subprocess or RPC,
  mapping the same glyphs into a Node renderer.
- A web dashboard reading from SQLite directly (per
  `docs/02-storage-and-memory.md`) with the same visual identity in CSS.
- A headless SDK consumer that ignores rendering and reads structured outputs
  (the `--json` event stream).

Because all rendering goes through `flug/tui/` or `flug/ui/`, none of these
front ends require engine changes. The icon, the palette, the glyph set, and
the layer rules are the contract.

## Decision Record: Terminal UI via pyratatui

Decision (2026-05-28, supersedes the same-day Rust-crate plan below): the main
rich interactive front end is a Python TUI built on `pyratatui`, which is
Python bindings for ratatui (PyO3 plus Maturin, shipped as a prebuilt
native-extension wheel). The TUI is written in Python and the Rust ratatui
engine does the rendering underneath.

Why this over a separate Rust crate talking to the engine over IPC: a Python
TUI lives in the same process as the engine, so it imports the engine directly
and consumes the existing `on_stage_event` and `StageRecord` callbacks in
process. No subprocess, no JSON wire, no protocol to design, and no
Rust/asyncio bridge. The engine is asyncio first, and pyratatui supports async,
so the TUI integrates in the same event loop.

Layer rules still hold. The TUI is a UI subpackage (`flug/tui/`) that consumes
the engine API. The engine stays UI agnostic. The rule that "only `flug/ui/`
imports `rich`" gains a sibling: only `flug/tui/` imports `pyratatui`, and the
engine imports neither.

Packaging: pyratatui is a core dependency because the TUI is the main UI. The
old `flug[tui]` extra may remain as a compatibility alias, but normal
`pip install flug` installs the TUI dependency. Rich remains the fallback for
non-TTY output, the `--json` path, and unusual platform dependency failures.

Verified before committing to this path (2026-05-28): prebuilt `abi3` wheels
exist for macOS arm64 and Linux x86_64 (manylinux2014), so no Rust toolchain is
needed to install on dev machines, WSL, or CI. Latest version at decision time
was 0.2.8.

Caveats recorded with the decision:

- pyratatui is young (pre 1.0, pinned to ratatui 0.30). Pin the version and
  accept some API churn risk for a core UI dependency.
- This is not a standalone Rust binary and not a Rust codebase. It gives
  ratatui rendering from Python. If a standalone native binary ever becomes a
  goal, that is a separate decision.
- The Rich UI in `flug/ui/` remains the fallback front end and the basis for
  scripted output.

### Superseded: Rust crate over IPC

The earlier same-day plan was a separate Rust crate talking to the engine over
a machine-readable protocol, since a Rust process cannot import the Python
engine. Two candidate shapes were recorded: subprocess plus a JSON-line event
stream (`flug run --json`), and a long-lived JSON-RPC daemon over stdio. This
path is shelved for the TUI in favor of pyratatui. The JSON event protocol
(`flug run --json`) is still worth building independently, since an MCP server,
an IDE extension, a web dashboard, and scripting would all consume it. It is
simply no longer on the TUI critical path.

## The Pitch

> Flug's terminal UI is intentionally separable: a Dr. Flug-inspired icon and a
> native pyratatui front end (with a Rich fallback) on top of an engine that has
> no opinions about how it is rendered. The same pipeline runs unchanged behind
> the TUI today, a non-TTY script path now, and a TypeScript or web front end
> later.
