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
