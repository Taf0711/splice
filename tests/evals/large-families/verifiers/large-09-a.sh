# large-09 A: Add (b *Backoff) Peek() time.Duration in the worker package: the delay
mkdir -p internal/worker
cat > internal/worker/probe_peek_test.go <<'EOF'
package worker

import (
	"testing"
	"time"
)

func TestProbePeek(t *testing.T) {
	b := &Backoff{Current: time.Second, Ceiling: 3 * time.Second}
	if got := b.Peek(); got != 2*time.Second {
		t.Fatalf("Peek = %v, want 2s", got)
	}
	if b.Current != time.Second {
		t.Fatal("Peek must not change the backoff")
	}
	b.Current = 2 * time.Second
	if got := b.Peek(); got != 3*time.Second {
		t.Fatalf("Peek at ceiling = %v, want 3s", got)
	}
}
EOF
go test -count=1 ./internal/worker/ ; rc=$?
rm -f internal/worker/probe_peek_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
