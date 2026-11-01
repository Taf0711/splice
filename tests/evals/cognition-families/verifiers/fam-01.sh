# fam-01: AdminForceSignOut invalidates every session of a user.
# Base fails (no admin.go), gold passes, partial invalidation fails.
cat > probe_signout_test.go <<'EOF'
package main

import (
	"testing"
	"time"
)

func TestProbeAdminForceSignOut(t *testing.T) {
	store := NewStore(time.Minute)
	store.now = func() time.Time { return time.Unix(1000, 0) }
	for _, id := range []string{"s1", "s2", "s3"} {
		store.Create(id, "alice")
	}
	store.Create("x1", "bob")

	if err := AdminForceSignOut(store, "alice"); err != nil {
		t.Fatalf("AdminForceSignOut: %v", err)
	}
	// Every session of alice is gone; other users unaffected.
	if _, err := store.Get("s1"); err == nil {
		t.Fatal("s1 still live after force sign-out")
	}
	if _, err := store.Get("s2"); err == nil {
		t.Fatal("s2 still live after force sign-out")
	}
	if _, err := store.Get("s3"); err == nil {
		t.Fatal("s3 still live after force sign-out")
	}
	if _, err := store.Get("x1"); err != nil {
		t.Fatalf("unrelated session was invalidated: %v", err)
	}
	// Unknown user is not an error (idempotent convention).
	if err := AdminForceSignOut(store, "nobody"); err != nil {
		t.Fatalf("sign-out of user with no sessions: %v", err)
	}
}
EOF
go test -count=1 . ; rc=$? ; rm -f probe_signout_test.go ; exit $rc