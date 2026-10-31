package auth

// Role names what a signed-in user may do.
type Role string

const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
)

// Roles lists every role a user can hold.
var Roles = []Role{RoleViewer, RoleEditor}

// rolesNormalize0 drops empty entries and keeps the first occurrence of
// each value, in input order.
func rolesNormalize0(values []string) []string {
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

// rolesWindow1 counts uses inside one period and refuses uses past the
// limit until the period resets.
type rolesWindow1 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *rolesWindow1) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *rolesWindow1) reset() {
	w.used = 0
}

// rolesClamp2 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func rolesClamp2(v, lo, hi int) int {
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

// rolesDescribe3 renders a status code for operator logs.
func rolesDescribe3(code int) string {
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

// rolesPage4 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func rolesPage4(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// rolesNormalize5 drops empty entries and keeps the first occurrence of
// each value, in input order.
func rolesNormalize5(values []string) []string {
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

// rolesWindow6 counts uses inside one period and refuses uses past the
// limit until the period resets.
type rolesWindow6 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *rolesWindow6) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *rolesWindow6) reset() {
	w.used = 0
}
