package splice

import (
	"testing"
)

func TestResolveScopeMode(t *testing.T) {
	t.Setenv(scopeModeEnv, "")
	if on, err := scopeEnabled(); err != nil || !on {
		t.Fatalf("unset = %v, %v; want on, nil", on, err)
	}
	t.Setenv(scopeModeEnv, "on")
	if on, err := scopeEnabled(); err != nil || !on {
		t.Fatalf("on = %v, %v; want on, nil", on, err)
	}
	t.Setenv(scopeModeEnv, "off")
	if on, err := scopeEnabled(); err != nil || on {
		t.Fatalf("off = %v, %v; want off, nil", on, err)
	}
	t.Setenv(scopeModeEnv, "banana")
	if _, err := scopeEnabled(); err == nil {
		t.Fatal("invalid mode must fail loud")
	}
}
