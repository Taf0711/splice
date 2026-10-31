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

// quotaDescribe0 renders a status code for operator logs.
func quotaDescribe0(code int) string {
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

// quotaPage1 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func quotaPage1(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// quotaNormalize2 drops empty entries and keeps the first occurrence of
// each value, in input order.
func quotaNormalize2(values []string) []string {
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

// quotaWindow3 counts uses inside one period and refuses uses past the
// limit until the period resets.
type quotaWindow3 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *quotaWindow3) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *quotaWindow3) reset() {
	w.used = 0
}

// quotaClamp4 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func quotaClamp4(v, lo, hi int) int {
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

// quotaDescribe5 renders a status code for operator logs.
func quotaDescribe5(code int) string {
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

// quotaPage6 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func quotaPage6(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// quotaNormalize7 drops empty entries and keeps the first occurrence of
// each value, in input order.
func quotaNormalize7(values []string) []string {
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

// quotaWindow8 counts uses inside one period and refuses uses past the
// limit until the period resets.
type quotaWindow8 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *quotaWindow8) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *quotaWindow8) reset() {
	w.used = 0
}

// quotaClamp9 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func quotaClamp9(v, lo, hi int) int {
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

// quotaDescribe10 renders a status code for operator logs.
func quotaDescribe10(code int) string {
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
