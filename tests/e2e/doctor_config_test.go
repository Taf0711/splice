package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/credstore"
)

// noKeyring makes the credential store use only the encrypted file. The
// seeding helper never reaches the OS keychain.
type noKeyring struct{}

func (noKeyring) Available() bool { return false }
func (noKeyring) Set(service, account, secret string) error {
	return errNoKeyring
}
func (noKeyring) Get(service, account string) (string, bool, error) {
	return "", false, errNoKeyring
}
func (noKeyring) Delete(service, account string) (bool, error) { return false, errNoKeyring }

type keyringErr string

func (e keyringErr) Error() string { return string(e) }

const errNoKeyring = keyringErr("keyring disabled in e2e")

// seedStoredKey writes FakeAPIKey into the scenario's encrypted-file store
// under the profile name "fake". It runs in the test process and never
// reads or writes the real user config directory.
func seedStoredKey(t *testing.T, s *Scenario) {
	t.Helper()
	store, err := credstore.New(credstore.Options{Dir: s.ConfigDir, Storage: "encrypted-file", Keyring: noKeyring{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("fake", FakeAPIKey); err != nil {
		t.Fatal(err)
	}
}

// TestD01StoredKeyDoctor is spec D-01: a profile whose key lives in the
// encrypted-file store passes doctor, never prints the key, and the
// connectivity probe sends the key in the auth header.
func TestD01StoredKeyDoctor(t *testing.T) {
	t.Parallel()
	s := NewScenario(t)
	seedStoredKey(t, s)
	cfg := s.DefaultUserConfig()
	profile := cfg["providers"].([]any)[0].(map[string]any)
	delete(profile, "apiKeyEnv")
	profile["apiKeyStored"] = true
	s.WriteUserConfig(cfg)

	r := s.Run("doctor")
	if strings.Contains(r.Stdout, "Overall: fail") {
		t.Errorf("doctor overall fail:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "[pass] provider.config") {
		t.Errorf("provider.config is not pass:\n%s", r.Stdout)
	}

	conn := s.Run("doctor", "--connectivity")
	if conn.ExitCode == 3 {
		t.Errorf("doctor --connectivity crashed: %s", conn.Stderr)
	}
	for name, out := range map[string]string{"doctor": r.Stdout + r.Stderr, "doctor --connectivity": conn.Stdout + conn.Stderr} {
		if strings.Contains(out, FakeAPIKey) {
			t.Errorf("%s printed the stored key", name)
		}
	}

	found := false
	for _, req := range s.Provider.Requests() {
		if req.Header.Get("Authorization") == "Bearer "+FakeAPIKey {
			found = true
		}
	}
	if !found {
		t.Errorf("connectivity probe did not reach the fake with the stored key; requests: %v", s.Provider.Requests())
	}
}

// TestD02UnparseableConfigWithActivePipeline is spec D-02: an unparseable
// config.json that names active_pipeline fails loud, names the file, and
// never falls back to the default topology.
func TestD02UnparseableConfigWithActivePipeline(t *testing.T) {
	t.Parallel()
	s := NewScenario(t)
	path := filepath.Join(s.ConfigDir, "config.json")
	writeFile(t, path, "{\"active_pipeline\": \"my-topology\", \n")

	doctor := s.Run("doctor")
	if !strings.Contains(doctor.Stdout, "[fail] config.resolve") || !strings.Contains(doctor.Stdout, path) {
		t.Errorf("doctor does not fail config.resolve naming %s:\n%s", path, doctor.Stdout)
	}
	if !strings.Contains(doctor.Stdout, "Overall: fail") {
		t.Errorf("doctor overall is not fail:\n%s", doctor.Stdout)
	}

	s.Provider.Script(StageCodeWriter, SubmitCode(h01Prompt, File("main.go", "modify", helloMain)))
	r := s.Run("exec", "-o", "stream-json", h01Prompt)
	r.WantExit(t, 3)
	if !strings.Contains(r.Stderr+r.Stdout, path) {
		t.Errorf("exec error does not name %s:\n%s", path, r.Stderr)
	}
	if n := len(s.Provider.Requests()); n != 0 {
		t.Errorf("exec sent %d provider request(s) with a broken config", n)
	}
}
