package v2

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// JournalStatus is the lifecycle state recorded for one scheduled trial.
type JournalStatus string

const (
	JournalStatusScheduled JournalStatus = "scheduled"
	JournalStatusStarted   JournalStatus = "started"
	JournalStatusCompleted JournalStatus = "completed"
)

// JournalEntry records the durable state of one trial identity.
type JournalEntry struct {
	Key         TrialKey `json:"key"`
	PersistedAt string   `json:"persisted_at"`
	Status      string   `json:"status"`
}

// TrialJournal is an append-only logical journal of trial lifecycle states.
// Appending a later state advances the existing identity without creating a
// duplicate key in the validated journal.
type TrialJournal struct {
	Entries []JournalEntry `json:"entries"`
}

// Journal is the short name used by resume callers.
type Journal = TrialJournal

// Validate checks journal entries, including unique identities and lifecycle
// statuses.
func (j TrialJournal) Validate() error {
	seen := make(map[TrialKey]bool, len(j.Entries))
	for i, entry := range j.Entries {
		if err := entry.validate(); err != nil {
			return fmt.Errorf("entries[%d]: %w", i, err)
		}
		if seen[entry.Key] {
			return fmt.Errorf("entries[%d]: duplicate trial key %s", i, entry.Key.String())
		}
		seen[entry.Key] = true
	}
	return nil
}

func (e JournalEntry) validate() error {
	if err := e.Key.Validate(); err != nil {
		return fmt.Errorf("key: %w", err)
	}
	if !validJournalStatus(e.Status) {
		return fmt.Errorf("key %s has unknown status %q", e.Key.String(), e.Status)
	}
	if _, err := time.Parse(time.RFC3339, e.PersistedAt); err != nil {
		return fmt.Errorf("key %s persisted_at must be RFC3339: %w", e.Key.String(), err)
	}
	return nil
}

func validJournalStatus(status string) bool {
	switch JournalStatus(status) {
	case JournalStatusScheduled, JournalStatusStarted, JournalStatusCompleted:
		return true
	default:
		return false
	}
}

func journalStatusRank(status string) int {
	switch JournalStatus(status) {
	case JournalStatusScheduled:
		return 1
	case JournalStatusStarted:
		return 2
	case JournalStatusCompleted:
		return 3
	default:
		return 0
	}
}

// Append records entry unless the identity already has an equal or later
// status. A status regression is rejected with the trial key.
func (j *TrialJournal) Append(entry JournalEntry) error {
	if j == nil {
		return fmt.Errorf("cannot append to a nil trial journal")
	}
	if err := entry.validate(); err != nil {
		return err
	}
	for i, existing := range j.Entries {
		if existing.Key != entry.Key {
			continue
		}
		existingRank := journalStatusRank(existing.Status)
		entryRank := journalStatusRank(entry.Status)
		if entryRank < existingRank {
			return fmt.Errorf("trial %s status regression from %q to %q", entry.Key.String(), existing.Status, entry.Status)
		}
		if entryRank == existingRank {
			return nil
		}
		j.Entries[i] = entry
		return nil
	}
	j.Entries = append(j.Entries, entry)
	return nil
}

// Encode validates and returns canonical JSON. encoding/json preserves the
// declared struct field order, which makes this representation byte-stable.
func (j TrialJournal) Encode() ([]byte, error) {
	if err := j.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(j)
}

// DecodeTrialJournal decodes and validates canonical journal JSON.
func DecodeTrialJournal(data []byte) (TrialJournal, error) {
	var journal TrialJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return TrialJournal{}, fmt.Errorf("decode trial journal: %w", err)
	}
	if err := journal.Validate(); err != nil {
		return TrialJournal{}, fmt.Errorf("validate trial journal: %w", err)
	}
	return journal, nil
}

// IncompleteTrials returns every scheduled identity whose latest journal
// status is not completed. Scheduled and started entries reappear after a
// crash so the future runner can choose retry or reconciliation explicitly.
func IncompleteTrials(j Journal, s Schedule) []TrialKey {
	completed := make(map[TrialKey]bool, len(j.Entries))
	for _, entry := range j.Entries {
		if entry.Status == string(JournalStatusCompleted) {
			completed[entry.Key] = true
		}
	}
	incomplete := make([]TrialKey, 0, len(s.Trials))
	for _, trial := range s.Trials {
		if !completed[trial.Key] {
			incomplete = append(incomplete, trial.Key)
		}
	}
	sort.Slice(incomplete, func(i, k int) bool {
		return incomplete[i].String() < incomplete[k].String()
	})
	return incomplete
}

// MissingTrials is retained for compatibility. New resume callers must use
// IncompleteTrials so crash-boundary states are not treated as completed.
// Deprecated: use IncompleteTrials.
func MissingTrials(j Journal, s Schedule) []TrialKey {
	return IncompleteTrials(j, s)
}
