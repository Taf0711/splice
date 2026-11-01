package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func writePipelineFile(t *testing.T, path, name string) {
	t.Helper()
	topology := schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    name,
		Nodes: []schemas.PipelineNode{
			{Name: "code_writer", Type: "code_writer", Model: &schemas.StageModelConfig{ProviderProfile: "local", Model: "qwen-node", ReasoningEffort: "high"}},
			{Name: "lint_cmd", Type: schemas.NodeTypeCommand, Command: []string{"true", "--strict"}},
		},
		Edges: []schemas.PipelineEdge{{From: "code_writer", To: "lint_cmd", Payload: schemas.EdgePayloadNone}},
	}
	data, err := json.MarshalIndent(topology, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPipelineCommandShowNamed pins that `pipeline show <name>` reads a library
// topology and lists its nodes, edges, and command nodes.
func TestPipelineCommandShowNamed(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	writePipelineFile(t, filepath.Join(base, "splice", "pipelines", "team.json"), "team")

	var stdout, stderr bytes.Buffer
	code := runPipelineCommand([]string{"show", "team"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"topology: team", "code_writer", "lint_cmd", "command nodes:", "true --strict", "code_writer -> lint_cmd (none)", "model: local/qwen-node (high)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show output missing %q:\n%s", want, out)
		}
	}
}

// TestPipelineCommandShowMissingFailsLoud pins the missing-reference case.
func TestPipelineCommandShowMissingFailsLoud(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := runPipelineCommand([]string{"show", "ghost"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("show of a missing topology exited 0")
	}
	if !strings.Contains(stderr.String(), "not found") {
		t.Fatalf("stderr = %q, want a not-found error", stderr.String())
	}
}

// TestPipelineCommandListResolvesActive pins that `pipeline list` reports the
// active topology and the library entries.
func TestPipelineCommandListResolvesActive(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	writePipelineFile(t, filepath.Join(base, "splice", "pipelines", "team.json"), "team")
	if err := os.MkdirAll(filepath.Join(base, "splice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "splice", "config.json"), []byte(`{"active_pipeline":"team"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	var stdout, stderr bytes.Buffer
	code := runPipelineCommand([]string{"list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "active: team (user library)") {
		t.Fatalf("list output missing the active library topology:\n%s", out)
	}
	if !strings.Contains(out, "team (active)") {
		t.Fatalf("list output missing the active marker:\n%s", out)
	}
}

// TestPipelineCommandUnknownVerb pins the usage error.
func TestPipelineCommandUnknownVerb(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runPipelineCommand([]string{"frobnicate"}, &stdout, &stderr); code == 0 {
		t.Fatal("unknown verb exited 0")
	}
	if !strings.Contains(stderr.String(), "unknown pipeline subcommand") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// TestParseExecArgsPipeline pins the --pipeline flag parse, both separated and
// inline, and the missing-value refusal.
func TestParseExecArgsPipeline(t *testing.T) {
	options, _, err := parseExecArgs([]string{"--pipeline", "team", "hello"})
	if err != nil {
		t.Fatalf("parseExecArgs: %v", err)
	}
	if options.pipeline != "team" {
		t.Fatalf("pipeline = %q, want team", options.pipeline)
	}
	inline, _, err := parseExecArgs([]string{"--pipeline=path/to/p.json", "hello"})
	if err != nil {
		t.Fatalf("parseExecArgs: %v", err)
	}
	if inline.pipeline != "path/to/p.json" {
		t.Fatalf("inline pipeline = %q, want path/to/p.json", inline.pipeline)
	}
	if _, _, err := parseExecArgs([]string{"--pipeline"}); err == nil {
		t.Fatal("--pipeline without a value did not error")
	}
}

// TestPipelineDefaultNameIsReserved pins that the library and import refuse the
// name default, and that `use default` selects the embedded topology.
func TestPipelineDefaultNameIsReserved(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	if _, err := splicerun.LibraryEntryPath("default"); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("LibraryEntryPath(default) error = %v, want the reserved-name refusal", err)
	}

	var stdout, stderr bytes.Buffer
	if code := runPipelineCommand([]string{"use", "default"}, &stdout, &stderr); code != 0 {
		t.Fatalf("use default exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "embedded default") {
		t.Fatalf("use default output = %q, want the embedded default named", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(base, "splice", "pipelines", "default.json")); !os.IsNotExist(err) {
		t.Fatalf("use default wrote a library entry: stat err = %v", err)
	}
}

// TestLibraryEntryPathRefusesTraversalName pins the name rule inside
// splicerun.LibraryEntryPath. The check must refuse the name on its own, because a path
// that leaves the library directory must never be returned.
func TestLibraryEntryPathRefusesTraversalName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, name := range []string{"../x", "a/b", `a\b`, "..", ""} {
		if path, err := splicerun.LibraryEntryPath(name); err == nil {
			t.Fatalf("LibraryEntryPath(%q) = %q, want a name error", name, path)
		}
	}
}

// TestPipelineCommandWarnsForMissingPinProfile pins that show and use warn,
// without failing, when a node pin names a profile the config does not define.
func TestPipelineCommandWarnsForMissingPinProfile(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Chdir(t.TempDir())
	writePipelineFile(t, filepath.Join(base, "splice", "pipelines", "team.json"), "team")
	const want = `warning: node code_writer pins provider profile "local"`

	var stdout, stderr bytes.Buffer
	if code := runPipelineCommand([]string{"show", "team"}, &stdout, &stderr); code != 0 {
		t.Fatalf("show exit = %d, want 0 (a missing profile is a warning)", code)
	}
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("show stderr = %q, want %q", stderr.String(), want)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runPipelineCommand([]string{"use", "team"}, &stdout, &stderr); code != 0 {
		t.Fatalf("use exit = %d, want 0 (a missing profile is a warning)", code)
	}
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("use stderr = %q, want %q", stderr.String(), want)
	}
}

// agentPipelineJSON is a one-node topology with an agent node, for disclosure
// tests. Its budget is explicit so it fits the trivial tier.
const agentPipelineJSON = `{"version": 1, "name": "investigate", "splice_min_version": "0.3.0", "nodes": [` +
	`{"name": "investigate", "type": "agent", "prompt": "Find {{intent}}.", "tools": ["read_file", "grep"], "permission": "ask", "max_turns": 3, "budget": {"input_max": 1000, "output_max": 500}}]}`

// TestPipelineImportAndShowDiscloseAgentNodes pins the import consent list and
// the show view for an agent node: its tools and its permission are named.
func TestPipelineImportAndShowDiscloseAgentNodes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	src := filepath.Join(t.TempDir(), "investigate.json")
	if err := os.WriteFile(src, []byte(agentPipelineJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	want := "investigate: tools read_file, grep; permission ask (prompts for every write, command, and network call); up to 3 turns"

	var stdout, stderr bytes.Buffer
	if code := runPipelineCommand([]string{"import", src, "--yes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("import exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "agent node "+want) {
		t.Fatalf("import consent output missing the agent node:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runPipelineCommand([]string{"show", "investigate"}, &stdout, &stderr); code != 0 {
		t.Fatalf("show exit = %d, stderr = %s", code, stderr.String())
	}
	for _, line := range []string{"agent nodes:", "  " + want} {
		if !strings.Contains(stdout.String(), line) {
			t.Fatalf("show output missing %q:\n%s", line, stdout.String())
		}
	}
}

// linkWriter collects stderr and hands over the first editor link.
type linkWriter struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	link chan string // buffered; it gets the link once
	sent bool
}

func (w *linkWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *linkWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if m := regexp.MustCompile(`Pipeline editor: (http://127\.0\.0\.1:\S+)`).FindStringSubmatch(w.buf.String()); m != nil && !w.sent {
		w.link <- m[1]
		w.sent = true
	}
	return len(p), nil
}

// TestPipelineEditServesTheEditorAndPrintsTheLinkOnStderr pins the headless
// entry point: the link goes to stderr, it opens the editor on a draft, and
// the server stops when the context ends.
func TestPipelineEditServesTheEditorAndPrintsTheLinkOnStderr(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prev := pipelineEditContext
	pipelineEditContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	t.Cleanup(func() { pipelineEditContext = prev })

	// The test reads the channel from a local: Write clears the field under
	// its lock, so reading the field here would race with it.
	links := make(chan string, 1)
	stderr := &linkWriter{link: links}
	done := make(chan int, 1)
	go func() { done <- runPipelineCommand([]string{"edit", "default"}, io.Discard, stderr) }()
	var link string
	select {
	case link = <-links:
	case code := <-done:
		t.Fatalf("pipeline edit exited %d before a link: %s", code, stderr.String())
	case <-time.After(10 * time.Second):
		t.Fatal("no link on stderr")
	}
	jar, _ := cookiejar.New(nil)
	resp, err := (&http.Client{Jar: jar, Timeout: 5 * time.Second}).Get(link)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Request.URL.String(), "/s/editor/?draft=") || !strings.Contains(string(body), "default-copy") {
		t.Fatalf("link opened %s (%d)", resp.Request.URL, resp.StatusCode)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pipeline edit did not stop with its context")
	}
	if !strings.Contains(stderr.String(), "Pipeline editor stopped.") {
		t.Fatalf("stderr = %s", stderr.String())
	}
	if drafts, _ := filepath.Glob(filepath.Join(base, "splice", "pipelines", ".drafts", "*.json")); len(drafts) != 1 {
		t.Fatalf("drafts = %v, want one", drafts)
	}
}
