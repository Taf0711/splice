# large-06 A: Add (t *Throttle) Remaining(to string) int in the notifications packag
mkdir -p internal/notifications
cat > internal/notifications/probe_remaining_test.go <<'EOF'
package notifications

import "testing"

func TestProbeRemaining(t *testing.T) {
	th := NewThrottle(3)
	th.Allow("a")
	if got := th.Remaining("a"); got != 2 {
		t.Fatalf("Remaining(a) = %d, want 2", got)
	}
	if got := th.Remaining("a"); got != 2 {
		t.Fatalf("Remaining must not use allowance, second call = %d", got)
	}
	if got := th.Remaining("b"); got != 3 {
		t.Fatalf("Remaining(b) = %d, want 3", got)
	}
}
EOF
go test -count=1 ./internal/notifications/ ; rc=$?
rm -f internal/notifications/probe_remaining_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
