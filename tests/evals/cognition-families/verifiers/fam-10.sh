# fam-10: GET /admin/session?id=X enforces the SAME id validation rule:
# invalid ids answer 400, valid-but-missing answers 404 through the store's
# sentinel path (reused, not re-implemented).
cat > probe_adminlookup_test.go <<'EOF'
package main

import (
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestProbeAdminSessionLookup(t *testing.T) {
	store := NewStore(time.Minute)
	store.now = func() time.Time { return time.Unix(1000, 0) }
	store.Create("good-id", "ada")

	// Valid, live id answers the same JSON shape as GET /session.
	handler := adminSessionHandler(store)
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest("GET", "/admin/session?id=good-id", nil))
	if rec.Code != 200 {
		t.Fatalf("valid id = %d, want 200", rec.Code)
	}

	// Invalid ids answer 400 (the validation rule at the boundary).
	// "-bad" is VALID per the rule (hyphens allowed), so it is excluded.
	// Ids are URL-escaped for the query string.
	bad := []string{"x", "has_underscore", make65()}
	for _, id := range bad {
		rec := httptest.NewRecorder()
		target := "/admin/session?id=" + strings.ReplaceAll(url.QueryEscape(id), "+", "%20")
		rec = httptest.NewRecorder()
		handler(rec, httptest.NewRequest("GET", target, nil))
		if rec.Code != 400 {
			t.Fatalf("invalid id %q = %d, want 400", id, rec.Code)
		}
	}

	// Valid but unknown: 404, via the store-level sentinel (reused).
	if _, err := store.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("precondition: store.Get(missing) = %v, want ErrNotFound", err)
	}
	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest("GET", "/admin/session?id=missing", nil))
	if rec.Code != 404 {
		t.Fatalf("missing id = %d, want 404", rec.Code)
	}

	// The store-level validation exists and rejects the same ids.
	if ValidSessionID("x") {
		t.Fatal("ValidSessionID accepted a 1-char id")
	}
}

func make65() string {
	out := make([]byte, 65)
	for i := range out {
		out[i] = 'a'
	}
	return string(out)
}
EOF
go test -count=1 . ; rc=$? ; rm -f probe_adminlookup_test.go ; exit $rc