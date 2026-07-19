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
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("store.New: pragma: %w", err)
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
	if q.QueryText == "" || len(q.Scopes) == 0 {
		return nil, false, nil
	}
	if !q.IncludePrivate && !q.IncludeShareable {
		return nil, false, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 8
	}

	ftsQ := sanitizeFTSQuery(q.QueryText)
	if ftsQ == "" {
		return nil, false, nil
	}

	// Build scope IN placeholders.
	scopeHolders := make([]string, len(q.Scopes))
	args := []any{ftsQ}
	for i, sc := range q.Scopes {
		scopeHolders[i] = "?"
		args = append(args, sc)
	}

	// Optionally filter by memory_type.
	memTypeClause := ""
	if len(q.MemoryTypes) > 0 {
		holders := make([]string, len(q.MemoryTypes))
		for i, mt := range q.MemoryTypes {
			holders[i] = "?"
			args = append(args, mt)
		}
		memTypeClause = fmt.Sprintf("AND o.memory_type IN (%s)", strings.Join(holders, ", "))
	}

	projectClause := "AND o.project_path IS NULL"
	if q.ProjectPath != "" {
		projectClause = "AND (o.project_path = ? OR o.project_path IS NULL)"
		args = append(args, q.ProjectPath)
	}

	var visParts []string
	if q.IncludePrivate {
		visParts = append(visParts, "o.owner_agent = ?")
		args = append(args, q.RequestingAgent)
	}
	if q.IncludeShareable {
		visParts = append(visParts, "o.visibility = 'shareable'")
	}
	visClause := "AND (" + strings.Join(visParts, " OR ") + ")"

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
		       o.review_after, o.created_at, o.updated_at, o.deleted_at
		FROM observations AS o
		JOIN (
			SELECT rowid, rank
			FROM observations_fts
			WHERE observations_fts MATCH ?
		) AS fts ON fts.rowid = o.id
		WHERE o.deleted_at IS NULL
		  AND o.scope IN (%s)
		  %s
		  %s
		  %s
		ORDER BY fts.rank
		LIMIT ?
	`, strings.Join(scopeHolders, ", "), memTypeClause, projectClause, visClause), args...)
	if err != nil {
		return nil, false, fmt.Errorf("search: query: %w", err)
	}
	defer rows.Close()

	var results []*Observation
	for rows.Next() {
		obs, err := scanObs(rows)
		if err != nil {
			return nil, false, fmt.Errorf("search: scan: %w", err)
		}
		results = append(results, obs)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	// Truncated if we got limit+1 rows (the extra row indicates more results).
	truncated := len(results) > limit
	if truncated {
		results = results[:limit]
	}
	return results, truncated, nil
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
// Search. pinned is stored as INTEGER (0/1); we convert to bool here.
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
