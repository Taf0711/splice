# Storage and Memory

## The Right Model for a CLI Tool

Local-first. No server, no daemon, no external dependencies. Everything lives on the user's disk in inspectable formats.

Three storage layers:

| Layer | Form | Purpose |
|---|---|---|
| In-memory | Pydantic model instances | Live state during the active pipeline run |
| SQLite | `flug.db` | Fast queries (runs, stages, cache, eval results) |
| Filesystem | JSON, JSONL, markdown | Human-inspectable artifacts (per-run traces, context, configs) |

SQLite specifically because it ships in Python's stdlib (zero install friction), is a single file (easy to back up or delete), supports JSON columns natively since 3.38, and is fast enough that pipeline I/O is invisible.

## Filesystem Layout

```
~/.config/flug/
  config.toml                      # API providers, default models, preferences

~/.local/share/flug/                # macOS: ~/Library/Application Support/flug/
  flug.db                          # SQLite: runs, stages, cache, eval results
  runs/
    2026-05-08T10-23-45_abc123/
      trace.jsonl                  # per-stage event stream
      input.json                   # original user request
      output.json                  # final pipeline result
      diff.patch                   # if code was modified
      snapshot.git/                # shadow git dir for per-iteration snapshots
      work/                        # transient run-local scratch space
      stages/
        01-orchestrator.json
        02-requirements.json
        ...
  cache/
    blobs/                         # large outputs spilled out of SQLite
  learnings.md                     # global cross-project memory

./.flug/                            # in-repo, optional, like .git
  context.md                       # project-specific working memory
  config.toml                      # project overrides for model/agent config
  evals/                           # project-specific golden benchmarks
```

This follows three principles:

1. **Config in `~/.config/`** following XDG Base Directory spec
2. **Mutable state in `~/.local/share/`** so config can be version-controlled separately
3. **Project state in `./.flug/`** so it lives with the code and follows the user when they switch machines

## SQLite Schema

```sql
CREATE TABLE runs (
    id TEXT PRIMARY KEY,             -- UUID
    started_at INTEGER NOT NULL,     -- unix timestamp
    completed_at INTEGER,
    status TEXT NOT NULL,            -- 'running', 'completed', 'failed', 'aborted'
    user_request TEXT,
    project_path TEXT,
    pipeline_tier TEXT,              -- 'trivial', 'light', 'standard', 'substantial', 'architectural'
    total_tokens_input INTEGER,
    total_tokens_output INTEGER,
    total_tokens_cached INTEGER,
    total_tokens_cache_write INTEGER,
    total_cost_usd REAL,
    final_output JSON,
    abort_reason TEXT
);

CREATE TABLE stages (
    run_id TEXT REFERENCES runs(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    iteration INTEGER NOT NULL DEFAULT 0,
    stage_name TEXT NOT NULL,
    provider TEXT,
    model TEXT,
    input JSON NOT NULL,
    output JSON NOT NULL,
    output_summary TEXT,             -- compressed form for downstream context
    tokens_in INTEGER,
    tokens_out INTEGER,
    tokens_cached INTEGER,
    tokens_cache_write INTEGER,
    cost_usd REAL,
    latency_ms INTEGER,
    confidence REAL,
    cache_hit INTEGER DEFAULT 0,
    started_at INTEGER,
    completed_at INTEGER,
    PRIMARY KEY (run_id, sequence, iteration)
);

CREATE TABLE cache (
    key TEXT PRIMARY KEY,            -- sha256(canonical_input + model + agent_version)
    value JSON,                      -- inline if small
    blob_path TEXT,                  -- path if large (>4KB)
    summary TEXT,                    -- the downstream summary form
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    hit_count INTEGER DEFAULT 0
);

CREATE TABLE iteration_state (
    run_id TEXT REFERENCES runs(id) ON DELETE CASCADE,
    iteration INTEGER NOT NULL,
    state_hash TEXT NOT NULL,        -- sha256 of all modified files
    score REAL,                      -- progress score
    tests_passing INTEGER,
    tests_failing INTEGER,
    lint_issues INTEGER,
    security_issues INTEGER,
    snapshot_sha TEXT,               -- git commit sha in snapshot.git
    PRIMARY KEY (run_id, iteration)
);

CREATE INDEX idx_runs_status ON runs(status);
CREATE INDEX idx_stages_run ON stages(run_id, sequence);
CREATE INDEX idx_stages_output_gin ON stages USING gin(output);  -- if SQLite supports it; else use json1 functions
CREATE INDEX idx_cache_expires ON cache(expires_at);
```

The `idx_stages_output_gin` index lets you slice runs by outcome in eval mode:

```sql
SELECT run_id FROM stages
WHERE stage_name = 'security_auditor'
  AND json_extract(output, '$.vulnerabilities[0].severity') = 'critical';
```

## Memory Tiers

For an agent system, "memory" is three different things, each stored differently.

### Tier 1: Working Memory (current run)
Pydantic objects in process RAM. Dies when the run ends. This is the fastest and cheapest layer.

### Tier 2: Run History (past runs)
Every completed run writes to two places simultaneously:

- **SQLite (`flug.db`)** for fast structured queries
- **JSONL trace (`runs/<id>/trace.jsonl`)** for human inspection

The reason for both: SQLite for `flug runs --where 'security.severity = critical'`, JSONL for `cat ~/.local/share/flug/runs/abc123/trace.jsonl | jq` when debugging. Different jobs, both cheap.

Each JSONL line is a structured event:

```json
{"event": "stage_start", "stage": "code_writer", "ts": 1715180625, "iteration": 0}
{"event": "llm_call", "model": "claude-sonnet-4-5", "tokens_in": 1240, "tokens_out": 380, "cost_usd": 0.0095}
{"event": "stage_complete", "stage": "code_writer", "confidence": 0.91, "latency_ms": 4200}
{"event": "trajectory_check", "score": 14, "decision": "continue"}
```

### Tier 3: Persistent Learnings (cross-run memory)

Two markdown files. This is the secret sauce that turns Flug from a stateless tool into something that gets smarter the more you use it.

**`./.flug/context.md`**: project-specific learnings, scoped to one repo:

```markdown
# Project Context

## Codebase Conventions
- Uses pytest with fixtures, not unittest
- All async functions, no sync DB calls
- Imports are absolute, never relative
- Type hints required, mypy strict mode

## Known Patterns
- Auth flows go through `app/auth/middleware.py`
- DB queries use the repository pattern in `app/repos/`

## Avoid
- Don't add new dependencies without checking pyproject.toml
- Team rejected Pydantic v1 → v2 migration last sprint
```

**`~/.local/share/flug/learnings.md`**: global, cross-project:

```markdown
# Global Learnings

- User prefers concise commit messages, no body unless necessary
- Default to TypeScript strict mode in new projects
```

After every pipeline run, a tiny "memory updater" agent (nano tier, ~200 tokens) decides whether anything from this run is worth appending. Most runs append nothing. The few that do build up a knowledge base over time.

This pattern works because:
- The user can read and edit it directly with any text editor
- It version-controls cleanly with the repo
- It's portable: clone the repo, get the context for free
- It's debuggable: when an agent does something weird, grep the file to figure out why
- It's bounded: a "summarizer" agent compacts it when it grows past a token threshold

These two files get prepended to the orchestrator's prompt at the start of every run. The whole pipeline operates with project context loaded.

**Planned upgrade, not yet built (recorded 2026-06-23):** the two-shared-file design above is in tension with a principle decided for the design phase (`docs/03-design-phase.md`) and meant to generalize to every agent eventually: each agent should get its own separated persistent memory, not a shared pool. LLMs are non-deterministic, and one shared blob read by every agent risks one agent's noisy or wrong context bleeding into another's reasoning, compounding hallucination risk. This is Commitment 1/3 (information minimalism) extended from per-call context to persistent memory. Cross-agent sharing, when needed, must be a deliberate, bounded, harness-mediated retrieval (the same pull-channel pattern as `CodebaseTools`/`ContextRequest`), never an automatic shared prepend. Mechanism recommendation: reuse the bare-repo-plus-`--work-tree`-flag pattern already proven below for shadow-git snapshots, rather than literal `git worktree` checkouts, so each agent gets versioned, diffable, git-native memory without a permanently checked-out directory per agent. Project-local vs. global scope (mirroring the `context.md`/`learnings.md` split above) is an open question. Not scheduled before Week 4 checkpoint 4F; see `MEMORY.md`'s 2026-06-23 "foundational ripple effects" entry.

## Cache Storage

The cache table in SQLite handles small outputs. For larger payloads (full code files, long security reports), spill to `cache/blobs/<hash>` and store just the path:

```python
async def cache_get(key: str) -> dict | None:
    row = db.execute(
        "SELECT value, blob_path, summary, expires_at FROM cache WHERE key = ?",
        (key,)
    ).fetchone()
    if not row or row["expires_at"] < time.time():
        return None
    if row["blob_path"]:
        with open(BLOBS_DIR / row["blob_path"], "r") as f:
            return json.load(f)
    return json.loads(row["value"])
```

For TTLs and eviction, run `DELETE FROM cache WHERE expires_at < ?` at startup. Don't bother with background workers for a CLI tool.

Cache hit rates by stage on typical workloads:

| Stage | Cache hit rate |
|---|---|
| Code Writer | <5% (creative work) |
| Static Analyzer | 30-40% (similar code patterns) |
| Test Generator | 25-35% (common patterns) |
| Security Auditor | 40-50% (boilerplate is repeated) |
| Plan Critic | 10-20% (designs are usually novel) |

Aggregate cache savings are typically 15-25% of total tokens once the cache is warm.

## Snapshot and Rollback (the Shadow Git Dir)

Flug now edits the user's project in place, with their normal git repository as the first undo net. The per-run snapshot system must therefore avoid creating a second work tree in `runs/<id>/work/` and must never mutate the user's `.git` directory.

Each run creates a shadow git directory at `runs/<id>/snapshot.git/` and binds it to the real project as the work tree when recording an iteration:

```bash
git --git-dir ~/.local/share/flug/runs/<id>/snapshot.git \
    --work-tree /path/to/project \
    add -A -- .

git --git-dir ~/.local/share/flug/runs/<id>/snapshot.git \
    --work-tree /path/to/project \
    commit --allow-empty -m "iter 1: after code writer"
```

This gives each run its own inspectable git history of attempted states without touching the user's repository metadata. The shadow repo can snapshot modified and untracked project files while leaving the user's index exactly as it was.

This design is conceptually related to ShadowGit's local shadow-history model for AI-assisted coding, but it is an internal Flug primitive rather than an external app, MCP server, or Node dependency. See `docs/09-agent-harness-principles.md` for the external reference.

Checkpoint 2A implements snapshot creation. Checkpoint 2E1 adds the restore primitive: Flug resolves a target iteration, creates a pre-rollback backup commit in the shadow repo, then restores files from the target commit through the shadow git index. User-facing CLI commands land separately in 2E2. Rollback restores from Flug's shadow history deliberately and visibly. It must not run `git reset` against the user's repo.

## Why No Postgres

Postgres was in the original design. Removed because:
- A CLI tool that requires running a database is dead on arrival
- SQLite handles all the same query patterns at this scale
- Single-file storage is portable, deletable, and inspectable
- No server to keep running, no auth to configure, no migrations to coordinate

The tradeoff is multi-machine state. We don't have it. If users want multi-machine sync, they version-control `.flug/context.md` and `.flug/evals/`. The mutable state in `~/.local/share/flug/` is intentionally per-machine.

## Why No Redis

Redis was in the original design as a semantic cache. Removed because SQLite handles this fine at the scales a CLI tool sees (hundreds to low thousands of cache entries). The eviction story is simpler too: a single SQL delete on startup vs managing a separate process.

If a power user generates millions of cache entries, the cache table is one of the things that can be migrated to a real KV store. Until then, KISS.

## Optional Remote Tracing

Users who want Langfuse, OpenTelemetry, or similar can opt in via config:

```toml
# ~/.config/flug/config.toml
[telemetry]
langfuse_enabled = false
langfuse_host = ""
langfuse_public_key = ""

[telemetry.otel]
enabled = false
endpoint = ""
```

Default is fully off. Nothing leaves the machine unless explicitly enabled. This is a feature, not a constraint, for engineers in regulated environments.

## What This Looks Like to the User

```bash
$ flug "fix the rate limiter bug"
▸ ... pipeline runs ...
✓ Pipeline complete. Trace at: ~/.local/share/flug/runs/abc123/

$ flug runs                         # list recent runs
$ flug inspect abc123                # pretty-print the trace
$ flug diff abc123 --iter 1 2        # show what changed between iterations
$ flug rollback abc123 --to-iter 2   # restore an earlier state
$ flug cost --period 7d              # show spend by provider/stage
$ flug evals run                     # run the eval harness
```

All of these read from the same SQLite + filesystem layout. No magic.
