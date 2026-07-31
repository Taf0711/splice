package cli

import (
	"testing"

	"github.com/Taf0711/splice/internal/config"
)

func TestResolveWorkspaceTrustFreshAskReturnsUndecided(t *testing.T) {
	setCLIUserConfigRoot(t)
	trusted, decision, persist, store, err := resolveWorkspaceTrust(t.TempDir(), "ask", false, false)
	if err != nil {
		t.Fatalf("resolveWorkspaceTrust() error = %v", err)
	}
	if trusted {
		t.Fatal("trusted = true, want false")
	}
	if decision != config.TrustUndecided {
		t.Fatalf("decision = %v, want TrustUndecided", decision)
	}
	if persist {
		t.Fatal("persist = true, want false")
	}
	if store == nil {
		t.Fatal("store = nil, want loaded trust store")
	}
}

func TestShouldPromptWorkspaceTrust(t *testing.T) {
	base := func() bool {
		return shouldPromptWorkspaceTrust(config.TrustUndecided, "ask", false, false, false, true)
	}
	if !base() {
		t.Fatal("baseline predicate = false, want true")
	}
	tests := []struct {
		name string
		call func() bool
	}{
		{"decision", func() bool {
			return shouldPromptWorkspaceTrust(config.TrustDeclined, "ask", false, false, false, true)
		}},
		{"setting", func() bool {
			return shouldPromptWorkspaceTrust(config.TrustUndecided, "always", false, false, false, true)
		}},
		{"trust flag", func() bool {
			return shouldPromptWorkspaceTrust(config.TrustUndecided, "ask", true, false, false, true)
		}},
		{"no trust flag", func() bool {
			return shouldPromptWorkspaceTrust(config.TrustUndecided, "ask", false, true, false, true)
		}},
		{"environment", func() bool {
			return shouldPromptWorkspaceTrust(config.TrustUndecided, "ask", false, false, true, true)
		}},
		{"stdin", func() bool {
			return shouldPromptWorkspaceTrust(config.TrustUndecided, "ask", false, false, false, false)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.call() {
				t.Fatal("predicate = true, want false")
			}
		})
	}
}
