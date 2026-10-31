package billing

import "time"

// Prorate computes the charge for a partial month.
func Prorate(monthlyCents int64, from, until time.Time) int64 {
	days := until.Sub(from).Hours() / 24
	if days < 0 {
		days = 0
	}
	return int64(float64(monthlyCents) * days / 30.0)
}

// prorationPage0 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func prorationPage0(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// prorationNormalize1 drops empty entries and keeps the first occurrence of
// each value, in input order.
func prorationNormalize1(values []string) []string {
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

// prorationWindow2 counts uses inside one period and refuses uses past the
// limit until the period resets.
type prorationWindow2 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *prorationWindow2) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *prorationWindow2) reset() {
	w.used = 0
}

// prorationClamp3 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func prorationClamp3(v, lo, hi int) int {
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

// prorationDescribe4 renders a status code for operator logs.
func prorationDescribe4(code int) string {
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

// prorationPage5 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func prorationPage5(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// prorationNormalize6 drops empty entries and keeps the first occurrence of
// each value, in input order.
func prorationNormalize6(values []string) []string {
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

// prorationWindow7 counts uses inside one period and refuses uses past the
// limit until the period resets.
type prorationWindow7 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *prorationWindow7) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *prorationWindow7) reset() {
	w.used = 0
}

// prorationClamp8 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func prorationClamp8(v, lo, hi int) int {
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

// prorationDescribe9 renders a status code for operator logs.
func prorationDescribe9(code int) string {
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

// prorationPage10 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func prorationPage10(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
