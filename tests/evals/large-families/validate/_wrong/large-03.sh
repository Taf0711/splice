#!/bin/bash
# Wrong Task B for large-03: re-derives the rule instead of reusing it.
set -e
cd "$1"
mkdir -p internal/billing
cat > internal/billing/overage_cents.go <<'GOEOF'
package billing

// OverageCents re-derives the overage over every SKU of the account.
func OverageCents(m *Meter, accountID, sku string, included int, unitCents int64) int64 {
	total := 0
	for _, n := range m.Snapshot(accountID) {
		total += n
	}
	if total <= included {
		return 0
	}
	return int64(total-included) * unitCents
}
GOEOF
