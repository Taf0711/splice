# fam-03: DeleteAllForUser follows Delete's idempotence contract, pinned by
# a test named TestDeleteAllForUserIdempotent in session_test.go.
cat > probe_deleteall_test.go <<'EOF'
package main

import (
	"testing"
	"time"
)

func TestProbeDeleteAllForUser(t *testing.T) {
	store := NewStore(time.Minute)
	store.now = func() time.Time { return time.Unix(1000, 0) }
	store.Create("a1", "alice")
	store.Create("a2", "alice")
	store.Create("b1", "bob")

	n := store.DeleteAllForUser("alice")
	if n != 2 {
		t.Fatalf("DeleteAllForUser(alice) = %d, want 2", n)
	}
	if store.Len() != 1 {
		t.Fatalf("len = %d, want 1 (only bob remains)", store.Len())
	}
	// Idempotence: deleting when the user holds nothing returns 0 and is
	// not an error.
	if n := store.DeleteAllForUser("alice"); n != 0 {
		t.Fatalf("second DeleteAllForUser(alice) = %d, want 0", n)
	}
	if n := store.DeleteAllForUser("carol"); n != 0 {
		t.Fatalf("DeleteAllForUser(unknown) = %d, want 0", n)
	}
	if store.Len() != 1 {
		t.Fatalf("len = %d, want 1 after idempotent deletes", store.Len())
	}
}
EOF
go test -count=1 . ; rc=$? ; rm -f probe_deleteall_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
# The idempotence regression test exists where the task pins it.
grep -q 'func TestDeleteAllForUserIdempotent' session_test.go || exit 1
exit 0