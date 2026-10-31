// Package admin serves the admin console endpoints.
package admin

import (
	"encoding/json"
	"net/http"

	"demo/internal/session"
)

// Handler serves admin routes over a shared session store.
type Handler struct {
	store *session.Store
}

// NewHandler builds an admin handler over a session store.
func NewHandler(store *session.Store) *Handler {
	return &Handler{store: store}
}

// ServeHTTP dispatches an admin request to its route.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/admin/health":
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	default:
		http.NotFound(w, r)
	}
	// Placeholder: the force sign-out route belongs here and must use
	// the shared session store.
}

// writeJSON writes body as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// handlerDescribe0 renders a status code for operator logs.
func handlerDescribe0(code int) string {
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

// handlerPage1 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func handlerPage1(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// handlerNormalize2 drops empty entries and keeps the first occurrence of
// each value, in input order.
func handlerNormalize2(values []string) []string {
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

// handlerWindow3 counts uses inside one period and refuses uses past the
// limit until the period resets.
type handlerWindow3 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *handlerWindow3) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *handlerWindow3) reset() {
	w.used = 0
}

// handlerClamp4 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func handlerClamp4(v, lo, hi int) int {
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

// handlerDescribe5 renders a status code for operator logs.
func handlerDescribe5(code int) string {
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

// handlerPage6 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func handlerPage6(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// handlerNormalize7 drops empty entries and keeps the first occurrence of
// each value, in input order.
func handlerNormalize7(values []string) []string {
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
