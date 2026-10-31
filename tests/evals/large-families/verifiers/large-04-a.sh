# large-04 A: Add ProrateDays(monthlyCents int64, days int) int64 in the billing pac
mkdir -p internal/billing
cat > internal/billing/probe_prorate_days_test.go <<'EOF'
package billing

import "testing"

func TestProbeProrateDays(t *testing.T) {
	if got := ProrateDays(2900, 15); got != 1450 {
		t.Fatalf("ProrateDays(2900,15) = %d, want 1450", got)
	}
	if got := ProrateDays(1000, 7); got != 233 {
		t.Fatalf("ProrateDays(1000,7) = %d, want 233 (truncated)", got)
	}
	if got := ProrateDays(1000, -3); got != 0 {
		t.Fatalf("ProrateDays negative = %d, want 0", got)
	}
}
EOF
go test -count=1 ./internal/billing/ ; rc=$?
rm -f internal/billing/probe_prorate_days_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
