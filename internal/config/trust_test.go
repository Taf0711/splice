package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrustStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	store, err := LoadTrustStore(path)
	if err != nil {
		t.Fatalf("LoadTrustStore() error = %v", err)
	}
	if got := store.IsTrusted(dir); got != TrustUndecided {
		t.Fatalf("IsTrusted() = %v, want Undecided", got)
	}

	workspace := filepath.Join(dir, "workspace")
	if err := store.SetTrusted(workspace, true); err != nil {
		t.Fatalf("SetTrusted() error = %v", err)
	}
	if err := store.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	reloaded, err := LoadTrustStore(path)
	if err != nil {
		t.Fatalf("LoadTrustStore() second error = %v", err)
	}
	if got := reloaded.IsTrusted(workspace); got != TrustTrusted {
		t.Fatalf("IsTrusted() = %v, want Trusted", got)
	}
}

func TestTrustStoreAncestorLookup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	store, err := LoadTrustStore(path)
	if err != nil {
		t.Fatalf("LoadTrustStore() error = %v", err)
	}

	parent := filepath.Join(dir, "parent")
	child := filepath.Join(parent, "child", "grandchild")

	if err := store.SetTrusted(parent, true); err != nil {
		t.Fatalf("SetTrusted() error = %v", err)
	}
	if err := store.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	reloaded, err := LoadTrustStore(path)
	if err != nil {
		t.Fatalf("LoadTrustStore() error = %v", err)
	}

	if got := reloaded.IsTrusted(child); got != TrustTrusted {
		t.Fatalf("IsTrusted(child) = %v, want Trusted", got)
	}
}

func TestTrustStoreClosestAncestorWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	store, err := LoadTrustStore(path)
	if err != nil {
		t.Fatalf("LoadTrustStore() error = %v", err)
	}

	parent := filepath.Join(dir, "parent")
	declined := filepath.Join(parent, "declined")

	if err := store.SetTrusted(parent, true); err != nil {
		t.Fatalf("SetTrusted() error = %v", err)
	}
	if err := store.SetTrusted(declined, false); err != nil {
		t.Fatalf("SetTrusted() error = %v", err)
	}

	if got := store.IsTrusted(filepath.Join(declined, "child")); got != TrustDeclined {
		t.Fatalf("IsTrusted() = %v, want Declined", got)
	}
}

func TestTrustStoreCorruptFileStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	store, err := LoadTrustStore(path)
	if err != nil {
		t.Fatalf("LoadTrustStore() error = %v", err)
	}
	if got := store.IsTrusted(dir); got != TrustUndecided {
		t.Fatalf("IsTrusted() = %v, want Undecided", got)
	}
}

func TestTrustStoreMissingFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "trust.json")
	store, err := LoadTrustStore(path)
	if err != nil {
		t.Fatalf("LoadTrustStore() error = %v", err)
	}
	if got := store.IsTrusted(t.TempDir()); got != TrustUndecided {
		t.Fatalf("IsTrusted() = %v, want Undecided", got)
	}
}

func TestTrustStoreDeclinedPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	store, err := LoadTrustStore(path)
	if err != nil {
		t.Fatalf("LoadTrustStore() error = %v", err)
	}

	workspace := filepath.Join(dir, "workspace")
	if err := store.SetTrusted(workspace, false); err != nil {
		t.Fatalf("SetTrusted() error = %v", err)
	}
	if err := store.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	reloaded, err := LoadTrustStore(path)
	if err != nil {
		t.Fatalf("LoadTrustStore() error = %v", err)
	}
	if got := reloaded.IsTrusted(workspace); got != TrustDeclined {
		t.Fatalf("IsTrusted() = %v, want Declined", got)
	}
}

func TestResolveTrustPrecedence(t *testing.T) {
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")

	trustedStore := func() *TrustStore {
		path := filepath.Join(dir, "trusted.json")
		store, err := LoadTrustStore(path)
		if err != nil {
			t.Fatalf("LoadTrustStore() error = %v", err)
		}
		if err := store.SetTrusted(workspace, true); err != nil {
			t.Fatalf("SetTrusted() error = %v", err)
		}
		return store
	}

	tests := []struct {
		name        string
		store       *TrustStore
		trustFlag   bool
		noTrustFlag bool
		envValue    string
		setting     string
		want        TrustDecision
		wantPersist bool
	}{
		{
			name:        "trust flag overrides everything",
			store:       trustedStore(),
			trustFlag:   true,
			noTrustFlag: false,
			envValue:    "0",
			setting:     "never",
			want:        TrustTrusted,
			wantPersist: true,
		},
		{
			name:        "no-trust flag overrides env and setting",
			store:       trustedStore(),
			trustFlag:   false,
			noTrustFlag: true,
			envValue:    "1",
			setting:     "always",
			want:        TrustDeclined,
			wantPersist: false,
		},
		{
			name:        "env 1 overrides store and setting",
			store:       trustedStore(),
			trustFlag:   false,
			noTrustFlag: false,
			envValue:    "1",
			setting:     "never",
			want:        TrustTrusted,
			wantPersist: false,
		},
		{
			name:        "env 0 overrides store and setting",
			store:       trustedStore(),
			trustFlag:   false,
			noTrustFlag: false,
			envValue:    "0",
			setting:     "always",
			want:        TrustDeclined,
			wantPersist: false,
		},
		{
			name:        "saved trusted decision overrides setting",
			store:       trustedStore(),
			trustFlag:   false,
			noTrustFlag: false,
			envValue:    "",
			setting:     "never",
			want:        TrustTrusted,
			wantPersist: false,
		},
		{
			name:        "setting always when nothing else",
			store:       nil,
			trustFlag:   false,
			noTrustFlag: false,
			envValue:    "",
			setting:     "always",
			want:        TrustTrusted,
			wantPersist: false,
		},
		{
			name:        "setting never when nothing else",
			store:       nil,
			trustFlag:   false,
			noTrustFlag: false,
			envValue:    "",
			setting:     "never",
			want:        TrustDeclined,
			wantPersist: false,
		},
		{
			name:        "setting ask yields undecided",
			store:       nil,
			trustFlag:   false,
			noTrustFlag: false,
			envValue:    "",
			setting:     "ask",
			want:        TrustUndecided,
			wantPersist: false,
		},
		{
			name:        "empty setting yields undecided",
			store:       nil,
			trustFlag:   false,
			noTrustFlag: false,
			envValue:    "",
			setting:     "",
			want:        TrustUndecided,
			wantPersist: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, persist := ResolveTrust(workspace, tt.store, tt.setting, tt.trustFlag, tt.noTrustFlag, tt.envValue)
			if got != tt.want || persist != tt.wantPersist {
				t.Fatalf("ResolveTrust() = (%v, %v), want (%v, %v)", got, persist, tt.want, tt.wantPersist)
			}
		})
	}
}

func TestResolveTrustNilStore(t *testing.T) {
	got, persist := ResolveTrust("/workspace", nil, "always", false, false, "")
	if got != TrustTrusted || persist != false {
		t.Fatalf("ResolveTrust() = (%v, %v), want (Trusted, false)", got, persist)
	}
}
