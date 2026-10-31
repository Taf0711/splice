package benchmarks_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// versionsLock mirrors benchmarks/versions.lock.json. Only fields the test
// needs are modeled; the rest round-trips through json.RawMessage.
type versionsLock struct {
	SchemaVersion int                        `json:"schema_version"`
	Adapters      map[string]json.RawMessage `json:"adapters"`
	Splice        map[string]any             `json:"splice"`
	Model         map[string]any             `json:"model"`
}

type harnessBenchPin struct {
	Status         string `json:"status"`
	UpstreamRepo   string `json:"upstream_repo"`
	UpstreamCommit string `json:"upstream_commit"`
	AdapterVersion string `json:"adapter_version"`
}

type terminalBenchPin struct {
	Status  string `json:"status"`
	Dataset struct {
		Name           string `json:"name"`
		Version        string `json:"version"`
		TaskRepo       string `json:"task_repo"`
		TaskRepoCommit string `json:"task_repo_commit"`
	} `json:"dataset"`
	Harbor struct {
		Package        string `json:"package"`
		Version        string `json:"version"`
		UpstreamRepo   string `json:"upstream_repo"`
		UpstreamCommit string `json:"upstream_commit"`
	} `json:"harbor"`
	AdapterVersion string `json:"adapter_version"`
}

func loadVersionsLock(t *testing.T) versionsLock {
	t.Helper()
	path := filepath.Join("..", "benchmarks", "versions.lock.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read versions.lock.json: %v", err)
	}
	var lock versionsLock
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatalf("versions.lock.json does not parse: %v", err)
	}
	if lock.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", lock.SchemaVersion)
	}
	return lock
}

// TestVersionsLockParses pins the file as valid JSON with the required
// top-level sections. Any upstream schema change must update this test.
func TestVersionsLockParses(t *testing.T) {
	lock := loadVersionsLock(t)
	for _, section := range []string{"harness-bench", "terminal-bench"} {
		if _, ok := lock.Adapters[section]; !ok {
			t.Errorf("adapters.%s missing from versions.lock.json", section)
		}
	}
	if lock.Splice == nil {
		t.Errorf("splice section missing")
	}
	if lock.Model == nil {
		t.Errorf("model section missing")
	}
}

// TestNoFloatingPins rejects floating refs ("latest", "main", "master", "HEAD")
// anywhere in the pin file, and rejects run-time placeholders outside the
// two fields documented as filled at run time (splice.commit, model.*).
func TestNoFloatingPins(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "benchmarks", "versions.lock.json"))
	if err != nil {
		t.Fatalf("read versions.lock.json: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	bad := map[string]bool{"latest": true, "main": true, "master": true, "head": true}
	var floaters []string
	walkFloating("", raw, bad, &floaters)
	if len(floaters) > 0 {
		t.Fatalf("floating pins found in versions.lock.json: %v", floaters)
	}

	// Placeholders are allowed only in splice.* and model.* (filled at run time).
	var adapters map[string]json.RawMessage
	if err := json.Unmarshal(raw["adapters"], &adapters); err != nil {
		t.Fatalf("parse adapters: %v", err)
	}
	adapterJSON := string(adapters["terminal-bench"]) + string(adapters["harness-bench"])
	for _, tok := range []string{"FILLED_AT_RUN_TIME", "TODO"} {
		if strings.Contains(adapterJSON, tok) {
			t.Errorf("adapter pin sections must not contain placeholder %q", tok)
		}
	}
}

func walkFloating(path string, v any, bad map[string]bool, out *[]string) {
	switch val := v.(type) {
	case map[string]any:
		for k, sub := range val {
			walkFloating(path+"/"+k, sub, bad, out)
		}
	case []any:
		for _, sub := range val {
			walkFloating(path, sub, bad, out)
		}
	case string:
		if bad[strings.ToLower(val)] {
			*out = append(*out, path+"="+val)
		}
	}
}

// TestHarnessBenchPinShape checks the harness-bench pin fields an operator
// needs before a run.
func TestHarnessBenchPinShape(t *testing.T) {
	lock := loadVersionsLock(t)
	var pin harnessBenchPin
	if err := json.Unmarshal(lock.Adapters["harness-bench"], &pin); err != nil {
		t.Fatalf("adapters.harness-bench does not parse: %v", err)
	}
	if pin.Status != "verified" {
		t.Errorf("harness-bench status = %q, want verified", pin.Status)
	}
	if !strings.HasPrefix(pin.UpstreamRepo, "https://github.com/Qihoo360/harness-bench") {
		t.Errorf("harness-bench upstream_repo = %q", pin.UpstreamRepo)
	}
	if len(pin.UpstreamCommit) != 40 {
		t.Errorf("harness-bench upstream_commit %q is not a 40-char sha", pin.UpstreamCommit)
	}
	if pin.AdapterVersion == "" {
		t.Errorf("harness-bench adapter_version empty")
	}
}

// TestTerminalBenchPinShape checks dataset, harbor, and adapter pins.
func TestTerminalBenchPinShape(t *testing.T) {
	lock := loadVersionsLock(t)
	var pin terminalBenchPin
	if err := json.Unmarshal(lock.Adapters["terminal-bench"], &pin); err != nil {
		t.Fatalf("adapters.terminal-bench does not parse: %v", err)
	}
	if pin.Dataset.Name != "terminal-bench" || pin.Dataset.Version != "2.0" {
		t.Errorf("dataset = %s@%s, want terminal-bench@2.0", pin.Dataset.Name, pin.Dataset.Version)
	}
	if len(pin.Dataset.TaskRepoCommit) != 40 {
		t.Errorf("task_repo_commit %q is not a 40-char sha", pin.Dataset.TaskRepoCommit)
	}
	if pin.Harbor.Package != "harbor" {
		t.Errorf("harbor package = %q", pin.Harbor.Package)
	}
	if pin.Harbor.Version == "" || strings.Contains(strings.ToLower(pin.Harbor.Version), "latest") {
		t.Errorf("harbor version = %q, want a concrete version", pin.Harbor.Version)
	}
	if len(pin.Harbor.UpstreamCommit) != 40 {
		t.Errorf("harbor upstream_commit %q is not a 40-char sha", pin.Harbor.UpstreamCommit)
	}
}

// TestEveryAdapterDirHasREADME enforces one README per adapter directory.
func TestEveryAdapterDirHasREADME(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "benchmarks"))
	if err != nil {
		t.Fatalf("read benchmarks dir: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		switch e.Name() {
		case "configs", "manifests", "report", "swebench_pro":
			continue
		}
		readme := filepath.Join("..", "benchmarks", e.Name(), "README.md")
		if _, err := os.Stat(readme); err != nil {
			t.Errorf("adapter dir benchmarks/%s has no README.md", e.Name())
		}
	}
	// The two adapters shipped by this workstream must exist.
	for _, adapter := range []string{"harnessbench", "harbor"} {
		readme := filepath.Join("..", "benchmarks", adapter, "README.md")
		if _, err := os.Stat(readme); err != nil {
			t.Errorf("adapter %s has no README.md at %s", adapter, readme)
		}
	}
}
