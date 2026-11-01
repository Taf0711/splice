# large-03 B: Add OverageCents(m *Meter, accountID, sku string, included int, unitCe
mkdir -p internal/billing
cat > internal/billing/probe_overage_cents_test.go <<'EOF'
package billing

import "testing"

func TestProbeOverageCents(t *testing.T) {
	m := NewMeter()
	m.Record("acct", "api", 13)
	m.Record("acct", "storage", 40)
	if got := OverageCents(m, "acct", "api", 10, 5); got != 15 {
		t.Fatalf("OverageCents = %d, want 15 (3 units over x 5c)", got)
	}
	if got := OverageCents(m, "acct", "api", 20, 5); got != 0 {
		t.Fatalf("OverageCents under = %d, want 0", got)
	}
}
EOF
go test -count=1 ./internal/billing/ ; rc=$?
rm -f internal/billing/probe_overage_cents_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
