package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/redaction"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

type stageModelWizardStep int

const (
	stageModelWizardStepOverview stageModelWizardStep = iota
	stageModelWizardStepEditDefault
	stageModelWizardStepEditEscalation
	stageModelWizardStepEditStage
)

const stageModelEditRowCount = 5

type stageModelPickerKind int

const (
	stageModelPickerNone stageModelPickerKind = iota
	stageModelPickerProvider
	stageModelPickerModel
	stageModelPickerEffort
)

type stageModelOption struct {
	label string
	value string
	meta  string
}

type stageModelEditFields struct {
	providerCursor int
	model          string
	effortCursor   int
	rowCursor      int // provider, model, effort, save, cancel
}

type stageModelWizardState struct {
	step                   stageModelWizardStep
	config                 schemas.StageModelConfigFile
	initialConfig          schemas.StageModelConfigFile
	providers              []config.ProviderProfile
	modelOptionsByProvider map[string][]stageModelOption
	editTarget             string
	editFields             stageModelEditFields
	picker                 stageModelPickerKind
	pickerCursor           int
	pickerQuery            string
	err                    string
	overviewCursor         int
	confirmDiscard         bool
}

type stageModelStageRow struct {
	name        string
	design      bool
	description string
}

var (
	stageModelWizardMinWidth   = 60
	stageModelWizardWidth      = 92
	stageModelWizardEffortOpts = []string{"", "minimal", "low", "medium", "high"}
	stageModelWizardEffortLbls = []string{"auto", "minimal", "low", "medium", "high"}
)

func knownStageModelStages() []stageModelStageRow {
	// F14a: /stages exposes only routing targets that are effective today.
	// code_writer and test_generator are model-backed pipeline stages. The
	// reserved deterministic and design names below remain loadable and
	// preserved in stage-models.json but are not presented as editable rows
	// because they are not model routing targets.
	return []stageModelStageRow{
		{name: "code_writer", description: "writes and modifies code"},
		{name: "test_generator", description: "generates tests"},
	}
}

// reservedInactiveStageNames are stage names that /stages must never present as
// editable rows. They are not model routing targets: static_analyzer,
// security_auditor, and test_runner are deterministic and model-free, while
// plan_critic and design_crystallize are design-phase stages that route
// through a separate path. Any of these names may still appear in
// stage-models.json and is preserved unchanged on save.
var reservedInactiveStageNames = map[string]bool{
	"static_analyzer":    true,
	"security_auditor":   true,
	"test_runner":        true,
	"plan_critic":        true,
	"design_crystallize": true,
}

func newStageModelWizard(userConfigPath string, savedProviders []config.ProviderProfile, activeProfile config.ProviderProfile) (*stageModelWizardState, error) {
	path := stageModelConfigPath(userConfigPath)
	cfg, err := schemas.LoadStageModelConfig(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}

	providers := make([]config.ProviderProfile, 0, len(savedProviders))
	for _, p := range savedProviders {
		if strings.TrimSpace(p.Name) != "" {
			providers = append(providers, p)
		}
	}
	if len(providers) == 0 && config.HasProviderProfile(activeProfile) {
		providers = append(providers, activeProfile)
	}

	if strings.TrimSpace(cfg.Default.ProviderProfile) == "" {
		cfg.Default = schemas.StageModelConfig{
			ProviderProfile: activeProfile.Name,
			Model:           activeProfile.Model,
			ReasoningEffort: "",
		}
	}

	wizard := &stageModelWizardState{
		step:                   stageModelWizardStepOverview,
		config:                 cfg,
		initialConfig:          cfg,
		providers:              providers,
		modelOptionsByProvider: map[string][]stageModelOption{},
	}
	for _, profile := range providers {
		wizard.mergeModelOptions(profile.Name, []stageModelOption{{label: profile.Model, value: profile.Model}})
	}
	return wizard, nil
}

func (m model) populateStageModelWizardModels(wizard *stageModelWizardState) {
	if wizard == nil {
		return
	}
	for _, profile := range wizard.providers {
		items := m.savedProviderModelPickerItems(profile, "", "")
		options := make([]stageModelOption, 0, len(items)+1)
		for _, item := range items {
			options = append(options, stageModelOption{label: item.Label, value: item.Value, meta: modelPickerItemDetail(item)})
		}
		options = append(options, stageModelOption{label: profile.Model, value: profile.Model})
		wizard.mergeModelOptions(profile.Name, options)
	}
}

func (w *stageModelWizardState) mergeModelOptions(providerName string, options []stageModelOption) {
	if w == nil {
		return
	}
	if w.modelOptionsByProvider == nil {
		w.modelOptionsByProvider = map[string][]stageModelOption{}
	}
	key := strings.TrimSpace(providerName)
	seen := map[string]bool{}
	merged := make([]stageModelOption, 0, len(w.modelOptionsByProvider[key])+len(options))
	for _, option := range append(w.modelOptionsByProvider[key], options...) {
		value := strings.TrimSpace(option.value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		option.value = value
		if strings.TrimSpace(option.label) == "" {
			option.label = value
		}
		merged = append(merged, option)
	}
	w.modelOptionsByProvider[key] = merged
}

func stageModelConfigPath(userConfigPath string) string {
	if strings.TrimSpace(userConfigPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(userConfigPath), "stage-models.json")
}

func (w *stageModelWizardState) isDirty() bool {
	if w == nil {
		return false
	}
	return !reflect.DeepEqual(w.config, w.initialConfig)
}

func (w *stageModelWizardState) knownStageRows() []stageModelStageRow {
	known := knownStageModelStages()
	rows := make([]stageModelStageRow, 0, len(known)+len(w.config.Stages))
	seen := make(map[string]bool, len(known)+len(w.config.Stages))
	for _, r := range known {
		rows = append(rows, r)
		seen[r.name] = true
	}
	for name := range w.config.Stages {
		if reservedInactiveStageNames[name] {
			// Preserved in JSON but not shown as an editable row.
			continue
		}
		if !seen[name] {
			rows = append(rows, stageModelStageRow{name: name, design: false, description: "(unknown stage)"})
		}
	}
	return rows
}

func (w *stageModelWizardState) currentOverviewRow() stageModelOverviewRow {
	if w == nil {
		return stageModelOverviewRow{}
	}
	rows := w.overviewRows()
	if len(rows) == 0 {
		return stageModelOverviewRow{}
	}
	w.overviewCursor = clampInt(w.overviewCursor, 0, len(rows)-1)
	return rows[w.overviewCursor]
}

func (w *stageModelWizardState) currentEditTarget() string {
	if w == nil {
		return ""
	}
	if w.step == stageModelWizardStepEditDefault {
		return "default"
	}
	if w.step == stageModelWizardStepEditEscalation {
		return "escalation"
	}
	return w.editTarget
}

func (w *stageModelWizardState) move(delta int) {
	if w == nil {
		return
	}
	if w.step == stageModelWizardStepOverview {
		rows := w.overviewRows()
		if len(rows) == 0 {
			return
		}
		w.overviewCursor = ((w.overviewCursor+delta)%len(rows) + len(rows)) % len(rows)
		return
	}
}

func (w *stageModelWizardState) overviewRowStageName(row stageModelOverviewRow) string {
	label := row.label
	if label == "default" || label == "escalation" {
		return label
	}
	// Strip " (design)" suffix if present.
	if idx := strings.Index(label, " ("); idx > 0 {
		return label[:idx]
	}
	return label
}

func (w *stageModelWizardState) advance() {
	if w == nil {
		return
	}
	w.err = ""
	switch w.step {
	case stageModelWizardStepOverview:
		w.openEditForCurrentRow()
	case stageModelWizardStepEditDefault, stageModelWizardStepEditEscalation, stageModelWizardStepEditStage:
		w.activateEditRow()
	}
}

func (w *stageModelWizardState) retreat() {
	if w == nil {
		return
	}
	w.err = ""
	switch w.step {
	case stageModelWizardStepOverview:
		// no-op; overview is the top level
	case stageModelWizardStepEditDefault, stageModelWizardStepEditEscalation, stageModelWizardStepEditStage:
		w.step = stageModelWizardStepOverview
		w.editFields = stageModelEditFields{}
		w.editTarget = ""
		w.picker = stageModelPickerNone
		w.pickerCursor = 0
		w.pickerQuery = ""
		w.confirmDiscard = false
	}
}

func (w *stageModelWizardState) openEditForCurrentRow() {
	if w == nil {
		return
	}
	row := w.currentOverviewRow()
	target := w.overviewRowStageName(row)
	switch target {
	case "default":
		w.editTarget = "default"
		w.step = stageModelWizardStepEditDefault
		w.setEditFields(w.config.Default)
	case "escalation":
		w.editTarget = "escalation"
		w.step = stageModelWizardStepEditEscalation
		var cfg schemas.StageModelConfig
		if w.config.Escalation != nil {
			cfg = *w.config.Escalation
		}
		w.setEditFields(cfg)
	default:
		w.editTarget = target
		w.step = stageModelWizardStepEditStage
		cfg, _ := w.config.Resolve(target)
		w.setEditFields(cfg)
	}
}

func (w *stageModelWizardState) setEditFields(cfg schemas.StageModelConfig) {
	if w == nil {
		return
	}
	w.editFields = stageModelEditFields{
		providerCursor: w.providerIndex(cfg.ProviderProfile),
		model:          cfg.Model,
		effortCursor:   w.effortIndex(cfg.ReasoningEffort),
		rowCursor:      0,
	}
	w.picker = stageModelPickerNone
	w.pickerCursor = 0
	w.pickerQuery = ""
}

func (w *stageModelWizardState) providerIndex(name string) int {
	if w == nil {
		return 0
	}
	name = strings.TrimSpace(name)
	for i, p := range w.providers {
		if strings.TrimSpace(p.Name) == name {
			return i
		}
	}
	return 0
}

func (w *stageModelWizardState) effortIndex(effort string) int {
	effort = strings.TrimSpace(effort)
	for i, e := range stageModelWizardEffortOpts {
		if e == effort {
			return i
		}
	}
	return 0
}

func (w *stageModelWizardState) moveEditRow(delta int) {
	if w == nil {
		return
	}
	w.editFields.rowCursor = ((w.editFields.rowCursor+delta)%stageModelEditRowCount + stageModelEditRowCount) % stageModelEditRowCount
	w.err = ""
}

func (w *stageModelWizardState) activateEditRow() {
	if w == nil {
		return
	}
	w.err = ""
	w.pickerQuery = ""
	switch w.editFields.rowCursor {
	case 0:
		if len(w.providers) == 0 {
			w.err = "no saved providers; run /provider first"
			return
		}
		w.picker = stageModelPickerProvider
		w.pickerCursor = clampInt(w.editFields.providerCursor, 0, len(w.providers)-1)
	case 1:
		options := w.currentModelOptions()
		if len(options) == 0 {
			w.err = "no models available for this provider"
			return
		}
		w.picker = stageModelPickerModel
		w.pickerCursor = optionIndex(options, w.editFields.model)
	case 2:
		w.picker = stageModelPickerEffort
		w.pickerCursor = clampInt(w.editFields.effortCursor, 0, len(stageModelWizardEffortOpts)-1)
	case 3:
		w.saveEditFieldsToTarget()
	case 4:
		w.retreat()
	}
}

func optionIndex(options []stageModelOption, value string) int {
	value = strings.TrimSpace(value)
	for index, option := range options {
		if option.value == value {
			return index
		}
	}
	return 0
}

func (w *stageModelWizardState) currentProvider() config.ProviderProfile {
	if w == nil || len(w.providers) == 0 {
		return config.ProviderProfile{}
	}
	w.editFields.providerCursor = clampInt(w.editFields.providerCursor, 0, len(w.providers)-1)
	return w.providers[w.editFields.providerCursor]
}

func (w *stageModelWizardState) currentModelOptions() []stageModelOption {
	if w == nil {
		return nil
	}
	name := strings.TrimSpace(w.currentProvider().Name)
	return w.modelOptionsByProvider[name]
}

func (w *stageModelWizardState) pickerOptions() []stageModelOption {
	if w == nil {
		return nil
	}
	switch w.picker {
	case stageModelPickerProvider:
		options := make([]stageModelOption, 0, len(w.providers))
		for _, profile := range w.providers {
			options = append(options, stageModelOption{label: profile.Name, value: profile.Name, meta: profile.Model})
		}
		return options
	case stageModelPickerModel:
		return filterStageModelOptions(w.currentModelOptions(), w.pickerQuery)
	case stageModelPickerEffort:
		options := make([]stageModelOption, 0, len(stageModelWizardEffortOpts))
		for index, effort := range stageModelWizardEffortOpts {
			options = append(options, stageModelOption{label: stageModelWizardEffortLbls[index], value: effort})
		}
		return options
	default:
		return nil
	}
}

func filterStageModelOptions(options []stageModelOption, query string) []stageModelOption {
	items := make([]pickerItem, 0, len(options))
	for _, option := range options {
		items = append(items, pickerItem{Label: option.label, Value: option.value, Meta: option.meta})
	}
	picker := commandPicker{
		items:    append([]pickerItem{}, items...),
		allItems: append([]pickerItem{}, items...),
		query:    query,
	}
	picker.applyQuery()
	filtered := make([]stageModelOption, 0, len(picker.items))
	for _, item := range picker.items {
		filtered = append(filtered, stageModelOption{label: item.Label, value: item.Value, meta: item.Meta})
	}
	return filtered
}

func (w *stageModelWizardState) appendPickerQuery(runes []rune) {
	if w == nil || w.picker != stageModelPickerModel {
		return
	}
	for _, r := range runes {
		if r < 32 {
			continue
		}
		w.pickerQuery += string(r)
	}
	w.pickerCursor = 0
}

func (w *stageModelWizardState) deletePickerQueryRune() {
	if w == nil || w.picker != stageModelPickerModel || w.pickerQuery == "" {
		return
	}
	runes := []rune(w.pickerQuery)
	w.pickerQuery = string(runes[:len(runes)-1])
	w.pickerCursor = 0
}

func (w *stageModelWizardState) movePicker(delta int) {
	if w == nil {
		return
	}
	options := w.pickerOptions()
	if len(options) == 0 {
		return
	}
	w.pickerCursor = ((w.pickerCursor+delta)%len(options) + len(options)) % len(options)
}

func (w *stageModelWizardState) confirmPicker() {
	if w == nil {
		return
	}
	options := w.pickerOptions()
	if len(options) == 0 {
		return
	}
	w.pickerCursor = clampInt(w.pickerCursor, 0, len(options)-1)
	switch w.picker {
	case stageModelPickerProvider:
		w.editFields.providerCursor = w.pickerCursor
		w.editFields.model = strings.TrimSpace(w.providers[w.pickerCursor].Model)
	case stageModelPickerModel:
		w.editFields.model = options[w.pickerCursor].value
	case stageModelPickerEffort:
		w.editFields.effortCursor = w.pickerCursor
	}
	w.picker = stageModelPickerNone
	w.pickerCursor = 0
	w.pickerQuery = ""
	w.err = ""
}

func (w *stageModelWizardState) saveEditFieldsToTarget() {
	if w == nil {
		return
	}
	modelID := strings.TrimSpace(w.editFields.model)
	if modelID == "" {
		w.err = "enter a model name"
		return
	}
	if len(w.providers) == 0 {
		w.err = "no saved providers; run /provider first"
		return
	}
	providerCursor := clampInt(w.editFields.providerCursor, 0, len(w.providers)-1)
	cfg := schemas.StageModelConfig{
		ProviderProfile: w.providers[providerCursor].Name,
		Model:           modelID,
		ReasoningEffort: stageModelWizardEffortOpts[w.editFields.effortCursor],
	}
	if err := cfg.Validate(); err != nil {
		w.err = err.Error()
		return
	}

	switch w.editTarget {
	case "default":
		w.config.Default = cfg
	case "escalation":
		w.config.Escalation = &cfg
	default:
		if w.config.Stages == nil {
			w.config.Stages = map[string]schemas.StageModelConfig{}
		}
		w.config.Stages[w.editTarget] = cfg
	}

	if err := w.config.Validate(); err != nil {
		w.err = err.Error()
		// Restore the previous state so the user can retry without leaving the form.
		return
	}

	w.step = stageModelWizardStepOverview
	w.editFields = stageModelEditFields{}
	w.editTarget = ""
	w.picker = stageModelPickerNone
	w.pickerCursor = 0
	w.pickerQuery = ""
	w.confirmDiscard = false
}

func (w *stageModelWizardState) removeCurrentOverride() {
	if w == nil {
		return
	}
	row := w.currentOverviewRow()
	target := w.overviewRowStageName(row)
	switch target {
	case "escalation":
		w.config.Escalation = nil
	case "default":
		// Cannot remove the default; it is always present.
	default:
		delete(w.config.Stages, target)
	}
}

func (w *stageModelWizardState) save(userConfigPath string) error {
	if w == nil {
		return errors.New("stage model wizard is nil")
	}
	path := stageModelConfigPath(userConfigPath)
	if path == "" {
		return errors.New("user config path is empty")
	}
	if err := w.config.Validate(); err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	data, err := json.MarshalIndent(w.config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal stage model config %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	w.initialConfig = w.config
	return nil
}

func (m model) handleStageModelWizardKey(msg tea.KeyMsg) (model, tea.Cmd) {
	if m.stageModelWizard == nil {
		return m, nil
	}
	if m.stageModelWizard.confirmDiscard {
		switch {
		case keyIs(msg, tea.KeyEnter),
			keyText(msg) == "y" || keyText(msg) == "Y":
			m.stageModelWizard = nil
			return m, nil
		case keyIs(msg, tea.KeyEsc),
			keyText(msg) == "n" || keyText(msg) == "N":
			m.stageModelWizard.confirmDiscard = false
			return m, nil
		}
		return m, nil
	}

	switch m.stageModelWizard.step {
	case stageModelWizardStepOverview:
		return m.handleStageModelWizardOverviewKey(msg)
	case stageModelWizardStepEditDefault, stageModelWizardStepEditEscalation, stageModelWizardStepEditStage:
		return m.handleStageModelWizardEditKey(msg)
	}
	return m, nil
}

func (m model) handleStageModelWizardOverviewKey(msg tea.KeyMsg) (model, tea.Cmd) {
	if m.stageModelWizard == nil {
		return m, nil
	}
	switch {
	case keyIs(msg, tea.KeyEsc):
		if m.stageModelWizard.isDirty() {
			m.stageModelWizard.confirmDiscard = true
			return m, nil
		}
		m.stageModelWizard = nil
		return m, nil
	case keyIs(msg, tea.KeyUp):
		m.stageModelWizard.move(-1)
	case keyIs(msg, tea.KeyDown), keyIs(msg, tea.KeyTab):
		m.stageModelWizard.move(1)
	case keyIs(msg, tea.KeyBackspace), keyText(msg) == "d", keyText(msg) == "D":
		m.stageModelWizard.removeCurrentOverride()
	case keyIs(msg, tea.KeyEnter):
		m.stageModelWizard.advance()
	case keyText(msg) == "s" || keyText(msg) == "S":
		if err := m.stageModelWizard.save(m.userConfigPath); err != nil {
			m.stageModelWizard.err = redaction.ErrorMessage(err, redaction.Options{})
		} else {
			m.stageModelWizard = nil
		}
	}
	return m, nil
}

func (m model) handleStageModelWizardEditKey(msg tea.KeyMsg) (model, tea.Cmd) {
	if m.stageModelWizard == nil {
		return m, nil
	}
	w := m.stageModelWizard
	if w.picker != stageModelPickerNone {
		switch {
		case keyIs(msg, tea.KeyEsc), keyIs(msg, tea.KeyLeft):
			w.picker = stageModelPickerNone
			w.pickerCursor = 0
			w.pickerQuery = ""
			w.err = ""
		case keyBackspace(msg):
			w.deletePickerQueryRune()
		case keyCtrl(msg, 'u'):
			if w.picker == stageModelPickerModel {
				w.pickerQuery = ""
				w.pickerCursor = 0
			}
		case keyIs(msg, tea.KeyUp):
			w.movePicker(-1)
		case keyIs(msg, tea.KeyDown), keyIs(msg, tea.KeyTab):
			w.movePicker(1)
		case keyIs(msg, tea.KeyEnter), keyIs(msg, tea.KeyRight):
			w.confirmPicker()
		case keyPrintable(msg):
			w.appendPickerQuery(keyRunes(msg))
		}
		return m, nil
	}

	switch {
	case keyIs(msg, tea.KeyEsc), keyIs(msg, tea.KeyLeft):
		w.retreat()
	case keyIs(msg, tea.KeyUp):
		w.moveEditRow(-1)
	case keyIs(msg, tea.KeyDown), keyIs(msg, tea.KeyTab):
		w.moveEditRow(1)
	case keyIs(msg, tea.KeyEnter), keyIs(msg, tea.KeyRight):
		w.activateEditRow()
	}
	return m, nil
}

func (m model) stageModelWizardOverlay(width int) string {
	if m.stageModelWizard == nil {
		return ""
	}
	return m.stageModelWizard.render(width)
}

func (w *stageModelWizardState) render(width int) string {
	if w == nil {
		return ""
	}
	overlayWidth := stageModelWizardWidth
	if width > 0 && overlayWidth > width {
		overlayWidth = maxInt(stageModelWizardMinWidth, width)
	}
	innerWidth := maxInt(20, overlayWidth-4)

	lines := []string{
		zeroTheme.faint.Render(w.stepLine()),
		zeroTheme.line.Render(strings.Repeat("-", innerWidth)),
	}
	if w.err != "" {
		lines = append(lines, zeroTheme.red.Render("error: "+w.err), "")
	}
	if w.confirmDiscard {
		lines = append(lines, w.renderDiscardConfirm(innerWidth)...)
	} else {
		switch w.step {
		case stageModelWizardStepOverview:
			lines = append(lines, w.renderOverview(innerWidth)...)
		case stageModelWizardStepEditDefault, stageModelWizardStepEditEscalation, stageModelWizardStepEditStage:
			if w.picker != stageModelPickerNone {
				lines = append(lines, w.renderPicker(innerWidth)...)
			} else {
				lines = append(lines, w.renderEdit(innerWidth)...)
			}
		}
	}

	lines = append(lines,
		zeroTheme.line.Render(strings.Repeat("-", innerWidth)),
		zeroTheme.faint.Render(w.footer()),
	)

	block := styledBlockFillTitle(overlayWidth, "Stage model routing", lines, zeroTheme.lineStrong, lipgloss.NewStyle())
	if width > overlayWidth {
		return indentBlock(block, (width-overlayWidth)/2)
	}
	return block
}

func (w *stageModelWizardState) renderDiscardConfirm(width int) []string {
	return []string{
		zeroTheme.accent.Render("Discard unsaved changes?"),
		"",
		fitStyledLine(zeroTheme.ink.Render("y")+zeroTheme.faint.Render(" discard   ")+zeroTheme.ink.Render("n")+zeroTheme.faint.Render(" / Esc keep editing"), width),
	}
}

func (w *stageModelWizardState) stepLine() string {
	if w == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if w.step == stageModelWizardStepOverview || w.confirmDiscard {
		parts = append(parts, "[overview]")
	} else {
		parts = append(parts, "overview")
	}
	if w.step == stageModelWizardStepEditDefault {
		parts = append(parts, "[default]")
	} else if w.step == stageModelWizardStepEditEscalation {
		parts = append(parts, "[escalation]")
	} else if w.step == stageModelWizardStepEditStage {
		parts = append(parts, "["+w.editTarget+"]")
	} else {
		parts = append(parts, "edit")
	}
	return strings.Join(parts, "  ")
}

func (w *stageModelWizardState) footer() string {
	if w == nil {
		return ""
	}
	if w.confirmDiscard {
		return "y discard   n/Esc keep editing"
	}
	switch w.step {
	case stageModelWizardStepOverview:
		return "up/down move   Enter edit   s save   d/Backspace delete   Esc close"
	case stageModelWizardStepEditDefault, stageModelWizardStepEditEscalation, stageModelWizardStepEditStage:
		if w.picker == stageModelPickerModel {
			return "type search   Backspace edit   Ctrl+U clear   up/down move   Enter choose   Esc back"
		}
		if w.picker != stageModelPickerNone {
			return "up/down move   Enter choose   Esc back"
		}
		return "up/down move   Enter select   Esc cancel"
	}
	return "Esc close"
}

func (w *stageModelWizardState) renderOverview(width int) []string {
	if w == nil {
		return nil
	}
	lines := []string{zeroTheme.accent.Render("Per-stage model routing"), ""}

	rows := w.overviewRows()
	maxVisible := minInt(12, len(rows))
	start := selectableListStart(len(rows), maxVisible, w.overviewCursor)
	for offset, row := range rows[start : start+maxVisible] {
		lines = append(lines, w.renderOverviewRow(width, start+offset, row))
	}

	if len(w.providers) == 0 {
		lines = append(lines, "", zeroTheme.red.Render("No saved providers. Run /provider first."))
	}
	return lines
}

type stageModelOverviewRow struct {
	label   string
	detail  string
	removed bool
}

func (w *stageModelWizardState) overviewRows() []stageModelOverviewRow {
	if w == nil {
		return nil
	}
	rows := make([]stageModelOverviewRow, 0, 2+len(w.config.Stages)+len(knownStageModelStages()))
	rows = append(rows, stageModelOverviewRow{label: "default", detail: stageModelConfigSummary(w.config.Default)})
	if w.config.Escalation != nil {
		rows = append(rows, stageModelOverviewRow{label: "escalation", detail: stageModelConfigSummary(*w.config.Escalation)})
	} else {
		rows = append(rows, stageModelOverviewRow{label: "escalation", detail: "(not set)"})
	}
	for _, r := range w.knownStageRows() {
		label := r.name
		if r.design {
			label += " (design)"
		}
		if cfg, ok := w.config.Stages[r.name]; ok {
			rows = append(rows, stageModelOverviewRow{label: label, detail: stageModelConfigSummary(cfg)})
		} else {
			rows = append(rows, stageModelOverviewRow{label: label, detail: "(default)"})
		}
	}
	return rows
}

func stageModelConfigSummary(cfg schemas.StageModelConfig) string {
	effort := cfg.ReasoningEffort
	if effort == "" {
		effort = "auto"
	}
	return fmt.Sprintf("%s · %s · %s", cfg.ProviderProfile, cfg.Model, effort)
}

func (w *stageModelWizardState) renderOverviewRow(width int, index int, row stageModelOverviewRow) string {
	selected := index == w.overviewCursor
	surface := transparentSurface
	marker := surface(zeroTheme.faintest).Render("  ")
	if selected {
		surface = zeroTheme.onSel
		marker = surface(zeroTheme.accent).Render("> ")
	}
	left := marker + surface(zeroTheme.ink).Render(row.label)
	right := zeroTheme.faint.Render(row.detail)
	line := fitStyledLine(left+"   "+right, width)
	return line
}

func (w *stageModelWizardState) renderEdit(width int) []string {
	if w == nil {
		return nil
	}
	target := w.currentEditTarget()
	pretty := target
	if target == "default" {
		pretty = "Default"
	} else if target == "escalation" {
		pretty = "Escalation"
	}
	lines := []string{zeroTheme.accent.Render(pretty), zeroTheme.faint.Render("Choose a setting, then press Enter.")}
	if target == "escalation" {
		lines = append(lines, zeroTheme.faint.Render("Used when trajectory monitor escalates on cycle or oscillation."))
	}

	providerName := "(none)"
	if len(w.providers) > 0 {
		providerName = w.currentProvider().Name
	}
	modelName := strings.TrimSpace(w.editFields.model)
	if modelName == "" {
		modelName = "(required)"
	}
	effortIndex := clampInt(w.editFields.effortCursor, 0, len(stageModelWizardEffortLbls)-1)
	rows := []stageModelOption{
		{label: "Provider", meta: providerName},
		{label: "Model", meta: modelName},
		{label: "Effort", meta: stageModelWizardEffortLbls[effortIndex]},
		{label: "Apply changes", meta: "return to overview"},
		{label: "Cancel", meta: "discard this edit"},
	}
	for index, row := range rows {
		lines = append(lines, renderStageModelSelectableRow(width, index == w.editFields.rowCursor, row))
	}
	if len(w.providers) == 0 {
		lines = append(lines, "", zeroTheme.red.Render("No saved providers. Run /provider first."))
	}
	return lines
}

func (w *stageModelWizardState) renderPicker(width int) []string {
	if w == nil {
		return nil
	}
	heading := "Choose an option"
	switch w.picker {
	case stageModelPickerProvider:
		heading = "Choose provider"
	case stageModelPickerModel:
		heading = "Choose model"
	case stageModelPickerEffort:
		heading = "Choose reasoning effort"
	}
	lines := []string{zeroTheme.accent.Render(heading)}
	if w.picker == stageModelPickerModel {
		lines = append(lines, renderModelPickerSearchLine(w.pickerQuery, width))
	}
	options := w.pickerOptions()
	if len(options) == 0 {
		message := "  no options available"
		if w.picker == stageModelPickerModel && strings.TrimSpace(w.pickerQuery) != "" {
			message = "  no matching models"
		}
		return append(lines, zeroTheme.faint.Render(message))
	}
	w.pickerCursor = clampInt(w.pickerCursor, 0, len(options)-1)
	maxVisible := minInt(10, len(options))
	start := selectableListStart(len(options), maxVisible, w.pickerCursor)
	for offset, option := range options[start : start+maxVisible] {
		lines = append(lines, renderStageModelSelectableRow(width, start+offset == w.pickerCursor, option))
	}
	return lines
}

func renderStageModelSelectableRow(width int, selected bool, option stageModelOption) string {
	surface := transparentSurface
	marker := surface(zeroTheme.faintest).Render("  ")
	if selected {
		surface = zeroTheme.onSel
		marker = surface(zeroTheme.accent).Render("❯ ")
	}
	line := marker + surface(zeroTheme.ink).Render(option.label)
	if meta := strings.TrimSpace(option.meta); meta != "" {
		line += surface(zeroTheme.faint).Render("   " + meta)
	}
	return fitStyledLine(line, width)
}
