package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRejectsInvalidUserAuthStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"auth":{"storage":"bogus"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(ResolveOptions{UserConfigPath: path, Env: map[string]string{}}); err == nil {
		t.Fatal("invalid user auth.storage should fail")
	}
}

func TestResolveAuthStorageAtPrecedence(t *testing.T) {
	for _, envName := range []string{CredentialStorageEnv, OAuthStorageEnv} {
		t.Run(envName, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(`{"auth":{"storage":"file"}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(envName, "keyring")
			got, err := ResolveAuthStorageAt(path, envName)
			if err != nil || got != "file" {
				t.Fatalf("config over env = %q,%v, want file,nil", got, err)
			}

			if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err = ResolveAuthStorageAt(path, envName)
			if err != nil || got != "keyring" {
				t.Fatalf("env over auto = %q,%v, want keyring,nil", got, err)
			}
		})
	}
}

func TestProviderCommandAuthStorageDoesNotMerge(t *testing.T) {
	dst := FileConfig{Auth: AuthConfig{Storage: "encrypted-file"}}
	mergeConfig(&dst, FileConfig{Auth: AuthConfig{Storage: "file"}})
	if dst.Auth.Storage != "encrypted-file" {
		t.Fatalf("auth storage = %q, provider command changed user auth storage", dst.Auth.Storage)
	}
}

func TestInvalidProjectAuthStorageIsIgnored(t *testing.T) {
	project := filepath.Join(t.TempDir(), "project.json")
	if err := os.WriteFile(project, []byte(`{"auth":{"storage":"bogus"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve(ResolveOptions{ProjectConfigPath: project, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("project auth.storage should be ignored: %v", err)
	}
	if resolved.Auth.Storage != "" {
		t.Fatalf("auth storage = %q, want empty", resolved.Auth.Storage)
	}
}

func TestProjectAuthStorageDoesNotMerge(t *testing.T) {
	user := filepath.Join(t.TempDir(), "user.json")
	project := filepath.Join(t.TempDir(), "project.json")
	provider := `"activeProvider":"local","providers":[{"name":"local","catalogID":"ollama","model":"qwen"}]`
	if err := os.WriteFile(user, []byte(`{`+provider+`,"auth":{"storage":"encrypted-file"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(project, []byte(`{"auth":{"storage":"file"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve(ResolveOptions{UserConfigPath: user, ProjectConfigPath: project, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Auth.Storage != "encrypted-file" {
		t.Fatalf("auth storage = %q, project config changed user auth storage", resolved.Auth.Storage)
	}
}

func TestSetAuthStorageRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"maxTurns":12}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetAuthStorage(path, "encrypted-file"); err != nil {
		t.Fatal(err)
	}
	var cfg FileConfig
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.Storage != "encrypted-file" || cfg.MaxTurns != 12 {
		t.Fatalf("config = %#v", cfg)
	}
}
