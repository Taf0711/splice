package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// trustConfigFixture is a project config with one MCP server, hooks (in the
// sibling hooks.json), a provider, and a sandbox tightening.
const trustConfigFixture = `{
  "activeProvider": "evil-provider",
  "maxTurns": 7,
  "providers": [{"name": "evil-provider", "model": "x"}],
  "mcp": {"servers": {"memory": {"command": "npx"}}},
  "sandbox": {"network": "deny", "blockUnixSockets": true}
}`

func writeTrustConfigFixture(t *testing.T, workspace string) {
	t.Helper()
	dir := filepath.Join(workspace, ".splice")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(trustConfigFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	// The real hooks schema (internal/hooks.Config): a file-level enabled
	// flag and an array of definitions, each with its own enabled flag.
	hooks := `{"enabled": true, "hooks": [
		{"id": "pre", "event": "beforeTool", "command": "echo", "args": ["hi"], "enabled": true},
		{"id": "post-a", "event": "afterTool", "command": "a", "enabled": true},
		{"id": "post-b", "event": "afterTool", "command": "b", "enabled": true}
	]}`
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDescribeProjectTrustConfigSumsExecutableContent(t *testing.T) {
	ws := t.TempDir()
	writeTrustConfigFixture(t, ws)
	cfg := describeProjectTrustConfig(filepath.Join(ws, ".splice", "config.json"))
	if cfg.ParseError != "" {
		t.Fatalf("unexpected parse error: %s", cfg.ParseError)
	}
	if cfg.MCPServerCount != 1 || len(cfg.MCPServerNames) != 1 || cfg.MCPServerNames[0] != "memory" {
		t.Errorf("mcp summary wrong: %+v", cfg)
	}
	if cfg.HookCount != 3 {
		t.Errorf("hook count = %d, want 3 (hooks.json entries)", cfg.HookCount)
	}
	if cfg.ProviderCount != 1 || cfg.ActiveProviderOverride != "evil-provider" || cfg.MaxTurnsOverride != 7 {
		t.Errorf("provider/turns summary wrong: %+v", cfg)
	}
	if len(cfg.SandboxTightening) != 2 {
		t.Errorf("sandbox tightening = %v, want 2 entries", cfg.SandboxTightening)
	}
}

func TestDescribeProjectTrustConfigMissingFileIsEmpty(t *testing.T) {
	cfg := describeProjectTrustConfig(filepath.Join(t.TempDir(), ".splice", "config.json"))
	if !cfg.Empty() {
		t.Errorf("missing config should be empty, got %+v", cfg)
	}
}

// The hooks summary must read the real hooks schema through the canonical
// reader: a file-level enabled flag plus per-entry enabled flags. Executable
// and disabled definitions are disclosed separately.
func TestDescribeProjectTrustConfigCountsRealHooksSchema(t *testing.T) {
	for _, tc := range []struct {
		name         string
		hooks        string
		wantEnabled  int
		wantDisabled int
		wantErr      bool
	}{
		{
			name:        "one executable hook",
			hooks:       `{"enabled":true,"hooks":[{"id":"hook","event":"sessionStart","command":"example","enabled":true}]}`,
			wantEnabled: 1,
		},
		{
			name:         "entry disabled",
			hooks:        `{"enabled":true,"hooks":[{"id":"hook","event":"sessionStart","command":"example","enabled":false}]}`,
			wantDisabled: 1,
		},
		{
			name:         "file disabled",
			hooks:        `{"enabled":false,"hooks":[{"id":"hook","event":"sessionStart","command":"example","enabled":true}]}`,
			wantDisabled: 1,
		},
		{
			name:    "invalid schema is reported, not counted as zero silently",
			hooks:   `{"enabled":true,"hooks":[{"id":"hook","event":"notAnEvent","command":"example"}]}`,
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), ".splice")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(tc.hooks), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := describeProjectTrustConfig(filepath.Join(dir, "config.json"))
			if cfg.HookCount != tc.wantEnabled {
				t.Errorf("executable hooks = %d, want %d", cfg.HookCount, tc.wantEnabled)
			}
			if cfg.HookDisabledCount != tc.wantDisabled {
				t.Errorf("disabled hooks = %d, want %d", cfg.HookDisabledCount, tc.wantDisabled)
			}
			if (cfg.HookParseError != "") != tc.wantErr {
				t.Errorf("hook parse error = %q, wantErr=%v", cfg.HookParseError, tc.wantErr)
			}
			if cfg.Empty() {
				t.Error("a hooks file with content must not summarize as empty")
			}
			if n := countHookEntries(filepath.Join(dir, "hooks.json")); n != tc.wantEnabled {
				t.Errorf("countHookEntries = %d, want %d", n, tc.wantEnabled)
			}
		})
	}
}

// A workspace can carry an executable hooks.json with no config.json. The
// summary must still disclose it instead of returning early.
func TestDescribeProjectTrustConfigSeesHooksWithoutConfigFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".splice")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"enabled":true,"hooks":[{"id":"hook","event":"sessionStart","command":"example","enabled":true}]}`
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := describeProjectTrustConfig(filepath.Join(dir, "config.json"))
	if cfg.HookCount != 1 {
		t.Fatalf("hooks beside an absent config.json = %d, want 1", cfg.HookCount)
	}
	if !strings.Contains(strings.Join(trustConfigCardLines(cfg, 120), "\n"), "hooks") {
		t.Error("hooks row missing from the card when config.json is absent")
	}
}

func TestDescribeProjectTrustConfigParseErrorIsHonest(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, ".splice")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o644)
	cfg := describeProjectTrustConfig(filepath.Join(dir, "config.json"))
	if cfg.ParseError == "" {
		t.Fatal("parse error not captured")
	}
	lines := trustConfigCardLines(cfg, 120)
	if len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "could not be read") {
		t.Errorf("parse error not rendered: %v", lines)
	}
}

func TestTrustConfigCardRendersI1Form(t *testing.T) {
	ws := t.TempDir()
	writeTrustConfigFixture(t, ws)
	cfg := describeProjectTrustConfig(filepath.Join(ws, ".splice", "config.json"))
	summary := trustConfigCardLines(cfg, 120)
	out := renderTrustConfigCard(cfg, summary, 120)
	plain := stripANSI(out)
	for _, want := range []string{
		"UNTRUSTED PROJECT CONFIG (not loaded)",
		"  config: /", // the path line; the box clips long temp-dir paths at 120 columns
		"[V] view config.json",
		"mcp servers        1 (memory)",
		"hooks              3",
		"active provider    evil-provider",
		"max turns          7",
		"this config can run commands",
		"NOT active in this session",
		"[V]", "[T]", "[D]",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("card missing %q\n---\n%s", want, plain)
		}
	}
	// Width safety at 80 columns.
	out80 := renderTrustConfigCard(cfg, summary, 80)
	for _, line := range strings.Split(out80, "\n") {
		if lipgloss.Width(line) > 80 {
			t.Errorf("80-col line overflows: %q", line)
		}
	}
}

func TestTrustConfigPayloadRoundTrip(t *testing.T) {
	ws := t.TempDir()
	writeTrustConfigFixture(t, ws)
	cfg := describeProjectTrustConfig(filepath.Join(ws, ".splice", "config.json"))
	row := trustConfigTranscriptPayload(cfg)
	got, summary, ok := parseTrustConfigTranscriptPayload(row)
	if !ok {
		t.Fatal("payload did not round-trip")
	}
	if got.Path != cfg.Path || got.ParseError != cfg.ParseError {
		t.Errorf("path/parse error lost: %+v", got)
	}
	// The payload's summary is authoritative: the render never re-reads the
	// file (it may change between append and render). It must match the
	// summary computed at append time.
	want := trustConfigCardLines(cfg, 120)
	if strings.Join(summary, "|") != strings.Join(want, "|") {
		t.Errorf("summary not stable across round-trip:\n%v\n%v", summary, want)
	}
	// The raw NUL marker must never leak into the rendered card.
	plain := stripANSI(renderTrustConfigCard(got, summary, 120))
	if strings.Contains(plain, "\x00") || strings.Contains(plain, "\x1f") {
		t.Error("payload markers leaked into the render")
	}
}

func TestTrustConfigNoticeSkipsEmpty(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	before := len(m.transcript)
	m.trustConfigNotice(projectTrustConfig{}) // empty: nothing to say
	if len(m.transcript) != before {
		t.Error("empty config produced a card")
	}
}

// Trust picker [V] wiring (real flow): when a project config exists, the
// trust menu carries the view-config row; choosing it re-opens the menu,
// appends the evidence card, and does NOT change trust state.
func TestTrustPickerViewConfigRowKeepsDecisionOpen(t *testing.T) {
	ws := t.TempDir()
	writeTrustConfigFixture(t, ws)
	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	m.projectConfigPath = filepath.Join(ws, ".splice", "config.json")
	m.trustPromptRequired = true
	m = m.openTrustPromptIfRequired()

	// The evidence card landed with the menu.
	foundCard := false
	for _, row := range m.transcript {
		if strings.Contains(row.text, trustConfigTranscriptMarker) {
			foundCard = true
		}
	}
	if !foundCard {
		t.Fatal("evidence card did not append at trust-prompt launch")
	}
	if m.picker == nil || m.picker.kind != pickerTrust {
		t.Fatal("trust menu did not open")
	}
	// The [V] row is present (between parent and decline).
	values := make([]string, 0, len(m.picker.items))
	for _, item := range m.picker.items {
		values = append(values, item.Value)
	}
	viewIdx := -1
	for i, v := range values {
		if v == trustActionView {
			viewIdx = i
		}
	}
	if viewIdx < 0 || viewIdx != len(values)-2 {
		t.Fatalf("view-config row placement wrong: %v", values)
	}

	// Choose the view row through the real choosePicker path.
	m.picker.selected = viewIdx
	updated, _ := m.choosePicker()
	next := updated.(model)
	// The menu re-opens (not a trust decision).
	if next.picker == nil || next.picker.kind != pickerTrust {
		t.Fatal("view-config did not return to the trust menu")
	}
	// Trust state untouched.
	if next.trusted {
		t.Error("view-config changed trust state")
	}
	if !next.trustPromptRequired {
		t.Error("view-config resolved the mandatory prompt")
	}
	// The evidence card landed (appended by the view action).
	cards := 0
	for _, row := range next.transcript {
		if strings.Contains(row.text, trustConfigTranscriptMarker) {
			cards++
		}
	}
	if cards < 1 {
		t.Fatal("view-config did not append the evidence card")
	}
}

// Without a project config, no [V] row and no card: the menu stays the
// plain three-option trust decision.
// A workspace can carry only a sibling pipeline.json. The trust menu must
// still offer the view row and the evidence must name the command node.
func TestTrustPickerViewRowWithPipelineOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".splice"), 0o755); err != nil {
		t.Fatal(err)
	}
	pipeline := `{"version": 1, "name": "lint-only", "nodes": [{"name": "lint", "type": "command", "command": ["go", "vet", "./..."]}]}`
	if err := os.WriteFile(filepath.Join(ws, ".splice", "pipeline.json"), []byte(pipeline), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	m.trustPromptRequired = true
	m = m.openTrustPromptIfRequired()
	if !trustPickerHasView(m) {
		t.Fatal("view-config row missing when only pipeline.json exists")
	}
	if !transcriptContains(m.transcript, "lint: go vet ./...") {
		t.Fatalf("evidence card does not name the pipeline command: %#v", m.transcript)
	}
}

// A workspace can carry only a sibling hooks.json. The trust menu must offer
// the view row even though config.json is absent.
func TestTrustPickerViewRowWithHooksOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".splice"), 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := `{"enabled": true, "hooks": [{"id": "pre", "event": "sessionStart", "command": "echo", "enabled": true}]}`
	if err := os.WriteFile(filepath.Join(ws, ".splice", "hooks.json"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	m.trustPromptRequired = true
	m = m.openTrustPromptIfRequired()
	if !trustPickerHasView(m) {
		t.Fatal("view-config row missing when only hooks.json exists")
	}
	for _, item := range m.picker.items {
		if item.Value == trustActionView && strings.Contains(item.Meta, "config.json") && !strings.Contains(item.Meta, "no config.json") {
			t.Errorf("view row claims config.json exists: meta %q", item.Meta)
		}
	}
}

func trustPickerHasView(m model) bool {
	if m.picker == nil {
		return false
	}
	for _, item := range m.picker.items {
		if item.Value == trustActionView {
			return true
		}
	}
	return false
}

func TestTrustPickerNoViewRowWithoutProjectConfig(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.trustPromptRequired = true
	m = m.openTrustPromptIfRequired()
	if m.picker == nil {
		t.Fatal("trust menu did not open")
	}
	for _, item := range m.picker.items {
		if item.Value == trustActionView {
			t.Error("view-config row present without a project config")
		}
	}
	for _, row := range m.transcript {
		if strings.Contains(row.text, trustConfigTranscriptMarker) {
			t.Error("evidence card appended without a project config")
		}
	}
}

func TestDescribeProjectTrustConfigDisclosesPipelineCommands(t *testing.T) {
	ws := t.TempDir()
	writeTrustConfigFixture(t, ws)
	pipeline := `{"version": 1, "name": "lint-only", "nodes": [{"name": "lint", "type": "command", "command": ["go", "vet", "./..."]}]}`
	if err := os.WriteFile(filepath.Join(ws, ".splice", "pipeline.json"), []byte(pipeline), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := describeProjectTrustConfig(filepath.Join(ws, ".splice", "config.json"))
	if !cfg.PipelineFile || cfg.PipelineParseError != "" {
		t.Fatalf("pipeline.json not disclosed cleanly: %+v", cfg)
	}
	if len(cfg.PipelineCommands) != 1 || cfg.PipelineCommands[0] != "lint: go vet ./..." {
		t.Fatalf("command nodes = %v, want [lint: go vet ./...]", cfg.PipelineCommands)
	}
	if cfg.Empty() {
		t.Fatal("a project pipeline.json must make the trust evidence non-empty")
	}
	card := strings.Join(trustConfigCardLines(cfg, 120), "\n")
	if !strings.Contains(card, "command node       lint: go vet ./...") {
		t.Fatalf("card does not render the command node:\n%s", card)
	}

	bad := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bad, ".splice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, ".splice", "pipeline.json"), []byte(`{"version": 9`), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := describeProjectTrustConfig(filepath.Join(bad, ".splice", "config.json"))
	if broken.PipelineParseError == "" || broken.Empty() {
		t.Fatalf("unparseable pipeline.json must be recorded, got %+v", broken)
	}
}

// Untrusted pipeline text must not carry terminal controls or the card's
// field delimiters into the trust card.
func TestDescribeProjectTrustConfigSanitizesPipelineText(t *testing.T) {
	ws := t.TempDir()
	writeTrustConfigFixture(t, ws)
	// A node name with an escape is refused at load. The card names the refusal,
	// and no escape or delimiter reaches the card.
	pipeline := `{"version": 1, "name": "x", "nodes": [{"name": "lint\u001b[8m", "type": "command", "command": ["go", "\u001b[8mhidden\u001b[0m", "a\u001fb"]}]}`
	if err := os.WriteFile(filepath.Join(ws, ".splice", "pipeline.json"), []byte(pipeline), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := describeProjectTrustConfig(filepath.Join(ws, ".splice", "config.json"))
	if !strings.Contains(cfg.PipelineParseError, "nodes[0]") {
		t.Fatalf("pipeline parse error = %q, want the refused node named by index", cfg.PipelineParseError)
	}
	card := strings.Join(trustConfigCardLines(cfg, 120), "\n")
	if strings.Contains(card, "\x1b") || strings.Contains(card, "\x1f") || strings.Contains(card, "\x00") {
		t.Fatalf("card carries terminal controls or delimiters: %q", card)
	}
}

// The transcript payload is stored at width 0, which must not clip parse
// errors: a long error survives whole in the payload.
func TestTrustConfigPayloadKeepsParseErrorsWhole(t *testing.T) {
	longErr := strings.Repeat("unexpected token near field ", 6) + "END"
	cfg := projectTrustConfig{PipelineFile: true, PipelineParseError: longErr, HookParseError: longErr}
	payload := trustConfigTranscriptPayload(cfg)
	if strings.Count(payload, longErr) != 2 {
		t.Fatalf("payload clipped a parse error:\n%s", payload)
	}
}

// writePipelineOnlyWorkspace writes one command node in .splice/pipeline.json
// and no config.json, the case where the card must not claim a config file.
func writePipelineOnlyWorkspace(t *testing.T, ws string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(ws, ".splice"), 0o755); err != nil {
		t.Fatal(err)
	}
	pipeline := `{"version": 1, "name": "lint-only", "nodes": [{"name": "lint", "type": "command", "command": ["go", "vet", "./..."]}]}`
	if err := os.WriteFile(filepath.Join(ws, ".splice", "pipeline.json"), []byte(pipeline), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A pipeline-only repo has no config.json. The card must say so, use the
// project-files heading, name pipeline.json as the [V] target, and use
// singular grammar for one command node.
func TestTrustConfigCardPipelineOnlyNamesFiles(t *testing.T) {
	ws := t.TempDir()
	writePipelineOnlyWorkspace(t, ws)
	cfg := describeProjectTrustConfig(filepath.Join(ws, ".splice", "config.json"))
	if cfg.ConfigFile {
		t.Fatal("config.json reported present when it does not exist")
	}
	plain := stripANSI(renderTrustConfigCard(cfg, trustConfigCardLines(cfg, 120), 120))
	for _, want := range []string{
		"UNTRUSTED PROJECT FILES (not loaded)",
		"config: none (no .splice/config.json)",
		"1 command node in .splice/pipeline.json",
		"[V] view pipeline.json",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("card missing %q\n---\n%s", want, plain)
		}
	}
	for _, bad := range []string{"UNTRUSTED PROJECT CONFIG", "config: " + ws, "command nodes", "—"} {
		if strings.Contains(plain, bad) {
			t.Errorf("card still contains %q\n---\n%s", bad, plain)
		}
	}
}

// Counts read correctly at 1 and at N, for hooks and pipeline command nodes.
func TestTrustConfigCardCountGrammar(t *testing.T) {
	one := strings.Join(trustConfigCardLines(projectTrustConfig{
		HookCount:        1,
		PipelineFile:     true,
		PipelineCommands: []string{"lint: go vet ./..."},
	}, 120), "\n")
	for _, want := range []string{"1 command on lifecycle events", "1 command node in .splice/pipeline.json"} {
		if !strings.Contains(one, want) {
			t.Errorf("singular card missing %q:\n%s", want, one)
		}
	}
	many := strings.Join(trustConfigCardLines(projectTrustConfig{
		HookCount:        2,
		PipelineFile:     true,
		PipelineCommands: []string{"a: x", "b: y"},
	}, 120), "\n")
	for _, want := range []string{"2 commands on lifecycle events", "2 command nodes in .splice/pipeline.json"} {
		if !strings.Contains(many, want) {
			t.Errorf("plural card missing %q:\n%s", want, many)
		}
	}
}

// The [V] target is the first existing file in config, hooks, pipeline order.
func TestProjectTrustViewFileOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
		want  string
	}{
		{"config wins", []string{"config.json", "hooks.json", "pipeline.json"}, "config.json"},
		{"hooks before pipeline", []string{"hooks.json", "pipeline.json"}, "hooks.json"},
		{"pipeline alone", []string{"pipeline.json"}, "pipeline.json"},
		{"none", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			dir := filepath.Join(ws, ".splice")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, name := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := projectTrustViewFile(filepath.Join(dir, "config.json"))
			want := ""
			if tc.want != "" {
				want = filepath.Join(dir, tc.want)
			}
			if got != want {
				t.Errorf("projectTrustViewFile = %q, want %q", got, want)
			}
		})
	}
}

// The [V] action opens the same file the card labels. With no $EDITOR the
// notice names the file it would have opened, so the test can read the target.
func TestTrustViewActionOpensLabeledFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	ws := t.TempDir()
	writePipelineOnlyWorkspace(t, ws)
	pipelinePath := filepath.Join(ws, ".splice", "pipeline.json")
	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	m.trustPromptRequired = true
	m = m.openTrustPromptIfRequired()
	viewIdx := -1
	for i, item := range m.picker.items {
		if item.Value == trustActionView {
			viewIdx = i
		}
	}
	if viewIdx < 0 {
		t.Fatal("view-config row missing when only pipeline.json exists")
	}
	m.picker.selected = viewIdx
	updated, _ := m.choosePicker()
	next := updated.(model)
	if !transcriptContains(next.transcript, "cat "+pipelinePath) {
		t.Errorf("view action did not target pipeline.json; transcript: %#v", next.transcript)
	}
	if transcriptContains(next.transcript, "Config file not found") {
		t.Error("view action targeted the missing config.json")
	}
}

// writeProjectPlugin writes one project plugin manifest under .splice/plugins.
func writeProjectPlugin(t *testing.T, ws, name, manifest string) {
	t.Helper()
	dir := filepath.Join(ws, ".splice", "plugins", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

const linterPluginManifest = `{"schemaVersion": 1, "id": "linter", "name": "linter", "version": "1.0.0", "tools": [{"name": "lint", "command": "npm", "args": ["run", "lint"]}]}`

// A project plugin's tool command runs once the folder is trusted. The card
// must name it before the trust decision, and the picker must offer the view row.
func TestTrustConfigDisclosesProjectPlugins(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ws := t.TempDir()
	writeProjectPlugin(t, ws, "linter", linterPluginManifest)

	cfg := describeProjectTrustConfig(filepath.Join(ws, ".splice", "config.json"))
	if cfg.Empty() {
		t.Fatal("config with a project plugin reports Empty")
	}
	if cfg.PluginCount != 1 || len(cfg.PluginCommands) != 1 || cfg.PluginCommands[0] != "linter tool lint: npm run lint" {
		t.Fatalf("plugin disclosure = count %d commands %v", cfg.PluginCount, cfg.PluginCommands)
	}
	card := strings.Join(trustConfigCardLines(cfg, 120), "\n")
	for _, want := range []string{"1 project plugin in .splice/plugins", "plugin command     linter tool lint: npm run lint"} {
		if !strings.Contains(card, want) {
			t.Errorf("card missing %q:\n%s", want, card)
		}
	}

	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	m.trustPromptRequired = true
	m = m.openTrustPromptIfRequired()
	if !trustPickerHasView(m) {
		t.Fatal("view-config row missing when only a project plugin exists")
	}
	if !transcriptContains(m.transcript, "linter tool lint: npm run lint") {
		t.Fatalf("evidence card does not name the plugin command: %#v", m.transcript)
	}
}

// A broken project plugin manifest is recorded as a parse error, the same way
// a broken pipeline file is.
func TestTrustConfigRecordsProjectPluginParseError(t *testing.T) {
	ws := t.TempDir()
	writeProjectPlugin(t, ws, "broken", `{"schemaVersion": 2, "id": "broken", "name": "broken", "version": "1.0.0"}`)

	cfg := describeProjectTrustConfig(filepath.Join(ws, ".splice", "config.json"))
	if cfg.Empty() {
		t.Fatal("broken plugin manifest reports Empty; the card would hide it")
	}
	if len(cfg.PluginParseErrors) != 1 || !strings.Contains(cfg.PluginParseErrors[0], "schemaVersion") {
		t.Fatalf("plugin parse errors = %v, want one naming schemaVersion", cfg.PluginParseErrors)
	}
	// Width 0 keeps the whole error; a narrow card clips the path before the field.
	card := strings.Join(trustConfigCardLines(cfg, 0), "\n")
	if !strings.Contains(card, "plugin             ") || !strings.Contains(card, "schemaVersion") {
		t.Errorf("card does not show the plugin parse error:\n%s", card)
	}
}

// The trust row names config.json only when the file exists. A config.json
// that exists but does not parse is still present, so the row must not call it
// absent. A workspace with no config.json gets the other-files wording.
func TestTrustPickerViewRowMetaFollowsConfigFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cases := []struct {
		name     string
		files    map[string]string
		wantMeta string
	}{
		{"broken config.json is present", map[string]string{"config.json": "{not json"}, "config.json"},
		{"plugin only has no config.json", map[string]string{"plugins/linter/plugin.json": linterPluginManifest}, "no config.json; other project files found"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ws := t.TempDir()
			for name, body := range tt.files {
				writeProjectFile(t, filepath.Join(ws, ".splice", name), body)
			}
			m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
			m.trustPromptRequired = true
			m = m.openTrustPromptIfRequired()
			var meta string
			for _, item := range m.picker.items {
				if item.Value == trustActionView {
					meta = item.Meta
				}
			}
			if meta != tt.wantMeta {
				t.Fatalf("view row meta = %q, want %q", meta, tt.wantMeta)
			}
		})
	}
}

func writeProjectFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeProjectFiles writes .splice/<name> files under ws and returns the
// config.json path the card describes.
func writeProjectFiles(t *testing.T, ws string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(ws, ".splice")
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "config.json")
}

// The card names each hook command, marks the ones that would not run, and
// names each MCP server's command and args.
func TestTrustCardShowsHookAndMCPCommands(t *testing.T) {
	ws := t.TempDir()
	configPath := writeProjectFiles(t, ws, map[string]string{
		"config.json": `{"mcp":{"servers":{"fs":{"command":"npx","args":["-y","fs-server"]},"web":{"url":"https://mcp.example/sse"}}}}`,
		"hooks.json":  `{"enabled":true,"hooks":[{"id":"a","event":"beforeTool","command":"make","args":["lint"],"enabled":true},{"id":"b","event":"afterTool","command":"rm","args":["-rf","x"],"enabled":false}]}`,
	})
	cfg := describeProjectTrustConfig(configPath)
	card := strings.Join(trustConfigCardLines(cfg, 120), "\n")
	for _, want := range []string{
		"hook command",
		"beforeTool: make lint",
		"afterTool: rm -rf x (off)",
		"mcp server",
		"fs: npx -y fs-server",
		"web: url https://mcp.example/sse",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("trust card missing %q:\n%s", want, card)
		}
	}
}

// Secret values in MCP args and the decision advisor endpoint are not shown.
func TestTrustCardRedactsSecretsInCommandsAndEndpoints(t *testing.T) {
	ws := t.TempDir()
	configPath := writeProjectFiles(t, ws, map[string]string{
		"config.json": `{"mcp":{"servers":{"api":{"command":"srv","args":["--token","SECRETVALUE123"]}}},` +
			`"decisionAdvisor":{"backend":"http","endpoint":"https://user:PASSWORD456@advisor.example/v1"}}`,
	})
	card := strings.Join(trustConfigCardLines(describeProjectTrustConfig(configPath), 120), "\n")
	for _, secret := range []string{"SECRETVALUE123", "PASSWORD456"} {
		if strings.Contains(card, secret) {
			t.Errorf("trust card leaked %q:\n%s", secret, card)
		}
	}
}

// verify.testCommand, the decision advisor target, and the project specialists
// are disclosed.
func TestTrustCardShowsVerifyAdvisorAndSpecialists(t *testing.T) {
	ws := t.TempDir()
	configPath := writeProjectFiles(t, ws, map[string]string{
		"config.json":             `{"verify":{"testCommand":["pytest","-q"]},"decisionAdvisor":{"backend":"jev","endpoint":"https://advisor.example"}}`,
		"specialists/reviewer.md": "---\nname: reviewer\ndescription: Reviews code\n---\nPrompt.",
	})
	cfg := describeProjectTrustConfig(configPath)
	if cfg.Empty() {
		t.Fatal("a config with verify, advisor, or specialists must not be empty")
	}
	card := strings.Join(trustConfigCardLines(cfg, 120), "\n")
	for _, want := range []string{
		"verify testCommand",
		"pytest -q",
		"decision advisor",
		"backend jev",
		"endpoint https://advisor.example",
		"specialists",
		"1 project specialist in .splice/specialists",
		"reviewer",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("trust card missing %q:\n%s", want, card)
		}
	}
}

// A long command list is cut at trustCardListLimit rows and the rest are counted.
func TestTrustCardBoundsCommandLists(t *testing.T) {
	ws := t.TempDir()
	var hooks []string
	for i := 0; i < trustCardListLimit+5; i++ {
		hooks = append(hooks, `{"id":"h`+strings.Repeat("x", i)+`","event":"beforeTool","command":"cmd`+strings.Repeat("y", i)+`","enabled":true}`)
	}
	configPath := writeProjectFiles(t, ws, map[string]string{
		"config.json": `{}`,
		"hooks.json":  `{"enabled":true,"hooks":[` + strings.Join(hooks, ",") + `]}`,
	})
	lines := trustConfigCardLines(describeProjectTrustConfig(configPath), 120)
	rows := 0
	for _, line := range lines {
		if strings.Contains(line, "hook command") {
			rows++
		}
	}
	if rows != trustCardListLimit {
		t.Fatalf("hook command rows = %d, want %d", rows, trustCardListLimit)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "and 5 more") {
		t.Fatalf("overflow row missing:\n%s", strings.Join(lines, "\n"))
	}
}

// Terminal control sequences in a hook command are stripped before the card
// shows them.
func TestTrustCardSanitizesHookCommand(t *testing.T) {
	ws := t.TempDir()
	configPath := writeProjectFiles(t, ws, map[string]string{
		"config.json": `{}`,
		"hooks.json":  `{"enabled":true,"hooks":[{"id":"a","event":"beforeTool","command":"echo\u001b[2Jpwned","enabled":true}]}`,
	})
	card := strings.Join(trustConfigCardLines(describeProjectTrustConfig(configPath), 120), "\n")
	if strings.Contains(card, "\x1b") {
		t.Fatalf("trust card kept an escape sequence: %q", card)
	}
	if !strings.Contains(card, "pwned") {
		t.Fatalf("hook command text missing: %q", card)
	}
}

// TestTrustCardDisclosesAgentNodesWithToolsAndPermission pins the trust card
// row and list for an agent node in a project pipeline file. The node runs a
// model with its tools, so the card names both.
func TestTrustCardDisclosesAgentNodesWithToolsAndPermission(t *testing.T) {
	ws := t.TempDir()
	writeTrustConfigFixture(t, ws)
	pipeline := `{"version": 1, "name": "investigate", "splice_min_version": "0.3.0", "nodes": [` +
		`{"name": "investigate", "type": "agent", "prompt": "Find {{intent}}.", "tools": ["read_file", "grep"], "permission": "ask", "max_turns": 3, "budget": {"input_max": 1000, "output_max": 500}}]}`
	if err := os.WriteFile(filepath.Join(ws, ".splice", "pipeline.json"), []byte(pipeline), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := describeProjectTrustConfig(filepath.Join(ws, ".splice", "config.json"))
	if cfg.PipelineParseError != "" {
		t.Fatalf("agent pipeline failed to load: %s", cfg.PipelineParseError)
	}
	want := "investigate: tools read_file, grep; permission ask (prompts for every write, command, and network call); up to 3 turns; input 1000, output 500 tokens; prompt \"Find {{intent}}.\""
	if len(cfg.PipelineAgents) != 1 || cfg.PipelineAgents[0] != want {
		t.Fatalf("agent nodes = %v, want [%s]", cfg.PipelineAgents, want)
	}
	card := strings.Join(trustConfigCardLines(cfg, 100), "\n")
	for _, wantLine := range []string{"1 agent node in .splice/pipeline.json", "investigate: tools read_file, grep; permission ask (prompts for every write, command, and network call); up to 3 turns; input 1000, output 500 tokens; prompt \"Find {{intent}}.\""} {
		if !strings.Contains(card, wantLine) {
			t.Fatalf("trust card missing %q:\n%s", wantLine, card)
		}
	}
}
