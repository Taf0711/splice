# mvp-01 A: the session store gains per-user invalidation and the password
# reset flow calls it. Base fails (no invalidation method), gold passes,
# partial wiring (method exists, reset does not call it) fails.
mkdir -p internal/session
cat > internal/session/probe_inval_test.go <<'EOF'
package session

import (
	"testing"
	"time"
)

func TestProbeInvalidateUserSessions(t *testing.T) {
	store := NewStore(time.Minute)
	store.now = func() time.Time { return time.Unix(1000, 0) }
	for _, id := range []string{"s1", "s2", "s3"} {
		store.Create(id, "alice")
	}
	store.Create("x1", "bob")

	removed := store.InvalidateUserSessions("alice")
	if removed != 3 {
		t.Fatalf("InvalidateUserSessions removed %d sessions, want 3", removed)
	}
	for _, id := range []string{"s1", "s2", "s3"} {
		if _, err := store.Get(id); err == nil {
			t.Fatalf("%s still live after invalidation", id)
		}
	}
	if _, err := store.Get("x1"); err != nil {
		t.Fatalf("unrelated session was invalidated: %v", err)
	}
	if n := store.InvalidateUserSessions("nobody"); n != 0 {
		t.Fatalf("invalidation of user with no sessions removed %d, want 0", n)
	}
}
EOF
go test -count=1 ./internal/session/ ; rc=$?
rm -f internal/session/probe_inval_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
# The reset flow must call the invalidation: structural grep on auth code.
if ! grep -q "InvalidateUserSessions" internal/auth/password.go; then
  echo "password reset flow does not invalidate sessions" >&2
  exit 1
fi
exit 0