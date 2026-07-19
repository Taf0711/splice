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

func TestStageModelWizardEditorNavigationUsesMenuRows(t *testing.T) {
	m := model{stageModelWizard: stageWizardFixture(t)}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	if m.stageModelWizard.step != stageModelWizardStepEditDefault {
		t.Fatalf("step = %d, want edit default", m.stageModelWizard.step)
	}
	if got := m.stageModelWizard.editFields.rowCursor; got != 0 {
		t.Fatalf("row cursor = %d, want provider row", got)
	}

	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyDown))
	if got := m.stageModelWizard.editFields.rowCursor; got != 1 {
		t.Fatalf("row cursor = %d, want model row", got)
	}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyUp))
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyUp))
	if got := m.stageModelWizard.editFields.rowCursor; got != 4 {
		t.Fatalf("row cursor = %d, want wrapped cancel row", got)
	}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyTab))
	if got := m.stageModelWizard.editFields.rowCursor; got != 0 {
		t.Fatalf("row cursor = %d, want Tab to wrap to provider", got)
	}
	if m.stageModelWizard.config.Default.ProviderProfile != "anthropic" {
		t.Fatal("menu navigation mutated config")
	}
}

func TestStageModelWizardProviderPickerConfirmAndCancel(t *testing.T) {
	m := model{stageModelWizard: stageWizardFixture(t)}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter)) // overview -> editor
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter)) // provider picker
	if m.stageModelWizard.picker != stageModelPickerProvider {
		t.Fatalf("picker = %d, want provider", m.stageModelWizard.picker)
	}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyDown))
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEsc))
	if m.stageModelWizard.editFields.providerCursor != 0 {
		t.Fatal("Esc from provider picker changed draft")
	}

	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyDown))
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	if m.stageModelWizard.picker != stageModelPickerNone {
		t.Fatal("provider picker did not close after confirmation")
	}
	if m.stageModelWizard.editFields.providerCursor != 1 || m.stageModelWizard.editFields.model != "gpt-4.1" {
		t.Fatalf("provider draft = %d/%q, want openai/gpt-4.1", m.stageModelWizard.editFields.providerCursor, m.stageModelWizard.editFields.model)
	}
	if m.stageModelWizard.config.Default.ProviderProfile != "anthropic" {
		t.Fatal("provider confirmation mutated config before Save")
	}
}

func TestStageModelWizardModelPickerSearch(t *testing.T) {
	m := model{stageModelWizard: stageWizardFixture(t)}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyDown))
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	if m.stageModelWizard.picker != stageModelPickerModel {
		t.Fatalf("picker = %d, want model", m.stageModelWizard.picker)
	}

	m, _ = m.handleStageModelWizardKey(testKeyText("haiku"))
	if m.stageModelWizard.pickerQuery != "haiku" {
		t.Fatalf("query = %q, want haiku", m.stageModelWizard.pickerQuery)
	}
	options := m.stageModelWizard.pickerOptions()
	if len(options) != 1 || options[0].value != "claude-haiku-4" {
		t.Fatalf("filtered options = %+v", options)
	}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyBackspace))
	if m.stageModelWizard.pickerQuery != "haik" {
		t.Fatalf("query after Backspace = %q", m.stageModelWizard.pickerQuery)
	}
	m, _ = m.handleStageModelWizardKey(testKeyCtrl('u'))
	if m.stageModelWizard.pickerQuery != "" || len(m.stageModelWizard.pickerOptions()) != 2 {
		t.Fatalf("Ctrl+U did not restore options: query=%q options=%+v", m.stageModelWizard.pickerQuery, m.stageModelWizard.pickerOptions())
	}

	m, _ = m.handleStageModelWizardKey(testKeyText("missing-model"))
	if len(m.stageModelWizard.pickerOptions()) != 0 {
		t.Fatal("expected no matching models")
	}
	view := strings.Join(stripStageWizardANSI(m.stageModelWizard.renderPicker(80)), "\n")
	if !strings.Contains(view, "search > missing-model") || !strings.Contains(view, "no matching models") {
		t.Fatalf("no-match view missing search state:\n%s", view)
	}

	m, _ = m.handleStageModelWizardKey(testKeyCtrl('u'))
	m, _ = m.handleStageModelWizardKey(testKeyText("haiku"))
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	if m.stageModelWizard.editFields.model != "claude-haiku-4" || m.stageModelWizard.pickerQuery != "" {
		t.Fatalf("confirmed model/query = %q/%q", m.stageModelWizard.editFields.model, m.stageModelWizard.pickerQuery)
	}
}

func TestStageModelWizardModelAndEffortPickers(t *testing.T) {
	m := model{stageModelWizard: stageWizardFixture(t)}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	m.stageModelWizard.editFields.providerCursor = 1
	m.stageModelWizard.editFields.model = "gpt-4.1"

	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyDown)) // model row
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	if m.stageModelWizard.picker != stageModelPickerModel {
		t.Fatalf("picker = %d, want model", m.stageModelWizard.picker)
	}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyDown))
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	if got := m.stageModelWizard.editFields.model; got != "gpt-4.1-mini" {
		t.Fatalf("model draft = %q, want gpt-4.1-mini", got)
	}

	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyDown)) // effort row
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	if m.stageModelWizard.picker != stageModelPickerEffort {
		t.Fatalf("picker = %d, want effort", m.stageModelWizard.picker)
	}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyDown))
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	if got := m.stageModelWizard.editFields.effortCursor; got != 1 {
		t.Fatalf("effort cursor = %d, want minimal", got)
	}
	if m.stageModelWizard.config.Default.Model != "claude-sonnet-4" {
		t.Fatal("picker confirmation mutated config before Save")
	}
}

func TestStageModelWizardSaveRowIsOnlyApplyAction(t *testing.T) {
	m := model{stageModelWizard: stageWizardFixture(t)}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	m.stageModelWizard.editFields.providerCursor = 1
	m.stageModelWizard.editFields.model = "gpt-4.1-mini"
	m.stageModelWizard.editFields.effortCursor = 4

	for range 3 {
		m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyDown))
	}
	if m.stageModelWizard.editFields.rowCursor != 3 {
		t.Fatal("expected Save changes row")
	}
	m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
	if m.stageModelWizard.step != stageModelWizardStepOverview {
		t.Fatal("Save changes did not return to overview")
	}
	got := m.stageModelWizard.config.Default
	if got.ProviderProfile != "openai" || got.Model != "gpt-4.1-mini" || got.ReasoningEffort != "high" {
		t.Fatalf("saved default = %+v", got)
	}
}

func TestStageModelWizardCancelAndEscDiscardDraft(t *testing.T) {
	for _, cancelWithEsc := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel row", true: "escape"}[cancelWithEsc], func(t *testing.T) {
			m := model{stageModelWizard: stageWizardFixture(t)}
			m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
			m.stageModelWizard.editFields.providerCursor = 1
			m.stageModelWizard.editFields.model = "gpt-4.1"
			if cancelWithEsc {
				m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEsc))
			} else {
				m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyUp)) // provider -> cancel
				m, _ = m.handleStageModelWizardKey(stageWizardKey(tea.KeyEnter))
			}
			if m.stageModelWizard.step != stageModelWizardStepOverview {
				t.Fatal("cancel did not return to overview")
			}
			if got := m.stageModelWizard.config.Default; got.ProviderProfile != "anthropic" || got.Model != "claude-sonnet-4" {
				t.Fatalf("cancel mutated config: %+v", got)
			}
		})
	}
}

func TestStageModelWizardAddAndRemoveStageOverride(t *testing.T) {
	wizard := stageWizardFixture(t)
	wizard.overviewCursor = 2
	wizard.advance()
	wizard.editFields.providerCursor = 1
	wizard.editFields.model = "gpt-4.1"
	wizard.editFields.effortCursor = 3
	wizard.editFields.rowCursor = 3
	wizard.advance()

	cfg, ok := wizard.config.Stages["code_writer"]
	if !ok || cfg.ProviderProfile != "openai" || cfg.Model != "gpt-4.1" {
		t.Fatalf("code_writer override = %+v, present=%v", cfg, ok)
	}
	wizard.overviewCursor = 2
	wizard.removeCurrentOverride()
	if _, ok := wizard.config.Stages["code_writer"]; ok {
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

func TestStageModelWizardEmptyModelCannotSave(t *testing.T) {
	wizard := stageWizardFixture(t)
	wizard.advance()
	wizard.editFields.model = ""
	wizard.editFields.rowCursor = 3
	wizard.advance()
	if wizard.step != stageModelWizardStepEditDefault || wizard.err == "" {
		t.Fatalf("empty model save step=%d err=%q", wizard.step, wizard.err)
	}
	if wizard.config.Default.Model != "claude-sonnet-4" {
		t.Fatal("failed save mutated config")
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
	m.width = 110
	m.height = 36
	m.input.SetValue("/stages")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if cmd != nil {
		t.Fatal("/stages should open without starting an agent command")
	}
	if m.stageModelWizard == nil {
		t.Fatal("/stages did not open the wizard")
	}
	assertContains(t, plainRender(t, m.View()), "Per-stage model routing")

	m.stageModelWizard.modelOptionsByProvider["openai"] = []stageModelOption{
		{label: "Alpha Model", value: "alpha-model"},
		{label: "Beta Mini", value: "beta-mini"},
	}
	updated, _ = m.Update(testKey(tea.KeyEnter)) // overview -> default editor
	m = updated.(model)
	editorView := plainRender(t, m.View())
	assertContains(t, editorView, "Choose a setting, then press Enter.")
	assertContains(t, editorView, "❯ Provider")

	updated, _ = m.Update(testKey(tea.KeyDown)) // model row
	m = updated.(model)
	updated, _ = m.Update(testKey(tea.KeyEnter)) // model picker
	m = updated.(model)
	pickerView := plainRender(t, m.View())
	assertContains(t, pickerView, "Choose model")
	assertContains(t, pickerView, "search >")

	updated, _ = m.Update(testKeyText("mini"))
	m = updated.(model)
	filteredView := plainRender(t, m.View())
	assertContains(t, filteredView, "search > mini")
	assertContains(t, filteredView, "❯ Beta Mini")
	assertNotContains(t, filteredView, "Alpha Model")

	updated, _ = m.Update(testKey(tea.KeyEnter)) // confirm beta-mini
	m = updated.(model)
	assertContains(t, plainRender(t, m.View()), "beta-mini")
	updated, _ = m.Update(testKey(tea.KeyDown)) // effort
	m = updated.(model)
	updated, _ = m.Update(testKey(tea.KeyDown)) // apply
	m = updated.(model)
	updated, _ = m.Update(testKey(tea.KeyEnter)) // apply draft
	m = updated.(model)
	assertContains(t, plainRender(t, m.View()), "openai · beta-mini · auto")

	updated, _ = m.Update(testKeyText("s")) // write stage-models.json and close
	m = updated.(model)
	if m.stageModelWizard != nil {
		t.Fatal("wizard remained open after overview save")
	}
	loaded, err := schemas.LoadStageModelConfig(stageModelConfigPath(configPath))
	if err != nil {
		t.Fatalf("reload saved stage config: %v", err)
	}
	if loaded.Default.ProviderProfile != "openai" || loaded.Default.Model != "beta-mini" {
		t.Fatalf("saved default = %+v", loaded.Default)
	}
}

func stripStageWizardANSI(lines []string) []string {
	plain := make([]string, len(lines))
	for index, line := range lines {
		plain[index] = ansi.Strip(line)
	}
	return plain
}
