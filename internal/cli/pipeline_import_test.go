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
	spliceversion "github.com/Taf0711/splice/internal/version"
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

// TestPipelineCommandExportDefaultReimports pins that export default writes the
// embedded default as byte-stable JSON that import accepts, so a user has a
// starting file. show default reads the same topology.
func TestPipelineCommandExportDefaultReimports(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	first := filepath.Join(t.TempDir(), "default-1.json")
	second := filepath.Join(t.TempDir(), "default-2.json")
	for _, target := range []string{first, second} {
		var stdout, stderr bytes.Buffer
		if code := runPipelineCommand([]string{"export", "default", target}, &stdout, &stderr); code != 0 {
			t.Fatalf("export default: exit = %d, stderr = %s", code, stderr.String())
		}
	}
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("export default is not byte-stable across runs")
	}

	// The exported file keeps the reserved name, so import refuses it and says
	// why. A copy with a new name is the starting file a user edits and imports.
	var stdout, stderr bytes.Buffer
	if code := runPipelineCommand([]string{"import", first, "--yes"}, &stdout, &stderr); code == 0 {
		t.Fatal("import of the exported default with its reserved name exited 0")
	}
	if !strings.Contains(stderr.String(), "reserved") {
		t.Fatalf("import refusal = %q, want the reserved name named", stderr.String())
	}
	var file map[string]any
	if err := json.Unmarshal(a, &file); err != nil {
		t.Fatal(err)
	}
	file["name"] = "my-default"
	renamed, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	starter := filepath.Join(t.TempDir(), "my-default.json")
	if err := os.WriteFile(starter, renamed, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runPipelineCommand([]string{"import", starter, "--yes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("import of the renamed starting file: exit = %d, stderr = %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runPipelineCommand([]string{"show", "default"}, &stdout, &stderr); code != 0 {
		t.Fatalf("show default: exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "source: embedded default") || !strings.Contains(stdout.String(), "code_writer") {
		t.Fatalf("show default output:\n%s", stdout.String())
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

// TestPipelineCommandImportRefusesUnknownField pins that an import with a
// misspelled key is refused, names the key, and writes nothing.
func TestPipelineCommandImportRefusesUnknownField(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	source := filepath.Join(t.TempDir(), "typo.json")
	doc := `{"version":1,"name":"typo","nodes":[{"name":"code_writer","type":"code_writer","modle":{"provider":"p"}}]}`
	if err := os.WriteFile(source, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runPipelineCommand([]string{"import", source, "--yes"}, &stdout, &stderr); code == 0 {
		t.Fatal("import with an unknown field exited 0")
	}
	if !strings.Contains(stderr.String(), "modle") {
		t.Fatalf("stderr = %q, want the unknown field named", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(base, "splice", "pipelines", "typo.json")); err == nil {
		t.Fatal("topology with an unknown field was written to the library")
	}
}

// TestPipelineCommandImportChecksMinSplice pins that import refuses a topology
// that needs a newer Splice, the same check the run loader applies.
func TestPipelineCommandImportChecksMinSplice(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	original := spliceversion.Version
	t.Cleanup(func() { spliceversion.Version = original })
	spliceversion.Version = "0.9.0"

	topology := schemas.PipelineTopology{
		Version:   schemas.TopologySchemaVersion,
		Name:      "newer",
		MinSplice: "99.0.0",
		Nodes:     []schemas.PipelineNode{{Name: "code_writer", Type: "code_writer"}},
	}
	data, err := json.MarshalIndent(topology, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "newer.json")
	if err := os.WriteFile(source, data, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runPipelineCommand([]string{"import", source, "--yes"}, &stdout, &stderr); code == 0 {
		t.Fatal("import of a topology that needs a newer Splice exited 0")
	}
	if !strings.Contains(stderr.String(), "requires Splice 99.0.0") {
		t.Fatalf("stderr = %q, want the minimum version named", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(base, "splice", "pipelines", "newer.json")); err == nil {
		t.Fatal("topology needing a newer Splice was written to the library")
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

// TestPipelineCommandImportRefusesTraversalNames pins the name rule: a
// topology name that is not a safe file name is refused before anything is
// written, so an import cannot write outside the library.
func TestPipelineCommandImportRefusesTraversalNames(t *testing.T) {
	for _, name := range []string{"../config", "../../evil", "a/b", `a\b`, "..", ".hidden", "Team", "has space", "-lead", strings.Repeat("a", 65), ""} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", base)
			configPath := filepath.Join(base, "splice", "config.json")
			if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
				t.Fatal(err)
			}
			original := []byte(`{"activeProvider":"work"}`)
			if err := os.WriteFile(configPath, original, 0o600); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "evil.json")
			if err := os.WriteFile(source, validTopologyJSON(t, name), 0o644); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			if code := runPipelineCommand([]string{"import", source, "--yes"}, &stdout, &stderr); code == 0 {
				t.Fatalf("import of name %q exited 0: %s", name, stdout.String())
			}
			if name != "" && !strings.Contains(stderr.String(), "invalid pipeline name") {
				t.Fatalf("stderr = %q, want a named error", stderr.String())
			}
			if data, err := os.ReadFile(configPath); err != nil || !bytes.Equal(data, original) {
				t.Fatalf("user config changed by import: %q, %v", data, err)
			}
			if entries, err := os.ReadDir(filepath.Join(base, "splice")); err != nil || len(entries) != 1 {
				t.Fatalf("import wrote outside the library: %v, %v", entries, err)
			}
		})
	}
}

// TestPipelineCommandImportAsksBeforeOverwrite pins that an existing library
// entry is replaced only after a yes, or with --yes.
func TestPipelineCommandImportAsksBeforeOverwrite(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		answer   string
		replaced bool
	}{
		{name: "no answer keeps the entry", args: []string{}, answer: "", replaced: false},
		{name: "n keeps the entry", args: []string{}, answer: "n\n", replaced: false},
		{name: "y replaces", args: []string{}, answer: "y\n", replaced: true},
		{name: "--yes replaces", args: []string{"--yes"}, replaced: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", base)
			entry := filepath.Join(base, "splice", "pipelines", "team.json")
			if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
				t.Fatal(err)
			}
			original := []byte(`{"original":true}`)
			if err := os.WriteFile(entry, original, 0o644); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "team.json")
			if err := os.WriteFile(source, validTopologyJSON(t, "team"), 0o644); err != nil {
				t.Fatal(err)
			}
			previous := pipelineStdin
			pipelineStdin = strings.NewReader(tc.answer)
			t.Cleanup(func() { pipelineStdin = previous })

			var stdout, stderr bytes.Buffer
			if code := runPipelineCommand(append([]string{"import", source}, tc.args...), &stdout, &stderr); code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
			}
			if tc.answer != "" || len(tc.args) == 0 {
				if !strings.Contains(stdout.String(), `Replace the existing library entry "team"`) {
					t.Fatalf("import did not ask before overwrite:\n%s", stdout.String())
				}
			}
			data, err := os.ReadFile(entry)
			if err != nil {
				t.Fatal(err)
			}
			if replaced := !bytes.Equal(data, original); replaced != tc.replaced {
				t.Fatalf("replaced = %v, want %v", replaced, tc.replaced)
			}
		})
	}
}

// TestPipelineCommandUseRefusesPathsAndBadNames pins that active_pipeline only
// ever holds a library name. A path is refused with a pointer to import, and
// the config is not changed.
func TestPipelineCommandUseRefusesPathsAndBadNames(t *testing.T) {
	for _, reference := range []string{"/tmp/team.json", "./team.json", "team.json", "../config", "Team"} {
		t.Run(reference, func(t *testing.T) {
			base := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", base)
			writePipelineFile(t, filepath.Join(base, "splice", "pipelines", "team.json"), "team")
			var stdout, stderr bytes.Buffer
			if code := runPipelineCommand([]string{"use", reference}, &stdout, &stderr); code == 0 {
				t.Fatalf("use %q exited 0: %s", reference, stdout.String())
			}
			if !strings.Contains(stderr.String(), "splice pipeline import <path>") {
				t.Fatalf("stderr = %q, want a pointer to pipeline import", stderr.String())
			}
			if _, err := os.Stat(filepath.Join(base, "splice", "config.json")); !os.IsNotExist(err) {
				t.Fatalf("use %q wrote config.json: %v", reference, err)
			}
		})
	}
}

// TestPipelineCommandShowAndExportRefuseBadNames pins the name rule on the
// library lookups of show and export.
func TestPipelineCommandShowAndExportRefuseBadNames(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, verb := range []string{"show", "export"} {
		var stdout, stderr bytes.Buffer
		if code := runPipelineCommand([]string{verb, "Bad..Name"}, &stdout, &stderr); code == 0 {
			t.Fatalf("%s of a bad name exited 0", verb)
		}
		if !strings.Contains(stderr.String(), "invalid pipeline name") {
			t.Fatalf("%s stderr = %q, want the named error", verb, stderr.String())
		}
	}
}
