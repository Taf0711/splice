package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// MaintenanceReport describes one bounded maintenance pass.
type MaintenanceReport struct {
	Skipped                 bool             `json:"skipped"`
	Disabled                bool             `json:"disabled"`
	MissingProjects         int64            `json:"missing_projects"`
	ArchivedNodes           int64            `json:"archived_nodes"`
	RestoredNodes           int64            `json:"restored_nodes"`
	DeletedObservations     int64            `json:"deleted_observations"`
	RestoredObservations    int64            `json:"restored_observations"`
	Compacted               CompactionReport `json:"compacted"`
	Collected               int64            `json:"collected"`
	HardDeletedNodes        int64            `json:"hard_deleted_nodes"`
	HardDeletedObservations int64            `json:"hard_deleted_observations"`
	HardDeletedTraces       int64            `json:"hard_deleted_traces"`
	Vacuumed                bool             `json:"vacuumed"`
}

// Maintenance runs at most once per 24 hours. It archives records for
// missing projects, compacts and collects the graph, then removes old data.
func (s *Store) Maintenance(ctx context.Context) (MaintenanceReport, error) {
	return s.MaintenanceAt(ctx, s.currentTime())
}

// MaintenanceAt is Maintenance with an explicit clock for deterministic tests.
func (s *Store) MaintenanceAt(ctx context.Context, now time.Time) (MaintenanceReport, error) {
	var report MaintenanceReport
	if os.Getenv("SPLICE_MEMD_MAINTENANCE") == "0" {
		report.Disabled = true
		return report, nil
	}
	last, err := s.maintenanceLast(ctx)
	if err != nil {
		return report, err
	}
	if last > 0 && now.Unix()-last < int64(24*time.Hour/time.Second) {
		report.Skipped = true
		return report, nil
	}
	if err := s.setMaintenanceLast(ctx, now.Unix()); err != nil {
		return report, err
	}

	paths, err := s.projectPaths(ctx)
	if err != nil {
		return report, err
	}
	var missingProjects []string
	for _, project := range paths {
		if !filepath.IsAbs(project) {
			continue
		}
		missing, statErr := projectMissing(project)
		if statErr != nil {
			continue
		}
		if !missing {
			if err := s.clearMissingSince(ctx, project); err != nil {
				return report, err
			}
			n, o, restoreErr := s.restoreProject(ctx, project, now.Unix())
			if restoreErr != nil {
				return report, restoreErr
			}
			report.RestoredNodes += n
			report.RestoredObservations += o
			continue
		}
		report.MissingProjects++
		missingProjects = append(missingProjects, project)
		if err := s.recordMissingSince(ctx, project, now.Unix()); err != nil {
			return report, err
		}
		n, o, archiveErr := s.archiveProject(ctx, project, now.Unix())
		if archiveErr != nil {
			return report, archiveErr
		}
		report.ArchivedNodes += n
		report.DeletedObservations += o
	}

	report.Compacted, err = s.Compact(ctx)
	if err != nil {
		return report, err
	}
	report.Collected, err = s.collectMissing(ctx, 7*24*time.Hour, missingProjects, now)
	if err != nil {
		return report, err
	}
	cutoff := now.Add(-30 * 24 * time.Hour).Unix()
	report.HardDeletedNodes, err = s.hardDeleteNodes(ctx, cutoff, now.Unix(), missingProjects)
	if err != nil {
		return report, err
	}
	report.HardDeletedObservations, err = s.hardDeleteObservations(ctx, cutoff)
	if err != nil {
		return report, err
	}
	report.HardDeletedTraces, err = s.hardDeleteTraces(ctx, missingProjects, cutoff)
	if err != nil {
		return report, err
	}
	if report.HardDeletedNodes+report.HardDeletedObservations+report.HardDeletedTraces > 0 {
		if _, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return report, fmt.Errorf("maintenance wal checkpoint: %w", err)
		}
		var pages, freelist int64
		if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
			return report, err
		}
		if err := s.db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&freelist); err != nil {
			return report, err
		}
		if pages > 0 && freelist*4 > pages {
			if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
				return report, fmt.Errorf("maintenance vacuum: %w", err)
			}
			report.Vacuumed = true
		}
	}
	return report, nil
}

func (s *Store) collectMissing(ctx context.Context, olderThan time.Duration, projects []string, now time.Time) (int64, error) {
	cutoff := now.Add(-olderThan).Unix()
	var collected int64
	for _, project := range projects {
		res, err := s.db.ExecContext(ctx, `DELETE FROM cognition_nodes WHERE project_path = ? AND status = ? AND (verified_at IS NULL OR verified_at < ?) AND id NOT IN (SELECT src_id FROM cognition_edges) AND id NOT IN (SELECT dst_id FROM cognition_edges) AND id NOT IN (SELECT node_id FROM cognition_evidence)`, project, NodeStatusEphemeral, cutoff)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		collected += n
	}
	if collected > 0 {
		recordCollection(collected)
	}
	return collected, nil
}

func projectMissing(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return false, nil
	}
	if os.IsNotExist(err) {
		return true, nil
	}
	return false, err
}

func (s *Store) projectPaths(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_path FROM cognition_nodes WHERE project_path IS NOT NULL
		UNION SELECT project_path FROM observations WHERE project_path IS NOT NULL
		UNION SELECT repo_root FROM run_traces WHERE repo_root <> ''
		UNION SELECT substr(key, length('missing_since:') + 1) FROM maintenance_meta WHERE key LIKE 'missing_since:%'`)
	if err != nil {
		return nil, fmt.Errorf("maintenance projects: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func maintenanceMetadata(raw sql.NullString, status string) (string, error) {
	meta := map[string]any{}
	if raw.Valid && raw.String != "" {
		if err := json.Unmarshal([]byte(raw.String), &meta); err != nil {
			return "", err
		}
		if meta == nil {
			meta = map[string]any{}
		}
	}
	meta["maintenance_previous_status"] = status
	meta["maintenance_archived"] = true
	b, err := json.Marshal(meta)
	return string(b), err
}

func (s *Store) archiveProject(ctx context.Context, project string, now int64) (int64, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, status, metadata_json FROM cognition_nodes WHERE project_path = ? AND status IN (?, ?, ?)`, project, NodeStatusActive, NodeStatusStale, NodeStatusEphemeral)
	if err != nil {
		return 0, 0, err
	}
	type item struct {
		id     int64
		status string
		meta   sql.NullString
	}
	var nodes []item
	for rows.Next() {
		var x item
		if err := rows.Scan(&x.id, &x.status, &x.meta); err != nil {
			rows.Close()
			return 0, 0, err
		}
		nodes = append(nodes, x)
	}
	rows.Close()
	for _, n := range nodes {
		meta, err := maintenanceMetadata(n.meta, n.status)
		if err != nil {
			return 0, 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cognition_nodes SET status = ?, status_changed_at = ?, metadata_json = ? WHERE id = ?`, NodeStatusArchived, now, meta, n.id); err != nil {
			return 0, 0, err
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE observations SET deleted_at = ?, maintenance_deleted_at = ? WHERE project_path = ? AND deleted_at IS NULL`, now, now, project)
	if err != nil {
		return 0, 0, err
	}
	obs, err := res.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return int64(len(nodes)), obs, nil
}

func (s *Store) restoreProject(ctx context.Context, project string, now int64) (int64, int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, metadata_json FROM cognition_nodes WHERE project_path = ? AND status = ?`, project, NodeStatusArchived)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	type item struct {
		id   int64
		meta sql.NullString
	}
	var nodes []item
	for rows.Next() {
		var x item
		if err := rows.Scan(&x.id, &x.meta); err != nil {
			rows.Close()
			return 0, 0, err
		}
		nodes = append(nodes, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()
	var restored int64
	for _, n := range nodes {
		var meta map[string]any
		if n.meta.Valid {
			_ = json.Unmarshal([]byte(n.meta.String), &meta)
		}
		if meta["maintenance_archived"] != true {
			continue
		}
		prev, _ := meta["maintenance_previous_status"].(string)
		if !ValidNodeStatus(prev) {
			prev = NodeStatusActive
		}
		delete(meta, "maintenance_archived")
		delete(meta, "maintenance_previous_status")
		b, _ := json.Marshal(meta)
		if _, err := s.db.ExecContext(ctx, `UPDATE cognition_nodes SET status = ?, status_changed_at = ?, metadata_json = ? WHERE id = ?`, prev, now, string(b), n.id); err != nil {
			return 0, 0, err
		}
		restored++
	}
	res, err := s.db.ExecContext(ctx, `UPDATE observations SET deleted_at = NULL, maintenance_deleted_at = NULL WHERE project_path = ? AND maintenance_deleted_at IS NOT NULL`, project)
	if err != nil {
		return 0, 0, err
	}
	obs, err := res.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	return restored, obs, nil
}

func (s *Store) hardDeleteNodes(ctx context.Context, cutoff, now int64, projects []string) (int64, error) {
	var deleted int64
	if _, err := s.db.ExecContext(ctx, `UPDATE cognition_nodes SET status_changed_at = ? WHERE status = ? AND status_changed_at IS NULL`, now, NodeStatusSuperseded); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM cognition_nodes WHERE status = ? AND status_changed_at < ?`, NodeStatusSuperseded, cutoff)
	if err != nil {
		return 0, err
	}
	deleted, err = res.RowsAffected()
	if err != nil {
		return 0, err
	}
	for _, project := range projects {
		if _, err := s.db.ExecContext(ctx, `UPDATE cognition_nodes SET status_changed_at = ? WHERE project_path = ? AND status = ? AND status_changed_at IS NULL`, now, project, NodeStatusArchived); err != nil {
			return 0, err
		}
		res, err := s.db.ExecContext(ctx, `DELETE FROM cognition_nodes WHERE project_path = ? AND status = ? AND status_changed_at < ?`, project, NodeStatusArchived, cutoff)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		deleted += n
	}
	return deleted, nil
}

func (s *Store) hardDeleteObservations(ctx context.Context, cutoff int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM observations WHERE deleted_at IS NOT NULL AND deleted_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) hardDeleteTraces(ctx context.Context, projects []string, cutoff int64) (int64, error) {
	var n int64
	for _, p := range projects {
		missingSince, err := s.missingSince(ctx, p)
		if err != nil {
			return 0, err
		}
		if missingSince == 0 || missingSince >= cutoff {
			continue
		}
		res, err := s.db.ExecContext(ctx, `DELETE FROM run_traces WHERE repo_root = ? AND created_at < ?`, p, cutoff)
		if err != nil {
			return 0, err
		}
		c, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		n += c
	}
	return n, nil
}

func (s *Store) maintenanceLast(ctx context.Context) (int64, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM maintenance_meta WHERE key = 'last_run'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(raw, 10, 64)
}

func (s *Store) setMaintenanceLast(ctx context.Context, value int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO maintenance_meta(key, value) VALUES('last_run', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, strconv.FormatInt(value, 10))
	return err
}

func missingSinceKey(project string) string { return "missing_since:" + project }

func (s *Store) recordMissingSince(ctx context.Context, project string, now int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO maintenance_meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO NOTHING`, missingSinceKey(project), strconv.FormatInt(now, 10))
	return err
}

func (s *Store) clearMissingSince(ctx context.Context, project string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM maintenance_meta WHERE key = ?`, missingSinceKey(project))
	return err
}

func (s *Store) missingSince(ctx context.Context, project string) (int64, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM maintenance_meta WHERE key = ?`, missingSinceKey(project)).Scan(&raw)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("maintenance missing_since %q: %w", project, err)
	}
	return value, nil
}

// ForgetNode hard-deletes one cognition node and its dependent rows.
func (s *Store) ForgetNode(ctx context.Context, id int64) error {
	return s.forget(ctx, `DELETE FROM cognition_nodes WHERE id = ?`, id)
}

// ForgetObservation hard-deletes one observation and its FTS row.
func (s *Store) ForgetObservation(ctx context.Context, id int64) error {
	return s.forget(ctx, `DELETE FROM observations WHERE id = ?`, id)
}

func (s *Store) forget(ctx context.Context, query string, id int64) error {
	if id < 1 {
		return fmt.Errorf("forget id must be >= 1, got %d", id)
	}
	res, err := s.db.ExecContext(ctx, query, id)
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
