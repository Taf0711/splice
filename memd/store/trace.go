package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TraceRow is one run_traces row. Payload is the full RunOutcome JSON; the
// indexed columns let /trace/query filter without parsing the payload.
type TraceRow struct {
	RunID     string
	SessionID string
	RepoRoot  string
	Tier      string
	Status    string
	Intent    string
	CreatedAt int64
	Payload   []byte
}

// VerdictRow is one verdicts row. Payload is the full VerdictRecord JSON.
type VerdictRow struct {
	RunID     string
	DecidedAt int64
	Verdict   string
	Reason    string
	Payload   []byte
}

// TraceFilter is the /trace/query filter. Zero-valued fields are ignored.
type TraceFilter struct {
	RepoRoot string
	Tier     string
	Status   string
	Verdict  string // e.g. "kept": filter to runs whose latest verdict matches
	Query    string // FTS match over intent; empty = no full-text filter
	Since    int64  // created_at >= Since (unix seconds); 0 = no bound
	Limit    int    // default 100
}

// TraceWithVerdict is a trace joined with its latest verdict. Verdict is nil
// when no verdict has been recorded (unknown). Rank is the FTS bm25 score when
// a Query filter was supplied (more negative = more relevant), else 0.
type TraceWithVerdict struct {
	Trace   TraceRow
	Verdict *VerdictRow
	Rank    float64
}

// UpsertTrace inserts a trace. run_traces is write-once: a duplicate run_id is
// an idempotent no-op, never an update, so the first payload is authoritative.
func (s *Store) UpsertTrace(ctx context.Context, row *TraceRow) (inserted bool, err error) {
	if row.RunID == "" {
		return false, errors.New("trace run_id is required")
	}
	if row.CreatedAt == 0 {
		row.CreatedAt = time.Now().Unix()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO run_traces (run_id, session_id, repo_root, tier, status, intent, created_at, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id) DO NOTHING
	`, row.RunID, nullIfEmpty(row.SessionID), row.RepoRoot, row.Tier, row.Status,
		nullIfEmpty(row.Intent), row.CreatedAt, row.Payload)
	if err != nil {
		return false, fmt.Errorf("upsert trace: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("upsert trace: %w", err)
	}
	return n > 0, nil
}

// UpsertVerdict appends a verdict. Verdicts are append-only; the effective
// verdict for a run is the latest row (max decided_at) at query time.
func (s *Store) UpsertVerdict(ctx context.Context, row *VerdictRow) error {
	if row.RunID == "" {
		return errors.New("verdict run_id is required")
	}
	if row.DecidedAt == 0 {
		row.DecidedAt = time.Now().Unix()
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO verdicts (run_id, decided_at, verdict, reason, payload)
		VALUES (?, ?, ?, ?, ?)
	`, row.RunID, row.DecidedAt, row.Verdict, nullIfEmpty(row.Reason), row.Payload); err != nil {
		return fmt.Errorf("upsert verdict: %w", err)
	}
	return nil
}

// QueryTraces returns traces matching the filter, each joined with its latest
// verdict, ordered by FTS rank (when Query is set) or newest first. A Verdict
// filter switches the join to inner so only runs whose latest verdict matches
// are returned.
func (s *Store) QueryTraces(ctx context.Context, filter TraceFilter) ([]TraceWithVerdict, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}

	// verdictJoin is the latest-verdict subquery, LEFT or INNER depending on
	// whether the caller filters on verdict.
	joinKind := "LEFT"
	if filter.Verdict != "" {
		joinKind = "INNER"
	}
	verdictJoin := fmt.Sprintf(`%s JOIN (
			SELECT v2.run_id, v2.decided_at, v2.verdict, v2.reason, v2.payload
			FROM verdicts AS v2
			JOIN (
				SELECT run_id, MAX(decided_at) AS max_dt FROM verdicts GROUP BY run_id
			) AS m ON v2.run_id = m.run_id AND v2.decided_at = m.max_dt
		) AS v ON v.run_id = t.run_id`, joinKind)

	where := "WHERE 1=1"
	args := []any{}
	if filter.RepoRoot != "" {
		where += " AND t.repo_root = ?"
		args = append(args, filter.RepoRoot)
	}
	if filter.Tier != "" {
		where += " AND t.tier = ?"
		args = append(args, filter.Tier)
	}
	if filter.Status != "" {
		where += " AND t.status = ?"
		args = append(args, filter.Status)
	}
	if filter.Verdict != "" {
		where += " AND v.verdict = ?"
		args = append(args, filter.Verdict)
	}
	if filter.Since > 0 {
		where += " AND t.created_at >= ?"
		args = append(args, filter.Since)
	}

	var orderBy string
	rankCol := "NULL"
	if strings.TrimSpace(filter.Query) != "" {
		orderBy = "ORDER BY f.rank"
		rankCol = "f.rank"
	} else {
		orderBy = "ORDER BY t.created_at DESC"
	}

	var fromClause string
	if strings.TrimSpace(filter.Query) != "" {
		ftsQ := sanitizeFTSQuery(filter.Query)
		if ftsQ == "" {
			return nil, nil
		}
		fromClause = fmt.Sprintf(`FROM run_traces AS t
		JOIN (SELECT rowid, rank FROM run_traces_fts WHERE run_traces_fts MATCH ?) AS f ON f.rowid = t.rowid
		%s`, verdictJoin)
		args = append([]any{ftsQ}, args...)
	} else {
		fromClause = fmt.Sprintf(`FROM run_traces AS t
		%s`, verdictJoin)
	}

	args = append(args, limit)

	query := fmt.Sprintf(`
		SELECT t.run_id, t.session_id, t.repo_root, t.tier, t.status, t.created_at, t.payload,
		       %s,
		       v.decided_at, v.verdict, v.reason, v.payload
		%s
		%s
		%s
		LIMIT ?
	`, rankCol, fromClause, where, orderBy)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query traces: %w", err)
	}
	defer rows.Close()

	var results []TraceWithVerdict
	for rows.Next() {
		var out TraceWithVerdict
		var sessionID sql.NullString
		var rank sql.NullFloat64
		var decidedAt sql.NullInt64
		var verdict sql.NullString
		var reason sql.NullString
		var verdictPayload []byte
		if err := rows.Scan(
			&out.Trace.RunID, &sessionID, &out.Trace.RepoRoot, &out.Trace.Tier,
			&out.Trace.Status, &out.Trace.CreatedAt, &out.Trace.Payload,
			&rank,
			&decidedAt, &verdict, &reason, &verdictPayload,
		); err != nil {
			return nil, fmt.Errorf("query traces: scan: %w", err)
		}
		out.Trace.SessionID = sessionID.String
		if rank.Valid {
			out.Rank = rank.Float64
		}
		if verdict.Valid {
			out.Verdict = &VerdictRow{
				RunID:     out.Trace.RunID,
				DecidedAt: decidedAt.Int64,
				Verdict:   verdict.String,
				Reason:    reason.String,
				Payload:   verdictPayload,
			}
		}
		results = append(results, out)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query traces: %w", err)
	}
	return results, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
