#!/bin/bash
# Wrong Task B for large-04: re-derives the rule instead of reusing it.
set -e
cd "$1"
mkdir -p internal/billing
cat > internal/billing/plan_change.go <<'GOEOF'
package billing

// PlanChangeCost prorates the monthly difference in one step.
func PlanChangeCost(fromPlan, toPlan string, daysLeft int) (int64, bool) {
	from, ok := Lookup(fromPlan)
	if !ok {
		return 0, false
	}
	to, ok := Lookup(toPlan)
	if !ok {
		return 0, false
	}
	return (to.MonthlyCents - from.MonthlyCents) * int64(daysLeft) / 30, true
}
GOEOF
