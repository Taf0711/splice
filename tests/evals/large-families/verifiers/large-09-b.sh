# large-09 B: Add RetryPlan(b Backoff, attempts int) []time.Duration in the worker p
mkdir -p internal/worker
cat > internal/worker/probe_retry_plan_test.go <<'EOF'
package worker

import (
	"testing"
	"time"
)

func TestProbeRetryPlan(t *testing.T) {
	b := Backoff{Current: time.Second, Ceiling: 5 * time.Second}
	got := RetryPlan(b, 4)
	want := []time.Duration{2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	if len(got) != len(want) {
		t.Fatalf("RetryPlan = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RetryPlan = %v, want %v", got, want)
		}
	}
	if b.Current != time.Second {
		t.Fatal("RetryPlan must not change the caller's backoff")
	}
}
EOF
go test -count=1 ./internal/worker/ ; rc=$?
rm -f internal/worker/probe_retry_plan_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
