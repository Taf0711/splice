package admin

import (
	"slices"
	"testing"

	"demo/internal/audit"
	"demo/internal/auth"
	"demo/internal/billing"
)

func TestEveryRoleHasPermissions(t *testing.T) {
	for _, role := range auth.Roles {
		if len(RolePermissions[role]) == 0 {
			t.Errorf("role %q grants no permissions in the admin console", role)
		}
	}
}

func TestPricingPageShowsEveryPlan(t *testing.T) {
	for _, plan := range billing.Catalog {
		if !slices.Contains(PricingOrder, plan.Name) {
			t.Errorf("plan %q is sold but the pricing page does not show it", plan.Name)
		}
	}
	for _, name := range PricingOrder {
		if _, ok := billing.Lookup(name); !ok {
			t.Errorf("the pricing page shows %q, which is not a plan", name)
		}
	}
}

func TestEveryAuditActionHasALabel(t *testing.T) {
	for _, action := range audit.Actions {
		if ActionLabels[action] == "" {
			t.Errorf("audit action %q has no console label", action)
		}
	}
}
