package tui

// evidence_band.go (P3 GAP-K slice 2, owner Tension-3 decision): the
// execution-only evidence rail as an IN-FLOW band. Per the E2E frames: the
// transcript already is the record, so evidence renders in the transcript
// flow during execution only and folds back under 120 columns ("here and
// nowhere else"). The band composes the SAME context-module registry the
// sidebar uses — one source of section truth, two hosts.

import (
	"strings"
)

// evidenceBandMinWidth is the fold-back boundary: below 120 columns the band
// does not render (the compact pipeline strip already covers run state at
// 80-119, and the rail would starve the chat).
const evidenceBandMinWidth = compactModeMinWidth

// evidenceBandMaxLines caps the band so a tall run state can never push the
// transcript off screen. The registry's own budget math applies beneath this.
const evidenceBandMaxLines = 10

// evidenceBandVisible reports whether the in-flow evidence band renders this
// frame: a run is streaming (the rail is execution-only), the terminal is
// wide enough, the layout is single-column (the sidebar already shows the
// same sections when two-column is active), no drill-in view has swapped the
// body, and no full-screen overlay owns the frame.
func (m model) evidenceBandVisible() bool {
	if !m.pending || m.width < evidenceBandMinWidth {
		return false
	}
	if m.sidebarActive() || m.subchat.active || m.fileView.active || m.diffView.active || m.detailView.active {
		return false
	}
	if m.setup.visible || m.helpOverlay || m.providerWizard != nil || m.stageModelWizard != nil ||
		m.mcpAddWizard != nil || m.mcpManager != nil || m.picker != nil {
		return false
	}
	return m.hasEvidenceContent()
}

// hasEvidenceContent reports whether any conditional module has something to
// show — the band renders only when there is evidence, never as furniture.
func (m model) hasEvidenceContent() bool {
	for _, module := range contextRegistry() {
		switch module.slot {
		case ContextSlotAgents, ContextSlotPlan, ContextSlotFiles:
			continue // always-present sections carry no evidence signal
		case ContextSlotDecisions, ContextSlotRun:
			// Sidebar/status surfaces, not band evidence: the band renders
			// only Pipeline/Trajectory/Agents/Memory (renderEvidenceBand), so
			// these must never make an otherwise-empty band visible.
			continue
		}
		if module.has(m) {
			return true
		}
	}
	return false
}

// renderEvidenceBand builds the in-flow band lines at full transcript width:
// a rule, the composed evidence modules (execution-relevant slots only,
// drop-whole budgeted), a closing rule. Empty when nothing qualifies.
//
// Budget order is EVIDENCE FIRST: pipeline, trajectory, and memory render
// before the agents section. Agents is always-present (its empty state is a
// placeholder, not evidence), so under the line cap it drops first — a
// registry-order walk would let 2 placeholder lines starve the 10-line
// PIPELINE roster and silently hide run state.
func (m model) renderEvidenceBand(width int) []string {
	if width <= 0 {
		return nil
	}
	var lines []string
	used := 0
	for _, module := range evidenceBandOrder() {
		body := module.render(m, width-2)
		for i, line := range body {
			body[i] = " " + line
		}
		remaining := evidenceBandMaxLines - used
		if remaining <= 0 {
			break
		}
		if len(body) > remaining {
			// A run roster taller than the remaining budget CLIPS (bounded
			// band): the header and the leading stages stay, the roster tail
			// drops. Drop-whole stays reserved for whole sections that never
			// fit at all; a clipping mid-roster keeps run state visible
			// instead of erasing the band wholesale.
			body = body[:remaining]
		}
		lines = append(lines, body...)
		used += len(body)
	}
	if len(lines) == 0 {
		return nil
	}
	rule := zeroTheme.line.Render(strings.Repeat("─", maxInt(1, width-2)))
	out := make([]string, 0, len(lines)+2)
	out = append(out, rule)
	out = append(out, lines...)
	out = append(out, rule)
	for i, line := range out {
		out[i] = padStyledLine(line, width)
	}
	return out
}

// evidenceBandOrder returns the band's budget walk: the real evidence modules
// (pipeline, trajectory, memory) ahead of the always-present agents section,
// so cap pressure drops the placeholder first, never run state.
func evidenceBandOrder() []contextModule {
	var evidence, trailing []contextModule
	for _, module := range contextRegistry() {
		switch module.slot {
		case ContextSlotPipeline, ContextSlotTrajectory, ContextSlotMemory:
			evidence = append(evidence, module)
		case ContextSlotAgents:
			trailing = append(trailing, module)
		}
	}
	return append(evidence, trailing...)
}

// evidenceBandBlock renders the band as a single string ("" when the band is
// not visible this frame).
func (m model) evidenceBandBlock(width int) string {
	if !m.evidenceBandVisible() {
		return ""
	}
	lines := m.renderEvidenceBand(width)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// evidenceBandHeight returns the rendered band's line count (0 when absent),
// for callers that need the geometry.
func (m model) evidenceBandHeight(width int) int {
	if !m.evidenceBandVisible() {
		return 0
	}
	return len(m.renderEvidenceBand(width))
}
