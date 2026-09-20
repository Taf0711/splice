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
		"UNTRUSTED PROJECT CONFIG",
		"not loaded",
		"config:",
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
