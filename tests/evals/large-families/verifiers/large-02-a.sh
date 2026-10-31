# large-02 A: audit Trail gains EnforceRetention. Base fails, gold passes,
# wrong (keeps OLDEST events, or wrong count) fails.
mkdir -p internal/audit
cat > internal/audit/probe_retention_test.go <<'EOF'
package audit

import (
	"testing"
	"time"
)

func TestProbeEnforceRetention(t *testing.T) {
	trail := NewTrail()
	now := time.Unix(2000, 0)
	// 3 old events (beyond the age cutoff), 2 fresh events. Every record
	// happens under an explicit clock so the ages are exact.
	old := now.Add(-48 * time.Hour)
	fresh := now.Add(-time.Hour)
	setClock(trail, old)
	trail.Record("a", "old0", "o1", "ok")
	setClock(trail, old.Add(time.Minute))
	trail.Record("a", "old1", "o2", "ok")
	setClock(trail, old.Add(2*time.Minute))
	trail.Record("a", "old2", "o3", "ok")
	setClock(trail, fresh)
	trail.Record("a", "fresh1", "o4", "ok")
	trail.Record("a", "fresh2", "o5", "ok")

	remaining := trail.EnforceRetention(24*time.Hour, 100)
	if remaining != 2 {
		t.Fatalf("age cutoff kept %d, want 2", remaining)
	}
	// Count cap: with 5 fresh events and cap 3, keep the newest 3.
	trail2 := NewTrail()
	setClock(trail2, fresh)
	for i := 0; i < 5; i++ {
		trail2.Record("a", "e", "o", "ok")
	}
	remaining2 := trail2.EnforceRetention(24*time.Hour, 3)
	if remaining2 != 3 {
		t.Fatalf("count cap kept %d, want 3", remaining2)
	}
}

// setClock is only available to package-internal tests; the probe runs in
// package audit so it can reach the unexported clock field.
func setClock(t *Trail, at time.Time) {
	t.clock = func() time.Time { return at }
}
EOF
go test -count=1 ./internal/audit/ ; rc=$?
rm -f internal/audit/probe_retention_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
