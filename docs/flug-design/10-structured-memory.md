# Structured Per-Agent Memory

## Status

Architecture decided. Go sidecar chosen. Track M1-M7 are implemented. Track M-R sidecar audit remediation is implemented through M-R3, with binary-gated E2E validation still pending.

Splice (Track F-Zero, F9) note: in the Go port, the sidecar is rebranded. The binary is
`splice-memd`, the module path is `github.com/Taf0711/splice/memd`, the env vars are
`SPLICE_MEMD_SOCKET` / `SPLICE_MEMD_DB` / `SPLICE_MEMD_BIN`, and the data directory is
`~/Library/Application Support/splice` on macOS or `$XDG_DATA_HOME/splice` (fallback
`~/.local/share/splice`) elsewhere. The Go client lives in `internal/memd` and speaks
HTTP over the Unix socket. The development-checkout cwd fallback (`memd/splice-memd`
relative to the current directory) was removed for trust-boundary reasons; developers
export `SPLICE_MEMD_BIN` or add the binary to PATH. The `flug-memd` /
`FLUG_MEMD_*` names below are the archived Python-era behavior.

Splice audit-remediation AR4 note: the sidecar now creates private storage on first
run. On Unix the data directory is created or tightened to `0700`, the SQLite database
file and any WAL/SHM companions are restricted to `0600`, and the Unix domain socket
is created under the same `0700` directory and chmodded to `0600`. Windows uses the
user-private data directory and inherited ACLs; the Unix-specific permission helpers
are no-ops there.

Splice audit-remediation AR7 note: consuming stages (Code Writer and Test Generator)
now receive bounded `SelectedMemory` entries in their typed input structs. The
orchestrator query scopes retrieval to `project` and `global`, and private visibility
remains constrained to the requesting agent. Observations are truncated to 500 runes and
capped at five per stage, so prompt growth stays bounded.

The M0 decision gate is closed: Flug uses a Go memory sidecar (`flug-memd`) backed by
pure-Go SQLite/FTS5. The original Python-first recommendation was overridden because
write-separation is worth more than implementation simplicity: `memory.db` must be owned
by exactly one writer to avoid `aiosqlite`/Go SQLite contention, and a dedicated binary
achieves that cleanly without adding a daemon or required network port.

Reverse-engineering notes (schema, triggers, upsert, dedupe SQL, and fidelity corrections from Engram's MIT source): `flug/knowledge/engram-reverse-engineering.md`.

## Why This Exists

Flug originally planned two markdown memory files:

- `./.flug/context.md` for project-local context
- `~/.local/share/flug/learnings.md` for global learnings

That design is simple and inspectable, but it has a dangerous failure mode: one shared
memory blob that every agent reads. That conflicts with information minimalism (Commitment
3). A Code Writer, Plan Critic, Security Auditor, and Test Generator should not all
receive the same persistent memory by default.

Engram points at a better shape: local SQLite plus FTS5 search over structured memory
observations, with topic-key upserts, scopes, session summaries, review lifecycle, and
progressive disclosure. Flug borrows the data model and retrieval discipline, not Engram's
product shape.

External comparison (agentic-cli, see `docs/09-agent-harness-principles.md`,
Reference model G): agentic-cli backs its knowledge base and semantic memory
with embeddings and a FAISS vector store, blending BM25 and vector hits via
reciprocal-rank-fusion, and adds contradiction detection plus forgetting
policies. Flug's Track M sidecar is deliberately the leaner, deterministic
counterpart: BM25 over SQLite FTS5 with topic-key upserts and rolling-window
dedupe, local-first, with no required embedding service. agentic-cli's
contradiction and forgetting behavior is a useful reference for the deferred
nano-tier memory updater (M8) if Flug ever needs active reconciliation.

## Core Thesis

```text
Engram: one brain for many external tools, shared pool, agent-direct mem_* calls
Flug:   one local memory store, per-agent namespaces, orchestrator-mediated retrieval
```

The orchestrator owns memory retrieval and persistence. Agents do not query or write memory
directly. Each agent receives only a bounded `MemoryBundle` selected for its stage, owner
namespace, visibility, scope, and current task.

## Non-Negotiable Constraints

1. **Local-first.** No required server, cloud account, daemon, or external service.
2. **Provider-agnostic.** Memory cannot depend on a particular model provider.
3. **Orchestrator-as-foreman.** Agents do not call memory tools directly.
4. **Information minimalism.** No automatic shared memory pool visible to all agents.
5. **Typed boundaries.** Memory retrieved into stage inputs must be Pydantic schemas, not
   raw dicts.
6. **Deterministic-first.** Storage, dedupe, retrieval, ranking limits, and freshness
   checks must be deterministic before any LLM memory updater exists.
7. **Inspectable.** A user must be able to inspect and export the memory store.
8. **Failure is non-fatal.** Sidecar absent or crashed means a memory-less run, same
   graceful-degrade posture as Bandit-absent. Never aborts a run.

## Go Sidecar Architecture (`flug-memd`)

### Binary

- Module path: `github.com/Taf0711/flug/memd` in a `memd/` subdirectory (monorepo, same
  precedent as the `tui/` Python subpackage decision from 2026-05-28).
- Pure-Go SQLite: `modernc.org/sqlite` (no CGO, prebuilt for macOS arm64, Linux x86_64,
  Windows amd64). FTS5 is supported.
- Store core forked and adapted from Engram's MIT `internal/store`, stripped of
  cloud/sync/relations/embeddings/obsidian, with `owner_agent` and `visibility` added as
  net-new columns Engram does not have.
- Data at `~/.local/share/flug/memory.db` (macOS: `~/Library/Application Support/flug/memory.db`),
  owned solely by the sidecar. Keeps `memory.db` off the main `flug.db` to eliminate any
  write-contention between the Python `aiosqlite` caller and Go writes.

### Protocol

Persistent HTTP server over a Unix domain socket (`~/.local/share/flug/mem.sock`,
`$XDG_DATA_HOME/flug/mem.sock` when XDG is set on non-macOS systems, or
`~/Library/Application Support/flug/mem.sock` on macOS). The server starts once
on first use and persists across runs and sessions (daemon lifecycle). No required
network port.

Five endpoints (all POST/GET over HTTP/1.1 on the Unix socket):

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/upsert` | Persist or update one observation (topic-key upsert + hash dedupe) |
| `POST` | `/search` | BM25 FTS5 search, returns bounded `MemoryBundle` |
| `POST` | `/mark_reviewed` | Mark an observation reviewed by ID |
| `GET` | `/stats` | Return row counts and DB size |
| `GET` | `/health` | Health check (returns `{"ok": true}`) |

The Python client (`flug/storage/memory.py` `SidecarMemoryStore`) auto-starts the server
on first use via `asyncio.create_subprocess_exec`. It passes `FLUG_MEMD_SOCKET` to the
child process so custom socket paths propagate, starts the daemon in a new session so a
terminal Ctrl-C does not kill it, and polls the socket up to 2 seconds. If the server
fails to start, the client degrades to no-op. An `asyncio.Lock` guards double-start races
in concurrent callers. When the binary is absent, the store is no-op from construction
(same graceful-degrade posture as Bandit-absent). Binary resolution order for the
Splice Go client is explicit `SPLICE_MEMD_BIN`, then `splice-memd` on PATH, then
disabled (empty string). There is no current-working-directory fallback; opening an
arbitrary project directory must never auto-execute a repository-provided sidecar.

Protocol defaults: omitted `/search` include flags default to true, so private memory for
the requesting agent and shareable memory are both eligible unless the caller opts out.
`POST /mark_reviewed` returns 404 for an unknown ID. `GET /stats` includes
`db_size_bytes` computed from SQLite page count and page size.

### What Was Rejected from Engram

- Cloud sync, Postgres, dashboard, autosync
- Obsidian export and sync chunks
- LLM semantic-conflict relations graph
- The 20-tool MCP surface as a core engine mechanism
- Agent-direct `mem_*` tool calls (violates Commitment 1/3)

Engram's Go choice is for its MCP/HTTP/cloud surfaces. Flug's Go choice is for
write-separation and no-CGO binary distribution. Both choices are correct for different
reasons.

## Go Store Schema

Adapted from Engram's MIT schema. Additions marked `-- FLUG ADD`.

```sql
CREATE TABLE IF NOT EXISTS observations (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    project_path     TEXT,
    scope            TEXT NOT NULL DEFAULT 'project',   -- project|global|personal
    owner_agent      TEXT NOT NULL,                     -- FLUG ADD
    visibility       TEXT NOT NULL DEFAULT 'private',   -- FLUG ADD: private|shareable
    memory_type      TEXT NOT NULL,
    title            TEXT NOT NULL,
    content          TEXT NOT NULL,
    topic_key        TEXT,
    normalized_hash  TEXT,
    source_run_id    TEXT,
    source_stage     TEXT,
    source_branch    TEXT,                              -- FLUG ADD (Track L)
    source_commit    TEXT,                              -- FLUG ADD (Track L)
    pinned           INTEGER NOT NULL DEFAULT 0,
    confidence       REAL,
    revision_count   INTEGER NOT NULL DEFAULT 1,
    duplicate_count  INTEGER NOT NULL DEFAULT 0,
    review_after     INTEGER,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL,
    deleted_at       INTEGER
);

CREATE INDEX IF NOT EXISTS idx_obs_project ON observations(project_path, scope, deleted_at);
CREATE INDEX IF NOT EXISTS idx_obs_topic   ON observations(topic_key, project_path, scope);
CREATE INDEX IF NOT EXISTS idx_obs_hash    ON observations(normalized_hash, project_path, scope);
CREATE INDEX IF NOT EXISTS idx_obs_owner   ON observations(owner_agent, project_path, scope); -- FLUG ADD
```

### FTS5 External-Content Table

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS observations_fts USING fts5(
    title,
    content,
    memory_type UNINDEXED,
    project_path UNINDEXED,
    owner_agent UNINDEXED,    -- FLUG ADD
    visibility UNINDEXED,     -- FLUG ADD
    content='observations',
    content_rowid='id'
);
```

The metadata columns are `UNINDEXED`: they ride along for trigger compatibility and
inspection, but terms like `private`, `code_writer`, or a project path must not match or
pollute ranking. Databases created before the UNINDEXED change are rebuilt automatically
on first open.

**Three mandatory sync triggers** (carry over verbatim from Engram, extended with new
columns; the FTS index silently rots without them):

```sql
CREATE TRIGGER IF NOT EXISTS obs_fts_insert AFTER INSERT ON observations BEGIN
    INSERT INTO observations_fts(rowid, title, content, memory_type, project_path, owner_agent, visibility)
    VALUES (new.id, new.title, new.content, new.memory_type, new.project_path, new.owner_agent, new.visibility);
END;

CREATE TRIGGER IF NOT EXISTS obs_fts_delete AFTER DELETE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, title, content, memory_type, project_path, owner_agent, visibility)
    VALUES ('delete', old.id, old.title, old.content, old.memory_type, old.project_path, old.owner_agent, old.visibility);
END;

CREATE TRIGGER IF NOT EXISTS obs_fts_update AFTER UPDATE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, title, content, memory_type, project_path, owner_agent, visibility)
    VALUES ('delete', old.id, old.title, old.content, old.memory_type, old.project_path, old.owner_agent, old.visibility);
    INSERT INTO observations_fts(rowid, title, content, memory_type, project_path, owner_agent, visibility)
    VALUES (new.id, new.title, new.content, new.memory_type, new.project_path, new.owner_agent, new.visibility);
END;
```

### topic_key Upsert

```sql
SELECT * FROM observations
WHERE topic_key = ?
  AND ifnull(project_path, '') = ifnull(?, '')
  AND scope = ?
  AND owner_agent = ?       -- FLUG: per-agent namespace
  AND deleted_at IS NULL
ORDER BY updated_at DESC LIMIT 1
```

If found: latest write wins for `title`, `content`, `normalized_hash`, `visibility`,
`confidence`, and `source_*`, with `revision_count=revision_count+1`. `memory_type` and
`pinned` are deliberately not clobbered: type is part of the row identity and pinned is a
curation flag. If not found: `INSERT`.

### normalized_hash Dedupe

```sql
SELECT * FROM observations
WHERE normalized_hash = ?
  AND project_path = ?
  AND scope = ?
  AND memory_type = ?
  AND title = ?
  AND owner_agent = ?       -- FLUG: per-agent so different agents don't collapse
  AND deleted_at IS NULL
  AND created_at > (unixepoch() - ?)   -- rolling dedupeWindow (e.g. 3600)
LIMIT 1
```

If found: bump `duplicate_count`, return the existing row. If not found: insert.

### BM25 Search

```sql
SELECT o.*
FROM observations AS o
JOIN (
    SELECT rowid, rank
    FROM observations_fts
    WHERE observations_fts MATCH ?
) AS fts ON fts.rowid = o.id
WHERE o.deleted_at IS NULL
  AND o.scope IN (?, ...)
  AND (o.project_path = ? OR o.project_path IS NULL) -- when project_path is set
  AND (o.owner_agent = ? OR o.visibility = 'shareable')
ORDER BY fts.rank
LIMIT ?
```

When `project_path` is not set, the project clause is `o.project_path IS NULL`. The
visibility clause is built from the include flags: `include_private` adds
`o.owner_agent = requesting_agent`, `include_shareable` adds
`o.visibility = 'shareable'`, and both disabled returns an empty result without querying.
There is no BM25 floor. Upstream Engram was verified on 2026-07-06 not to have one either;
the old `HAVING rank < -2.0` note was a reverse-engineering error.

## Python-Facing Schemas (`flug/schemas/memory.py`)

```python
class MemoryObservation(BaseModel):
    id: int = 0
    project_path: str | None = None
    scope: Literal["project", "global", "personal"] = "project"
    owner_agent: str
    visibility: Literal["private", "shareable"] = "private"
    memory_type: Literal[
        "decision", "architecture", "bugfix", "pattern",
        "discovery", "preference", "config", "session_summary",
    ]
    title: str
    content: str
    topic_key: str | None = None
    normalized_hash: str | None = None
    source_run_id: str | None = None
    source_stage: str | None = None
    source_branch: str | None = None   # Track L
    source_commit: str | None = None   # Track L
    pinned: bool = False
    confidence: float | None = None
    revision_count: int = 1
    duplicate_count: int = 0
    review_after: int | None = None
    created_at: int = 0
    updated_at: int = 0
    deleted_at: int | None = None

class MemoryQuery(BaseModel):
    project_path: str | None = None
    requesting_agent: str
    query: str
    scopes: list[Literal["project", "global", "personal"]] = Field(
        default_factory=lambda: ["project", "global"]
    )
    include_private: bool = True      # only the requesting_agent's own private rows
    include_shareable: bool = True
    memory_types: list[str] = Field(default_factory=list)
    limit: int = 8

class MemoryBundle(BaseModel):
    requesting_agent: str
    observations: list[MemoryObservation] = Field(default_factory=list)
    truncated: bool = False

class MemoryStore(Protocol):
    async def upsert_observation(self, obs: MemoryObservation) -> MemoryObservation: ...
    async def search(self, query: MemoryQuery) -> MemoryBundle: ...
    async def mark_reviewed(self, observation_id: int) -> None: ...
```

`HarnessStageInput` (`flug/schemas/pipeline.py`) gains `memory_bundle: MemoryBundle | None = None`,
injected at the `_run_pass` stage-input build site in `flug/orchestrator/runner.py`.

## Visibility and Owner Rules

### Owner Agent

Memory is namespaced by `owner_agent`. Examples: `code_writer`, `test_generator`,
`security_auditor`, `plan_critic`, `design_conversation`, `orchestrator`.

Private memory is visible only to the same owner agent, unless the orchestrator has an
explicit cross-agent retrieval rule (not built in v1).

### Visibility

- `private`: agent-specific memory. Example: Code Writer learned a retry gotcha for a
  particular patch format.
- `shareable`: project convention or decision that other agents may see if it matches
  their query and scope.

Shareable does not mean automatically prepended. It only means eligible for
orchestrator-mediated retrieval by a different agent.

## Retrieval Flow

```text
1. Planner builds ExecutionPlan.
2. Orchestrator prepares HarnessStageInput for one stage.
3. Orchestrator builds MemoryQuery from:
   - stage name (-> owner_agent)
   - bounded request intent (first 200 characters)
   - project_path

Target paths and prior deterministic summaries are future enrichment, not v1 query inputs.
4. Python client sends the search request to the sidecar server (HTTP POST /search).
5. Sidecar server returns compact matching observations (BM25, bounded).
6. Orchestrator wraps them into MemoryBundle, enforces limit.
7. Stage receives MemoryBundle as typed context in HarnessStageInput.
```

Agents never receive the user's full raw request through memory. Agents never receive
another agent's private memory by default.

## Write Flow

Order (deterministic-first):

1. Storage and retrieval APIs (M2, M3, M4, M5).
2. Deterministic writes from the orchestrator: config observations, discovered test
   command, tool degradation events (M7).
3. Only then a nano-tier Memory Updater agent (M8, deferred).

When the Memory Updater eventually exists, it emits typed candidates:

```python
class MemoryCandidate(BaseModel):
    owner_agent: str
    visibility: Literal["private", "shareable"]
    memory_type: str
    title: str
    content: str
    topic_key: str | None = None
    confidence: float
```

The orchestrator validates, dedupes via the sidecar, and persists. The updater does not
write directly.

## Checkpoint Tracks

Implementation lives in three parallel tracks. See `ROADMAP.md` for the green-to-green
checkpoint list.

### Track M: Memory Sidecar

M1: Go module scaffold in `memd/` + CI build.
M2: Fork and adapt Engram's store core (schema, triggers, upsert, dedupe, search).
M3: HTTP server over Unix domain socket in the binary (`--serve` flag, five endpoints).
M4: Python schemas `flug/schemas/memory.py` + `MemoryStore` Protocol.
M5: Python client `flug/storage/memory.py::SidecarMemoryStore` with no-op degrade.
M6: Orchestrator-mediated retrieval (stage-input injection, owner isolation tests).
M7: Deterministic writes (config, test-command discovery, tool degradation).
M8: Nano-tier memory updater (deferred).

Track M-R: sidecar audit remediation (`plans/memd-audit-remediation-2026-07-06.md`).
M-R1: Store correctness: project isolation, include flags, UNINDEXED FTS metadata,
zero-hit arrays, topic metadata refresh, stats DB size, mark_reviewed 404s.
M-R2: Go/Python socket path parity, spawned-daemon `FLUG_MEMD_SOCKET`, daemon session
detach, and PATH fallback for `flug-memd`.
M-R3: Daemon single-instance guard, stale-socket cleanup, socket mode 0600,
SQLite busy timeout, and this documentation reconciliation.
E2E gate: binary-gated integration validation still pending.

### Track W: Git-Worktree Execution

See `docs/11-execution-worktrees.md`. Supersedes `flug/storage/snapshot.py`.

### Daemon Lifecycle

`flug run` now auto-starts `flug-memd --serve` on first memory use. The process binds to
the same socket path computed by the Python client and persists across runs and sessions.
It is NOT spawned per-session: one daemon services all concurrent and subsequent `flug`
invocations until it is killed manually or the system restarts. Concurrent auto-starts are
benign: a second daemon dials an existing live socket, logs that another instance is
serving it, and exits cleanly. A stale socket is removed only after the dial check fails.
The socket is chmodded to `0600`, SQLite uses `busy_timeout=5000`, and the Python client
starts the daemon in a separate process session so terminal Ctrl-C does not kill it. After
a Flug upgrade, an already-running daemon keeps serving the old binary until killed.
Users who want to reset memory can `rm -f ~/.local/share/flug/{memory.db,mem.sock}` and
restart.

### Track L: Commit Provenance

L1: Add `source_branch`/`source_commit` to `MemoryObservation` and tag deterministic
writes with the agent commit SHA. Links intra-run git context to cross-run memory.
Depends on M4 (Python schema) and W2 (worktrees producing real commits).
