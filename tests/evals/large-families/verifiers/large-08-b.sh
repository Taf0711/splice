# large-08 B: Add (s *Store) Sweep() int in the session package: delete every sessio
mkdir -p internal/session
cat > internal/session/probe_sweep_test.go <<'EOF'
package session

import (
	"testing"
	"time"
)

func TestProbeSweep(t *testing.T) {
	s := NewStore(time.Minute)
	at := time.Unix(1000, 0)
	s.now = func() time.Time { return at }
	s.Create("old", "ada")
	at = at.Add(30 * time.Second)
	s.Create("edge", "bob")
	at = at.Add(61 * time.Second)
	s.Create("new", "cy")
	at = at.Add(-1 * time.Second)
	if got := s.Sweep(); got != 1 {
		t.Fatalf("Sweep = %d, want 1 (only old is past ttl)", got)
	}
	if s.Len() != 2 {
		t.Fatalf("Len after sweep = %d, want 2", s.Len())
	}
}
EOF
go test -count=1 ./internal/session/ ; rc=$?
rm -f internal/session/probe_sweep_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
