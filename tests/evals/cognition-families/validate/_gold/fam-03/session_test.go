package main

import (
	"sync"
	"testing"
	"time"
)

// TestDeleteAllForUserIdempotent pins the idempotence contract for
// DeleteAllForUser (the gold arm's version).
func TestDeleteAllForUserIdempotent(t *testing.T) {
	store := NewStore(time.Minute)
	store.now = func() time.Time { return time.Unix(1000, 0) }
	store.Create("a1", "alice")
	store.Create("b1", "bob")
	if n := store.DeleteAllForUser("alice"); n != 1 {
		t.Fatalf("first delete = %d, want 1", n)
	}
	if n := store.DeleteAllForUser("alice"); n != 0 {
		t.Fatalf("second delete = %d, want 0 (idempotent)", n)
	}
	if store.Len() != 1 {
		t.Fatalf("len = %d, want 1", store.Len())
	}
}

var _ = sync.RWMutex{}
