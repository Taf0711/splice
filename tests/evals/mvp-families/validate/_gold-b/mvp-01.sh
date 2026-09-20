#!/bin/bash
# Gold solution for mvp-01 Task B: admin ForceSignOut reusing the session
# invalidation, wired to POST /admin/signout. Applied AFTER gold-A.
set -e
cd "$1"
cat > internal/admin/signout.go <<'EOF'
package admin

import "demo/internal/session"

// ForceSignOut invalidates every active session that belongs to userID,
// reusing the session package's invalidation behavior. A user with no
// sessions is not an error.
func ForceSignOut(store *session.Store, userID string) error {
	store.InvalidateUserSessions(userID)
	return nil
}
EOF
python3 - <<'PYEOF'
path = "internal/admin/handler.go"
src = open(path).read()
old = """// Handler serves admin routes over a shared session store.
type Handler struct {
	store *session.Store
}"""
new = """// Handler serves admin routes over a shared session store.
type Handler struct {
	store *session.Store
	forceSignOut func(*session.Store, string) error
}"""
assert old in src
src = src.replace(old, new)
old2 = """	switch r.URL.Path {
	case "/admin/health":
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	default:
		http.NotFound(w, r)
	}
	// Placeholder: the force sign-out route belongs here and must use
	// the shared session store."""
new2 = """	switch r.URL.Path {
	case "/admin/health":
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case "/admin/signout":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		user := r.URL.Query().Get("user")
		if user == "" {
			http.Error(w, "user is required", http.StatusBadRequest)
			return
		}
		if h.forceSignOut == nil {
			h.forceSignOut = ForceSignOut
		}
		if err := h.forceSignOut(h.store, user); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "signed-out"})
	default:
		http.NotFound(w, r)
	}"""
assert old2 in src
open(path, "w").write(src.replace(old2, new2))
PYEOF