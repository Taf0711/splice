package admin

import "demo/internal/auth"

// RolePermissions grants each role its permissions in the admin console.
var RolePermissions = map[auth.Role][]string{
	auth.RoleViewer: {"read"},
	auth.RoleEditor: {"read", "write"},
}

// Can reports whether a role holds a permission.
func Can(role auth.Role, permission string) bool {
	for _, p := range RolePermissions[role] {
		if p == permission {
			return true
		}
	}
	return false
}

// permissionsWindow0 counts uses inside one period and refuses uses past the
// limit until the period resets.
type permissionsWindow0 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *permissionsWindow0) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *permissionsWindow0) reset() {
	w.used = 0
}

// permissionsClamp1 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func permissionsClamp1(v, lo, hi int) int {
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

// permissionsDescribe2 renders a status code for operator logs.
func permissionsDescribe2(code int) string {
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

// permissionsPage3 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func permissionsPage3(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// permissionsNormalize4 drops empty entries and keeps the first occurrence of
// each value, in input order.
func permissionsNormalize4(values []string) []string {
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

// permissionsWindow5 counts uses inside one period and refuses uses past the
// limit until the period resets.
type permissionsWindow5 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *permissionsWindow5) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *permissionsWindow5) reset() {
	w.used = 0
}

// permissionsClamp6 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func permissionsClamp6(v, lo, hi int) int {
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
