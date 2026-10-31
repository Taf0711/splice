# large-08 A: Add (s *Store) Expired(id string) bool in the session package: true wh
mkdir -p internal/session
cat > internal/session/probe_expiry_test.go <<'EOF'
package session

import (
	"testing"
	"time"
)

func TestProbeExpired(t *testing.T) {
	s := NewStore(time.Minute)
	at := time.Unix(1000, 0)
	s.now = func() time.Time { return at }
	s.Create("a", "ada")
	at = at.Add(time.Minute)
	if s.Expired("a") {
		t.Fatal("idle exactly ttl is still live")
	}
	at = at.Add(time.Second)
	if !s.Expired("a") {
		t.Fatal("idle past ttl must be expired")
	}
	if s.Len() != 1 {
		t.Fatal("Expired must not delete")
	}
	if !s.Expired("nobody") {
		t.Fatal("unknown id is expired")
	}
}
EOF
go test -count=1 ./internal/session/ ; rc=$?
rm -f internal/session/probe_expiry_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
