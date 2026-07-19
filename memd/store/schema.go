package store

// ddl is the DDL for the observations store. All statements are idempotent
// (IF NOT EXISTS / CREATE INDEX IF NOT EXISTS), so migrate() is safe to call
// on an existing database.
//
// The four metadata columns in observations_fts are UNINDEXED: they ride along
// for completeness but must not be matchable, otherwise query terms like
// "private" or "code_writer" hit every row and pollute BM25 ranking. Databases
// created before this change are rebuilt by migrateDB.
const ddl = `
CREATE TABLE IF NOT EXISTS observations (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    project_path     TEXT,
    scope            TEXT    NOT NULL DEFAULT 'project',
    owner_agent      TEXT    NOT NULL,
    visibility       TEXT    NOT NULL DEFAULT 'private',
    memory_type      TEXT    NOT NULL,
    title            TEXT    NOT NULL,
    content          TEXT    NOT NULL,
    topic_key        TEXT,
    normalized_hash  TEXT,
    source_run_id    TEXT,
    source_stage     TEXT,
    source_branch    TEXT,
    source_commit    TEXT,
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
CREATE INDEX IF NOT EXISTS idx_obs_owner   ON observations(owner_agent, project_path, scope);

CREATE VIRTUAL TABLE IF NOT EXISTS observations_fts USING fts5(
    title,
    content,
    memory_type UNINDEXED,
    project_path UNINDEXED,
    owner_agent UNINDEXED,
    visibility UNINDEXED,
    content='observations',
    content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS obs_fts_insert AFTER INSERT ON observations BEGIN
    INSERT INTO observations_fts(
        rowid, title, content, memory_type, project_path, owner_agent, visibility
    ) VALUES (
        new.id, new.title, new.content, new.memory_type,
        new.project_path, new.owner_agent, new.visibility
    );
END;

CREATE TRIGGER IF NOT EXISTS obs_fts_delete AFTER DELETE ON observations BEGIN
    INSERT INTO observations_fts(
        observations_fts, rowid,
        title, content, memory_type, project_path, owner_agent, visibility
    ) VALUES (
        'delete', old.id, old.title, old.content, old.memory_type,
        old.project_path, old.owner_agent, old.visibility
    );
END;

CREATE TRIGGER IF NOT EXISTS obs_fts_update AFTER UPDATE ON observations BEGIN
    INSERT INTO observations_fts(
        observations_fts, rowid,
        title, content, memory_type, project_path, owner_agent, visibility
    ) VALUES (
        'delete', old.id, old.title, old.content, old.memory_type,
        old.project_path, old.owner_agent, old.visibility
    );
    INSERT INTO observations_fts(
        rowid, title, content, memory_type, project_path, owner_agent, visibility
    ) VALUES (
        new.id, new.title, new.content, new.memory_type,
        new.project_path, new.owner_agent, new.visibility
    );
END;
`
