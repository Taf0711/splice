#!/bin/bash
# Gold solution for large-04 Task B.
set -e
cd "$1"
mkdir -p internal/billing
cat > internal/billing/plan_change.go <<'GOEOF'
package billing

// PlanChangeCost is the new plan's prorated charge minus the old plan's.
func PlanChangeCost(fromPlan, toPlan string, daysLeft int) (int64, bool) {
	from, ok := Lookup(fromPlan)
	if !ok {
		return 0, false
	}
	to, ok := Lookup(toPlan)
	if !ok {
		return 0, false
	}
	return ProrateDays(to.MonthlyCents, daysLeft) - ProrateDays(from.MonthlyCents, daysLeft), true
}
GOEOF
