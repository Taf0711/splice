package tui

// context_module.go (P3 GAP-K slice 1): the context-module registry. The
// sidebar's sections (AGENTS / PLAN / PIPELINE / TRAJECTORY / MEMORY / FILES
// / ACTIVITY / tokens) become registered modules with semantic slots, so the
// sidebar composition is declarative data, not a hardcoded render walk —
// DoD 30/31. Modules never see orchestrator internals: each renders from the
// same normalized model state the sidebar already used (DoD 32).

// ContextSlot orders the sections in the sidebar band. The order is the
// composition contract; modules register against a slot and the sidebar
// renders slots in sequence.
type ContextSlot int

const (
	ContextSlotAgents ContextSlot = iota
	ContextSlotPlan
	ContextSlotDecisions
	ContextSlotRun
	ContextSlotPipeline
	ContextSlotTrajectory
	ContextSlotMemory
	ContextSlotFiles
	ContextSlotActivity
	contextSlotCount // sentinel
)

// contextModule is one registered sidebar section. Render returns the
// section's lines at the given width (header included); Has reports whether
// the module has anything to show (an absent module renders nothing, not a
// placeholder — the sidebar only renders placeholders where a module is
// always present, e.g. AGENTS/PLAN/FILES).
type contextModule struct {
	name   string
	slot   ContextSlot
	has    func(m model) bool
	render func(m model, width int) []string
}

// contextRegistry returns the builtin module set in slot order. A slice, not
// a map: composition order IS the registry's meaning, and Go ranges maps in
// random order.
func contextRegistry() []contextModule {
	return []contextModule{
		{
			name: "agents",
			slot: ContextSlotAgents,
			has:  func(m model) bool { return true }, // always present (placeholder when empty)
			render: func(m model, width int) []string {
				lines := []string{m.sidebarAgentHeader(width)}
				if agentLines := m.sidebarAgentLines(width); len(agentLines) > 0 {
					return append(lines, agentLines...)
				}
				return append(lines, sidebarPlaceholder("no agents spawned", width))
			},
		},
		{
			name: "plan",
			slot: ContextSlotPlan,
			has:  func(m model) bool { return true },
			render: func(m model, width int) []string {
				lines := []string{m.sidebarPlanHeader(width)}
				if planLines := m.sidebarPlanLines(width); len(planLines) > 0 {
					return append(lines, planLines...)
				}
				return append(lines, sidebarPlaceholder("no active plan", width))
			},
		},
		{
			// P1.4 delta (frame gEVp1, S1): the pinned-decisions ledger wakes
			// the sidebar during design mode — the same append-only data the
			// transcript ledger card projects, never invented states. Absent
			// while zero pins exist (no placeholder; an empty ledger is noise).
			name: "decisions",
			slot: ContextSlotDecisions,
			has: func(m model) bool {
				decisions, ok := m.designDecisions()
				return ok && len(decisions) > 0
			},
			render: func(m model, width int) []string {
				return m.sidebarDecisionsLines(width)
			},
		},
		{
			// P1.4 delta (frame gEVp1, S1): RUN readout (elapsed/tokens/stage)
			// while a run is live or design mode is active. Event-driven:
			// absent on an idle non-design session.
			name:   "run",
			slot:   ContextSlotRun,
			has:    func(m model) bool { return m.pending || m.designMode },
			render: func(m model, width int) []string { return m.sidebarRunLines(width) },
		},
		{
			name: "pipeline",
			slot: ContextSlotPipeline,
			has: func(m model) bool {
				pipeline := m.pipeline.presentation()
				return pipeline.active && pipeline.total > 0
			},
			render: func(m model, width int) []string {
				pipeline := m.pipeline.presentation()
				budget := width - len("PIPELINE ") - len("99/99")
				chip := m.worktreeChip()
				if lifecycle := m.pipeline.lifecycleChip(budget); lifecycle != "" {
					if chip != "" {
						chip += " · "
					}
					chip += lifecycle
				}
				lines := []string{pipeline.headerLineWithChip(width, chip)}
				return append(lines, pipeline.renderSection(width, m.spinnerPhase)...)
			},
		},
		{
			name: "trajectory",
			slot: ContextSlotTrajectory,
			has:  func(m model) bool { return !m.pipeline.isEmpty() },
			render: func(m model, width int) []string {
				return []string{m.renderTrajectorySurface(m.lastState, width)}
			},
		},
		{
			name: "memory",
			slot: ContextSlotMemory,
			has:  func(m model) bool { return m.memoryStatus == "active" },
			render: func(m model, width int) []string {
				lines := []string{sidebarHeader("🧵 Memory", width)}
				return append(lines, m.memorySidebarLines(width)...)
			},
		},
		{
			name: "files",
			slot: ContextSlotFiles,
			has:  func(m model) bool { return true },
			render: func(m model, width int) []string {
				lines := []string{m.sidebarFilesHeader(width)}
				fileLines, _ := m.sidebarFileLines(width)
				if len(fileLines) > 0 {
					return append(lines, fileLines...)
				}
				return append(lines, sidebarPlaceholder("no files touched", width))
			},
		},
		{
			name: "activity",
			slot: ContextSlotActivity,
			has: func(m model) bool {
				return len(m.sidebarActivityLines(80, maxSidebarActivityLines)) > 0
			},
			render: func(m model, width int) []string {
				activity := m.sidebarActivityLines(width, maxSidebarActivityLines)
				if len(activity) == 0 {
					return nil
				}
				lines := []string{sidebarHeader("ACTIVITY", width)}
				return append(lines, activity...)
			},
		},
	}
}

// renderContextModules composes the registered modules in slot order with a
// blank line between sections — the declarative replacement for the sidebar's
// hardcoded render walk. Bounded by budget: a module that no longer fits is
// skipped whole (drop-whole, never a truncated section).
func (m model) renderContextModules(width, budget int) []string {
	if width <= 0 || budget <= 0 {
		return nil
	}
	var lines []string
	used := 0
	for _, module := range contextRegistry() {
		if !module.has(m) {
			continue
		}
		if len(lines) > 0 {
			if used+1 > budget {
				break
			}
			lines = append(lines, "")
			used++
		}
		body := module.render(m, width)
		if used+len(body) > budget {
			break
		}
		lines = append(lines, body...)
		used += len(body)
	}
	return lines
}
