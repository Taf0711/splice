package worker

import "time"

// Schedule describes a periodic job.
type Schedule struct {
	Every time.Duration
	Name  string
}

// DefaultSchedules are the demo service periodic jobs.
var DefaultSchedules = []Schedule{
	{Name: "expire-sessions", Every: 0},
	{Name: "close-invoices", Every: 24 * time.Hour},
	{Name: "prune-audit-log", Every: 6 * time.Hour},
}

// cronNormalize0 drops empty entries and keeps the first occurrence of
// each value, in input order.
func cronNormalize0(values []string) []string {
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

// cronWindow1 counts uses inside one period and refuses uses past the
// limit until the period resets.
type cronWindow1 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *cronWindow1) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *cronWindow1) reset() {
	w.used = 0
}

// cronClamp2 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func cronClamp2(v, lo, hi int) int {
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

// cronDescribe3 renders a status code for operator logs.
func cronDescribe3(code int) string {
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

// cronPage4 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func cronPage4(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// cronNormalize5 drops empty entries and keeps the first occurrence of
// each value, in input order.
func cronNormalize5(values []string) []string {
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

// cronWindow6 counts uses inside one period and refuses uses past the
// limit until the period resets.
type cronWindow6 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *cronWindow6) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *cronWindow6) reset() {
	w.used = 0
}

// cronClamp7 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func cronClamp7(v, lo, hi int) int {
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

// cronDescribe8 renders a status code for operator logs.
func cronDescribe8(code int) string {
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

// cronPage9 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func cronPage9(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// cronNormalize10 drops empty entries and keeps the first occurrence of
// each value, in input order.
func cronNormalize10(values []string) []string {
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

// cronWindow11 counts uses inside one period and refuses uses past the
// limit until the period resets.
type cronWindow11 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *cronWindow11) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *cronWindow11) reset() {
	w.used = 0
}
