package tui

// layout_presets_test.go (P3 GAP-K rest, DoD 36): named layout presets.
// Presets are projection bundles over the existing knobs; probes pin the
// closed set, the real /layout <preset> command path, persistence wiring,
// and startup application.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/config"
)

// The preset set is closed and each preset is internally coherent: compact/
// execution/minimal are the quiet narrow tiers; default is the built-in
// starting state (detailed, sidebar, no pin).
func TestLayoutPresetSetClosed(t *testing.T) {
	names := map[string]bool{}
	for _, preset := range layoutPresets {
		if preset.name == "" || names[preset.name] {
			t.Fatalf("preset names must be unique and non-empty, got %q", preset.name)
		}
		names[preset.name] = true
		if _, err := layoutPresetByName(preset.name); !err {
			t.Fatalf("preset %q not resolvable by name", preset.name)
		}
	}
	def, ok := layoutPresetByName("default")
	if !ok || def.verbosity != verbosityDetailed || def.sidebarHidden || def.planPersistent {
		t.Fatalf("default preset must match the built-in starting state: %+v", def)
	}
	if _, ok := layoutPresetByName("nope"); ok {
		t.Fatal("unknown name resolved")
	}
}

// The real command path applies every knob of the bundle.
func TestLayoutPresetCommandAppliesBundle(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)

	updated, cmd := m.Update(testKeyText("/layout execution"))
	if cmd != nil {
		t.Log("typing produced a cmd (unexpected but harmless)")
	}
	updated, _ = updated.(model).Update(testKey(tea.KeyEnter))
	next, ok := updated.(model)
	if !ok {
		t.Fatalf("Enter produced %T, want model", updated)
	}
	if next.narrationVerbosityLevel != verbosityQuiet {
		t.Fatalf("execution preset did not set quiet narration: %d", next.narrationVerbosityLevel)
	}
	if !next.sidebarHidden || !next.planPanelPersistent {
		t.Fatalf("execution preset did not set sidebar/plan knobs: sidebar=%v plan=%v", next.sidebarHidden, next.planPanelPersistent)
	}
	if next.altScreenSettledWidth != 0 {
		t.Fatal("preset application did not invalidate the settled cache")
	}

	updated, _ = next.Update(testKeyText("/layout default"))
	updated, _ = updated.(model).Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if next.narrationVerbosityLevel != verbosityDetailed || next.sidebarHidden || next.planPanelPersistent {
		t.Fatalf("default preset did not restore the built-in state: %+v", next)
	}
}

// Bare /layout keeps its existing plan-panel toggle; /layout list shows the
// state view; an unknown name says so and lists the options.
func TestLayoutCommandBareListUnknown(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)

	updated, _ := m.Update(testKeyText("/layout"))
	updated, _ = updated.(model).Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if !next.planPanelPersistent {
		t.Fatal("bare /layout no longer toggles the plan panel")
	}

	updated, _ = next.Update(testKeyText("/layout list"))
	updated, _ = updated.(model).Update(testKey(tea.KeyEnter))
	next = updated.(model)
	joined := transcriptText(next.transcript)
	if !strings.Contains(joined, "active preset:") {
		t.Fatalf("list output missing the state view:\n%s", joined)
	}

	updated, _ = next.Update(testKeyText("/layout nope"))
	updated, _ = updated.(model).Update(testKey(tea.KeyEnter))
	next = updated.(model)
	joined = transcriptText(next.transcript)
	if !strings.Contains(joined, "Unknown layout preset: nope") || !strings.Contains(joined, "execution") {
		t.Fatalf("unknown preset error missing name/hint:\n%s", joined)
	}
}

// Persistence: the chosen preset name lands in Preferences.Layout through
// the same read-modify-write path as the theme.
func TestLayoutPresetPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	m := newDesignModeTestModel(dir, &fakeProvider{}, nil)
	m.userConfigPath = path

	// Seed a theme first: SetLayout must preserve it (read-modify-write).
	if _, err := config.SetTheme(path, "dracula"); err != nil {
		t.Fatal(err)
	}
	if note := m.persistLayoutPreference("execution"); note != "" {
		t.Fatalf("persist failed: %s", note)
	}
	cfg := config.FileConfig{}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Preferences.Layout != "execution" {
		t.Fatalf("layout not persisted: %q", cfg.Preferences.Layout)
	}
	if cfg.Preferences.Theme != "dracula" {
		t.Fatalf("SetLayout clobbered the theme: %q", cfg.Preferences.Theme)
	}
}

// Startup applies the persisted preset; an unknown stored name fails quiet
// to the built-in defaults instead of erroring.
func TestLayoutPresetAppliedAtStartup(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-test", SavedLayout: "compact"})
	if m.narrationVerbosityLevel != verbosityQuiet || !m.sidebarHidden || m.planPanelPersistent {
		t.Fatalf("startup did not apply the compact preset: %+v", m)
	}
	forgiven := newModel(t.Context(), Options{ModelName: "gpt-test", SavedLayout: "renamed-away"})
	if forgiven.narrationVerbosityLevel != verbosityDetailed || forgiven.sidebarHidden {
		t.Fatal("unknown stored preset must keep the built-in defaults")
	}
}
