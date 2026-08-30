package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/presentation"
)

const (
	stageMarkerBegin = "\x00STAGE"
	stageMarkerEnd   = "\x00"
)

type pipelineStageStatus int

const (
	pipelineStagePending pipelineStageStatus = iota
	pipelineStageRunning
	pipelineStageCompleted
	pipelineStageFailed
	pipelineStageSkipped
	pipelineStageIncomplete
)

type pipelineStageRow struct {
	name     string
	kind     presentation.NodeKind
	status   pipelineStageStatus
	detail   string
	progress int // 0-100
	// reentered is true when a terminal stage (completed/failed/skipped/
	// incomplete) was re-entered as running. This marks the repair loop
	// (test_runner -> code_writer re-entry -> test_runner) without inventing a
	// "repair" stage: it is the same stage being revisited.
	reentered bool
	// workspace is the stage's isolation state (DoD 26): "isolated" or
	// "shared_cwd"; empty means unset (renderer projects shared_cwd).
	workspace    string
	worktreePath string
}

// pipelineMessageRow is one repair-loop message surfaced in the panel (DM4).
// resolved flips true when the matching "repaired" event lands.
type pipelineMessageRow struct {
	from     string
	to       string
	detail   string
	resolved bool
}

type pipelinePanelState struct {
	stages       []pipelineStageRow
	active       bool // true when a pipeline run is in progress
	changedFiles []string
	messages     []pipelineMessageRow
	// Lifecycle/health/gate come from the same presentation.State snapshot
	// (v0.5 phase x health x gate). They are projection inputs, not policy:
	// the pipeline header chip renders them verbatim.
	lifecycle string
	health    string
	gate      string
}

// pipelinePresentation is the immutable display model shared by pipeline
// surfaces. It derives current work and aggregate progress once so renderers do
// not implement lifecycle semantics independently.
type pipelinePresentation struct {
	active   bool
	stages   []pipelineStageRow
	messages []pipelineMessageRow
	current  *pipelineStageRow
	done     int
	total    int
	progress int
	allDone  bool
	failed   bool
	warning  bool
}

func (s pipelinePanelState) presentation() pipelinePresentation {
	p := pipelinePresentation{
		active:   s.active,
		stages:   append([]pipelineStageRow(nil), s.stages...),
		messages: append([]pipelineMessageRow(nil), s.messages...),
		total:    len(s.stages),
	}
	progressUnits := 0
	for i := range p.stages {
		stage := &p.stages[i]
		switch stage.status {
		case pipelineStageCompleted:
			p.done++
			progressUnits += 100
		case pipelineStageFailed:
			p.done++
			progressUnits += 100
			p.failed = true
		case pipelineStageSkipped, pipelineStageIncomplete:
			p.done++
			progressUnits += 100
			p.warning = true
		case pipelineStageRunning:
			progressUnits += stage.progress
			if p.current == nil {
				p.current = stage
			}
		}
	}
	p.allDone = p.total > 0 && p.done == p.total
	if p.total > 0 {
		p.progress = progressUnits / p.total
	}
	return p
}

// applyState rebuilds the panel from a presentation.State snapshot. After
// the P1.2 projection switch this is the ONLY source of pipeline view state:
// the TUI derives nothing from raw pipeline events anymore.
func (s *pipelinePanelState) applyState(state presentation.State) {
	s.stages = nil
	s.messages = nil
	s.changedFiles = nil
	s.active = true
	s.lifecycle = string(state.Lifecycle)
	s.health = string(state.Health.Effective())
	if state.Gate != nil {
		s.gate = string(state.Gate.Kind)
	} else {
		s.gate = ""
	}
	for _, node := range state.Nodes {
		row := pipelineStageRow{
			name:         node.ID,
			kind:         node.Kind,
			status:       pipelineStageStatusFromNode(node.Status),
			progress:     int(node.Progress * 100),
			workspace:    node.Workspace,
			worktreePath: node.WorktreePath,
		}
		// A running node with an intervention against it is a repair
		// re-entry, not a fresh run: the repair loop re-enters a terminal
		// stage (test_runner -> code_writer -> test_runner). The roster
		// keeps exactly the planned stages; the intervention list carries
		// the repair story.
		if node.Status == presentation.NodeStatusRunning && hasInterventionForNode(state.Interventions, node.ID) {
			row.reentered = true
		}
		s.stages = append(s.stages, row)
	}
	for _, intervention := range state.Interventions {
		row := pipelineMessageRow{
			from:   intervention.TargetNodeID,
			detail: intervention.Reason,
		}
		switch {
		case intervention.Kind == presentation.InterventionRollback && intervention.Status == presentation.InterventionProposed:
			row.resolved = false
		case intervention.Kind == presentation.InterventionContinue && intervention.Status == presentation.InterventionApplied:
			row.resolved = true
		default:
			continue
		}
		s.messages = append(s.messages, row)
	}
	for _, file := range state.Files {
		s.changedFiles = append(s.changedFiles, file.Path)
	}
}

// pipelineStageStatusFromNode maps a presentation node status onto the
// panel's display status. NodeStatusPending covers both pending and skipped
// stages: the presentation contract normalizes "skipped" to pending, so the
// panel cannot distinguish them (the skip glyph is a P1.3+ kind concern).
func pipelineStageStatusFromNode(status presentation.NodeStatus) pipelineStageStatus {
	switch status {
	case presentation.NodeStatusRunning:
		return pipelineStageRunning
	case presentation.NodeStatusComplete:
		return pipelineStageCompleted
	case presentation.NodeStatusFailed:
		return pipelineStageFailed
	case presentation.NodeStatusDegraded:
		return pipelineStageIncomplete
	default:
		return pipelineStagePending
	}
}

// hasInterventionForNode reports whether any intervention targets the node.
// The repair loop's message/repaired pair (rollback proposed, continue
// applied) both carry the target node id, so either one marks a re-entry.
func hasInterventionForNode(interventions []presentation.Intervention, nodeID string) bool {
	for _, intervention := range interventions {
		if intervention.TargetNodeID == nodeID {
			return true
		}
	}
	return false
}

// kindTag abbreviates a node kind to a compact tag shown faintly after the
// roster label: the first letter of the kind plus, when the kind has an
// underscore part, the first letter of that part. WRITE -> "w", SECURITY ->
// "s", TEST -> "t", VERIFY -> "v", CUSTOM -> "c", DATA_TRANSFORM -> "dt".
// The kind set is open (presentation.NodeKind.Validate accepts any
// uppercase-safe value), so unknown kinds still render a sensible tag and
// unknown or empty formats fall back to "?". The tag is metadata, never a
// status signal: the glyph and color stay owned by NodeStatus.
func kindTag(kind presentation.NodeKind) string {
	parts := strings.Split(string(kind), "_")
	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToLower(part[:1]))
	}
	if b.Len() == 0 {
		return "?"
	}
	return b.String()
}

// reset clears stage rows and marks a new pipeline run active.
func (s *pipelinePanelState) reset() {
	s.stages = nil
	s.active = true
	s.changedFiles = nil
	s.messages = nil
}

// clear removes all pipeline state and hides the panel.
func (s *pipelinePanelState) clear() {
	s.stages = nil
	s.active = false
	s.changedFiles = nil
	s.messages = nil
	s.lifecycle = ""
	s.health = ""
	s.gate = ""
}

func (s pipelinePanelState) isEmpty() bool {
	return !s.active || len(s.stages) == 0
}

// lifecycleChip renders the phase/health/gate readout for the pipeline
// header, contract form `phase | health | gate` (P8 state chips). Segments
// drop by priority under width pressure (DoD 18) rather than truncating:
// a blocking gate preempts health, health preempts nothing (both are
// alerts), and the phase is the mandatory base. budget is the cell width
// available after the "PIPELINE " prefix and the done/total count.
func (s pipelinePanelState) lifecycleChip(budget int) string {
	if s.lifecycle == "" {
		return ""
	}
	chip := s.lifecycle
	fits := func(extra string) bool {
		return budget <= 0 || lipgloss.Width(chip+extra) <= budget
	}
	// Priority ladder (DoD 18, DoD 22): gate visibility is never sacrificed
	// and the phase is the mandatory base, so health is the only segment
	// that drops under width pressure. Segments drop whole; nothing is
	// ellipsis-truncated mid-word.
	if s.gate != "" {
		full := chip + " | " + s.healthChipText() + " | gate " + s.gate
		if s.healthText() != "" && lipgloss.Width(full) <= budget {
			return full
		}
		return chip + " | gate " + s.gate
	}
	if h := s.healthText(); h != "" && fits(" | "+h) {
		chip += " | " + h
	}
	return chip
}

// healthText returns the health segment or "" when normal/absent (normal
// health is noise, not an alert).
func (s pipelinePanelState) healthText() string {
	if s.health == "" || s.health == "normal" {
		return ""
	}
	return s.health
}

func (s pipelinePanelState) healthChipText() string {
	if h := s.healthText(); h != "" {
		return h
	}
	return "normal"
}

func (s pipelinePanelState) headerLineWithChip(width int, chip string) string {
	return s.presentation().headerLineWithChip(width, chip)
}

func (p pipelinePresentation) headerLineWithChip(width int, chip string) string {
	label := "PIPELINE"
	if strings.TrimSpace(chip) != "" {
		label = "PIPELINE " + chip
	}
	if !p.active || p.total == 0 {
		return sidebarHeader(label, width)
	}
	style := zeroTheme.amber
	switch {
	case p.failed:
		style = zeroTheme.red
	case p.warning:
		style = zeroTheme.amber
	case p.allDone:
		style = zeroTheme.green
	}
	return sidebarHeaderWithCount(label, formatDoneTotal(p.done, p.total), style, width)
}

func (s pipelinePanelState) renderSection(width int, phase int) []string {
	return s.presentation().renderSection(width, phase)
}

// renderStrip renders the narrow pipeline strip, the second consumer of
// pipelinePresentation (the sidebar section is the first). It is a compact
// single-run surface for terminals that cannot host the wide sidebar, shown
// in the pinned footer area. It degrades independently by width:
//
//	wide:  every stage label + a compact truthful progress bar,
//	mid:   the current-running stage label only,
//	tiny:  a truthful header-and-count only.
//
// A run with zero stages yields nil so the caller can skip the surface.
func (p pipelinePresentation) renderStrip(width int, phase int) []string {
	return p.renderStripWithChip(width, phase, "")
}

// renderStripWithChip is renderStrip with an optional header chip (e.g. the
// worktree name), mirroring the wide sidebar's headerLineWithChip. The chip
// renders truncated when it does not fit, so the header degrades gracefully.
func (p pipelinePresentation) renderStripWithChip(width int, phase int, chip string) []string {
	if !p.active || p.total == 0 {
		return nil
	}
	state := p.stripState()
	headColor := zeroTheme.amber
	if state == pipelineStripFailed {
		headColor = zeroTheme.red
	} else if state == pipelineStripDone {
		headColor = zeroTheme.green
	}
	label := "PIPELINE"
	if strings.TrimSpace(chip) != "" {
		label += " " + chip
	}
	// Zero-pad the done counter to the total's digit width so the header never
	// reflows mid-run (0/10 -> 10/10 keeps one stable display width).
	count := formatDoneTotal(p.done, p.total)
	header := label + " " + count

	lines := make([]string, 0, 4)
	switch widthTier(width) {
	case tierFull:
		// Wide: header with count, then a stage-label run, then a compact bar.
		lines = append(lines, headColor.Render(truncateDisplayWidth(header, width)))
		lines = append(lines, " "+p.stripLabels(width-1, phase))
		bar := renderPipelineProgressBar(p.progress, width)
		if bar != "" {
			lines = append(lines, " "+bar)
		}
	case tierMedium:
		// Compact (80-119): header, stage-label run, bar, then the section
		// shortcut row — the sidebar sections become tabs (spec §4.2 rule
		// "sidebar_sections_become_tabs") because the permanent sidebar is
		// removed in compact mode (DoD 16).
		lines = append(lines, headColor.Render(truncateDisplayWidth(header, width)))
		lines = append(lines, " "+p.stripLabels(width-1, phase))
		bar := renderPipelineProgressBar(p.progress, width)
		if bar != "" {
			lines = append(lines, " "+bar)
		}
		lines = append(lines, " "+compactSectionTabs(width))
	case tierNarrow:
		// Mid: header plus the current-running label (or first pending).
		headLabel := p.stripCurrentLabel()
		if headLabel == "" {
			lines = append(lines, headColor.Render(truncateDisplayWidth(header, width)))
		} else {
			lines = append(lines, headColor.Render(truncateDisplayWidth(header+" · "+headLabel, width)))
		}
	default:
		// Tiny: truthful header only (done/total), no stage names.
		lines = append(lines, headColor.Render(truncateDisplayWidth(header, width)))
	}
	return lines
}

// stripCurrentLabel returns the compact label of the running stage, or the
// first not-yet-terminal stage when nothing is running. A terminal roster
// yields "".
func (p pipelinePresentation) stripCurrentLabel() string {
	if p.current != nil {
		return pipelineStageLabel(p.current.name)
	}
	for _, stage := range p.stages {
		if stage.status != pipelineStageCompleted && stage.status != pipelineStageFailed &&
			stage.status != pipelineStageSkipped && stage.status != pipelineStageIncomplete {
			return pipelineStageLabel(stage.name)
		}
	}
	return ""
}

// stripLabels renders every stage label in roster order, glyph-prefixed,
// joined by spaces. Labels use the compact abbreviation so the run stays
// legible at narrower widths.
func (p pipelinePresentation) stripLabels(width int, phase int) string {
	cells := make([]string, 0, len(p.stages))
	for _, stage := range p.stages {
		glyph, style := pipelineStageGlyphAndStyle(stage.status, phase)
		cells = append(cells, style.Render(glyph+" "+pipelineStageLabel(stage.name)))
	}
	return fitRun(cells, width, " ")
}

// fitRun right-packs rendered cells joined by sep into width by dropping
// trailing cells that would overflow. A stage either shows whole or drops, so
// the run never shows a half-written label. Empty when even the first cell is
// too wide.
func fitRun(cells []string, width int, sep string) string {
	keep := make([]string, 0, len(cells))
	extent := 0
	for i, cell := range cells {
		extent += lipgloss.Width(cell)
		if i > 0 {
			extent += lipgloss.Width(sep)
		}
		if extent > width {
			break
		}
		keep = append(keep, cell)
	}
	return strings.Join(keep, sep)
}

func (p pipelinePresentation) renderSection(width int, phase int) []string {
	if !p.active || p.total == 0 {
		return nil
	}
	room := maxInt(4, width-3)
	lines := make([]string, 0, len(p.stages)+7)
	for _, stage := range p.stages {
		glyph, bodyStyle := pipelineStageGlyphAndStyle(stage.status, phase)
		line := " " + glyph + " " + bodyStyle.Render(truncateStep(stage.name, room))
		// Isolation badge (DoD 26): "isolated" lanes are badged distinctly
		// from "shared cwd" so parallel-lane honesty survives the compact
		// panel. Renders only when the whole row fits, like the kind tag.
		if badge := workspaceBadge(stage.workspace); badge != "" && len([]rune(stage.name))+1+len(badge) <= room {
			line += " " + zeroTheme.faint.Render(badge)
		}
		// The kind tag is metadata shown faintly after the label. It renders
		// only when the whole row fits; otherwise the row degrades exactly as
		// P1.2 (name truncated by truncateStep, tag dropped), so narrow widths
		// need no layout changes.
		if tag := kindTag(stage.kind); tag != "" && len([]rune(stage.name))+1+len(tag) <= room {
			line += " " + zeroTheme.faint.Render(tag)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", " "+renderPipelineProgressBar(p.progress, width))
	if p.current != nil {
		lines = append(lines, "")
		lines = append(lines, zeroTheme.muted.Bold(true).Render("CURRENT"))
		lines = append(lines, " "+zeroTheme.faint.Render("stage: ")+zeroTheme.ink.Render(truncateStep(p.current.name, maxInt(4, width-8))))
		// The presentation contract does not carry a per-node action detail, so
		// the action line renders only when a detail is available.
		if strings.TrimSpace(p.current.detail) != "" {
			lines = append(lines, " "+zeroTheme.faint.Render("action: ")+zeroTheme.muted.Render(truncateStep(p.current.detail, maxInt(4, width-9))))
		}
	}
	if len(p.messages) > 0 {
		lines = append(lines, "")
		lines = append(lines, zeroTheme.muted.Bold(true).Render("MESSAGES"))
		start := 0
		if len(p.messages) > 3 {
			start = len(p.messages) - 3
		}
		for _, msg := range p.messages[start:] {
			glyph := zeroTheme.amber.Render("…")
			if msg.resolved {
				glyph = zeroTheme.green.Render("✓")
			}
			body := msg.from
			if msg.to != "" {
				body += " -> " + msg.to
			}
			body += ": " + msg.detail
			lines = append(lines, " "+glyph+" "+zeroTheme.muted.Render(truncateStep(body, room)))
		}
	}
	return lines
}

func (s pipelinePanelState) counts() (done int, total int, allDone bool) {
	p := s.presentation()
	return p.done, p.total, p.allDone
}

// pipelineStripState is the coarse display state the narrow strip renders. It
// does not duplicate lifecycle derivation: it reads the already-derived
// presentation fields (current, reentered, failed, allDone, messages).
type pipelineStripState int

const (
	pipelineStripInactive  pipelineStripState = iota // no run / no stages
	pipelineStripNoRunning                           // active, nothing running yet
	pipelineStripRunning                             // a fresh stage is current
	pipelineStripRepair                              // a terminal stage was re-run
	pipelineStripFailed                              // terminal failure
	pipelineStripDone                                // every stage reached terminal
)

// stripState classifies the presentation for the narrow strip. repair wins
// over running because the re-entry flag is the signal that a failed pass is
// being revisited (test_runner -> code_writer re-entry -> test_runner).
func (p pipelinePresentation) stripState() pipelineStripState {
	if !p.active || p.total == 0 {
		return pipelineStripInactive
	}
	if p.failed {
		return pipelineStripFailed
	}
	if p.allDone {
		return pipelineStripDone
	}
	if p.current != nil {
		if p.current.reentered {
			return pipelineStripRepair
		}
		return pipelineStripRunning
	}
	return pipelineStripNoRunning
}

func (s pipelinePanelState) stripState() pipelineStripState {
	return s.presentation().stripState()
}

// workspaceBadge renders the isolation badge text for a stage row (DoD 26).
// Unset workspace projects as shared cwd (the honest default: most runs
// share the authoritative directory); "isolated" badges the worktree lane.
func workspaceBadge(workspace string) string {
	switch workspace {
	case "isolated":
		return "isolated"
	default:
		// "shared_cwd" and unset both project the shared badge.
		return "shared cwd"
	}
}

// compactSectionTabs renders the compact-mode section shortcut row: the
// sidebar's permanent sections collapse into keyboard-reachable tabs
// (spec §4.2). Keys follow the existing bindings: Ctrl+B toggles the
// sidebar (restoring it on a wide-enough terminal), Ctrl+P toggles the
// plan panel, ? opens shortcuts. The row fits the width whole (segments
// are short); if even the row cannot fit it returns empty rather than
// truncating into noise.
func compactSectionTabs(width int) string {
	tabs := zeroTheme.muted.Render("[B]") + zeroTheme.faint.Render(" sidebar ") +
		zeroTheme.muted.Render("[P]") + zeroTheme.faint.Render(" plan ") +
		zeroTheme.muted.Render("[?]") + zeroTheme.faint.Render(" shortcuts")
	if lipgloss.Width(tabs) > width {
		return ""
	}
	return tabs
}

// pipelineStageLabel abbreviates a stage name for the narrow strip: the first
// rune of each underscore part (code_writer -> cw, static_analyzer -> sa,
// test_runner -> tr). A single-part name keeps its first two runes. Missing
// or empty names fall back to "?".
func pipelineStageLabel(name string) string {
	if strings.TrimSpace(name) == "" {
		return "?"
	}
	parts := strings.Split(name, "_")
	if len(parts) > 1 {
		var b strings.Builder
		for _, part := range parts {
			if part == "" {
				continue
			}
			r := []rune(part)[0]
			b.WriteString(strings.ToLower(string(r)))
		}
		if b.Len() == 0 {
			return "?"
		}
		return b.String()
	}
	r := []rune(name)
	if len(r) <= 2 {
		return name
	}
	return string(r[:2])
}

// arcFrames is the running-stage spinner (cli-spinners "arc" cycle), rendered
// in the accent color. The running glyph cycles through these six arcs, one
// frame per shared spinnerPhase tick (~80ms); done stages keep ✓, pending ○.
var arcFrames = []string{"◜", "◠", "◝", "◞", "◡", "◟"}

func pipelineStageGlyphAndStyle(status pipelineStageStatus, phase int) (string, interface{ Render(...string) string }) {
	switch status {
	case pipelineStageCompleted:
		return zeroTheme.green.Render("✓"), zeroTheme.muted
	case pipelineStageRunning:
		return zeroTheme.amber.Render(arcFrames[phase%len(arcFrames)]), zeroTheme.ink
	case pipelineStageFailed:
		return zeroTheme.red.Render("✗"), zeroTheme.red
	case pipelineStageSkipped:
		return zeroTheme.amber.Render("↩"), zeroTheme.muted
	case pipelineStageIncomplete:
		return zeroTheme.amber.Render("◐"), zeroTheme.muted
	default:
		return zeroTheme.faint.Render("○"), zeroTheme.faint
	}
}

// renderPipelineProgressBar renders the aggregate progress bar. The bar body
// comes from presentation.ProgressBar (bracketed ASCII, width-exact by
// construction — DoD 24); this wrapper adds the theme styling and the
// right-aligned percent, which keeps one stable display width from 0% to
// 100%.
func renderPipelineProgressBar(progress, width int) string {
	barWidth := width - 8
	if barWidth > 16 {
		barWidth = 16
	}
	if barWidth < 4 {
		barWidth = 4
	}
	bar := presentation.ProgressBar(float64(progress)/100, barWidth)
	body := zeroTheme.amber.Render(strings.ReplaceAll(bar, "-", "─"))
	// Right-aligned percent: 0% -> 100% keeps one stable display width.
	return body + " " + zeroTheme.faint.Render(fmt.Sprintf("%3d%%", progress))
}
