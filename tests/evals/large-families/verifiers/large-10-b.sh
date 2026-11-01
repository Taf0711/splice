# large-10 B: Add Healthy(p *Pool, min float64) bool in the worker package: true whe
mkdir -p internal/worker
cat > internal/worker/probe_healthy_test.go <<'EOF'
package worker

import (
	"errors"
	"testing"
)

func TestProbeHealthy(t *testing.T) {
	p := &Pool{}
	if Healthy(p, 0) {
		t.Fatal("a pool with no jobs is not healthy")
	}
	p.Submit(Job{Run: func() error { return nil }})
	p.Submit(Job{Run: func() error { return errors.New("x") }})
	if !Healthy(p, 0.5) {
		t.Fatal("rate 0.5 meets min 0.5")
	}
	if Healthy(p, 0.6) {
		t.Fatal("rate 0.5 misses min 0.6")
	}
}
EOF
go test -count=1 ./internal/worker/ ; rc=$?
rm -f internal/worker/probe_healthy_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
