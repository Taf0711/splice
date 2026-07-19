package stages

import (
	"testing"
)

func TestDefaultSecurityChecksIncludesGosec(t *testing.T) {
	checks := DefaultSecurityChecks()
	found := false
	for _, c := range checks {
		if c.Name() == "gosec" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DefaultSecurityChecks() missing gosec: %#v", checks)
	}
}

func TestDefaultSecurityChecksIncludesSarif(t *testing.T) {
	checks := DefaultSecurityChecks()
	found := false
	for _, c := range checks {
		if c.Name() == "sarif" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DefaultSecurityChecks() missing sarif: %#v", checks)
	}
}
