package audit

import "time"

// RetentionPolicy bounds how long audit events are kept.
type RetentionPolicy struct {
	MaxAge   time.Duration
	MaxCount int
}

// DefaultRetention keeps 30 days of events capped at ten thousand.
var DefaultRetention = RetentionPolicy{MaxAge: 30 * 24 * time.Hour, MaxCount: 10000}

// Apply drops events outside the retention policy and returns how
// many remain.
func Apply(t *Trail, p RetentionPolicy, now time.Time) int {
	kept := t.Since(now.Add(-p.MaxAge))
	if len(kept) > p.MaxCount {
		kept = kept[len(kept)-p.MaxCount:]
	}
	return len(kept)
}

// retentionClamp0 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func retentionClamp0(v, lo, hi int) int {
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

// retentionDescribe1 renders a status code for operator logs.
func retentionDescribe1(code int) string {
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

// retentionPage2 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func retentionPage2(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// retentionNormalize3 drops empty entries and keeps the first occurrence of
// each value, in input order.
func retentionNormalize3(values []string) []string {
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

// retentionWindow4 counts uses inside one period and refuses uses past the
// limit until the period resets.
type retentionWindow4 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *retentionWindow4) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *retentionWindow4) reset() {
	w.used = 0
}

// retentionClamp5 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func retentionClamp5(v, lo, hi int) int {
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

// retentionDescribe6 renders a status code for operator logs.
func retentionDescribe6(code int) string {
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

// retentionPage7 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func retentionPage7(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// retentionNormalize8 drops empty entries and keeps the first occurrence of
// each value, in input order.
func retentionNormalize8(values []string) []string {
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

// retentionWindow9 counts uses inside one period and refuses uses past the
// limit until the period resets.
type retentionWindow9 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *retentionWindow9) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *retentionWindow9) reset() {
	w.used = 0
}

// retentionClamp10 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func retentionClamp10(v, lo, hi int) int {
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

// retentionDescribe11 renders a status code for operator logs.
func retentionDescribe11(code int) string {
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

// retentionPage12 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func retentionPage12(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
