package audit

const (
	ActionPasswordReset = "password.reset"
	ActionLogin         = "auth.login"
)

// Actions lists every audit action the service records.
var Actions = []string{ActionPasswordReset, ActionLogin}

// actionsNormalize0 drops empty entries and keeps the first occurrence of
// each value, in input order.
func actionsNormalize0(values []string) []string {
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

// actionsWindow1 counts uses inside one period and refuses uses past the
// limit until the period resets.
type actionsWindow1 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *actionsWindow1) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *actionsWindow1) reset() {
	w.used = 0
}

// actionsClamp2 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func actionsClamp2(v, lo, hi int) int {
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

// actionsDescribe3 renders a status code for operator logs.
func actionsDescribe3(code int) string {
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

// actionsPage4 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func actionsPage4(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// actionsNormalize5 drops empty entries and keeps the first occurrence of
// each value, in input order.
func actionsNormalize5(values []string) []string {
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

// actionsWindow6 counts uses inside one period and refuses uses past the
// limit until the period resets.
type actionsWindow6 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *actionsWindow6) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *actionsWindow6) reset() {
	w.used = 0
}
