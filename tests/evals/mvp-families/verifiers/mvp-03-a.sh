# mvp-03 A: the store gains ActiveSessionsFor listing one user's non-expired
# session ids. Base fails (no method), gold passes, wrong (lists expired or
# other users' sessions) fails.
mkdir -p internal/session
cat > internal/session/probe_listing_test.go <<'EOF'
package session

import (
	"testing"
	"time"
)

func TestProbeActiveSessionsFor(t *testing.T) {
	store := NewStore(time.Minute)
	store.now = func() time.Time { return time.Unix(1000, 0) }
	store.Create("a1", "alice")
	store.Create("a2", "alice")
	store.Create("b1", "bob")

	ids := store.ActiveSessionsFor("alice")
	if len(ids) != 2 {
		t.Fatalf("ActiveSessionsFor(alice) = %v, want 2 ids", ids)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if !seen["a1"] || !seen["a2"] {
		t.Fatalf("missing live alice sessions: %v", ids)
	}

	if got := store.ActiveSessionsFor("nobody"); len(got) != 0 {
		t.Fatalf("ActiveSessionsFor(nobody) = %v, want empty", got)
	}

	// Expired sessions never appear.
	store.now = func() time.Time { return time.Unix(2000, 0) }
	if got := store.ActiveSessionsFor("alice"); len(got) != 0 {
		t.Fatalf("expired sessions listed: %v", got)
	}
}
EOF
go test -count=1 ./internal/session/ ; rc=$?
rm -f internal/session/probe_listing_test.go
exit $rc