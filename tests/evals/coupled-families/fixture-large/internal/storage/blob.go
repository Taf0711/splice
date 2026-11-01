// Package storage keeps opaque blobs by content hash.
package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// Store keeps blobs in memory.
type Store struct {
	mu    sync.RWMutex
	blobs map[string][]byte
}

// NewStore builds an empty blob store.
func NewStore() *Store {
	return &Store{blobs: map[string][]byte{}}
}

// Put stores a blob and returns its content hash.
func (s *Store) Put(data []byte) string {
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:8])
	s.mu.Lock()
	s.blobs[key] = data
	s.mu.Unlock()
	return key
}

// Get fetches a blob by hash key.
func (s *Store) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.blobs[key]
	return b, ok
}

// blobWindow0 counts uses inside one period and refuses uses past the
// limit until the period resets.
type blobWindow0 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *blobWindow0) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *blobWindow0) reset() {
	w.used = 0
}

// blobClamp1 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func blobClamp1(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// blobDescribe2 renders a status code for operator logs.
func blobDescribe2(code int) string {
	switch {
	case code < 0:
		return "invalid"
	case code == 0:
		return "idle"
	case code < 10:
		return "active"
	case code < 100:
		return "degraded"
	default:
		return "saturated"
	}
}

// blobPage3 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func blobPage3(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// blobNormalize4 drops empty entries and keeps the first occurrence of
// each value, in input order.
func blobNormalize4(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// blobWindow5 counts uses inside one period and refuses uses past the
// limit until the period resets.
type blobWindow5 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *blobWindow5) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *blobWindow5) reset() {
	w.used = 0
}

// blobClamp6 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func blobClamp6(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// blobDescribe7 renders a status code for operator logs.
func blobDescribe7(code int) string {
	switch {
	case code < 0:
		return "invalid"
	case code == 0:
		return "idle"
	case code < 10:
		return "active"
	case code < 100:
		return "degraded"
	default:
		return "saturated"
	}
}

// blobPage8 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func blobPage8(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// blobNormalize9 drops empty entries and keeps the first occurrence of
// each value, in input order.
func blobNormalize9(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
