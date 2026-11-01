#!/bin/bash
# Gold solution for large-03 Task B.
set -e
cd "$1"
mkdir -p internal/billing
cat > internal/billing/overage_cents.go <<'GOEOF'
package billing

// OverageCents charges unitCents per unit beyond the included amount.
func OverageCents(m *Meter, accountID, sku string, included int, unitCents int64) int64 {
	return int64(OverageUnits(m, accountID, sku, included)) * unitCents
}
GOEOF
