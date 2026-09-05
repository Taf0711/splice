# large-01 B: audit package gains LatestDunningEvent (latest-match query).
# Base+gold-A fails (no function), gold passes, wrong (first match) fails.
mkdir -p internal/audit
cat > internal/audit/probe_latest_test.go <<'EOF'
package audit

import (
	"testing"
	"time"
)

func TestProbeLatestDunningEvent(t *testing.T) {
	trail := NewTrail()
	trail.Record("a1", "other.action", "acct-1", "ok")
	trail.Record("a2", "billing.dunning.notice", "acct-1", "ok")
	time.Sleep(2 * time.Millisecond)
	trail.Record("a3", "billing.dunning.notice", "acct-1", "ok")

	got := LatestDunningEvent(trail, "billing.dunning.notice")
	if got == nil {
		t.Fatal("LatestDunningEvent returned nil")
	}
	if got.Actor != "a3" {
		t.Fatalf("latest event actor = %q, want a3", got.Actor)
	}
	if LatestDunningEvent(trail, "nonexistent.action") != nil {
		t.Fatal("unknown action must return nil")
	}
}
EOF
go test -count=1 ./internal/audit/ ; rc=$?
rm -f internal/audit/probe_latest_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
