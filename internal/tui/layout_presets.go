package tui

// layout_presets.go (P3 GAP-K rest, DoD 36): named layout presets as bundles
// over the EXISTING layout state. Post one-column (owner Tension-3 decision)
// the preset controls the projection knobs that remain:
//
//	default    — detailed off, sidebar available, persistent plan off
//	compact    — quiet narration, sidebar hidden, persistent plan off
//	execution  — quiet narration, sidebar hidden, persistent plan on
//	review     — detailed off, sidebar available, persistent plan on
//	minimal    — quiet narration, sidebar hidden, persistent plan off
//
// A preset is a projection change only: nothing recorded changes, the
// settled cache re-measures, and it persists to Preferences.Layout so the
// choice survives restart. The narrow tiers (compact/minimal) exist because
// the one-column decision removed the persistent sidebar as a rival — a
// preset now only decides which OPTIONAL surfaces are up.

import (
	"strings"

	"github.com/Taf0711/splice/internal/config"
)

// layoutPreset is one named bundle. Fields are the post-one-column knobs.
type layoutPreset struct {
	name           string
	description    string
	verbosity      narrationVerbosity
	sidebarHidden  bool
	planPersistent bool
}

// layoutPresets is the closed preset set (DoD 36). Order is the listing order.
var layoutPresets = []layoutPreset{
	{name: "default", description: "normal narration, sidebar, plan panel off", verbosity: verbosityDetailed, sidebarHidden: false, planPersistent: false},
	{name: "compact", description: "quiet narration, sidebar hidden", verbosity: verbosityQuiet, sidebarHidden: true, planPersistent: false},
	{name: "execution", description: "quiet narration, sidebar hidden, plan pinned", verbosity: verbosityQuiet, sidebarHidden: true, planPersistent: true},
	{name: "review", description: "normal narration, sidebar, plan pinned", verbosity: verbosityNormal, sidebarHidden: false, planPersistent: true},
	{name: "minimal", description: "quiet narration, sidebar hidden, no extras", verbosity: verbosityQuiet, sidebarHidden: true, planPersistent: false},
}

// layoutPresetByName resolves a preset name (exact match).
func layoutPresetByName(name string) (layoutPreset, bool) {
	for _, preset := range layoutPresets {
		if preset.name == name {
			return preset, true
		}
	}
	return layoutPreset{}, false
}

// applyLayoutPreset applies the bundle: verbosity, sidebar, plan pin. Pure
// projection — the settled cache re-measures because the visible row set
// changed (the GAP-L rule).
func (m model) applyLayoutPreset(preset layoutPreset) model {
	m.narrationVerbosityLevel = preset.verbosity
	m.sidebarHidden = preset.sidebarHidden
	m.planPanelPersistent = preset.planPersistent
	m.altScreenSettledWidth = 0
	return m
}

// layoutStateText renders the /layout state view: the active bundle values
// and the available presets.
func (m model) layoutStateText() string {
	active := "custom"
	for _, preset := range layoutPresets {
		if m.narrationVerbosityLevel == preset.verbosity && m.sidebarHidden == preset.sidebarHidden && m.planPanelPersistent == preset.planPersistent {
			active = preset.name
			break
		}
	}
	names := make([]string, 0, len(layoutPresets))
	for _, preset := range layoutPresets {
		names = append(names, preset.name)
	}
	return renderCommandOutput(commandOutput{
		Title:  "Layout",
		Status: commandStatusOK,
		Sections: []commandSection{{
			Title: "State",
			Lines: []string{
				"active preset: " + active,
				"narration: " + m.narrationVerbosityLevel.label(),
				"sidebar: " + boolOnOff(!m.sidebarHidden),
				"plan panel: " + boolOnOff(m.planPanelPersistent),
			},
		}},
		Hints: []string{"run /layout <name> to apply a preset (available: " + strings.Join(names, ", ") + ")"},
	})
}

// handleLayoutPresetCommand applies a named preset, persists it, and reports.
// Bare /layout keeps its existing toggle; /layout list shows the state view.
func (m model) handleLayoutPresetCommand(arg string) (model, string) {
	arg = strings.TrimSpace(strings.ToLower(arg))
	if arg == "" || arg == "list" || arg == "status" {
		return m, m.layoutStateText()
	}
	preset, ok := layoutPresetByName(arg)
	if !ok {
		return m, "Unknown layout preset: " + arg + ". " + layoutPresetNamesHint()
	}
	m = m.applyLayoutPreset(preset)
	if note := m.persistLayoutPreference(preset.name); note != "" {
		return m, "Preset " + preset.name + " applied. " + note
	}
	return m, "Preset " + preset.name + " applied (" + preset.description + "). Saved."
}

// persistLayoutPreference writes the preset name to user config
// (Preferences.Layout), mirroring persistThemePreference. Best-effort: a
// short note on failure, "" on success or when there is no config path.
func (m model) persistLayoutPreference(name string) string {
	if strings.TrimSpace(m.userConfigPath) == "" {
		return ""
	}
	if _, err := config.SetLayout(m.userConfigPath, name); err != nil {
		return "note: could not save layout preference (" + err.Error() + ")"
	}
	return ""
}

// layoutPresetNamesHint lists the preset names for error text.
func layoutPresetNamesHint() string {
	names := make([]string, 0, len(layoutPresets))
	for _, preset := range layoutPresets {
		names = append(names, preset.name)
	}
	return "Available: " + strings.Join(names, ", ") + "."
}

// boolOnOff renders a boolean as on/off for the state view.
func boolOnOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
