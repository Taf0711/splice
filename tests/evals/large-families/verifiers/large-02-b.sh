# large-02 B: audit package gains RetentionDeficit reusing the enforcement
# cutoff/cap rules. Base+gold-A fails (no function), gold passes, wrong
# (reports kept-count instead of drop-count, or drops events) fails.
cat > internal/audit/probe_deficit_test.go <<'EOF'
package audit

import (
	"testing"
	"time"
)

func setClock(t *Trail, at time.Time) {
	t.clock = func() time.Time { return at }
}

func TestProbeRetentionDeficit(t *testing.T) {
	trail := NewTrail()
	fresh := time.Unix(5000, 0)
	setClock(trail, fresh)
	for i := 0; i < 5; i++ {
		trail.Record("a", "e", "o", "ok")
	}

	// Zero deficit: nothing would be dropped.
	if d := RetentionDeficit(trail, 24*time.Hour, 100); d != 0 {
		t.Fatalf("deficit = %d, want 0", d)
	}
	// Positive deficit: cap 3 means 2 would drop.
	if d := RetentionDeficit(trail, 24*time.Hour, 3); d != 2 {
		t.Fatalf("deficit = %d, want 2", d)
	}
	// Deficit by age: after the clock advances beyond the maxAge window,
	// all 5 events would drop.
	setClock(trail, fresh.Add(48*time.Hour))
	if d := RetentionDeficit(trail, 24*time.Hour, 100); d != 5 {
		t.Fatalf("deficit = %d, want 5", d)
	}
	// Enforcement must not have run: the trail still holds its events.
	if got := trail.Count(); got != 5 {
		t.Fatalf("deficit mutated the trail: %d events left", got)
	}
}
EOF
go test -count=1 ./internal/audit/ ; rc=$?
rm -f internal/audit/probe_deficit_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
