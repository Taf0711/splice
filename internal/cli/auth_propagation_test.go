package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/credstore"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// seedStoredProviderKey writes key into a credential store rooted beside the
// returned config path, and returns deps fields wired at that path — the same
// layout applyStoredProviderKey resolves through in production.
func seedStoredProviderKey(t *testing.T, providerName, key string) (userConfigPath string) {
	t.Helper()
	t.Setenv("SPLICE_CRED_STORAGE", "encrypted-file") // never touch the real OS keychain in tests
	dir := t.TempDir()
	store, err := config.ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("ProviderKeyStoreAt: %v", err)
	}
	if err := store.Set(providerName, key); err != nil {
		t.Fatalf("seed stored key: %v", err)
	}
	return filepath.Join(dir, "config.json")
}

// applyStoredProviderKeyAt must fill APIKey from the credential store for an
// apiKeyStored profile — the single central helper the newProvider wrapper uses.
func TestApplyStoredProviderKeyAtFillsFromCredstore(t *testing.T) {
	configPath := seedStoredProviderKey(t, "echo", "sk-stored-test")
	ucp := func() (string, error) { return configPath, nil }

	stored, err := applyStoredProviderKeyAt(config.ProviderProfile{Name: "echo", APIKeyStored: true}, ucp)
	if err != nil {
		t.Fatalf("applyStoredProviderKeyAt: %v", err)
	}
	if stored.APIKey != "sk-stored-test" {
		t.Fatalf("APIKey = %q, want the stored key", stored.APIKey)
	}

	// A profile that did NOT opt into stored-key auth must be left unchanged —
	// a stale store entry must not silently reactivate credentials.
	plain, err := applyStoredProviderKeyAt(config.ProviderProfile{Name: "echo"}, ucp)
	if err != nil {
		t.Fatalf("applyStoredProviderKeyAt plain: %v", err)
	}
	if plain.APIKey != "" {
		t.Fatalf("non-stored profile APIKey = %q, want empty", plain.APIKey)
	}
}

// fillAppDeps wraps newProvider so EVERY surface that builds a runtime provider
// (buildProvider, the ACP builder — which is just deps.newProvider — and exec's
// escalation switcher) gets the stored key applied. Passing the raw resolved
// profile previously sent unauthenticated requests for apiKeyStored profiles,
// the default onboarding outcome. Testing the wrap covers all those surfaces.
type failingCredentialKeyring struct {
	cause error
}

func (f failingCredentialKeyring) Available() bool { return true }
func (f failingCredentialKeyring) Set(string, string, string) error {
	return nil
}
func (f failingCredentialKeyring) Get(string, string) (string, bool, error) {
	return "", false, f.cause
}
func (f failingCredentialKeyring) Delete(string, string) (bool, error) {
	return false, nil
}

func TestMockedKeyringReadErrorIsWrapped(t *testing.T) {
	cause := errors.New("keychain locked")
	store, err := credstore.New(credstore.Options{Storage: "keyring", Keyring: failingCredentialKeyring{cause: cause}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = config.ApplyStoredAPIKey(config.ProviderProfile{Name: "echo", APIKeyStored: true}, store)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "keyring backend") {
		t.Fatalf("error = %v, want wrapped keyring read failure", err)
	}
}

func TestFillAppDepsSurfacesStoredKeyReadError(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"auth":{"storage":"file"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	deps := fillAppDeps(appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			called = true
			return nil, nil
		},
	})
	_, err := deps.newProvider(config.ProviderProfile{Name: "echo", APIKeyStored: true})
	if err == nil || !strings.Contains(err.Error(), "file backend") {
		t.Fatalf("error = %v, want selected backend read error", err)
	}
	if called {
		t.Fatal("provider constructor ran after credential read failed")
	}
}

func TestFillAppDepsWrapsNewProviderWithStoredKey(t *testing.T) {
	configPath := seedStoredProviderKey(t, "echo", "sk-stored-wrap")

	var captured config.ProviderProfile
	deps := fillAppDeps(appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
		newProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			captured = profile
			return nil, nil // only the captured profile matters here
		},
	})

	if _, err := deps.newProvider(config.ProviderProfile{Name: "echo", APIKeyStored: true}); err != nil {
		t.Fatalf("wrapped newProvider: %v", err)
	}
	if captured.APIKey != "sk-stored-wrap" {
		t.Fatalf("wrapped newProvider built with APIKey = %q, want the stored key (unauthenticated regression)", captured.APIKey)
	}
}

// buildProvider (the TUI/exec STARTUP construction site) must export
// SPLICE_PROVIDER so children spawned at any point in the run are pinned to the
// parent's provider from launch — not only after an in-session switch. Without
// this, a provider switch persisted by another splice process mid-session moves
// new children onto a different provider (and credentials) than the parent.
func TestBuildProviderExportsActiveProviderEnv(t *testing.T) {
	t.Setenv(config.ActiveProviderEnv, "stale-from-elsewhere")
	// The exporter is injected (defaultAppDeps wires config.SetActiveProviderEnv;
	// fillAppDeps deliberately leaves it nil so ordinary tests never mutate the
	// process env). Inject the real one here to assert the actual env effect.
	deps := appDeps{
		exportActiveProvider: config.SetActiveProviderEnv,
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return nil, nil
		},
	}

	resolved := config.ResolvedConfig{Provider: config.ProviderProfile{Name: "pinned", ProviderKind: config.ProviderKindAnthropic, Model: "m"}}
	if _, err := buildProvider(resolved, deps); err != nil {
		t.Fatalf("buildProvider: %v", err)
	}
	if got := os.Getenv(config.ActiveProviderEnv); got != "pinned" {
		t.Fatalf("%s = %q after startup build, want %q (children would spawn on the stale provider)", config.ActiveProviderEnv, got, "pinned")
	}

	// A FAILED build must not move the pin: the process never committed to the
	// new provider, so children must keep resolving whatever was in effect.
	t.Setenv(config.ActiveProviderEnv, "still-current")
	failing := appDeps{
		exportActiveProvider: config.SetActiveProviderEnv,
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return nil, errors.New("boom")
		},
	}
	if _, err := buildProvider(resolved, failing); err == nil {
		t.Fatal("expected build error")
	}
	if got := os.Getenv(config.ActiveProviderEnv); got != "still-current" {
		t.Fatalf("%s = %q after failed build, want it untouched", config.ActiveProviderEnv, got)
	}

	// No provider profile at all → no provider, no pin change.
	if _, err := buildProvider(config.ResolvedConfig{}, deps); err != nil {
		t.Fatalf("buildProvider(empty): %v", err)
	}
	if got := os.Getenv(config.ActiveProviderEnv); got != "still-current" {
		t.Fatalf("%s = %q after profile-less build, want it untouched", config.ActiveProviderEnv, got)
	}
}

// A mid-run model switch previously rebuilt the provider from the PURE resolved
// profile (the stored key lives only in the credential store), so the escalated
// provider was keyless and the next completion call 401'd mid-run. Escalation is
// currently agent-loop-only and inert under the deterministic pipeline exec
// path, so this test drives agent.Run directly while still using the CLI
// fillAppDeps/newProvider wrapper that production escalation uses.
func TestRunExecEscalationSwitchKeepsStoredKey(t *testing.T) {
	if _, ok := mustUpgradeTarget(t, "claude-haiku-4.5"); !ok {
		t.Skip("registry has no upgrade target for claude-haiku-4.5")
	}

	configPath := seedStoredProviderKey(t, "echo", "sk-stored-escalation")
	var builtProfiles []config.ProviderProfile
	deps := fillAppDeps(appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
		newProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			builtProfiles = append(builtProfiles, profile)
			return &usageEmittingEscalatingProvider{escalate: len(builtProfiles) == 1}, nil
		},
	})

	profile := config.ProviderProfile{
		Name:         "echo",
		ProviderKind: config.ProviderKindAnthropic,
		Model:        "claude-haiku-4.5",
		APIKeyStored: true, // key lives ONLY in the credstore
	}
	provider, err := deps.newProvider(profile)
	if err != nil {
		t.Fatalf("initial provider build: %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(tools.NewEscalateModelTool())

	_, err = agent.Run(context.Background(), "escalate please", provider, agent.Options{
		Registry:       registry,
		Model:          profile.Model,
		MaxTurns:       3,
		PermissionMode: agent.PermissionModeAuto,
		ModelSwitcher: func(_ context.Context, modelID string) (agent.Provider, error) {
			switchedProfile := profile
			switchedProfile.Model = modelID
			return deps.newProvider(switchedProfile)
		},
	})
	if err != nil {
		t.Fatalf("agent.Run: %v", err)
	}
	if len(builtProfiles) < 2 {
		t.Fatalf("want at least 2 provider builds (initial + escalated), got %d", len(builtProfiles))
	}
	for i, profile := range builtProfiles {
		if profile.APIKey != "sk-stored-escalation" {
			t.Fatalf("provider build %d APIKey = %q, want the stored key (escalated build went out keyless)", i, profile.APIKey)
		}
	}
}
