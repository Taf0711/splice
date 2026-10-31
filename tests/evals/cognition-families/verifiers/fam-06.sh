# fam-06: SessionsForUser follows the audit's locking discipline. Two probes.
# (1) Deterministic: a read must not mutate. The clock moves after Create, so a
# read that refreshes LastSeen leaves a visible trace. This probe fails the
# wrong variant on every run, with or without -race.
# (2) Race: concurrent readers plus a writer, released together by a barrier,
# under -race. The wrong variant also fails here when the race detector fires.
cat > probe_race_test.go <<'EOF'
package main

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestProbeSessionsForUserReadOnly(t *testing.T) {
	store := NewStore(time.Minute)
	clock := time.Unix(1000, 0)
	store.now = func() time.Time { return clock }
	store.Create("a0", "alice")
	clock = clock.Add(30 * time.Second)

	if got := store.SessionsForUser("alice"); len(got) != 1 {
		t.Fatalf("SessionsForUser(alice) = %d, want 1", len(got))
	}
	store.mu.RLock()
	seen := store.sessions["a0"].LastSeen
	store.mu.RUnlock()
	if want := time.Unix(1000, 0); !seen.Equal(want) {
		t.Fatalf("read path changed LastSeen to %v, want %v", seen, want)
	}
}

func TestProbeSessionsForUserRace(t *testing.T) {
	store := NewStore(time.Minute)
	store.now = func() time.Time { return time.Unix(1000, 0) }
	for i := 0; i < 4; i++ {
		store.Create(fmt.Sprintf("a%d", i), "alice")
	}
	store.Create("b0", "bob")

	// The barrier starts all readers at once, so their reads overlap the
	// writer below instead of finishing before it starts.
	var wg sync.WaitGroup
	start := make(chan struct{})
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 2000; i++ {
				_ = store.SessionsForUser("alice")
				_ = store.Len()
			}
		}()
	}
	close(start)
	// The writer mutates the map while readers walk it. Read paths must
	// never mutate, and expired entries take the write lock.
	for w := 0; w < 500; w++ {
		store.Create("w", "writer")
		store.Delete("w")
	}
	wg.Wait()

	sessions := store.SessionsForUser("alice")
	if len(sessions) != 4 {
		t.Fatalf("SessionsForUser(alice) = %d, want 4", len(sessions))
	}
	for _, session := range sessions {
		if session.User != "alice" {
			t.Fatalf("session for wrong user: %+v", session)
		}
	}
}
EOF
go test -race -count=3 . ; rc=$? ; rm -f probe_race_test.go ; exit $rc
