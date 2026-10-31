package store

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestMaintenanceArchivesRestoresAndLeavesLiveProjects(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	live := filepath.Join(t.TempDir(), "live")
	if err := os.Mkdir(live, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "gone")
	ctx := context.Background()
	if _, err := s.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "live", ProjectPath: live}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "gone", ProjectPath: missing}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertObservation(ctx, &Observation{ProjectPath: nullString(missing), OwnerAgent: "test", Visibility: "private", MemoryType: "note", Title: "gone", Content: "gone"}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2_000_000_000, 0)
	first, err := s.MaintenanceAt(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.ArchivedNodes != 1 || first.DeletedObservations != 1 {
		t.Fatalf("archive report = %+v", first)
	}
	var liveStatus, goneStatus string
	if err := s.db.QueryRow(`SELECT status FROM cognition_nodes WHERE project_path = ?`, live).Scan(&liveStatus); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT status FROM cognition_nodes WHERE project_path = ?`, missing).Scan(&goneStatus); err != nil {
		t.Fatal(err)
	}
	if liveStatus != NodeStatusActive || goneStatus != NodeStatusArchived {
		t.Fatalf("statuses = %q, %q", liveStatus, goneStatus)
	}
	if err := os.Mkdir(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	second, err := s.MaintenanceAt(ctx, now.Add(25*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if second.RestoredNodes != 1 || second.RestoredObservations != 1 {
		t.Fatalf("restore report = %+v", second)
	}
}

func TestMaintenanceHardDeletesAfterGraceAndKeepsFTSConsistent(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	project := filepath.Join(t.TempDir(), "gone")
	n, err := s.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "old", ProjectPath: project})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(ctx, n.ID, NodeStatusArchived); err != nil {
		t.Fatal(err)
	}
	obs, err := s.UpsertObservation(ctx, &Observation{ProjectPath: nullString(project), OwnerAgent: "test", Visibility: "private", MemoryType: "note", Title: "old title", Content: "old body"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE cognition_nodes SET status_changed_at = ?, metadata_json = metadata_json`, time.Unix(1_000_000_000, 0).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE observations SET deleted_at = ?, maintenance_deleted_at = ? WHERE id = ?`, time.Unix(1_000_000_000, 0).Unix(), time.Unix(1_000_000_000, 0).Unix(), obs.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaintenanceAt(ctx, time.Unix(1_000_000_000, 0).Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cognition_nodes WHERE id = ?`, n.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("old archived node was not deleted")
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM observations_fts WHERE observations_fts MATCH 'old'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("FTS retained deleted observation: %d", count)
	}
}

func TestMaintenanceTraceDeletionWaitsForMissingProjectGrace(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	project := filepath.Join(t.TempDir(), "gone")
	now := time.Unix(2_000_000_000, 0)
	old := now.Add(-60 * 24 * time.Hour).Unix()
	if _, err := s.UpsertTrace(ctx, &TraceRow{
		RunID: "old-trace", RepoRoot: project, Tier: "standard", Status: "completed",
		CreatedAt: old, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}

	first, err := s.MaintenanceAt(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.HardDeletedTraces != 0 {
		t.Fatalf("first missing pass deleted %d traces", first.HardDeletedTraces)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM run_traces WHERE run_id = 'old-trace'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("old trace was deleted when the project first went missing")
	}
	var missingSince string
	if err := s.db.QueryRow(`SELECT value FROM maintenance_meta WHERE key = ?`, missingSinceKey(project)).Scan(&missingSince); err != nil {
		t.Fatal(err)
	}
	if missingSince != strconv.FormatInt(now.Unix(), 10) {
		t.Fatalf("missing_since = %q, want %d", missingSince, now.Unix())
	}

	second, err := s.MaintenanceAt(ctx, now.Add(31*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if second.HardDeletedTraces != 1 {
		t.Fatalf("second missing pass deleted %d traces, want 1", second.HardDeletedTraces)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM run_traces WHERE run_id = 'old-trace'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("old trace survived after the missing-project grace period")
	}

	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaintenanceAt(ctx, now.Add(32*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM maintenance_meta WHERE key = ?`, missingSinceKey(project)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("missing_since key was not cleared when the project reappeared")
	}
}

func TestMaintenanceHardDeletesSupersededAcrossProjects(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	live := filepath.Join(t.TempDir(), "live")
	if err := os.Mkdir(live, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "gone")
	now := time.Unix(2_000_000_000, 0)
	old := now.Add(-31 * 24 * time.Hour).Unix()
	makeNode := func(claim, project, status string) int64 {
		n, err := s.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: claim, ProjectPath: project})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE cognition_nodes SET status = ?, status_changed_at = ? WHERE id = ?`, status, old, n.ID); err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	liveSuperseded := makeNode("live superseded", live, NodeStatusSuperseded)
	missingSuperseded := makeNode("missing superseded", missing, NodeStatusSuperseded)
	liveArchived := makeNode("live archived", live, NodeStatusArchived)
	missingArchived := makeNode("missing archived", missing, NodeStatusArchived)
	unset := makeNode("unset superseded", live, NodeStatusSuperseded)
	if _, err := s.db.Exec(`UPDATE cognition_nodes SET status_changed_at = NULL WHERE id = ?`, unset); err != nil {
		t.Fatal(err)
	}

	report, err := s.MaintenanceAt(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.HardDeletedNodes != 3 {
		t.Fatalf("hard-deleted nodes = %d, want 3", report.HardDeletedNodes)
	}
	for _, id := range []int64{liveSuperseded, missingSuperseded, missingArchived} {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM cognition_nodes WHERE id = ?`, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("node %d was not deleted", id)
		}
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cognition_nodes WHERE id = ?`, liveArchived).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("archived node in a live project was deleted")
	}
	var statusChangedAt int64
	if err := s.db.QueryRow(`SELECT status_changed_at FROM cognition_nodes WHERE id = ?`, unset).Scan(&statusChangedAt); err != nil {
		t.Fatal(err)
	}
	if statusChangedAt != now.Unix() {
		t.Fatalf("NULL superseded status_changed_at = %d, want %d", statusChangedAt, now.Unix())
	}
}

func TestMaintenanceGuardAndDisable(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Unix(2_000_000_000, 0)
	first, err := s.MaintenanceAt(ctx, now)
	if err != nil || first.Skipped {
		t.Fatalf("first = %+v, err=%v", first, err)
	}
	second, err := s.MaintenanceAt(ctx, now.Add(time.Hour))
	if err != nil || !second.Skipped {
		t.Fatalf("second = %+v, err=%v", second, err)
	}
	t.Setenv("SPLICE_MEMD_MAINTENANCE", "0")
	disabled, err := s.MaintenanceAt(ctx, now.Add(48*time.Hour))
	if err != nil || !disabled.Disabled {
		t.Fatalf("disabled = %+v, err=%v", disabled, err)
	}
}
