# mvp-01 B: the admin package gains ForceSignOut reusing the existing
# invalidation behavior, wired to POST /admin/signout. Base+gold-A fails
# (no admin function), gold-A+gold-B passes, wrong-B (own deletion loop
# instead of reuse, or broken wiring) fails.
mkdir -p internal/admin
cat > internal/admin/probe_signout_test.go <<'EOF'
package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"demo/internal/session"
)

func TestProbeAdminForceSignOut(t *testing.T) {
	store := session.NewStore(time.Hour)
	for _, id := range []string{"s1", "s2", "s3"} {
		store.Create(id, "alice")
	}
	store.Create("x1", "bob")

	if err := ForceSignOut(store, "alice"); err != nil {
		t.Fatalf("ForceSignOut: %v", err)
	}
	for _, id := range []string{"s1", "s2", "s3"} {
		if _, err := store.Get(id); err == nil {
			t.Fatalf("%s still live after force sign-out", id)
		}
	}
	if _, err := store.Get("x1"); err != nil {
		t.Fatalf("unrelated session was invalidated: %v", err)
	}
	if err := ForceSignOut(store, "nobody"); err != nil {
		t.Fatalf("sign-out of user with no sessions: %v", err)
	}

	handler := NewHandler(store)
	req := httptest.NewRequest(http.MethodPost, "/admin/signout?user=bob&id=x1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /admin/signout status = %d, want 200", rec.Code)
	}
	if _, err := store.Get("x1"); err == nil {
		t.Fatal("x1 still live after admin route sign-out")
	}
}
EOF
go test -count=1 ./internal/admin/ ; rc=$?
rm -f internal/admin/probe_signout_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
# Reuse, not reinvention: the admin function must route through the session
# package's invalidation (the behavior Task A established), not roll its own
# map walk. Structural grep: admin source references the session invalidation.
if ! grep -rq "InvalidateUserSessions" internal/admin/; then
  echo "admin force sign-out does not reuse the session invalidation behavior" >&2
  exit 1
fi
exit 0