package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// memoryResultMsg carries the result of an asynchronous /memory command back
// to the TUI update loop.
type memoryResultMsg struct {
	text    string
	isError bool
}

// handleMemoryCommand handles /memory, /memory search <query>, /memory recent.
func (m model) handleMemoryCommand(text string) (model, tea.Cmd) {
	text = strings.TrimSpace(text)

	if text == "" {
		return m.handleMemoryStats()
	}
	if strings.HasPrefix(text, "search ") {
		query := strings.TrimSpace(strings.TrimPrefix(text, "search "))
		if query == "" {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Usage: /memory search <query>"})
			return m, nil
		}
		return m.handleMemorySearch(query)
	}
	if text == "recent" {
		return m.handleMemoryRecent()
	}
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Usage: /memory [search <query> | recent]"})
	return m, nil
}

// resolveMemdOrError resolves the sidecar client, returning an error
// memoryResultMsg when resolution fails or the binary is absent. Shared by
// all three /memory subcommands.
func resolveMemdOrError(ctx context.Context) (*memd.Client, memoryResultMsg) {
	client, err := memd.Resolve(ctx)
	if err != nil {
		return nil, memoryResultMsg{text: "Memory sidecar error: " + err.Error(), isError: true}
	}
	if client == nil {
		return nil, memoryResultMsg{text: "Memory sidecar not running. Run 'make install-memd' or set SPLICE_MEMD_BIN.", isError: true}
	}
	return client, memoryResultMsg{}
}

func (m model) handleMemoryStats() (model, tea.Cmd) {
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "/memory"})
	runCtx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	return m, func() tea.Msg {
		defer cancel()
		client, errMsg := resolveMemdOrError(runCtx)
		if client == nil {
			return errMsg
		}
		stats, err := client.Stats(runCtx)
		if err != nil {
			return memoryResultMsg{text: "Memory stats error: " + err.Error(), isError: true}
		}
		return memoryResultMsg{text: formatMemoryStats(stats)}
	}
}

func (m model) handleMemorySearch(query string) (model, tea.Cmd) {
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "/memory search " + query})
	runCtx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	return m, func() tea.Msg {
		defer cancel()
		client, errMsg := resolveMemdOrError(runCtx)
		if client == nil {
			return errMsg
		}
		bundle, err := client.Search(runCtx, schemas.MemoryQuery{
			RequestingAgent: "tui",
			Query:           query,
			Limit:           10,
		})
		if err != nil {
			return memoryResultMsg{text: "Memory search error: " + err.Error(), isError: true}
		}
		return memoryResultMsg{text: formatMemoryList(
			fmt.Sprintf("🧵 Memory Search: %q — %d result(s)", query, len(bundle.Observations)),
			"No memories found.",
			bundle.Observations,
		)}
	}
}

func (m model) handleMemoryRecent() (model, tea.Cmd) {
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "/memory recent"})
	runCtx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	return m, func() tea.Msg {
		defer cancel()
		client, errMsg := resolveMemdOrError(runCtx)
		if client == nil {
			return errMsg
		}
		bundle, err := client.Search(runCtx, schemas.MemoryQuery{
			RequestingAgent: "tui",
			Query:           "*",
			Limit:           10,
		})
		if err != nil {
			return memoryResultMsg{text: "Memory search error: " + err.Error(), isError: true}
		}
		return memoryResultMsg{text: formatMemoryList(
			fmt.Sprintf("🧵 Recent Memories — %d", len(bundle.Observations)),
			"No memories yet.",
			bundle.Observations,
		)}
	}
}

func formatMemoryStats(stats memd.MemoryStats) string {
	var b strings.Builder
	b.WriteString("🧵 Memory Sidecar Stats\n\n")
	b.WriteString(fmt.Sprintf("  Total observations: %d\n", stats.Total))
	if len(stats.ByType) > 0 {
		b.WriteString("  By type:\n")
		types := make([]string, 0, len(stats.ByType))
		for t := range stats.ByType {
			types = append(types, t)
		}
		sort.Strings(types)
		for _, t := range types {
			b.WriteString(fmt.Sprintf("    %s: %d\n", t, stats.ByType[t]))
		}
	}
	if stats.DBSizeBytes > 0 {
		b.WriteString(fmt.Sprintf("  DB size: %s\n", humanizeBytes(stats.DBSizeBytes)))
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatMemoryList renders a header line plus a numbered observation list.
// Shared by search and recent: only the header and empty-message differ.
func formatMemoryList(header, emptyText string, observations []schemas.MemoryObservation) string {
	var b strings.Builder
	b.WriteString(header + "\n\n")
	if len(observations) == 0 {
		b.WriteString("  " + emptyText)
		return b.String()
	}
	for i, obs := range observations {
		title := obs.Title
		if title == "" {
			title = "(untitled)"
		}
		content := obs.Content
		if len(content) > 100 {
			content = content[:97] + "..."
		}
		b.WriteString(fmt.Sprintf("  %d. [%s] %s\n     %s\n", i+1, obs.MemoryType, title, content))
	}
	return strings.TrimRight(b.String(), "\n")
}

func humanizeBytes(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
