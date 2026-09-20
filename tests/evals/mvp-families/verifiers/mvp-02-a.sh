# mvp-02 A: the session package gains DefaultTTL as the single lifetime
# source of truth and the construction site reads it. Base fails (no
# constant), gold passes, wrong (wrong value or still a literal at the
# construction site) fails.
mkdir -p internal/session
cat > internal/session/probe_ttl_test.go <<'EOF'
package session

import (
	"testing"
	"time"
)

func TestProbeDefaultTTL(t *testing.T) {
	if DefaultTTL != 30*time.Minute {
		t.Fatalf("DefaultTTL = %v, want 30m", DefaultTTL)
	}
	store := NewStore(DefaultTTL)
	store.now = func() time.Time { return time.Unix(0, 0) }
	store.Create("a", "u")
	// Get refreshes LastSeen, so the expiry probe must not Get first.
	store.now = func() time.Time { return time.Unix(1799, 0) }
	if _, err := store.Get("a"); err != nil {
		t.Fatalf("session expired before DefaultTTL: %v", err)
	}
	store.Create("b", "u")
	store.now = func() time.Time { return time.Unix(3601, 0) }
	if _, err := store.Get("b"); err == nil {
		t.Fatal("session survived past DefaultTTL")
	}
}
EOF
go test -count=1 ./internal/session/ ; rc=$?
rm -f internal/session/probe_ttl_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
# The construction site must read the constant, not keep its own literal.
if grep -q "30 \* time.Minute" cmd/server/main.go; then
  echo "cmd/server/main.go still hard-codes the TTL literal" >&2
  exit 1
fi
exit 0