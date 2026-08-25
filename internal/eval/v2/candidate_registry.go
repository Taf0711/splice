package v2

import (
	"fmt"
	"time"
)

// Candidate statuses. The status closes over the append-only lifecycle:
// a candidate registers, then either is rejected or accepted. Rejection can
// only be undone by a fresh registration, which starts a new entry.
const (
	CandidateStatusRegistered = "registered"
	CandidateStatusRejected   = "rejected"
	CandidateStatusAccepted   = "accepted"
)

// CandidateEntry is one append-only record in a candidate's history.
type CandidateEntry struct {
	CandidateID         string `json:"candidate_id"`
	RegisteredAtRFC3339 string `json:"registered_at_rfc3339"`
	ContentSHA256       string `json:"content_sha256"`
	Status              string `json:"status"`
}

// CandidateRegistry is the append-only registration history for eval task
// candidates. Entries are never mutated or removed; a status change appends
// a new entry superseding the prior one for that CandidateID. Latest wins.
type CandidateRegistry struct {
	Entries []CandidateEntry `json:"entries"`
}

// Register appends a registration for id. Duplicate active registration and
// registration of an accepted candidate are errors naming the id. A rejected
// candidate may re-register, which appends a fresh registered entry.
func (r *CandidateRegistry) Register(id, contentHash string) error {
	if id == "" {
		return fmt.Errorf("candidate id is required")
	}
	if !validHash(contentHash) {
		return fmt.Errorf("candidate %s: content hash must be a sha256 hex digest, got %q", id, contentHash)
	}
	if latest, ok := r.latest(id); ok {
		switch latest.Status {
		case CandidateStatusRegistered:
			return fmt.Errorf("candidate %s is already registered", id)
		case CandidateStatusAccepted:
			return fmt.Errorf("candidate %s is accepted; a new version requires a new candidate id", id)
		}
	}
	r.Entries = append(r.Entries, CandidateEntry{
		CandidateID:         id,
		RegisteredAtRFC3339: time.Now().UTC().Format(time.RFC3339),
		ContentSHA256:       contentHash,
		Status:              CandidateStatusRegistered,
	})
	return nil
}

// SetStatus appends a status transition for id. Only rejected and accepted
// are transition targets, each allowed once. Re-opening a rejected candidate
// is an error: revival requires a fresh Register call.
func (r *CandidateRegistry) SetStatus(id, status string) error {
	if status != CandidateStatusRejected && status != CandidateStatusAccepted {
		return fmt.Errorf("candidate %s: status %q is not a transition target", id, status)
	}
	latest, ok := r.latest(id)
	if !ok {
		return fmt.Errorf("candidate %s is not registered", id)
	}
	if latest.Status == CandidateStatusRejected {
		return fmt.Errorf("candidate %s is rejected; re-opening requires a new registration", id)
	}
	if latest.Status == CandidateStatusAccepted {
		return fmt.Errorf("candidate %s is already accepted", id)
	}
	r.Entries = append(r.Entries, CandidateEntry{
		CandidateID:         id,
		RegisteredAtRFC3339: time.Now().UTC().Format(time.RFC3339),
		ContentSHA256:       latest.ContentSHA256,
		Status:              status,
	})
	return nil
}

// Latest returns the most recent entry for id and whether one exists.
func (r *CandidateRegistry) Latest(id string) (CandidateEntry, bool) {
	return r.latest(id)
}

func (r *CandidateRegistry) latest(id string) (CandidateEntry, bool) {
	for i := len(r.Entries) - 1; i >= 0; i-- {
		if r.Entries[i].CandidateID == id {
			return r.Entries[i], true
		}
	}
	return CandidateEntry{}, false
}

// Validate checks the append-only history: unknown statuses, empty ids,
// malformed hashes and timestamps, legal transitions per candidate, and
// content-hash immutability across every entry for the same candidate id.
// The only legal hash change is none; a new version requires a new candidate
// id per the identity rule.
func (r *CandidateRegistry) Validate() error {
	firstHash := make(map[string]string)
	prevStatus := make(map[string]string)
	for i, entry := range r.Entries {
		if entry.CandidateID == "" {
			return fmt.Errorf("entries[%d]: candidate id is required", i)
		}
		if !validHash(entry.ContentSHA256) {
			return fmt.Errorf("candidate %s: entries[%d]: content hash must be a sha256 hex digest, got %q", entry.CandidateID, i, entry.ContentSHA256)
		}
		switch entry.Status {
		case CandidateStatusRegistered, CandidateStatusRejected, CandidateStatusAccepted:
		default:
			return fmt.Errorf("candidate %s: entries[%d]: unknown status %q", entry.CandidateID, i, entry.Status)
		}
		if _, err := time.Parse(time.RFC3339, entry.RegisteredAtRFC3339); err != nil {
			return fmt.Errorf("candidate %s: entries[%d]: registered_at_rfc3339 must be RFC3339: %w", entry.CandidateID, i, err)
		}
		if first, seen := firstHash[entry.CandidateID]; seen {
			if entry.ContentSHA256 != first {
				return fmt.Errorf("candidate %s: content hash changed from %s to %s; a new version requires a new candidate id", entry.CandidateID, first, entry.ContentSHA256)
			}
			switch prevStatus[entry.CandidateID] {
			case CandidateStatusRegistered:
				if entry.Status == CandidateStatusRegistered {
					return fmt.Errorf("candidate %s: duplicate active registration", entry.CandidateID)
				}
			case CandidateStatusRejected:
				if entry.Status != CandidateStatusRegistered {
					return fmt.Errorf("candidate %s: %s after rejection without a re-registration", entry.CandidateID, entry.Status)
				}
			case CandidateStatusAccepted:
				return fmt.Errorf("candidate %s: accepted is terminal; entries[%d] appends after acceptance", entry.CandidateID, i)
			}
		} else {
			if entry.Status != CandidateStatusRegistered {
				return fmt.Errorf("candidate %s: first entry status %q, want registered", entry.CandidateID, entry.Status)
			}
			firstHash[entry.CandidateID] = entry.ContentSHA256
		}
		prevStatus[entry.CandidateID] = entry.Status
	}
	return nil
}
