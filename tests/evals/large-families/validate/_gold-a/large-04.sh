#!/bin/bash
# Gold solution for large-04 Task A.
set -e
cd "$1"
mkdir -p internal/billing
cat > internal/billing/prorate_days.go <<'GOEOF'
package billing

// ProrateDays charges days of a 30-day month, truncated to whole cents.
func ProrateDays(monthlyCents int64, days int) int64 {
	if days < 0 {
		return 0
	}
	return monthlyCents * int64(days) / 30
}
GOEOF
