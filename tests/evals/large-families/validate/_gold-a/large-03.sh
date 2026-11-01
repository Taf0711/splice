#!/bin/bash
# Gold solution for large-03 Task A.
set -e
cd "$1"
mkdir -p internal/billing
cat > internal/billing/overage.go <<'GOEOF'
package billing

// OverageUnits reports the units of sku used beyond included, never negative.
func OverageUnits(m *Meter, accountID, sku string, included int) int {
	over := m.Snapshot(accountID)[sku] - included
	if over < 0 {
		return 0
	}
	return over
}
GOEOF
