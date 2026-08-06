package sessions

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PruneOptions selects sessions for removal.
type PruneOptions struct {
	OlderThanDays   int
	Now             time.Time
	ActiveSessionID string
}

// PruneItem describes one session considered by a prune plan.
type PruneItem struct {
	SessionID string `json:"sessionId"`
	UpdatedAt string `json:"updatedAt"`
	SizeBytes int64  `json:"sizeBytes"`
	Reason    string `json:"reason"`
}

// PrunePlan contains the complete preview. It does not remove files.
type PrunePlan struct {
	OlderThanDays int         `json:"olderThanDays"`
	Cutoff        string      `json:"cutoff"`
	Selected      []PruneItem `json:"selected"`
	Kept          []PruneItem `json:"kept"`
	Skipped       []PruneItem `json:"skipped"`
	TotalSize     int64       `json:"totalSizeBytes"`
	Oldest        string      `json:"oldestUpdatedAt,omitempty"`
	Newest        string      `json:"newestUpdatedAt,omitempty"`
}

// PruneResult reports the sessions removed by an execution.
type PruneResult struct {
	Removed   []PruneItem `json:"removed"`
	Skipped   []PruneItem `json:"skipped"`
	TotalSize int64       `json:"totalSizeBytes"`
}

// Remove deletes one session directory. The id is validated before any path
// is built. A missing session is a successful no-op.
func (store *Store) Remove(sessionID string) (bool, error) {
	if !ValidSessionID(sessionID) {
		return false, fmt.Errorf("invalid splice session id %q", sessionID)
	}
	sessionID = strings.TrimSpace(sessionID)
	path := store.sessionPath(sessionID)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat splice session %s: %w", sessionID, err)
	}
	unlock, err := store.lockSession(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer unlock()

	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat splice session %s: %w", sessionID, err)
	}
	if err := os.RemoveAll(path); err != nil {
		return false, fmt.Errorf("remove splice session %s: %w", sessionID, err)
	}
	return true, nil
}

// PlanPrune builds a preview and never removes a session.
func (store *Store) PlanPrune(options PruneOptions) (PrunePlan, error) {
	if options.OlderThanDays < 0 {
		return PrunePlan{}, fmt.Errorf("invalid older-than days %d", options.OlderThanDays)
	}
	now := options.Now
	if now.IsZero() {
		now = store.now()
	}
	now = now.UTC()
	cutoff := now.Add(-time.Duration(options.OlderThanDays) * 24 * time.Hour)
	all, err := store.List()
	if err != nil {
		return PrunePlan{}, err
	}
	byID := make(map[string]Metadata, len(all))
	for _, session := range all {
		byID[session.SessionID] = session
	}
	initial := make(map[string]bool)
	sizes := make(map[string]int64)
	for _, session := range all {
		if session.SessionID == strings.TrimSpace(options.ActiveSessionID) {
			continue
		}
		updated, err := time.Parse(time.RFC3339, strings.TrimSpace(session.UpdatedAt))
		if err != nil {
			return PrunePlan{}, fmt.Errorf("parse updated time for splice session %s: %w", session.SessionID, err)
		}
		if updated.Before(cutoff) && session.SessionID != strings.TrimSpace(options.ActiveSessionID) {
			initial[session.SessionID] = true
			sizes[session.SessionID] = sessionSize(store.sessionPath(session.SessionID))
		}
	}

	// A selected ancestor is safe only when every descendant is also selected.
	// Any descendant outside the initial selection survives the prune.
	selected := make(map[string]bool, len(initial))
	for id := range initial {
		selected[id] = true
	}
	// Remove ancestors when a descendant was itself disqualified. Repeat until
	// the set is stable, so no surviving child can be orphaned.
	changed := true
	for changed {
		changed = false
		for id := range selected {
			if hasSurvivingDescendant(id, byID, selected) {
				delete(selected, id)
				changed = true
			}
		}
	}
	plan := PrunePlan{
		OlderThanDays: options.OlderThanDays,
		Cutoff:        cutoff.Format(time.RFC3339),
		Selected:      []PruneItem{},
		Kept:          []PruneItem{},
		Skipped:       []PruneItem{},
	}
	for _, session := range all {
		if session.SessionID == strings.TrimSpace(options.ActiveSessionID) {
			plan.Skipped = append(plan.Skipped, PruneItem{SessionID: session.SessionID, UpdatedAt: session.UpdatedAt, Reason: "active"})
			continue
		}
		if initial[session.SessionID] && !selected[session.SessionID] {
			plan.Skipped = append(plan.Skipped, PruneItem{SessionID: session.SessionID, UpdatedAt: session.UpdatedAt, SizeBytes: sizes[session.SessionID], Reason: "has-descendants"})
			continue
		}
		if !initial[session.SessionID] {
			plan.Kept = append(plan.Kept, PruneItem{SessionID: session.SessionID, UpdatedAt: session.UpdatedAt, Reason: "not-old-enough"})
			continue
		}
		if selected[session.SessionID] {
			item := PruneItem{SessionID: session.SessionID, UpdatedAt: session.UpdatedAt, SizeBytes: sizes[session.SessionID], Reason: "older-than"}
			plan.Selected = append(plan.Selected, item)
			plan.TotalSize += item.SizeBytes
		}
	}
	sort.Slice(plan.Selected, func(i, j int) bool { return plan.Selected[i].UpdatedAt < plan.Selected[j].UpdatedAt })
	sort.Slice(plan.Kept, func(i, j int) bool { return plan.Kept[i].SessionID < plan.Kept[j].SessionID })
	sort.Slice(plan.Skipped, func(i, j int) bool { return plan.Skipped[i].SessionID < plan.Skipped[j].SessionID })
	if len(plan.Selected) > 0 {
		plan.Oldest = plan.Selected[0].UpdatedAt
		plan.Newest = plan.Selected[len(plan.Selected)-1].UpdatedAt
	}
	return plan, nil
}

// Prune executes a previously computed plan. Each removal takes the session
// lock before inspecting or deleting its files.
func (store *Store) Prune(plan PrunePlan) (PruneResult, error) {
	result := PruneResult{Removed: []PruneItem{}, Skipped: append([]PruneItem{}, plan.Skipped...)}
	for _, item := range plan.Selected {
		removed, err := store.Remove(item.SessionID)
		if err != nil {
			return result, err
		}
		if removed {
			result.Removed = append(result.Removed, item)
			result.TotalSize += item.SizeBytes
		}
	}
	return result, nil
}

func hasSurvivingDescendant(sessionID string, all map[string]Metadata, selected map[string]bool) bool {
	for id, session := range all {
		if id == sessionID {
			continue
		}
		if session.RootSessionID == sessionID && session.ParentSessionID != sessionID {
			if !selected[id] {
				return true
			}
			continue
		}
		if session.ParentSessionID == "" {
			continue
		}
		seen := map[string]bool{}
		current := session.ParentSessionID
		for current != "" && !seen[current] {
			if current == sessionID {
				return !selected[id]
			}
			seen[current] = true
			parent, ok := all[current]
			if !ok {
				break
			}
			current = parent.ParentSessionID
		}
	}
	return false
}

func sessionSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}
