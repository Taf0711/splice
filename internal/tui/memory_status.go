package tui

import (
	"fmt"
	"maps"
	"slices"
)

// memoryStatusSegment returns the status-line text for the memory sidecar,
// or "" when memory status is unknown (not yet resolved). The caller handles
// width-tier gating and styling.
func (m model) memoryStatusSegment() string {
	switch m.memoryStatus {
	case "active":
		return fmt.Sprintf("🧵 %d", m.memoryCount)
	case "off":
		return "🧵 off"
	default:
		return ""
	}
}

// memorySidebarLines renders the compact memory section for the sidebar:
// observation count and top 3 types by count. One to four lines.
func (m model) memorySidebarLines(width int) []string {
	if m.memoryStatus != "active" {
		return nil
	}
	lines := []string{zeroTheme.muted.Render(fmt.Sprintf("  %d observations", m.memoryCount))}
	if len(m.memoryByType) == 0 {
		return lines
	}
	types := slices.Collect(maps.Keys(m.memoryByType))
	slices.SortFunc(types, func(a, b string) int {
		return m.memoryByType[b] - m.memoryByType[a] // descending by count
	})
	for _, t := range types[:min(3, len(types))] {
		lines = append(lines, zeroTheme.muted.Render(fmt.Sprintf("  %s: %d", t, m.memoryByType[t])))
	}
	return lines
}
