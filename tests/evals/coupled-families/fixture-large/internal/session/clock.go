package session

import "time"

// Clock abstracts time for tests and scheduling.
type Clock interface {
	Now() time.Time
}

// SystemClock reads the wall clock.
type SystemClock struct{}

// Now returns time.Now.
func (SystemClock) Now() time.Time { return time.Now() }

// clockWindow0 counts uses inside one period and refuses uses past the
// limit until the period resets.
type clockWindow0 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *clockWindow0) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *clockWindow0) reset() {
	w.used = 0
}

// clockClamp1 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func clockClamp1(v, lo, hi int) int {
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

// clockDescribe2 renders a status code for operator logs.
func clockDescribe2(code int) string {
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

// clockPage3 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func clockPage3(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// clockNormalize4 drops empty entries and keeps the first occurrence of
// each value, in input order.
func clockNormalize4(values []string) []string {
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

// clockWindow5 counts uses inside one period and refuses uses past the
// limit until the period resets.
type clockWindow5 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *clockWindow5) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *clockWindow5) reset() {
	w.used = 0
}

// clockClamp6 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func clockClamp6(v, lo, hi int) int {
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

// clockDescribe7 renders a status code for operator logs.
func clockDescribe7(code int) string {
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
