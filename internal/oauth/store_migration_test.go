package oauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type migrationKeyring struct {
	data   map[string]string
	setErr error
}

func newMigrationKeyring() *migrationKeyring {
	return &migrationKeyring{data: map[string]string{}}
}

func (k *migrationKeyring) Get(service, account string) (string, bool, error) {
	value, ok := k.data[service+"/"+account]
	return value, ok, nil
}

func (k *migrationKeyring) Set(service, account, secret string) error {
	if k.setErr != nil {
		return k.setErr
	}
	k.data[service+"/"+account] = secret
	return nil
}

func (k *migrationKeyring) Delete(service, account string) (bool, error) {
	key := service + "/" + account
	_, ok := k.data[key]
	delete(k.data, key)
	return ok, nil
}

func writeLegacyStore(t *testing.T, path string, token Token) {
	t.Helper()
	data, err := json.MarshalIndent(emptyStoreFileWithToken(ProviderKey("legacy"), token), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func migrationEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{"XDG_CONFIG_HOME": t.TempDir()}
}

func emptyStoreFileWithToken(key string, token Token) storeFile {
	return storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{key: token}}
}

func TestNewStoreAutoPolicyMatchesCredstore(t *testing.T) {
	// Account-level credentials had the weaker default before this regression fix.
	for _, test := range []struct {
		name string
		goos string
		want string
	}{
		{name: "macOS", goos: "darwin", want: "keyring"},
		{name: "other", goos: "linux", want: "encrypted-file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := NewStore(StoreOptions{
				FilePath: filepath.Join(t.TempDir(), "oauth-tokens.json"),
				GOOS:     test.goos,
				Keyring:  newMigrationKeyring(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := store.Backend(); got != test.want {
				t.Fatalf("backend = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNewStoreFileStorageRemainsPlaintextOptOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth-tokens.json")
	writeLegacyStore(t, path, Token{AccessToken: "legacy"})
	store, err := NewStore(StoreOptions{Storage: "file", FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if store.Backend() != "file" {
		t.Fatalf("backend = %q, want file", store.Backend())
	}
	if _, ok, err := store.Load(ProviderKey("legacy")); err != nil || !ok {
		t.Fatalf("plaintext opt-out could not read token: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("plaintext opt-out removed its file: %v", err)
	}
}

func TestStoreLoadDoesNotMigrateLegacyPlaintext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth-tokens.json")
	token := Token{AccessToken: "access", RefreshToken: "refresh", Account: "account"}
	writeLegacyStore(t, path, token)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeEntries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	keyring := newMigrationKeyring()
	store, err := NewStore(StoreOptions{Storage: "keyring", FilePath: path, Keyring: keyring, Env: migrationEnv(t)})
	if err != nil {
		t.Fatal(err)
	}
	// Regression: a read deleted the developer's live token file.
	got, ok, err := store.Load(ProviderKey("legacy"))
	if err != nil || !ok || !reflect.DeepEqual(got, token) {
		t.Fatalf("Load = %#v, ok=%v, err=%v", got, ok, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("legacy plaintext file was mutated or removed by Load: %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("legacy plaintext file changed after Load: before=%q after=%q", before, after)
	}
	afterEntries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(afterEntries) != len(beforeEntries) {
		t.Fatalf("Load mutated the legacy directory: before=%v after=%v", beforeEntries, afterEntries)
	}
	if _, ok, err := keyring.Get(keyringService, keyringAccount); err != nil || ok {
		t.Fatalf("Load performed migration: keyring record present=%v err=%v", ok, err)
	}
}

func TestMigratePlaintextProviderTokensLeavesPlaintextWhenBackendWriteFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth-tokens.json")
	token := Token{AccessToken: "access", RefreshToken: "refresh"}
	writeLegacyStore(t, path, token)
	keyring := newMigrationKeyring()
	keyring.setErr = errors.New("keychain locked")
	store, err := NewStore(StoreOptions{Storage: "keyring", FilePath: path, Keyring: keyring, Env: migrationEnv(t)})
	if err != nil {
		t.Fatal(err)
	}
	n, err := MigratePlaintextProviderTokens(path, store)
	if err != nil || n != 0 {
		t.Fatalf("migration with failing backend = %d,%v; want 0,nil", n, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		t.Fatalf("plaintext source was removed after failed migration: %v", err)
	}
	if _, ok, err := keyring.Get(keyringService, keyringAccount); err != nil || ok {
		t.Fatalf("failed migration wrote backend record: ok=%v err=%v", ok, err)
	}
	got, ok, err := store.Load(ProviderKey("legacy"))
	if err != nil || !ok || !reflect.DeepEqual(got, token) {
		t.Fatalf("Load after failed migration = %#v, ok=%v, err=%v", got, ok, err)
	}
}

func TestMigratePlaintextProviderTokensRemovesPlaintextAfterSuccessfulMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth-tokens.json")
	writeLegacyStore(t, path, Token{AccessToken: "access"})
	keyring := newMigrationKeyring()
	store, err := NewStore(StoreOptions{Storage: "keyring", FilePath: path, Keyring: keyring, Env: migrationEnv(t)})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := MigratePlaintextProviderTokens(path, store); err != nil || n != 1 {
		t.Fatalf("migration = %d,%v; want 1,nil", n, err)
	}
	if _, _, err := store.Load(ProviderKey("legacy")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plaintext file was not removed after successful migration: %v", err)
	}
}

func TestMigratePlaintextProviderTokensEncryptsInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth-tokens.json")
	token := Token{AccessToken: "access", RefreshToken: "refresh"}
	writeLegacyStore(t, path, token)
	store, err := NewStore(StoreOptions{Storage: "encrypted-file", FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := MigratePlaintextProviderTokens(path, store); err != nil || n != 1 {
		t.Fatalf("migration = %d,%v; want 1,nil", n, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(token.AccessToken)) {
		t.Fatal("encrypted migration left the access token in plaintext")
	}
	got, ok, err := store.Load(ProviderKey("legacy"))
	if err != nil || !ok || !reflect.DeepEqual(got, token) {
		t.Fatalf("Load = %#v, ok=%v, err=%v", got, ok, err)
	}
}

func TestStoreKeyringRoundTripsRealisticOAuthRecord(t *testing.T) {
	store, err := NewStore(StoreOptions{Storage: "keyring", Keyring: newMigrationKeyring(), Env: migrationEnv(t)})
	if err != nil {
		t.Fatal(err)
	}
	token := Token{
		AccessToken:  "access-token-with-realistic-length-and-claims-0123456789",
		RefreshToken: "refresh-token-with-realistic-length-0123456789",
		TokenType:    "Bearer",
		Scopes:       []string{"openid", "profile", "offline_access", "model.read", "model.write"},
		ExpiresAt:    time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
		Account:      "account@example.com",
		IDToken:      "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.payload.signature",
	}
	if err := store.Save(ProviderKey("realistic"), token); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Load(ProviderKey("realistic"))
	if err != nil || !ok || !reflect.DeepEqual(got, token) {
		t.Fatalf("round trip = %#v, ok=%v, err=%v", got, ok, err)
	}
}
