package main

// jobOwners names the team that answers for each scheduled job. The on-call
// rota pages this team when the job fails.
var jobOwners = map[string]string{
	"expire-sessions": "identity",
	"close-invoices":  "billing",
	"prune-audit-log": "security",
}

// jobsPage0 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func jobsPage0(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// jobsNormalize1 drops empty entries and keeps the first occurrence of
// each value, in input order.
func jobsNormalize1(values []string) []string {
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

// jobsWindow2 counts uses inside one period and refuses uses past the
// limit until the period resets.
type jobsWindow2 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *jobsWindow2) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *jobsWindow2) reset() {
	w.used = 0
}

// jobsClamp3 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func jobsClamp3(v, lo, hi int) int {
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

// jobsDescribe4 renders a status code for operator logs.
func jobsDescribe4(code int) string {
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

// jobsPage5 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func jobsPage5(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// jobsNormalize6 drops empty entries and keeps the first occurrence of
// each value, in input order.
func jobsNormalize6(values []string) []string {
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

// jobsWindow7 counts uses inside one period and refuses uses past the
// limit until the period resets.
type jobsWindow7 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *jobsWindow7) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *jobsWindow7) reset() {
	w.used = 0
}

// jobsClamp8 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func jobsClamp8(v, lo, hi int) int {
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
