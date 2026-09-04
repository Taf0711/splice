# fam-06: SessionsForUser follows the audit's locking discipline. The probe
# runs under -race with concurrent readers plus a writer; the wrong variant
# mutates under RLock and the race detector fires.
cat > probe_race_test.go <<'EOF'
package main

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestProbeSessionsForUserRace(t *testing.T) {
	store := NewStore(time.Minute)
	store.now = func() time.Time { return time.Unix(1000, 0) }
	for i := 0; i < 4; i++ {
		store.Create(fmt.Sprintf("a%d", i), "alice")
	}
	store.Create("b0", "bob")

	var wg sync.WaitGroup
	stop := make(chan struct{})
	// Eight readers hammer the read path with no pauses: on the wrong
	// variant (mutation under RLock) the race detector is guaranteed a
	// collision window, not a lucky miss.
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = store.SessionsForUser("alice")
					_ = store.Len()
				}
			}
		}()
	}
	// A writer mutates the map while readers walk it: the discipline under
	// test (read paths never mutate) must hold, and dropping expired
	// entries takes the write lock.
	for w := 0; w < 200; w++ {
		store.Create("w", "writer")
		store.Delete("w")
	}
	close(stop)
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
go test -race -count=1 . ; rc=$? ; rm -f probe_race_test.go ; exit $rc