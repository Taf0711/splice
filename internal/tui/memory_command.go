package tui

import (
	"context"
	"errors"
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

type memoryCommandClient interface {
	Search(context.Context, schemas.MemoryQuery) (schemas.MemoryBundle, error)
	Recent(context.Context, schemas.MemoryQuery) (schemas.MemoryBundle, error)
	Stats(context.Context) (memd.MemoryStats, error)
}

var tuiResolveMemoryCommand = func(ctx context.Context) (memoryCommandClient, error) {
	client, err := tuiResolveMemory(ctx)
	if client == nil {
		return nil, err
	}
	return client, err
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
func resolveMemdOrError(ctx context.Context) (memoryCommandClient, memoryResultMsg) {
	client, err := tuiResolveMemoryCommand(ctx)
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
		return memoryResultMsg{text: renderMemoryStatsCard(stats)}
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
		projectPath := m.cwd
		bundle, err := client.Search(runCtx, schemas.MemoryQuery{
			RequestingAgent: "tui",
			Query:           query,
			ProjectPath:     &projectPath,
			Scopes:          []string{"project", "global"},
			Limit:           10,
		})
		if err != nil {
			return memoryResultMsg{text: "Memory search error: " + err.Error(), isError: true}
		}
		return memoryResultMsg{text: renderMemoryListCard("Search", memoryListSummary(
			len(bundle.Observations), query, nil), bundle.Observations)}
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
		projectPath := m.cwd
		bundle, err := client.Recent(runCtx, schemas.MemoryQuery{
			RequestingAgent: "tui",
			ProjectPath:     &projectPath,
			Scopes:          []string{"project", "global"},
			Limit:           10,
		})
		if err != nil {
			if errors.Is(err, memd.ErrRecentUnsupported) {
				return memoryResultMsg{text: "Memory daemon is out of date. Update splice-memd to use /memory recent.", isError: true}
			}
			return memoryResultMsg{text: "Memory search error: " + err.Error(), isError: true}
		}
		return memoryResultMsg{text: renderMemoryListCard("Recent", memoryListSummary(
			len(bundle.Observations), "", nil), bundle.Observations)}
	}
}

// memoryObservationAge renders the "settled 4d ago" tail of a detail line. It
// returns "" when the observation carries no timestamp so the separator never
// dangles.
func memoryObservationAge(obs schemas.MemoryObservation) string {
	if obs.UpdatedAt <= 0 {
		return ""
	}
	now := time.Now()
	stamp := time.Unix(obs.UpdatedAt, 0)
	if stamp.After(now) {
		stamp = now
	}
	days := int(now.Sub(stamp).Hours() / 24)
	switch {
	case days <= 0:
		hours := int(now.Sub(stamp).Hours())
		if hours <= 0 {
			return "just now"
		}
		return fmt.Sprintf("%dh ago", hours)
	case days < 30:
		return fmt.Sprintf("%dd ago", days)
	case days < 365:
		return fmt.Sprintf("%dmo ago", days/30)
	default:
		return fmt.Sprintf("%dy ago", days/365)
	}
}

// memoryObservationDetail builds the continuation line under an observation
// title: the content (folded to one line and bounded), plus the age suffix
// when the observation carries a timestamp.
func memoryObservationDetail(obs schemas.MemoryObservation) string {
	detail := strings.Join(strings.Fields(obs.Content), " ")
	detail = truncateRunes(detail, 120)
	if age := memoryObservationAge(obs); age != "" {
		if detail == "" {
			detail = age
		} else {
			detail = detail + " — " + age
		}
	}
	return detail
}

// memoryListSummary assembles the header counts for a /memory result card.
// The query (when present) and the memory-types breakdown ride the summary
// line the way the P14 frame's "3 hits · 412 observations · 8.2 MB" does.
func memoryListSummary(count int, query string, byType map[string]int) string {
	parts := []string{fmt.Sprintf("%d hits", count)}
	if query != "" {
		parts = append(parts, fmt.Sprintf("query %q", query))
	}
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	rendered := make([]string, 0, len(types))
	for _, t := range types {
		rendered = append(rendered, fmt.Sprintf("%s %d", t, byType[t]))
	}
	if len(rendered) > 0 {
		parts = append(parts, strings.Join(rendered, "/"))
	}
	return strings.Join(parts, " · ")
}

// renderMemoryListCard renders /memory search and /memory recent results as
// the P14 tagged two-column card: one row per observation ("[type] title"),
// the detail on an indented continuation line, and the /memory + /search
// pointer footer. The empty state is honest — the card renders with the
// zero-hit summary, not a fake list. The /search pointer footer carries the
// frame's store note: search reads this repo's event log, memory reads prior
// sessions, so the two stay separate commands with one result grammar.
func renderMemoryListCard(title, summary string, observations []schemas.MemoryObservation) string {
	section := commandCardSection{}
	if len(observations) == 0 {
		section.Lines = []string{"no observations match"}
	} else {
		lines := make([]string, 0, 2*len(observations))
		for _, obs := range observations {
			memoryType := obs.MemoryType
			if memoryType == "" {
				memoryType = "note"
			}
			heading := obs.Title
			if heading == "" {
				heading = "(untitled)"
			}
			// One observation = one tagged heading line plus its indented
			// detail continuation line. Two Lines entries (not a multi-line
			// Row) because the card section compactor folds row whitespace.
			lines = append(lines,
				fmt.Sprintf("[%s] %s", memoryType, heading),
				"    "+memoryObservationDetail(obs))
		}
		section.Lines = lines
	}
	return renderCommandCardTranscript(commandCard{
		Title:    title,
		Summary:  []string{summary},
		Sections: []commandCardSection{section},
		Actions:  []string{"/memory", "/memory recent", "/search <query>"},
	})
}

// renderMemoryStatsCard renders /memory as the P14 stats card: the counts
// (total observations, DB size) ride the header summary per the frame's
// "3 hits · 412 observations · 8.2 MB" pattern, and the per-type counts use
// the same tagged two-column rows as the search card.
func renderMemoryStatsCard(stats memd.MemoryStats) string {
	summary := fmt.Sprintf("%d observations · %s", stats.Total, humanizeBytes(stats.DBSizeBytes))
	section := commandCardSection{}
	if len(stats.ByType) == 0 {
		section.Lines = []string{"no observations stored yet"}
	} else {
		types := make([]string, 0, len(stats.ByType))
		for t := range stats.ByType {
			types = append(types, t)
		}
		sort.Strings(types)
		lines := make([]string, 0, len(types))
		for _, t := range types {
			lines = append(lines, fmt.Sprintf("[%s] %d", t, stats.ByType[t]))
		}
		section.Lines = lines
	}
	return renderCommandCardTranscript(commandCard{
		Title:    "Memory",
		Summary:  []string{summary},
		Sections: []commandCardSection{section},
		Actions:  []string{"/memory", "/memory recent", "/search <query>"},
	})
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
