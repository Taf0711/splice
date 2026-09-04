// Package store implements the observations SQLite store for splice-memd.
// It is adapted from Gentleman-Programming/engram (MIT) with per-agent
// owner_agent and visibility columns added; cloud sync, embeddings, and the
// MCP tool surface are omitted.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// ErrNotFound is returned when an operation targets an observation ID that
// does not exist.
var ErrNotFound = errors.New("store: observation not found")

// sqliteCoder is implemented by modernc.org/sqlite's error type.
type sqliteCoder interface{ Code() int }

// sqliteBusy is SQLITE_BUSY, returned when a competing connection holds the
// lock a statement needs.
const sqliteBusy = 5

// isBusyLocked reports whether err is a wrapped SQLITE_BUSY error.
func isBusyLocked(err error) bool {
	var coded sqliteCoder
	return errors.As(err, &coded) && coded.Code()&0xff == sqliteBusy
}

// dedupeWindow is the rolling window for normalized-hash exact-dup detection.
const dedupeWindow = int64(3600) // seconds

// Observation is one persisted memory entry. Nullable fields use sql.Null* so
// the splice value ("") is distinguishable from SQL NULL.
type Observation struct {
	ID             int64
	ProjectPath    sql.NullString
	Scope          string // project | global | personal
	OwnerAgent     string
	Visibility     string // private | shareable
	MemoryType     string
	Title          string
	Content        string
	TopicKey       sql.NullString
	NormalizedHash sql.NullString
	SourceRunID    sql.NullString
	SourceStage    sql.NullString
	SourceBranch   sql.NullString // Track L
	SourceCommit   sql.NullString // Track L
	Pinned         bool
	Confidence     sql.NullFloat64
	RevisionCount  int
	DuplicateCount int
	ReviewAfter    sql.NullInt64
	CreatedAt      int64
	UpdatedAt      int64
	DeletedAt      sql.NullInt64
}

// Query describes a memory search request from one agent.
type Query struct {
	ProjectPath      string
	RequestingAgent  string
	QueryText        string
	Scopes           []string // project | global | personal
	IncludePrivate   bool     // include requesting agent's own private rows
	IncludeShareable bool     // include shareable rows from other agents
	MemoryTypes      []string // empty = all types
	Limit            int      // default 8
}

// Store wraps a SQLite database.
type Store struct {
	db *sql.DB
}

// New opens (or creates) a SQLite database at path and runs the schema
// migration. The caller must call Close when done.
func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store.New: open %s: %w", path, err)
	}
	// Single writer; WAL allows concurrent reads without blocking, and
	// busy_timeout lets a second process back off instead of failing instantly.
	db.SetMaxOpenConns(1)
	// Set busy_timeout before switching to WAL: the WAL switch takes an
	// exclusive lock that a concurrent cold opener may hold, and without a
	// timeout in place first the competing opener fails immediately with
	// SQLITE_BUSY instead of waiting for the lock to free. All three pragmas
	// run on the same connection in one Exec so the timeout is in effect on
	// the connection that performs the WAL switch.
	//
	// modernc's WAL switch does not engage the busy handler (it returns
	// SQLITE_BUSY immediately when another connection holds the lock), so a
	// short bounded retry on the busy code covers the concurrent cold-start
	// case the timeout alone cannot. Non-busy errors fail immediately.
	const (
		walPragma   = "PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"
		walRetries  = 10
		walRetryGap = 25 * time.Millisecond
	)
	var pragmaErr error
	for attempt := 0; attempt <= walRetries; attempt++ {
		if _, pragmaErr = db.Exec(walPragma); pragmaErr == nil {
			break
		}
		if !isBusyLocked(pragmaErr) || attempt == walRetries {
			db.Close()
			return nil, fmt.Errorf("store.New: pragma: %w", pragmaErr)
		}
		time.Sleep(walRetryGap)
	}
	if err := migrateDB(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store.New: migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database connection.
func (s *Store) Close() error { return s.db.Close() }

// DB returns the underlying database for direct queries.
func (s *Store) DB() *sql.DB { return s.db }

// migrateDB executes the DDL. CREATE TABLE/INDEX/TRIGGER IF NOT EXISTS makes
// this idempotent. Databases whose FTS table predates the UNINDEXED metadata
// columns are dropped and rebuilt from the content table; the sync triggers
// reference the same column names, so they carry over unchanged.
func migrateDB(db *sql.DB) error {
	rebuild, err := ftsNeedsRebuild(db)
	if err != nil {
		return fmt.Errorf("migrate: fts check: %w", err)
	}
	if rebuild {
		if _, err := db.Exec("DROP TABLE observations_fts"); err != nil {
			return fmt.Errorf("migrate: drop stale fts: %w", err)
		}
	}
	// run_traces predates the intent column. Add it before the FTS table is
	// created so content='run_traces' can resolve the column. Existing rows keep
	// NULL intent and never match the index (no backfill).
	if err := addTraceIntentColumn(db); err != nil {
		return fmt.Errorf("migrate: trace intent column: %w", err)
	}
	// The schema is split into statements so errors can name the failing DDL
	// fragment; splitSQL keeps trigger bodies (BEGIN...END) intact.
	for _, stmt := range splitSQL(ddl) {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w (stmt: %.80s)", err, stmt)
		}
	}
	if rebuild {
		if _, err := db.Exec(
			"INSERT INTO observations_fts(observations_fts) VALUES('rebuild')",
		); err != nil {
			return fmt.Errorf("migrate: fts rebuild: %w", err)
		}
	}
	return nil
}

// addTraceIntentColumn adds the intent column to a run_traces table created
// before the column existed. A fresh database has no run_traces table yet (the
// DDL creates it with the column), so it is a no-op there. Existing rows keep
// NULL intent and never match the FTS index (no backfill).
func addTraceIntentColumn(db *sql.DB) error {
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'run_traces'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	rows, err := db.Query(`PRAGMA table_info(run_traces)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var colName, colType string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if colName == "intent" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE run_traces ADD COLUMN intent TEXT`)
	return err
}

// ftsNeedsRebuild reports whether an existing observations_fts table predates
// the UNINDEXED metadata columns and must be recreated.
func ftsNeedsRebuild(db *sql.DB) (bool, error) {
	var sqlText string
	err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'observations_fts'`,
	).Scan(&sqlText)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // fresh database, ddl creates the current shape
	}
	if err != nil {
		return false, err
	}
	return !strings.Contains(strings.ToUpper(sqlText), "UNINDEXED"), nil
}

// splitSQL splits a SQL string into individual statements. It respects
// BEGIN...END blocks (used in trigger bodies) so that semicolons inside
// a trigger are not treated as statement boundaries.
func splitSQL(s string) []string {
	var stmts []string
	depth := 0 // BEGIN...END nesting depth
	start := 0

	upper := strings.ToUpper(s)
	n := len(s)

	for i := 0; i < n; {
		switch {
		case s[i] == ';' && depth == 0:
			if stmt := strings.TrimSpace(s[start:i]); stmt != "" {
				stmts = append(stmts, stmt)
			}
			start = i + 1
			i++
		case i+5 <= n && upper[i:i+5] == "BEGIN" && isWordEnd(s, i+5):
			depth++
			i += 5
		case i+3 <= n && upper[i:i+3] == "END" && isWordEnd(s, i+3) && depth > 0:
			depth--
			i += 3
		default:
			i++
		}
	}
	if stmt := strings.TrimSpace(s[start:]); stmt != "" {
		stmts = append(stmts, stmt)
	}
	return stmts
}

// isWordEnd returns true when position i is not followed by an identifier
// character, so "BEGIN" does not match inside "BEGINNERS".
func isWordEnd(s string, i int) bool {
	if i >= len(s) {
		return true
	}
	c := s[i]
	return !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '_')
}

// UpsertObservation persists obs, applying dedup and topic-key upsert logic:
//  1. If an identical normalized hash exists within dedupeWindow, bump
//     duplicate_count on the existing row and return it.
//  2. Else if topic_key is set and a live row matches (owner_agent, project,
//     scope, topic_key), update title/content/hash and bump revision_count.
//  3. Otherwise insert a new row.
func (s *Store) UpsertObservation(ctx context.Context, obs *Observation) (*Observation, error) {
	now := time.Now().Unix()
	hash := normalizeHash(obs.Title, obs.Content)
	obs.NormalizedHash = sql.NullString{String: hash, Valid: true}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("upsert: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// 1. Exact-dup check (rolling window).
	var dupID int64
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM observations
		WHERE normalized_hash = ?
		  AND ifnull(project_path, '') = ifnull(?, '')
		  AND scope        = ?
		  AND memory_type  = ?
		  AND title        = ?
		  AND owner_agent  = ?
		  AND deleted_at   IS NULL
		  AND created_at   > ?
		LIMIT 1
	`, hash, obs.ProjectPath, obs.Scope, obs.MemoryType,
		obs.Title, obs.OwnerAgent, now-dedupeWindow).Scan(&dupID)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		// A real lookup failure must not fall through to a fresh insert:
		// that would silently duplicate rows on transient errors.
		return nil, fmt.Errorf("upsert: dup lookup: %w", err)
	}
	if err == nil {
		if _, err := tx.ExecContext(ctx,
			`UPDATE observations SET duplicate_count = duplicate_count + 1 WHERE id = ?`, dupID,
		); err != nil {
			return nil, fmt.Errorf("upsert: dup bump: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("upsert: dup commit: %w", err)
		}
		return s.byID(ctx, dupID)
	}

	// 2. Topic-key upsert.
	if obs.TopicKey.Valid && obs.TopicKey.String != "" {
		var topicID int64
		err = tx.QueryRowContext(ctx, `
			SELECT id FROM observations
			WHERE topic_key   = ?
			  AND ifnull(project_path, '') = ifnull(?, '')
			  AND scope       = ?
			  AND owner_agent = ?
			  AND deleted_at  IS NULL
			ORDER BY updated_at DESC
			LIMIT 1
		`, obs.TopicKey.String, obs.ProjectPath, obs.Scope, obs.OwnerAgent).Scan(&topicID)

		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("upsert: topic lookup: %w", err)
		}
		if err == nil {
			// Latest write wins for visibility, confidence, and provenance,
			// so a revision can promote private -> shareable. memory_type and
			// pinned are deliberately left untouched: type is part of the
			// row's identity (the dedupe key), and pinned is a curation flag.
			if _, err := tx.ExecContext(ctx, `
				UPDATE observations
				   SET title = ?, content = ?, normalized_hash = ?,
				       visibility = ?, confidence = ?,
				       source_run_id = ?, source_stage = ?,
				       source_branch = ?, source_commit = ?,
				       updated_at = ?, revision_count = revision_count + 1
				 WHERE id = ?
			`, obs.Title, obs.Content, hash,
				obs.Visibility, obs.Confidence,
				obs.SourceRunID, obs.SourceStage,
				obs.SourceBranch, obs.SourceCommit,
				now, topicID); err != nil {
				return nil, fmt.Errorf("upsert: topic update: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return nil, fmt.Errorf("upsert: topic commit: %w", err)
			}
			return s.byID(ctx, topicID)
		}
	}

	// 3. Insert new row.
	res, err := tx.ExecContext(ctx, `
		INSERT INTO observations (
			project_path, scope, owner_agent, visibility, memory_type,
			title, content, topic_key, normalized_hash,
			source_run_id, source_stage, source_branch, source_commit,
			pinned, confidence, revision_count, duplicate_count,
			review_after, created_at, updated_at
		) VALUES (
			?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, 1, 0,
			?, ?, ?
		)
	`,
		obs.ProjectPath, obs.Scope, obs.OwnerAgent, obs.Visibility, obs.MemoryType,
		obs.Title, obs.Content, obs.TopicKey, obs.NormalizedHash,
		obs.SourceRunID, obs.SourceStage, obs.SourceBranch, obs.SourceCommit,
		boolToInt(obs.Pinned), obs.Confidence,
		obs.ReviewAfter, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert: insert: %w", err)
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("upsert: insert commit: %w", err)
	}

	obs.ID = id
	obs.CreatedAt = now
	obs.UpdatedAt = now
	obs.RevisionCount = 1
	obs.DuplicateCount = 0
	return obs, nil
}

// LookupTopic returns live observations matching an exact topic key within
// one project scope. It is the deterministic direct-lookup path (C0): no
// full-text search, no new table, and it uses the existing idx_obs_topic
// index (topic_key, project_path, scope). Ordering is stable: created_at
// DESC with id DESC as the tiebreak, so repeated lookups return the same
// roster. Visibility mirrors Search: the requesting agent's own rows (any
// visibility) plus shareable rows from any agent; the admission layer above
// still applies project/duplicate/review policy to whatever this returns.
// Returns (observations, truncated, error); an empty result is not an error.
func (s *Store) LookupTopic(ctx context.Context, projectPath, requestingAgent, scope, topicKey string, limit int) ([]*Observation, bool, error) {
	if scope == "" || topicKey == "" {
		return nil, false, nil
	}
	if limit <= 0 {
		limit = 8
	}
	args := []any{topicKey, scope, requestingAgent, limit + 1}
	projectClause := "o.project_path IS NULL"
	if projectPath != "" {
		projectClause = "(o.project_path = ? OR o.project_path IS NULL)"
		args = []any{topicKey, scope, projectPath, requestingAgent, limit + 1}
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT o.id, o.project_path, o.scope, o.owner_agent, o.visibility,
		       o.memory_type, o.title, o.content, o.topic_key, o.normalized_hash,
		       o.source_run_id, o.source_stage, o.source_branch, o.source_commit,
		       o.pinned, o.confidence, o.revision_count, o.duplicate_count,
		       o.review_after, o.created_at, o.updated_at, o.deleted_at
		FROM observations AS o
		WHERE o.topic_key = ?
		  AND o.scope = ?
		  AND %s
		  AND o.deleted_at IS NULL
		  AND (o.owner_agent = ? OR o.visibility = 'shareable')
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT ?
	`, projectClause), args...)
	if err != nil {
		return nil, false, fmt.Errorf("lookup_topic: query: %w", err)
	}
	defer rows.Close()

	var results []*Observation
	for rows.Next() {
		obs, err := scanObs(rows)
		if err != nil {
			return nil, false, fmt.Errorf("lookup_topic: scan: %w", err)
		}
		results = append(results, obs)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(results) > limit
	if truncated {
		results = results[:limit]
	}
	return results, truncated, nil
}

// Search returns observations matching q using FTS5 BM25, filtered by
// project, scope, owner, and visibility:
//
//   - Project isolation: a query with a project path sees that project's rows
//     plus project-less rows; a query without one sees only project-less rows.
//   - IncludePrivate admits the requesting agent's own rows (any visibility).
//   - IncludeShareable admits shareable rows from any agent.
//   - Neither flag set returns an empty result without querying.
//
// Returns (observations, truncated, error) where truncated is true if the
// result set was capped by the limit.
func (s *Store) Search(ctx context.Context, q *Query) ([]*Observation, bool, error) {
	obs, truncated, _, err := s.SearchRanked(ctx, q)
	return obs, truncated, err
}

// SearchRanked is Search plus the FTS BM25 rank per result (negative; more
// negative = more relevant). ranks[i] corresponds to observations[i]. The
// rank lets the orchestrator rerank candidates deterministically (report
// section 28) without re-deriving BM25.
func (s *Store) SearchRanked(ctx context.Context, q *Query) ([]*Observation, bool, []float64, error) {
	if q.QueryText == "" || len(q.Scopes) == 0 {
		return nil, false, nil, nil
	}
	if !q.IncludePrivate && !q.IncludeShareable {
		return nil, false, nil, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 8
	}

	ftsQ := sanitizeFTSQuery(q.QueryText)
	if ftsQ == "" {
		return nil, false, nil, nil
	}
	args := []any{ftsQ}
	scopeClause, memTypeClause, projectClause, visClause := buildFilterClauses(q, &args)

	args = append(args, limit+1) // LIMIT+1 to detect truncation

	// rank is BM25 from FTS5 (negative; more negative = more relevant).
	// No floor filter: FTS5 only returns rows that matched the pattern, and
	// with small corpora BM25 scores can be close to 0 but still valid.
	// The LIMIT caps results; the caller can apply quality thresholds above.
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT o.id, o.project_path, o.scope, o.owner_agent, o.visibility,
		       o.memory_type, o.title, o.content, o.topic_key, o.normalized_hash,
		       o.source_run_id, o.source_stage, o.source_branch, o.source_commit,
		       o.pinned, o.confidence, o.revision_count, o.duplicate_count,
		       o.review_after, o.created_at, o.updated_at, o.deleted_at, fts.rank
		FROM observations AS o
		JOIN (
			SELECT rowid, rank
			FROM observations_fts
			WHERE observations_fts MATCH ?
		) AS fts ON fts.rowid = o.id
		WHERE o.deleted_at IS NULL
		  %s
		  %s
		  %s
		  %s
		ORDER BY fts.rank
		LIMIT ?
	`, scopeClause, memTypeClause, projectClause, visClause), args...)
	if err != nil {
		return nil, false, nil, fmt.Errorf("search: query: %w", err)
	}
	defer rows.Close()

	var results []*Observation
	var ranks []float64
	for rows.Next() {
		obs, rank, err := scanObsRanked(rows)
		if err != nil {
			return nil, false, nil, fmt.Errorf("search: scan: %w", err)
		}
		results = append(results, obs)
		ranks = append(ranks, rank)
	}
	if err := rows.Err(); err != nil {
		return nil, false, nil, err
	}
	// Truncated if we got limit+1 rows (the extra row indicates more results).
	truncated := len(results) > limit
	if truncated {
		results = results[:limit]
		ranks = ranks[:limit]
	}
	return results, truncated, ranks, nil
}

// Recent returns the most recently updated observations matching q's filters.
// It does not inspect the full-text index, so an empty QueryText is valid.
func (s *Store) Recent(ctx context.Context, q *Query) ([]*Observation, bool, error) {
	if len(q.Scopes) == 0 {
		return nil, false, nil
	}
	if !q.IncludePrivate && !q.IncludeShareable {
		return nil, false, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 8
	}

	args := make([]any, 0, len(q.Scopes)+len(q.MemoryTypes)+2)
	scopeClause, memTypeClause, projectClause, visClause := buildFilterClauses(q, &args)
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT o.id, o.project_path, o.scope, o.owner_agent, o.visibility,
		       o.memory_type, o.title, o.content, o.topic_key, o.normalized_hash,
		       o.source_run_id, o.source_stage, o.source_branch, o.source_commit,
		       o.pinned, o.confidence, o.revision_count, o.duplicate_count,
		       o.review_after, o.created_at, o.updated_at, o.deleted_at
		FROM observations AS o
		WHERE o.deleted_at IS NULL
		  %s
		  %s
		  %s
		  %s
		ORDER BY o.updated_at DESC, o.created_at DESC, o.id DESC
		LIMIT ?
	`, scopeClause, memTypeClause, projectClause, visClause), args...)
	if err != nil {
		return nil, false, fmt.Errorf("recent: query: %w", err)
	}
	defer rows.Close()

	var results []*Observation
	for rows.Next() {
		obs, err := scanObs(rows)
		if err != nil {
			return nil, false, fmt.Errorf("recent: scan: %w", err)
		}
		results = append(results, obs)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(results) > limit
	if truncated {
		results = results[:limit]
	}
	return results, truncated, nil
}

// buildFilterClauses appends the arguments for the shared scope, type,
// project-isolation, and visibility predicates in placeholder order.
func buildFilterClauses(q *Query, args *[]any) (scopeClause, memTypeClause, projectClause, visClause string) {
	scopeHolders := make([]string, len(q.Scopes))
	for i, sc := range q.Scopes {
		scopeHolders[i] = "?"
		*args = append(*args, sc)
	}
	scopeClause = fmt.Sprintf("AND o.scope IN (%s)", strings.Join(scopeHolders, ", "))

	if len(q.MemoryTypes) > 0 {
		holders := make([]string, len(q.MemoryTypes))
		for i, mt := range q.MemoryTypes {
			holders[i] = "?"
			*args = append(*args, mt)
		}
		memTypeClause = fmt.Sprintf("AND o.memory_type IN (%s)", strings.Join(holders, ", "))
	}

	projectClause = "AND o.project_path IS NULL"
	if q.ProjectPath != "" {
		projectClause = "AND (o.project_path = ? OR o.project_path IS NULL)"
		*args = append(*args, q.ProjectPath)
	}

	var visParts []string
	if q.IncludePrivate {
		visParts = append(visParts, "o.owner_agent = ?")
		*args = append(*args, q.RequestingAgent)
	}
	if q.IncludeShareable {
		visParts = append(visParts, "o.visibility = 'shareable'")
	}
	visClause = "AND (" + strings.Join(visParts, " OR ") + ")"
	return scopeClause, memTypeClause, projectClause, visClause
}

// MarkReviewed clears the review_after field on the given observation.
// Returns ErrNotFound when no row has that id.
func (s *Store) MarkReviewed(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE observations SET review_after = NULL WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetProject hard-deletes every observation and every run trace whose
// project path or repo root matches projectPath, including the FTS index
// rows (the delete triggers keep observations_fts and run_traces_fts in
// sync). The match is exact: no prefix, no normalization. This is the
// eval-harness isolation primitive: it restores one project's memory state
// to empty so a fresh seed is the only cognition the project carries.
// Counts report what was removed, never a silent zero.
func (s *Store) ResetProject(ctx context.Context, projectPath string) (ResetCounts, error) {
	if projectPath == "" {
		return ResetCounts{}, errors.New("reset project: project_path is required")
	}
	var counts ResetCounts
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return counts, fmt.Errorf("reset project: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	obsRes, err := tx.ExecContext(ctx,
		`DELETE FROM observations WHERE project_path = ?`, projectPath)
	if err != nil {
		return counts, fmt.Errorf("reset project: delete observations: %w", err)
	}
	observations, err := obsRes.RowsAffected()
	if err != nil {
		return counts, fmt.Errorf("reset project: count observations: %w", err)
	}

	traceRes, err := tx.ExecContext(ctx,
		`DELETE FROM run_traces WHERE repo_root = ?`, projectPath)
	if err != nil {
		return counts, fmt.Errorf("reset project: delete traces: %w", err)
	}
	traces, err := traceRes.RowsAffected()
	if err != nil {
		return counts, fmt.Errorf("reset project: count traces: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return counts, fmt.Errorf("reset project: commit: %w", err)
	}
	counts.Observations = observations
	counts.Traces = traces
	return counts, nil
}

// ResetCounts reports what one ResetProject removed.
type ResetCounts struct {
	Observations int64 `json:"observations"`
	Traces       int64 `json:"traces"`
}

// byID fetches one observation by primary key. Used after upsert.
func (s *Store) byID(ctx context.Context, id int64) (*Observation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, project_path, scope, owner_agent, visibility,
		       memory_type, title, content, topic_key, normalized_hash,
		       source_run_id, source_stage, source_branch, source_commit,
		       pinned, confidence, revision_count, duplicate_count,
		       review_after, created_at, updated_at, deleted_at
		FROM observations WHERE id = ?
	`, id)
	return scanObs(row)
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanObs scans one observation row in the fixed column order used by byID and
// Search/Recent: 22 columns (pinned stored as INTEGER, converted to bool).
func scanObs(sc scanner) (*Observation, error) {
	var obs Observation
	var pinned int64
	err := sc.Scan(
		&obs.ID, &obs.ProjectPath, &obs.Scope, &obs.OwnerAgent, &obs.Visibility,
		&obs.MemoryType, &obs.Title, &obs.Content, &obs.TopicKey, &obs.NormalizedHash,
		&obs.SourceRunID, &obs.SourceStage, &obs.SourceBranch, &obs.SourceCommit,
		&pinned, &obs.Confidence, &obs.RevisionCount, &obs.DuplicateCount,
		&obs.ReviewAfter, &obs.CreatedAt, &obs.UpdatedAt, &obs.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	obs.Pinned = pinned != 0
	return &obs, nil
}

// scanObsRanked scans an observation row that carries ONE extra trailing
// column (the FTS rank) beyond the fixed 22. Rows.Scan consumes every column
// in a single call, so the extra destination must ride in the same Scan.
func scanObsRanked(sc scanner) (*Observation, float64, error) {
	var obs Observation
	var pinned int64
	var rank float64
	err := sc.Scan(
		&obs.ID, &obs.ProjectPath, &obs.Scope, &obs.OwnerAgent, &obs.Visibility,
		&obs.MemoryType, &obs.Title, &obs.Content, &obs.TopicKey, &obs.NormalizedHash,
		&obs.SourceRunID, &obs.SourceStage, &obs.SourceBranch, &obs.SourceCommit,
		&pinned, &obs.Confidence, &obs.RevisionCount, &obs.DuplicateCount,
		&obs.ReviewAfter, &obs.CreatedAt, &obs.UpdatedAt, &obs.DeletedAt,
		&rank,
	)
	if err != nil {
		return nil, 0, err
	}
	obs.Pinned = pinned != 0
	return &obs, rank, nil
}

// normalizeHash computes a content fingerprint for exact-dup detection. Lower-
// cases, splits by whitespace, rejoins, then sha256s. Matches Engram's approach.
func normalizeHash(title, content string) string {
	words := strings.Fields(strings.ToLower(title + " " + content))
	h := sha256.Sum256([]byte(strings.Join(words, " ")))
	return fmt.Sprintf("%x", h)
}

// sanitizeFTSQuery converts free text into a quoted OR-joined FTS5 query,
// matching Engram's sanitization. Each word is double-quoted so special FTS5
// syntax characters are treated literally.
func sanitizeFTSQuery(q string) string {
	words := strings.Fields(q)
	if len(words) == 0 {
		return ""
	}
	parts := make([]string, len(words))
	for i, w := range words {
		w = strings.ReplaceAll(w, `"`, `""`) // escape internal double quotes
		parts[i] = `"` + w + `"`
	}
	return strings.Join(parts, " OR ")
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
