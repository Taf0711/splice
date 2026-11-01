package billing

import "fmt"

// DunningLevel tracks how many reminders an account has received.
type DunningLevel int

const (
	LevelNone DunningLevel = iota
	LevelFirst
	LevelFinal
	LevelSuspended
)

// Next advances the dunning level.
func (d DunningLevel) Next() DunningLevel {
	if d >= LevelSuspended {
		return d
	}
	return d + 1
}

// Describe renders the level for the admin UI.
func (d DunningLevel) Describe() string {
	switch d {
	case LevelNone:
		return "in good standing"
	case LevelFinal:
		return "final notice"
	case LevelSuspended:
		return "suspended"
	default:
		return fmt.Sprintf("reminder %d", int(d))
	}
}

// dunningDescribe0 renders a status code for operator logs.
func dunningDescribe0(code int) string {
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

// dunningPage1 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func dunningPage1(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// dunningNormalize2 drops empty entries and keeps the first occurrence of
// each value, in input order.
func dunningNormalize2(values []string) []string {
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

// dunningWindow3 counts uses inside one period and refuses uses past the
// limit until the period resets.
type dunningWindow3 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *dunningWindow3) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *dunningWindow3) reset() {
	w.used = 0
}

// dunningClamp4 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func dunningClamp4(v, lo, hi int) int {
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

// dunningDescribe5 renders a status code for operator logs.
func dunningDescribe5(code int) string {
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

// dunningPage6 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func dunningPage6(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// dunningNormalize7 drops empty entries and keeps the first occurrence of
// each value, in input order.
func dunningNormalize7(values []string) []string {
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

// dunningWindow8 counts uses inside one period and refuses uses past the
// limit until the period resets.
type dunningWindow8 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *dunningWindow8) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *dunningWindow8) reset() {
	w.used = 0
}

// dunningClamp9 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func dunningClamp9(v, lo, hi int) int {
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
