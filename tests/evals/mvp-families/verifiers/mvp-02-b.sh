# mvp-02 B: the store gains a Sweep that removes expired sessions and reads
# the SAME configured lifetime source. Base+gold-A fails (no Sweep), gold-A+
# gold-B passes, wrong-B (own duration literal in the sweep) fails.
mkdir -p internal/session
cat > internal/session/probe_sweep_test.go <<'EOF'
package session

import (
	"testing"
	"time"
)

func TestProbeSweep(t *testing.T) {
	store := NewStore(DefaultTTL)
	store.now = func() time.Time { return time.Unix(0, 0) }
	store.Create("live", "u")
	store.Create("dead", "u")
	store.ttl = time.Minute
	// "dead" is expired relative to the store's lifetime; "live" is not.
	// Recreate with precise last-seen values instead of relying on ttl edits.
	store2 := NewStore(time.Minute)
	store2.now = func() time.Time { return time.Unix(1000, 0) }
	store2.Create("s1", "u")
	store2.Create("s2", "u")
	store2.Create("s3", "u")
	store2.now = func() time.Time { return time.Unix(1030, 0) }
	if removed := store2.Sweep(); removed != 0 {
		t.Fatalf("Sweep removed %d before any expiry, want 0", removed)
	}
	store2.now = func() time.Time { return time.Unix(2000, 0) }
	if removed := store2.Sweep(); removed != 3 {
		t.Fatalf("Sweep removed %d after expiry, want 3", removed)
	}
	if store2.Len() != 0 {
		t.Fatalf("Len after sweep = %d, want 0", store2.Len())
	}
}
EOF
go test -count=1 ./internal/session/ ; rc=$?
rm -f internal/session/probe_sweep_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
# The sweep must read the shared lifetime source, not a private literal.
if grep -E "time\.(Minute|Hour|Second)" internal/session/store.go | grep -v "DefaultTTL" | grep -v "ttl " | grep -q "Sweep"; then
  echo "sweep uses its own duration literal" >&2
  exit 1
fi
exit 0