package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/oauth"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// fakeKeyStore is a credential store double. It counts reads, and it can fail.
type fakeKeyStore struct {
	keys map[string]string
	err  error
	gets int
}

func (store *fakeKeyStore) Get(provider string) (string, bool, error) {
	store.gets++
	if store.err != nil {
		return "", false, store.err
	}
	key, ok := store.keys[provider]
	return key, ok, nil
}

// Keys match no redaction pattern, so a test that sees them in the list proves
// the hydration path, not the pattern path.
const (
	storedKey = "zq7Lm2Vt9Rp4Xw8K"
	envKey    = "Vb2Kx7Lq4Nm9Tr5P"
)

func opener(store config.APIKeyGetter, err error, calls *int) func() (config.APIKeyGetter, error) {
	return func() (config.APIKeyGetter, error) {
		*calls++
		return store, err
	}
}

func TestRunSecretValuesHydratesStoredKeyFromStore(t *testing.T) {
	store := &fakeKeyStore{keys: map[string]string{"stored": storedKey}}
	calls := 0
	primary := config.ProviderProfile{Name: "stored", APIKeyStored: true}
	got, err := runSecretValues(primary, nil, secretSources{openStore: opener(store, nil, &calls)})
	if err != nil {
		t.Fatalf("runSecretValues: %v", err)
	}
	if len(got) != 1 || got[0] != storedKey {
		t.Fatalf("secret values = %q, want the stored key", got)
	}
	if calls != 1 {
		t.Fatalf("store opened %d times, want once", calls)
	}
}

func TestRunSecretValuesStoreReadErrorFailsLoud(t *testing.T) {
	store := &fakeKeyStore{err: errors.New("keychain locked")}
	calls := 0
	primary := config.ProviderProfile{Name: "stored", APIKeyStored: true}
	_, err := runSecretValues(primary, nil, secretSources{openStore: opener(store, nil, &calls)})
	if err == nil || !strings.Contains(err.Error(), "keychain locked") || !strings.Contains(err.Error(), `provider "stored"`) {
		t.Fatalf("error = %v, want a named read failure", err)
	}
}

func TestRunSecretValuesOpensStoreOnlyWhenNeeded(t *testing.T) {
	calls := 0
	inline := config.ProviderProfile{Name: "inline", APIKey: storedKey}
	got, err := runSecretValues(inline, []config.ProviderProfile{{Name: "other", APIKey: envKey}}, secretSources{openStore: opener(nil, errors.New("must not open"), &calls)})
	if err != nil {
		t.Fatalf("runSecretValues: %v", err)
	}
	if calls != 0 {
		t.Fatalf("store opened %d times for profiles with inline keys", calls)
	}
	if len(got) != 2 {
		t.Fatalf("secret values = %q", got)
	}
}

func TestRunSecretValuesReadsEnvKey(t *testing.T) {
	t.Setenv("SPLICE_TEST_RUN_KEY", envKey)
	calls := 0
	primary := config.ProviderProfile{Name: "env", APIKeyEnv: "SPLICE_TEST_RUN_KEY"}
	got, err := runSecretValues(primary, nil, secretSources{openStore: opener(nil, nil, &calls)})
	if err != nil {
		t.Fatalf("runSecretValues: %v", err)
	}
	if len(got) != 1 || got[0] != envKey {
		t.Fatalf("secret values = %q, want the env key", got)
	}
}

// unreadableStoreDeps returns deps whose credential store fails every read. The
// provider has a stored key, so only a run that reads the store for redaction hits
// the failure.
func unreadableStoreDeps(t *testing.T) appDeps {
	t.Helper()
	cfg := execResolvedConfig()
	cfg.Provider.APIKey = ""
	cfg.Provider.APIKeyStored = true
	return appDeps{
		getwd: func() (string, error) { return t.TempDir(), nil },
		resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
			return cfg, nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return newExecStageAwareProvider(execStageProviderOptions{}), nil
		},
		keyStoreOpener: func() (config.APIKeyGetter, error) {
			return &fakeKeyStore{err: errors.New("keychain locked")}, nil
		},
	}
}

func TestRunExecWithoutServeIgnoresUnreadableStore(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "add a Hello function"}, &stdout, &stderr, unreadableStoreDeps(t))
	if exitCode != exitSuccess {
		t.Fatalf("run without --serve failed on the credential store: exit %d, stderr=%s", exitCode, stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "event surface") {
		t.Fatal("redaction read the credential store without --serve")
	}
}

func TestRunExecServeStopsBeforeURLWhenStoreUnreadable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "--serve", "add a Hello function"}, &stdout, &stderr, unreadableStoreDeps(t))
	if exitCode == exitSuccess {
		t.Fatalf("--serve run succeeded with an unreadable store; stderr=%s", stderr.String())
	}
	if !strings.Contains(stdout.String()+stderr.String(), "key for the event surface") {
		t.Fatalf("missing the named secret_error; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "[splice] surface: ") {
		t.Fatalf("surface URL printed for a run that cannot start: %s", stderr.String())
	}
}

// TestRunSecretValuesAddsStoredOAuthBearer: a keyless profile sends its stored
// OAuth login as the bearer, so the redaction list holds that token. The
// token is opaque: it matches no redaction pattern.
func TestRunSecretValuesAddsStoredOAuthBearer(t *testing.T) {
	const token = "Qm4Tz8Wc2Hn6Lp1R"
	path := filepath.Join(t.TempDir(), "tok.json")
	t.Setenv("SPLICE_OAUTH_STORAGE", "file")
	t.Setenv("SPLICE_OAUTH_TOKENS_PATH", path)
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Save(oauth.ProviderKey("chatgpt"), oauth.Token{AccessToken: token, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	calls := 0
	primary := config.ProviderProfile{Name: "inline", APIKey: storedKey}
	keyless := config.ProviderProfile{Name: "codex", CatalogID: "chatgpt"}
	got, err := runSecretValues(primary, []config.ProviderProfile{keyless}, secretSources{
		openStore:  opener(nil, errors.New("must not open"), &calls),
		oauthToken: storedOAuthToken(context.Background()),
	})
	if err != nil {
		t.Fatalf("runSecretValues: %v", err)
	}
	if !slices.Contains(got, token) || !slices.Contains(got, storedKey) {
		t.Fatalf("secret values = %q, want the OAuth token and the inline key", got)
	}
}

// TestRunSecretValuesFailsClosedOnlyForRoutedProfiles: a read error for a
// profile the run routes to stops the surface. A read error for a saved
// profile the run never routes to is skipped, because the run never sends
// that key.
func TestRunSecretValuesFailsClosedOnlyForRoutedProfiles(t *testing.T) {
	broken := &fakeKeyStore{err: errors.New("keychain locked")}
	primary := config.ProviderProfile{Name: "inline", APIKey: storedKey}
	saved := []config.ProviderProfile{{Name: "unused", APIKeyStored: true}, {Name: "staged", APIKeyStored: true}}
	calls := 0
	routedOnly := func(names ...string) func(string) bool {
		return func(name string) bool { return slices.Contains(names, name) }
	}
	got, err := runSecretValues(primary, saved[:1], secretSources{openStore: opener(broken, nil, &calls), routed: routedOnly("inline", "staged")})
	if err != nil {
		t.Fatalf("an unused profile's read error stopped the surface: %v", err)
	}
	if !slices.Equal(got, []string{storedKey}) {
		t.Fatalf("secret values = %q, want the primary key", got)
	}
	_, err = runSecretValues(primary, saved, secretSources{openStore: opener(broken, nil, &calls), routed: routedOnly("inline", "staged")})
	if err == nil || !strings.Contains(err.Error(), `provider "staged"`) {
		t.Fatalf("error = %v, want the routed profile's named read failure", err)
	}
	// Unknown routes treat every profile as routed.
	if _, err := runSecretValues(primary, saved[:1], secretSources{openStore: opener(broken, nil, &calls)}); err == nil {
		t.Fatal("with unknown routes, every profile must load")
	}
}

// TestRunExecServeSkipsUnusedProfileKeyError: with --serve, an unreadable key
// of a saved profile that the run does not route to never stops the surface.
// The same profile behind a stage route stops it with a named error.
func TestRunExecServeSkipsUnusedProfileKeyError(t *testing.T) {
	for _, routed := range []bool{false, true} {
		t.Run(fmt.Sprintf("routed=%v", routed), func(t *testing.T) {
			configDir := t.TempDir()
			if routed {
				stage := `{"stages":{"test_generator":{"provider_profile":"spare","model":"m"}}}`
				if err := os.WriteFile(filepath.Join(configDir, "stage-models.json"), []byte(stage), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			deps := unreadableStoreDeps(t)
			cfg := execResolvedConfig()
			cfg.Providers = []config.ProviderProfile{cfg.Provider, {Name: "spare", APIKeyStored: true, ProviderKind: config.ProviderKindOpenAICompatible, BaseURL: "http://127.0.0.1/v1", Model: "m"}}
			deps.resolveConfig = func(string, config.Overrides) (config.ResolvedConfig, error) { return cfg, nil }
			deps.userConfigPath = func() (string, error) { return filepath.Join(configDir, "config.json"), nil }
			var stdout, stderr bytes.Buffer
			exitCode := runWithDeps([]string{"exec", "--serve", "add a Hello function"}, &stdout, &stderr, deps)
			named := strings.Contains(stdout.String()+stderr.String(), `provider "spare" key for the event surface`)
			if routed && (exitCode == exitSuccess || !named) {
				t.Fatalf("routed profile: exit %d, named error %v; stderr=%s", exitCode, named, stderr.String())
			}
			if !routed && (exitCode != exitSuccess || named) {
				t.Fatalf("unused profile stopped the run: exit %d; stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
			}
		})
	}
}
