# large-04 B: Add PlanChangeCost(fromPlan, toPlan string, daysLeft int) (int64, bool
mkdir -p internal/billing
cat > internal/billing/probe_plan_change_test.go <<'EOF'
package billing

import "testing"

func TestProbePlanChangeCost(t *testing.T) {
	got, ok := PlanChangeCost("team", "scale", 15)
	if !ok || got != 3500 {
		t.Fatalf("team->scale 15d = %d,%v, want 3500,true", got, ok)
	}
	got, ok = PlanChangeCost("scale", "team", 7)
	if !ok || got != -1634 {
		t.Fatalf("scale->team 7d = %d,%v, want -1634,true", got, ok)
	}
	if _, ok := PlanChangeCost("team", "gold", 10); ok {
		t.Fatal("unknown plan must report false")
	}
}
EOF
go test -count=1 ./internal/billing/ ; rc=$?
rm -f internal/billing/probe_plan_change_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
