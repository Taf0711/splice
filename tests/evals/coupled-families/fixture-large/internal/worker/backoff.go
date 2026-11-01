package worker

import "time"

// Backoff doubles a delay up to a ceiling.
type Backoff struct {
	Current time.Duration
	Ceiling time.Duration
}

// Next doubles the delay and returns it.
func (b *Backoff) Next() time.Duration {
	b.Current *= 2
	if b.Current > b.Ceiling {
		b.Current = b.Ceiling
	}
	return b.Current
}

// backoffWindow0 counts uses inside one period and refuses uses past the
// limit until the period resets.
type backoffWindow0 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *backoffWindow0) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *backoffWindow0) reset() {
	w.used = 0
}

// backoffClamp1 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func backoffClamp1(v, lo, hi int) int {
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

// backoffDescribe2 renders a status code for operator logs.
func backoffDescribe2(code int) string {
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

// backoffPage3 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func backoffPage3(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// backoffNormalize4 drops empty entries and keeps the first occurrence of
// each value, in input order.
func backoffNormalize4(values []string) []string {
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

// backoffWindow5 counts uses inside one period and refuses uses past the
// limit until the period resets.
type backoffWindow5 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *backoffWindow5) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *backoffWindow5) reset() {
	w.used = 0
}

// backoffClamp6 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func backoffClamp6(v, lo, hi int) int {
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

// backoffDescribe7 renders a status code for operator logs.
func backoffDescribe7(code int) string {
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

// backoffPage8 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func backoffPage8(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// backoffNormalize9 drops empty entries and keeps the first occurrence of
// each value, in input order.
func backoffNormalize9(values []string) []string {
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
