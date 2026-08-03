package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlanPruneIsPreviewOnly(t *testing.T) {
	root := t.TempDir()
	store := NewStore(StoreOptions{RootDir: root, Now: func() time.Time { return time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC) }})
	old, err := store.Create(CreateInput{SessionID: "old"})
	if err != nil {
		t.Fatal(err)
	}
	newer, err := store.Create(CreateInput{SessionID: "newer"})
	if err != nil {
		t.Fatal(err)
	}
	setUpdatedAt(t, store, old.SessionID, "2026-07-01T12:00:00Z")
	setUpdatedAt(t, store, newer.SessionID, "2026-08-01T12:00:00Z")

	// Regression: prune-plan is a preview and must not remove directories.
	plan, err := store.PlanPrune(PruneOptions{OlderThanDays: 7, Now: time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC), ActiveSessionID: "not-active"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Selected) != 1 || plan.Selected[0].SessionID != old.SessionID {
		t.Fatalf("plan selected %#v, want old only", plan.Selected)
	}
	if _, err := os.Stat(filepath.Join(root, old.SessionID)); err != nil {
		t.Fatalf("prune-plan removed old session directory: %v", err)
	}
}

func TestPruneRemovesSelectedAndPreservesRecent(t *testing.T) {
	store := newPruneTestStore(t)
	oldA := createWithTime(t, store, "old_a", "2026-06-01T00:00:00Z")
	oldB := createWithTime(t, store, "old_b", "2026-06-02T00:00:00Z")
	recent := createWithTime(t, store, "recent", "2026-08-01T00:00:00Z")
	plan, err := store.PlanPrune(PruneOptions{OlderThanDays: 7, Now: pruneNow(), ActiveSessionID: recent.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Prune(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 2 {
		t.Fatalf("removed %d sessions, want 2", len(result.Removed))
	}
	for _, id := range []string{oldA.SessionID, oldB.SessionID} {
		if got, err := store.Get(id); err != nil || got != nil {
			t.Fatalf("pruned session %s still exists: %#v, %v", id, got, err)
		}
	}
	if got, err := store.Get(recent.SessionID); err != nil || got == nil {
		t.Fatalf("recent session was removed: %#v, %v", got, err)
	}
}

func TestPlanPruneSkipsLivingDescendantAndActive(t *testing.T) {
	store := newPruneTestStore(t)
	parent, err := store.Create(CreateInput{SessionID: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.CreateChild(parent.SessionID, ChildInput{SessionID: "child", Title: "child"})
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.Create(CreateInput{SessionID: "active"})
	if err != nil {
		t.Fatal(err)
	}
	setUpdatedAt(t, store, parent.SessionID, "2026-06-01T00:00:00Z")
	setUpdatedAt(t, store, child.SessionID, "2026-08-01T00:00:00Z")
	setUpdatedAt(t, store, active.SessionID, "2026-06-01T00:00:00Z")
	plan, err := store.PlanPrune(PruneOptions{OlderThanDays: 7, Now: pruneNow(), ActiveSessionID: active.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Selected) != 0 {
		t.Fatalf("selected %#v, want no sessions", plan.Selected)
	}
	if !hasReason(plan, parent.SessionID, "has-descendants") || !hasReason(plan, active.SessionID, "active") {
		t.Fatalf("skips %#v, want descendant and active reasons", plan.Skipped)
	}
}

func TestRemoveValidatesIDBeforeFilesystem(t *testing.T) {
	root := t.TempDir()
	store := NewStore(StoreOptions{RootDir: root})
	outside := filepath.Join(filepath.Dir(root), "outside-session")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := store.Remove("../outside-session")
	if err == nil || !strings.Contains(err.Error(), "invalid splice session id") {
		t.Fatalf("Remove error = %v, want validation error", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("invalid id reached filesystem removal: %v", err)
	}
}

func TestRemoveMissingIsNoop(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	removed, err := store.Remove("missing")
	if err != nil || removed {
		t.Fatalf("Remove missing = %v, %v, want false nil", removed, err)
	}
}

func newPruneTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(StoreOptions{RootDir: t.TempDir(), Now: pruneNow})
}

func pruneNow() time.Time { return time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC) }

func createWithTime(t *testing.T, store *Store, id, updated string) Metadata {
	t.Helper()
	session, err := store.Create(CreateInput{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	setUpdatedAt(t, store, id, updated)
	return session
}

func setUpdatedAt(t *testing.T, store *Store, id, updated string) {
	t.Helper()
	session, err := store.Get(id)
	if err != nil || session == nil {
		t.Fatalf("get %s: %v", id, err)
	}
	session.UpdatedAt = updated
	if err := store.writeMetadata(*session); err != nil {
		t.Fatal(err)
	}
}

func hasReason(plan PrunePlan, id, reason string) bool {
	for _, item := range plan.Skipped {
		if item.SessionID == id && item.Reason == reason {
			return true
		}
	}
	return false
}
