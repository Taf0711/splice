package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func validTopologyJSON(t *testing.T, name string) []byte {
	t.Helper()
	topology := schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    name,
		Nodes: []schemas.PipelineNode{
			{Name: "code_writer", Type: "code_writer"},
			{Name: "lint_cmd", Type: schemas.NodeTypeCommand, Command: []string{"true"}},
		},
	}
	data, err := json.MarshalIndent(topology, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestPipelineCommandUsePreservesConfig pins that `use` sets active_pipeline
// without dropping unrelated configuration keys.
func TestPipelineCommandUsePreservesConfig(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	writePipelineFile(t, filepath.Join(base, "splice", "pipelines", "team.json"), "team")
	configPath := filepath.Join(base, "splice", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"defaultProjectTrust":"always","active_pipeline":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runPipelineCommand([]string{"use", "team"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse written config: %v", err)
	}
	if parsed["active_pipeline"] != "team" {
		t.Fatalf("active_pipeline = %v, want team", parsed["active_pipeline"])
	}
	if parsed["defaultProjectTrust"] != "always" {
		t.Fatalf("unrelated config key dropped: %v", parsed)
	}
}

// TestPipelineCommandUseMissingFailsLoud pins the missing-library case.
func TestPipelineCommandUseMissingFailsLoud(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := runPipelineCommand([]string{"use", "ghost"}, &stdout, &stderr); code == 0 {
		t.Fatal("use of a missing pipeline exited 0")
	}
}

// TestPipelineCommandExportWritesFile pins that export copies the topology.
func TestPipelineCommandExportWritesFile(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	writePipelineFile(t, filepath.Join(base, "splice", "pipelines", "team.json"), "team")
	target := filepath.Join(t.TempDir(), "out.json")

	var stdout, stderr bytes.Buffer
	if code := runPipelineCommand([]string{"export", "team", target}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	exported, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var topology schemas.PipelineTopology
	if err := json.Unmarshal(exported, &topology); err != nil {
		t.Fatalf("exported file does not parse: %v", err)
	}
	if topology.Name != "team" {
		t.Fatalf("exported topology = %q, want team", topology.Name)
	}
}

// TestPipelineCommandImportPathInstalls pins that import validates and installs
// a topology file into the user library.
func TestPipelineCommandImportPathInstalls(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	source := filepath.Join(t.TempDir(), "team.json")
	if err := os.WriteFile(source, validTopologyJSON(t, "team"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runPipelineCommand([]string{"import", source, "--yes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(base, "splice", "pipelines", "team.json")); err != nil {
		t.Fatalf("imported topology not written: %v", err)
	}
	if !strings.Contains(stdout.String(), "lint_cmd") {
		t.Fatalf("import did not list command nodes:\n%s", stdout.String())
	}
}

// TestPipelineCommandImportRejectsInvalid pins that a topology without nodes is
// refused before anything is written.
func TestPipelineCommandImportRejectsInvalid(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	source := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(source, []byte(`{"version":1,"name":"bad","nodes":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runPipelineCommand([]string{"import", source, "--yes"}, &stdout, &stderr); code == 0 {
		t.Fatal("import of an invalid topology exited 0")
	}
	if _, err := os.Stat(filepath.Join(base, "splice", "pipelines", "bad.json")); err == nil {
		t.Fatal("invalid topology was written to the library")
	}
}

// TestPromptYesNo pins the affirmative read.
func TestPromptYesNo(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"Y\n", true},
		{"n\n", false},
		{"\n", false},
		{"", false},
	} {
		if got := promptYesNo(strings.NewReader(tc.input), io.Discard, "install?"); got != tc.want {
			t.Fatalf("promptYesNo(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func withImportClient(t *testing.T, server *httptest.Server) {
	t.Helper()
	previous := topologyImportClient
	topologyImportClient = &http.Client{Transport: server.Client().Transport, CheckRedirect: checkTopologyRedirect}
	t.Cleanup(func() { topologyImportClient = previous })
}

// TestFetchTopologyRejectsHTTP pins that a remote import must be https.
func TestFetchTopologyRejectsHTTP(t *testing.T) {
	_, err := fetchTopology("http://example.com/pipeline.json")
	if err == nil || !strings.Contains(err.Error(), "requires https") {
		t.Fatalf("error = %v, want the https refusal", err)
	}
}

// TestFetchTopologyRefusesCrossHostRedirect pins that a trusted URL cannot
// bounce an import to another origin.
func TestFetchTopologyRefusesCrossHostRedirect(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(validTopologyJSON(t, "target"))
	}))
	defer target.Close()
	redirector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/pipeline.json", http.StatusFound)
	}))
	defer redirector.Close()
	withImportClient(t, redirector)

	_, err := fetchTopology(redirector.URL + "/pipeline.json")
	if err == nil || !strings.Contains(err.Error(), "cross-host") {
		t.Fatalf("error = %v, want the cross-host refusal", err)
	}
}

// TestFetchTopologyCapsSize pins the 1 MB import cap.
func TestFetchTopologyCapsSize(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), maxTopologyImportBytes+16))
	}))
	defer server.Close()
	withImportClient(t, server)

	_, err := fetchTopology(server.URL + "/pipeline.json")
	if err == nil || !strings.Contains(err.Error(), "import cap") {
		t.Fatalf("error = %v, want the size-cap refusal", err)
	}
}

// TestFetchTopologyRejectsStatus pins the non-200 refusal.
func TestFetchTopologyRejectsStatus(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	withImportClient(t, server)

	_, err := fetchTopology(server.URL + "/pipeline.json")
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("error = %v, want the status refusal", err)
	}
}

// TestFetchTopologySuccess pins the happy path.
func TestFetchTopologySuccess(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(validTopologyJSON(t, "remote"))
	}))
	defer server.Close()
	withImportClient(t, server)

	data, err := fetchTopology(server.URL + "/pipeline.json")
	if err != nil {
		t.Fatalf("fetchTopology: %v", err)
	}
	var topology schemas.PipelineTopology
	if err := json.Unmarshal(data, &topology); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if topology.Name != "remote" {
		t.Fatalf("topology = %q, want remote", topology.Name)
	}
}
