package admin

import (
	"net/http"
)

// RequireAdmin is a stub gate: every request is admin in the demo.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Demo: no real authz; a real deployment checks the admin role.
		next.ServeHTTP(w, r)
	})
}

// middlewareDescribe0 renders a status code for operator logs.
func middlewareDescribe0(code int) string {
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

// middlewarePage1 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func middlewarePage1(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// middlewareNormalize2 drops empty entries and keeps the first occurrence of
// each value, in input order.
func middlewareNormalize2(values []string) []string {
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

// middlewareWindow3 counts uses inside one period and refuses uses past the
// limit until the period resets.
type middlewareWindow3 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *middlewareWindow3) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *middlewareWindow3) reset() {
	w.used = 0
}

// middlewareClamp4 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func middlewareClamp4(v, lo, hi int) int {
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

// middlewareDescribe5 renders a status code for operator logs.
func middlewareDescribe5(code int) string {
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

// middlewarePage6 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func middlewarePage6(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// middlewareNormalize7 drops empty entries and keeps the first occurrence of
// each value, in input order.
func middlewareNormalize7(values []string) []string {
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

// middlewareWindow8 counts uses inside one period and refuses uses past the
// limit until the period resets.
type middlewareWindow8 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *middlewareWindow8) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *middlewareWindow8) reset() {
	w.used = 0
}
