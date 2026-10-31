# large-06 B: Add Headroom(t *Throttle, recipients []string) int in the notification
mkdir -p internal/notifications
cat > internal/notifications/probe_headroom_test.go <<'EOF'
package notifications

import "testing"

func TestProbeHeadroom(t *testing.T) {
	th := NewThrottle(3)
	th.Allow("a")
	if got := Headroom(th, []string{"a", "b"}); got != 5 {
		t.Fatalf("Headroom = %d, want 5", got)
	}
	if got := Headroom(th, []string{"a", "b"}); got != 5 {
		t.Fatalf("Headroom must not use allowance, second call = %d", got)
	}
}
EOF
go test -count=1 ./internal/notifications/ ; rc=$?
rm -f internal/notifications/probe_headroom_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
