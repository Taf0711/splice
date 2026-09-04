package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/providermodeldiscovery"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func stageWizardFixture(t *testing.T) *stageModelWizardState {
	t.Helper()
	dir := t.TempDir()
	providers := []config.ProviderProfile{
		{Name: "anthropic", CatalogID: "anthropic", Model: "claude-sonnet-4"},
		{Name: "openai", CatalogID: "openai", Model: "gpt-4.1"},
	}
	wizard, err := newStageModelWizard(filepath.Join(dir, "config.json"), providers, providers[0])
	if err != nil {
		t.Fatalf("newStageModelWizard: %v", err)
	}
	wizard.modelOptionsByProvider["anthropic"] = []stageModelOption{
		{label: "Claude Sonnet 4", value: "claude-sonnet-4"},
		{label: "Claude Haiku", value: "claude-haiku-4"},
	}
	wizard.modelOptionsByProvider["openai"] = []stageModelOption{
		{label: "GPT 4.1", value: "gpt-4.1"},
		{label: "GPT 4.1 Mini", value: "gpt-4.1-mini"},
	}
	return wizard
}

func stageWizardKey(code rune) tea.KeyMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}

func TestNewStageModelWizardSeedsDefaultFromActiveProfile(t *testing.T) {
	wizard := stageWizardFixture(t)
	if wizard.config.Default.ProviderProfile != "anthropic" {
		t.Fatalf("default provider = %q, want anthropic", wizard.config.Default.ProviderProfile)
	}
	if wizard.config.Default.Model != "claude-sonnet-4" {
		t.Fatalf("default model = %q, want claude-sonnet-4", wizard.config.Default.Model)
	}
	if len(wizard.providers) != 2 {
		t.Fatalf("providers count = %d, want 2", len(wizard.providers))
	}
	if wizard.isDirty() {
		t.Fatal("freshly seeded wizard should not be dirty")
	}
}

func TestNewStageModelWizardFallsBackToActiveProvider(t *testing.T) {
	dir := t.TempDir()
	active := config.ProviderProfile{Name: "openai", Model: "gpt-4.1"}
	wizard, err := newStageModelWizard(filepath.Join(dir, "config.json"), nil, active)
	if err != nil {
		t.Fatal(err)
	}
	if len(wizard.providers) != 1 || wizard.providers[0].Name != "openai" {
		t.Fatalf("providers = %+v, want active provider fallback", wizard.providers)
	}
	if options := wizard.modelOptionsByProvider["openai"]; len(options) != 1 || options[0].value != "gpt-4.1" {
		t.Fatalf("model options = %+v", options)
	}
}

func TestNewStageModelWizardLoadsExisting(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	content := `{
		"default": {"provider_profile": "openai", "model": "gpt-4.1", "reasoning_effort": "high"},
		"stages": {
			"code_writer": {"provider_profile": "anthropic", "model": "claude-sonnet-4"}
		}
	}`
	if err := os.WriteFile(stageModelConfigPath(configPath), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	providers := []config.ProviderProfile{
		{Name: "openai", Model: "gpt-4.1"},
		{Name: "anthropic", Model: "claude-sonnet-4"},
	}
	wizard, err := newStageModelWizard(configPath, providers, providers[0])
	if err != nil {
		t.Fatalf("newStageModelWizard: %v", err)
	}
	if wizard.config.Default.Model != "gpt-4.1" {
		t.Fatalf("default model = %q, want gpt-4.1", wizard.config.Default.Model)
	}
	if cfg, ok := wizard.config.Stages["code_writer"]; !ok || cfg.Model != "claude-sonnet-4" {
		t.Fatalf("code_writer override missing or wrong: %+v", cfg)
	}
}

func TestNewStageModelWizardLoadError(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(stageModelConfigPath(configPath), []byte(`{invalid json`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := newStageModelWizard(configPath, []config.ProviderProfile{{Name: "openai"}}, config.ProviderProfile{Name: "openai", Model: "gpt-4.1"})
	if err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestStageModelWizardOverviewNavigationWraps(t *testing.T) {
	m := model{stageModelWizard: stageWizardFixture(t)}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyDown))
	if got := m.stageModelWizard.overviewCursor; got != 1 {
		t.Fatalf("cursor = %d, want 1", got)
	}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyUp))
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyUp))
	if got := m.stageModelWizard.overviewCursor; got != 3 {
		t.Fatalf("cursor = %d, want wrapped to 3", got)
	}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyTab))
	if got := m.stageModelWizard.overviewCursor; got != 0 {
		t.Fatalf("cursor = %d, want Tab wrap to 0", got)
	}
	if m.stageModelWizard.config.Default.ProviderProfile != "anthropic" {
		t.Fatal("menu navigation mutated config")
	}
}

func TestStageModelWizardPickerSearchNoMatch(t *testing.T) {
	m := model{stageModelWizard: stageWizardFixture(t)}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	m, _ = m.handleStageModelWizardKey(testKeyText("missing-model"))
	if len(m.stageModelWizard.pickerOptions()) != 0 {
		t.Fatal("expected no matching models")
	}
	view := strings.Join(stripStageWizardANSI(m.stageModelWizard.renderPicker(80)), "\n")
	if !strings.Contains(view, "search > missing-model") || !strings.Contains(view, "no matching models") {
		t.Fatalf("no-match view missing search state:\n%s", view)
	}
	// Esc closes the picker and keeps the wizard.
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEsc))
	if m.stageModelWizard.picker != stageModelPickerNone || m.stageModelWizard == nil {
		t.Fatal("Esc did not close the picker into the overview")
	}
}

func TestStageModelWizardModelAndEffortPickers(t *testing.T) {
	m := model{stageModelWizard: stageWizardFixture(t)}
	// One-pass flow: Enter on the highlighted row opens the model picker;
	// confirming writes provider+model straight into that row's config.
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	if m.stageModelWizard.picker != stageModelPickerModel {
		t.Fatalf("picker = %d, want model", m.stageModelWizard.picker)
	}
	if m.stageModelWizard.editTarget != "default" {
		t.Fatalf("editTarget = %q, want default", m.stageModelWizard.editTarget)
	}
	// Search narrows the cross-provider list, then Enter applies.
	m, _ = m.handleStageModelWizardKey(testKeyText("gpt-4.1"))
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	got := m.stageModelWizard.config.Default
	if got.Model != "gpt-4.1" || got.ProviderProfile != "openai" {
		t.Fatalf("applied default = %+v, want openai/gpt-4.1", got)
	}
	if m.stageModelWizard.picker != stageModelPickerNone {
		t.Fatal("picker did not close after confirm")
	}

	// Effort adjusts inline: → from auto lands on minimal.
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyRight))
	if got := m.stageModelWizard.config.Default.ReasoningEffort; got != "minimal" {
		t.Fatalf("effort after → = %q, want minimal", got)
	}
	// Clamped at the top: no wrap.
	for i := 0; i < 10; i++ {
		m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyRight))
	}
	if got := m.stageModelWizard.config.Default.ReasoningEffort; got != "high" {
		t.Fatalf("effort after clamp = %q, want high", got)
	}
	// ← walks back down.
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyLeft))
	if got := m.stageModelWizard.config.Default.ReasoningEffort; got != "medium" {
		t.Fatalf("effort after ← = %q, want medium", got)
	}
}

func TestStageModelWizardSaveFromOverview(t *testing.T) {
	m := model{stageModelWizard: stageWizardFixture(t)}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter)) // open picker
	m, _ = m.handleStageModelWizardKey(testKeyText("gpt-4.1"))
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter)) // apply openai/gpt-4.1
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyRight)) // effort minimal
	if !m.stageModelWizard.isDirty() {
		t.Fatal("edits did not mark the wizard dirty")
	}
	dir := t.TempDir()
	if err := m.stageModelWizard.save(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := m.stageModelWizard.config.Default
	if got.ProviderProfile != "openai" || got.Model != "gpt-4.1" || got.ReasoningEffort != "minimal" {
		t.Fatalf("saved default = %+v", got)
	}
}

func TestStageModelWizardEscClosesPickerThenDiscardGuard(t *testing.T) {
	m := model{stageModelWizard: stageWizardFixture(t)}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter)) // picker opens
	if m.stageModelWizard.picker != stageModelPickerModel {
		t.Fatal("expected model picker")
	}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyDown))
	// Esc at the picker closes the picker, not the wizard, and keeps the draft.
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEsc))
	if m.stageModelWizard.picker != stageModelPickerNone {
		t.Fatal("Esc did not close the picker")
	}
	if m.stageModelWizard == nil {
		t.Fatal("Esc at the picker closed the wizard")
	}
	if m.stageModelWizard.editTarget != "default" {
		t.Fatalf("editTarget = %q, want default (draft kept)", m.stageModelWizard.editTarget)
	}
	// A pristine Esc closes outright (nothing to discard).
	m2 := model{stageModelWizard: stageWizardFixture(t)}
	m2, _ = m2.handleStageModelWizardKey(stageWizardKey(tea.KeyEsc))
	if m2.stageModelWizard != nil {
		t.Fatal("pristine Esc did not close the wizard")
	}
	// Make a real edit (inline effort), then Esc opens the discard guard.
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyRight))
	if !m.stageModelWizard.isDirty() {
		t.Fatal("effort adjust did not dirty the wizard")
	}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEsc))
	if m.stageModelWizard == nil {
		t.Fatal("wizard closed without the discard guard despite edits")
	}
	if !m.stageModelWizard.confirmDiscard {
		t.Fatal("dirty Esc did not open the discard guard")
	}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEsc))
	if m.stageModelWizard == nil || m.stageModelWizard.confirmDiscard {
		t.Fatal("n did not keep editing")
	}
	// Reopen the guard, then y discards: unsaved changes revert and the wizard closes.
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEsc))
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	if m.stageModelWizard != nil {
		t.Fatal("y did not close the wizard")
	}
}

func TestStageModelWizardAddAndRemoveStageOverride(t *testing.T) {
	m := model{stageModelWizard: stageWizardFixture(t)}
	m.stageModelWizard.overviewCursor = 2                            // code_writer
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter)) // picker for code_writer
	if m.stageModelWizard.editTarget != "code_writer" {
		t.Fatalf("editTarget = %q, want code_writer", m.stageModelWizard.editTarget)
	}
	m, _ = m.handleStageModelWizardKey(testKeyText("gpt-4.1"))
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	cfg, ok := m.stageModelWizard.config.Stages["code_writer"]
	if !ok || cfg.ProviderProfile != "openai" || cfg.Model != "gpt-4.1" {
		t.Fatalf("code_writer override = %+v, present=%v", cfg, ok)
	}
	// d removes the override.
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyBackspace))
	if _, ok := m.stageModelWizard.config.Stages["code_writer"]; ok {
		t.Fatal("code_writer override was not removed")
	}
}

func TestStageModelWizardRemoveEscalation(t *testing.T) {
	wizard := stageWizardFixture(t)
	cfg := schemas.StageModelConfig{ProviderProfile: "openai", Model: "gpt-4.1"}
	wizard.config.Escalation = &cfg
	wizard.overviewCursor = 1
	wizard.removeCurrentOverride()
	if wizard.config.Escalation != nil {
		t.Fatal("escalation was not removed")
	}
}

func TestStageModelWizardSaveWritesFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	wizard, err := newStageModelWizard(configPath, []config.ProviderProfile{{Name: "openai", Model: "gpt-4.1"}}, config.ProviderProfile{Name: "openai", Model: "gpt-4.1"})
	if err != nil {
		t.Fatal(err)
	}
	wizard.config.Stages = map[string]schemas.StageModelConfig{
		"code_writer": {ProviderProfile: "openai", Model: "gpt-4.1", ReasoningEffort: "high"},
	}
	if err := wizard.save(configPath); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(stageModelConfigPath(configPath))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
	}
	loaded, err := schemas.LoadStageModelConfig(stageModelConfigPath(configPath))
	if err != nil || loaded.Stages["code_writer"].Model != "gpt-4.1" {
		t.Fatalf("reloaded config = %+v, err=%v", loaded, err)
	}
	if wizard.isDirty() {
		t.Fatal("wizard dirty after disk save")
	}
}

func TestStageModelWizardSaveValidationFails(t *testing.T) {
	wizard := stageWizardFixture(t)
	wizard.config.Default = schemas.StageModelConfig{}
	if err := wizard.save(filepath.Join(t.TempDir(), "config.json")); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestStageModelWizardDirtyTracking(t *testing.T) {
	wizard := stageWizardFixture(t)
	wizard.config.Default.ReasoningEffort = "high"
	if !wizard.isDirty() {
		t.Fatal("expected dirty after change")
	}
	wizard.config.Default.ReasoningEffort = ""
	if wizard.isDirty() {
		t.Fatal("expected clean after revert")
	}
}

func TestStageModelWizardKnownStageRows(t *testing.T) {
	wizard := &stageModelWizardState{config: schemas.StageModelConfigFile{Default: schemas.StageModelConfig{ProviderProfile: "x", Model: "y"}}}
	rows := wizard.knownStageRows()
	// F14a: only code_writer and test_generator are editable model-backed rows.
	// Reserved deterministic and design stages are hidden.
	expectedBase := []string{"code_writer", "test_generator"}
	if len(rows) != len(expectedBase) {
		t.Fatalf("rows = %v, want only %v", rows, expectedBase)
	}
	for i, want := range expectedBase {
		if rows[i].name != want {
			t.Fatalf("row %d = %q, want %q", i, rows[i].name, want)
		}
	}
}

func TestStageModelWizardPreservesUnknownExtensionRow(t *testing.T) {
	wizard := &stageModelWizardState{config: schemas.StageModelConfigFile{
		Default: schemas.StageModelConfig{ProviderProfile: "x", Model: "y"},
		Stages: map[string]schemas.StageModelConfig{
			"my_custom_stage": {ProviderProfile: "x", Model: "z"},
		},
	}}
	rows := wizard.knownStageRows()
	// knownStageRows returns base rows plus unknown config rows not in reservedInactiveStageNames.
	foundCustom := false
	for _, r := range rows {
		if r.name == "my_custom_stage" {
			foundCustom = true
			break
		}
	}
	if !foundCustom {
		t.Fatalf("unknown extension stage my_custom_stage should be visible, got %v", rows)
	}
}

func TestStageModelWizardHidesReservedInactiveStages(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	wizard := &stageModelWizardState{config: schemas.StageModelConfigFile{
		Default: schemas.StageModelConfig{ProviderProfile: "x", Model: "y"},
		Stages: map[string]schemas.StageModelConfig{
			"static_analyzer":    {ProviderProfile: "x", Model: "m1"},
			"security_auditor":   {ProviderProfile: "x", Model: "m2"},
			"test_runner":        {ProviderProfile: "x", Model: "m3"},
			"plan_critic":        {ProviderProfile: "x", Model: "m4"},
			"design_crystallize": {ProviderProfile: "x", Model: "m5"},
		},
	}}
	rows := wizard.knownStageRows()
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.name] = true
	}
	reservedNames := []string{"static_analyzer", "security_auditor", "test_runner", "plan_critic", "design_crystallize"}
	for _, reserved := range reservedNames {
		if seen[reserved] {
			t.Fatalf("reserved stage %q should be hidden but appears in knownStageRows: %v", reserved, rows)
		}
	}
	if err := wizard.save(configPath); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := schemas.LoadStageModelConfig(stageModelConfigPath(configPath))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for _, reserved := range reservedNames {
		if _, ok := loaded.Stages[reserved]; !ok {
			t.Fatalf("hidden reserved stage %q was deleted on save", reserved)
		}
	}
}

func TestStageModelWizardRenderUsesInheritedSelectionMarker(t *testing.T) {
	wizard := stageWizardFixture(t)
	wizard.advance()
	plain := strings.Join(stripStageWizardANSI(wizard.renderEdit(80)), "\n")
	if !strings.Contains(plain, "❯ Provider") {
		t.Fatalf("editor selection marker missing:\n%s", plain)
	}
	wizard.activateEditRow()
	plain = strings.Join(stripStageWizardANSI(wizard.renderPicker(80)), "\n")
	if !strings.Contains(plain, "Choose provider") || !strings.Contains(plain, "❯ anthropic") {
		t.Fatalf("picker selection marker missing:\n%s", plain)
	}
}

func TestStageModelWizardApplyRejectsEmptyModel(t *testing.T) {
	wizard := stageWizardFixture(t)
	wizard.editTarget = "default"
	wizard.applyModelChoice("", "openai")
	if wizard.err == "" {
		t.Fatal("empty model choice did not set an error")
	}
	if wizard.config.Default.Model != "claude-sonnet-4" {
		t.Fatal("failed apply mutated config")
	}
}

func TestStageModelWizardEndToEndFeature(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	profile := config.ProviderProfile{Name: "openai", CatalogID: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}
	m := newModel(context.Background(), Options{
		UserConfigPath:  configPath,
		ProviderName:    profile.Name,
		ModelName:       profile.Model,
		ProviderProfile: profile,
		SavedProviders:  []config.ProviderProfile{profile},
	})
	m.width = 130
	m.height = 36
	m.input.SetValue("/stages")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if cmd == nil {
		t.Fatal("/stages with a saved provider should start live model discovery")
	}
	if m.stageModelWizard == nil {
		t.Fatal("/stages did not open the wizard")
	}
	assertContains(t, plainRender(t, m.View()), "Per-stage model routing")

	m.stageModelWizard.modelOptionsByProvider["openai"] = []stageModelOption{
		{label: "Alpha Model", value: "alpha-model", ownerProvider: "openai"},
		{label: "Beta Mini", value: "beta-mini", ownerProvider: "openai"},
	}
	updated, _ = m.Update(testKey(tea.KeyEnter)) // open model picker for default
	m = updated.(model)
	pickerView := plainRender(t, m.View())
	assertContains(t, pickerView, "Choose model for default")
	assertContains(t, pickerView, "search >")

	updated, _ = m.Update(testKeyText("mini"))
	m = updated.(model)
	filteredView := plainRender(t, m.View())
	assertContains(t, filteredView, "search > mini")
	assertContains(t, filteredView, "❯ Beta Mini")
	assertNotContains(t, filteredView, "Alpha Model")

	updated, _ = m.Update(testKey(tea.KeyEnter)) // confirm beta-mini
	m = updated.(model)
	assertContains(t, plainRender(t, m.View()), "openai · beta-mini")

	// Effort inline, then save: the one-pass surface is complete.
	updated, _ = m.Update(testKey(tea.KeyRight))
	m = updated.(model)
	updated, _ = m.Update(testKeyText("s")) // write stage-models.json and close
	m = updated.(model)
	if m.stageModelWizard != nil {
		t.Fatal("wizard remained open after overview save")
	}
	loaded, err := schemas.LoadStageModelConfig(stageModelConfigPath(configPath))
	if err != nil {
		t.Fatalf("reload saved stage config: %v", err)
	}
	if loaded.Default.ProviderProfile != "openai" || loaded.Default.Model != "beta-mini" || loaded.Default.ReasoningEffort != "minimal" {
		t.Fatalf("saved default = %+v", loaded.Default)
	}
}

func TestStageModelWizardMergesLiveDiscoveredModels(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	profile := config.ProviderProfile{Name: "openai", CatalogID: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}
	m := newModel(context.Background(), Options{
		UserConfigPath:  configPath,
		ProviderName:    profile.Name,
		ModelName:       profile.Model,
		ProviderProfile: profile,
		SavedProviders:  []config.ProviderProfile{profile},
		DiscoverProviderModels: func(ctx context.Context, p config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
			return []providermodeldiscovery.Model{
				{ID: "live-alpha", Description: "Live Alpha"},
				{ID: "live-beta", Description: "Live Beta"},
			}, nil
		},
	})
	m.input.SetValue("/stages")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if cmd == nil {
		t.Fatal("/stages with a saved provider should start live model discovery")
	}
	if m.stageModelWizard == nil {
		t.Fatal("/stages did not open the stage wizard")
	}

	// Before discovery lands, the picker shows the immediate fallback options.
	if !containsStageOption(m.stageModelWizard.currentModelOptions(), "gpt-4.1") {
		t.Fatalf("fallback options before discovery = %+v, want saved model gpt-4.1", m.stageModelWizard.modelOptionsByProvider["openai"])
	}

	// Deliver the live discovery result into the open wizard.
	updated, _ = m.Update(cmd())
	m = updated.(model)

	opts := m.stageModelWizard.modelOptionsByProvider["openai"]
	if !containsStageOption(opts, "live-alpha") || !containsStageOption(opts, "live-beta") {
		t.Fatalf("wizard options after discovery = %+v, want live-alpha/live-beta", opts)
	}
	if !containsStageOption(opts, "gpt-4.1") {
		t.Fatalf("wizard options lost the saved model after discovery: %+v", opts)
	}

	// Both user-visible edit targets (escalation and code_writer) read their model
	// options from the same modelOptionsByProvider source. If either splits to a
	// separate data source later, this assertion catches the regression.
	for _, target := range []struct {
		name          string
		overviewIndex int
	}{
		{name: "escalation", overviewIndex: 1},
		{name: "code_writer", overviewIndex: 2},
	} {
		m.stageModelWizard.overviewCursor = target.overviewIndex
		m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter)) // open picker for the row
		if m.stageModelWizard.picker != stageModelPickerModel {
			t.Fatalf("%s edit did not open the model picker", target.name)
		}
		modelOptions := m.stageModelWizard.pickerOptions()
		if !containsStageOption(modelOptions, "live-alpha") || !containsStageOption(modelOptions, "live-beta") {
			t.Fatalf("%s model options = %+v, want live models", target.name, modelOptions)
		}
		m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEsc)) // back to overview
	}
}

func containsStageOption(options []stageModelOption, value string) bool {
	for _, option := range options {
		if option.value == value {
			return true
		}
	}
	return false
}

func stripStageWizardANSI(lines []string) []string {
	plain := make([]string, len(lines))
	for index, line := range lines {
		plain[index] = ansi.Strip(line)
	}
	return plain
}

// TestKnownStageModelStagesAreOnlyRoutingTargets is the D3 parity pin: the
// stage model wizard exposes only model-backed stages as editable routing
// targets, preserving the invariant that the pipeline panel renders from
// Kind metadata, not from stage-name conditionals.
func TestKnownStageModelStagesAreOnlyRoutingTargets(t *testing.T) {
	stages := knownStageModelStages()
	names := make([]string, len(stages))
	for i, s := range stages {
		names[i] = s.name
	}
	if got := strings.Join(names, ","); got != "code_writer,test_generator" {
		t.Fatalf("knownStageModelStages = %v, want exactly code_writer,test_generator", got)
	}
}
