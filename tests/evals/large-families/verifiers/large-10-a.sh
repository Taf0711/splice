# large-10 A: Add (p *Pool) SuccessRate() float64 in the worker package: completed j
mkdir -p internal/worker
cat > internal/worker/probe_success_rate_test.go <<'EOF'
package worker

import (
	"errors"
	"testing"
)

func TestProbeSuccessRate(t *testing.T) {
	p := &Pool{}
	if got := p.SuccessRate(); got != 0 {
		t.Fatalf("empty SuccessRate = %v", got)
	}
	p.Submit(Job{Run: func() error { return nil }})
	p.Submit(Job{Run: func() error { return nil }})
	p.Submit(Job{Run: func() error { return nil }})
	p.Submit(Job{Run: func() error { return errors.New("x") }})
	if got := p.SuccessRate(); got != 0.75 {
		t.Fatalf("SuccessRate = %v, want 0.75", got)
	}
}
EOF
go test -count=1 ./internal/worker/ ; rc=$?
rm -f internal/worker/probe_success_rate_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
