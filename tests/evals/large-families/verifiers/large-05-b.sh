# large-05 B: Add QueueWelcome(d *Dispatcher, to string) in the notifications packag
mkdir -p internal/notifications
cat > internal/notifications/probe_queue_welcome_test.go <<'EOF'
package notifications

import "testing"

func TestProbeQueueWelcome(t *testing.T) {
	d := NewDispatcher()
	QueueWelcome(d, "ada")
	out := d.Flush()
	if len(out) != 1 || out[0] != "email|ada|your account is ready" {
		t.Fatalf("Flush = %v", out)
	}
}
EOF
go test -count=1 ./internal/notifications/ ; rc=$?
rm -f internal/notifications/probe_queue_welcome_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
