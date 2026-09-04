# mvp-03 B: the session package gains CountActiveSessions reusing the SAME
# active-session rule the listing established. Base+gold-A fails (no count
# function), gold-A+gold-B passes, wrong-B (count that disagrees with the
# listing) fails.
mkdir -p internal/session
cat > internal/session/probe_count_test.go <<'EOF'
package session

import (
	"testing"
	"time"
)

func TestProbeCountActiveSessions(t *testing.T) {
	store := NewStore(time.Minute)
	store.now = func() time.Time { return time.Unix(1000, 0) }
	if got := CountActiveSessions(store, "carol"); got != 0 {
		t.Fatalf("count for user with no sessions = %d, want 0", got)
	}
	store.Create("c1", "carol")
	if got := CountActiveSessions(store, "carol"); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
	store.Create("c2", "carol")
	store.Create("d1", "dave")
	if got := CountActiveSessions(store, "carol"); got != 2 {
		t.Fatalf("count = %d, want 2", got)
	}

	// Consistency with the listing rule: count must equal len(listing) for
	// every user, before and after expiry.
	store.now = func() time.Time { return time.Unix(2000, 0) }
	for _, user := range []string{"carol", "dave"} {
		if got, want := CountActiveSessions(store, user), len(store.ActiveSessionsFor(user)); got != want {
			t.Fatalf("count/listing disagree for %s: %d vs %d", user, got, want)
		}
	}
}
EOF
go test -count=1 ./internal/session/ ; rc=$?
rm -f internal/session/probe_count_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
# Reuse, not reinvention: the count must route through the same active-session
# rule (the per-user listing or the same expiry comparison), not duplicate a
# divergent definition. Structural: the new function's file references the
# listing method or shares its expiry expression.
if ! grep -rq "ActiveSessionsFor" internal/session/count_active.go 2>/dev/null; then
  # Allow an in-store implementation sharing the same file as the listing.
  if ! grep -rq "ActiveSessionsFor\|activeSessions" internal/session/*.go; then
    echo "count does not reuse the established active-session rule" >&2
    exit 1
  fi
fi
exit 0