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
	status   pipelineStageStatus
	detail   string
	progress int // 0-100
	// reentered is true when a terminal stage (completed/failed/skipped/
	// incomplete) was re-entered as running. This marks the repair loop
	// (test_runner -> code_writer re-entry -> test_runner) without inventing a
	// "repair" stage: it is the same stage being revisited.
	reentered bool
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
	for _, node := range state.Nodes {
		row := pipelineStageRow{
			name:     node.ID,
			status:   pipelineStageStatusFromNode(node.Status),
			progress: int(node.Progress * 100),
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
}

func (s pipelinePanelState) isEmpty() bool {
	return !s.active || len(s.stages) == 0
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

	lines := make([]string, 0, 3)
	switch widthTier(width) {
	case tierFull, tierMedium:
		// Wide: header with count, then a stage-label run, then a compact bar.
		lines = append(lines, headColor.Render(truncateDisplayWidth(header, width)))
		lines = append(lines, " "+p.stripLabels(width-1, phase))
		bar := renderPipelineProgressBar(p.progress, width)
		if bar != "" {
			lines = append(lines, " "+bar)
		}
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
		lines = append(lines, " "+glyph+" "+bodyStyle.Render(truncateStep(stage.name, room)))
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

func renderPipelineProgressBar(progress, width int) string {
	barWidth := width - 8
	if barWidth > 16 {
		barWidth = 16
	}
	if barWidth < 4 {
		barWidth = 4
	}
	filled := (progress * barWidth) / 100
	if filled > barWidth {
		filled = barWidth
	}
	bar := zeroTheme.amber.Render(strings.Repeat("█", filled)) + zeroTheme.faint.Render(strings.Repeat("░", barWidth-filled))
	// Right-aligned percent: 0% -> 100% keeps one stable display width.
	return bar + " " + zeroTheme.faint.Render(fmt.Sprintf("%3d%%", progress))
}
