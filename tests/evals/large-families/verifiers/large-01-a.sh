# large-01 A: billing Service gains RecordDunningNotice advancing per-account
# dunning levels. Base fails (no method), gold passes, wrong (shared level
# across accounts) fails.
mkdir -p internal/billing
cat > internal/billing/probe_dunning_test.go <<'EOF'
package billing

import (
	"testing"
)

func TestProbeRecordDunningNotice(t *testing.T) {
	svc := NewService()
	if level := svc.RecordDunningNotice("acct-1"); level != LevelFirst {
		t.Fatalf("first notice level = %v, want LevelFirst", level)
	}
	if level := svc.RecordDunningNotice("acct-1"); level != LevelFinal {
		t.Fatalf("second notice level = %v, want LevelFinal", level)
	}
	// Account isolation: a different account starts fresh.
	if level := svc.RecordDunningNotice("acct-2"); level != LevelFirst {
		t.Fatalf("acct-2 first notice level = %v, want LevelFirst", level)
	}
}

func TestProbeRecordDunningNoticeIsolation(t *testing.T) {
	svc := NewService()
	svc.RecordDunningNotice("a")
	svc.RecordDunningNotice("a")
	svc.RecordDunningNotice("b")
	svc.RecordDunningNotice("b")
	svc.RecordDunningNotice("b")
	if level := svc.RecordDunningNotice("a"); level != LevelSuspended {
		t.Fatalf("a after two notices = %v, want LevelSuspended", level)
	}
}
EOF
go test -count=1 ./internal/billing/ ; rc=$?
rm -f internal/billing/probe_dunning_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
