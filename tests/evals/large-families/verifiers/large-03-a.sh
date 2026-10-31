# large-03 A: Add OverageUnits(m *Meter, accountID, sku string, included int) int in
mkdir -p internal/billing
cat > internal/billing/probe_overage_test.go <<'EOF'
package billing

import "testing"

func TestProbeOverageUnits(t *testing.T) {
	m := NewMeter()
	m.Record("acct", "api", 13)
	m.Record("acct", "seats", 2)
	m.Record("other", "api", 50)
	if got := OverageUnits(m, "acct", "api", 10); got != 3 {
		t.Fatalf("OverageUnits over = %d, want 3", got)
	}
	if got := OverageUnits(m, "acct", "seats", 5); got != 0 {
		t.Fatalf("OverageUnits under = %d, want 0", got)
	}
	if got := OverageUnits(m, "nobody", "api", 0); got != 0 {
		t.Fatalf("OverageUnits unknown = %d, want 0", got)
	}
}
EOF
go test -count=1 ./internal/billing/ ; rc=$?
rm -f internal/billing/probe_overage_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
