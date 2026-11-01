package splice

import (
	"testing"

	"github.com/Taf0711/splice/internal/flags"
)

// TestScopeEnabledReadsFlag pins the consumer: memory.scope, not the
// environment, decides the scope. Invalid legacy values are rejected by
// flag resolution, so they never reach this function.
func TestScopeEnabledReadsFlag(t *testing.T) {
	t.Setenv(treatmentEnv, "")
	if on, err := scopeEnabled(nil); err != nil || on {
		t.Fatalf("nil set = %v, %v; want off, nil", on, err)
	}
	if on, err := scopeEnabled(flags.Set{flags.MemoryScope: true}); err != nil || !on {
		t.Fatalf("on = %v, %v; want on, nil", on, err)
	}
	if on, err := scopeEnabled(flags.Set{flags.MemoryScope: false}); err != nil || on {
		t.Fatalf("off = %v, %v; want off, nil", on, err)
	}
}
