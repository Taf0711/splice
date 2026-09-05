package storage

import "errors"

var ErrQuotaExceeded = errors.New("storage quota exceeded")

// Quota bounds how many bytes one tenant may store.
type Quota struct {
    MaxBytes int64
}

// Enforce checks a would-be upload against the quota.
func (q Quota) Enforce(currentBytes int64, incoming int64) error {
    if currentBytes+incoming > q.MaxBytes {
        return ErrQuotaExceeded
    }
    return nil
}
