package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/tui"
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

func TestInteractiveTrustPromptRunsInsideMainTUI(t *testing.T) {
	setCLIUserConfigRoot(t)
	t.Setenv("SPLICE_TRUST_WORKSPACE", "")
	var got tui.Options
	launched := false
	var stdout, stderr bytes.Buffer
	exit := runWithDeps(nil, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return t.TempDir(), nil },
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{MaxTurns: 3}, nil
		},
		runTUI: func(_ context.Context, options tui.Options) int {
			launched = true
			got = options
			return 0
		},
	})
	if exit != 0 || !launched {
		t.Fatalf("exit = %d, launched = %v, stderr = %q", exit, launched, stderr.String())
	}
	// An explicitly empty environment value counts as a decision source. Clear it
	// and check the predicate separately because test stdin is not a terminal.
	if got.TrustPrompt {
		t.Fatal("TrustPrompt = true with an explicit environment value")
	}
	if !shouldPromptWorkspaceTrust(config.TrustUndecided, "ask", false, false, false, true) {
		t.Fatal("interactive undecided trust did not request the main TUI prompt")
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
