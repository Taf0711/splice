package oauth

import (
	"errors"
	"strings"
	"testing"
)

// fakeKR is an in-memory KeyringClient for exercising the keyring backend
// without touching a real OS keychain.
type fakeKR struct {
	data   map[string]string
	getErr error
}

func newFakeKR() *fakeKR { return &fakeKR{data: map[string]string{}} }

func (f *fakeKR) Get(service, account string) (string, bool, error) {
	if f.getErr != nil {
		return "", false, f.getErr
	}
	v, ok := f.data[service+"/"+account]
	return v, ok, nil
}
func (f *fakeKR) Set(service, account, secret string) error {
	f.data[service+"/"+account] = secret
	return nil
}
func (f *fakeKR) Delete(service, account string) (bool, error) {
	key := service + "/" + account
	_, ok := f.data[key]
	delete(f.data, key)
	return ok, nil
}

func TestFirstStoredPropagatesKeyringReadError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cause := errors.New("keychain locked")
	store, err := NewStore(StoreOptions{Storage: "keyring", Keyring: &fakeKR{data: map[string]string{}, getErr: cause}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = FirstStored(store, []string{"chatgpt"})
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want wrapped keychain error", err)
	}
	if !strings.Contains(err.Error(), "keyring") || !strings.Contains(err.Error(), "provider:chatgpt") {
		t.Fatalf("error = %q, want backend and key", err)
	}
}

func TestStoreKeyringBackendRoundTrip(t *testing.T) {
	// Keep the cross-process keyring lock file inside a temp config dir.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatalf("NewStore(keyring): %v", err)
	}
	if !strings.HasPrefix(s.FilePath(), "keyring:") {
		t.Fatalf("FilePath = %q, want keyring identifier", s.FilePath())
	}

	if err := s.Save(ProviderKey("demo"), Token{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := s.Load(ProviderKey("demo"))
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if got.AccessToken != "a" || got.RefreshToken != "r" {
		t.Fatalf("Load = %#v", got)
	}

	// The blob is stored base64-encoded, so the raw JSON field names never appear.
	raw := kr.data[keyringService+"/"+keyringAccount]
	if raw == "" {
		t.Fatal("nothing stored in keyring")
	}
	if strings.Contains(raw, "access_token") {
		t.Fatalf("keyring blob is not encoded: %s", raw)
	}

	removed, err := s.Delete(ProviderKey("demo"))
	if err != nil || !removed {
		t.Fatalf("Delete: removed=%v err=%v", removed, err)
	}
	if _, ok, _ := s.Load(ProviderKey("demo")); ok {
		t.Fatal("token still present after delete")
	}
}

func TestNewStoreStorageSelection(t *testing.T) {
	// Unknown storage is rejected (fail closed).
	if _, err := NewStore(StoreOptions{Storage: "bogus"}); err == nil {
		t.Fatal("unknown storage should error")
	}
	// SPLICE_OAUTH_STORAGE selects the keyring (with an injected client).
	s, err := NewStore(StoreOptions{
		Env:     map[string]string{"SPLICE_OAUTH_STORAGE": "keyring"},
		Keyring: newFakeKR(),
	})
	if err != nil {
		t.Fatalf("NewStore(env keyring): %v", err)
	}
	if !strings.HasPrefix(s.FilePath(), "keyring:") {
		t.Fatalf("env did not select keyring backend: %q", s.FilePath())
	}
	// Explicit file storage remains the plaintext opt-out.
	fileStore, err := NewStore(StoreOptions{Storage: "file", FilePath: t.TempDir() + "/oauth-tokens.json"})
	if err != nil {
		t.Fatalf("NewStore(file): %v", err)
	}
	if strings.HasPrefix(fileStore.FilePath(), "keyring:") {
		t.Fatalf("explicit file backend should not use keyring, got %q", fileStore.FilePath())
	}
}

func TestStoreKeyringStatus(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ProviderKey("demo"), Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	statuses, err := s.Status(KeyPrefixProvider)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Key != ProviderKey("demo") || !statuses[0].HasToken {
		t.Fatalf("status = %#v", statuses)
	}
}
