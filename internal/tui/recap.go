package tui

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

const (
	// recapTimeout bounds a single recap generation so a hung provider can never
	// wedge the background command.
	recapTimeout = 30 * time.Second
	// recapMaxAnswerChars bounds how much of the final answer feeds the recap
	// prompt. The gist is enough, and it keeps the call cheap.
	recapMaxAnswerChars = 1600
	// recapMinAnswerChars avoids recaps that merely restate short answers; one
	// recap sentence is 80 to 120 characters, so shorter answers need no compression.
	recapMinAnswerChars = 600
)

const recapSystemPrompt = "You write ONE short, plain-English sentence summarizing what the assistant just did this turn, for a transcript footnote. " +
	"Reply with ONLY that sentence: no preamble, no markdown, no bullet list, no trailing question."

// recapGeneratedMsg carries the outcome of a background recap generation back to
// the Update loop.
type recapGeneratedMsg struct {
	runID int
	recap string
	err   error
}

// cleanGeneratedRecap normalizes a raw model response into a single recap line:
// the first non-empty line, surrounding markup stripped.
func cleanGeneratedRecap(raw string) string {
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if trimmed := strings.Trim(line, " \t\r\n\"'`*#"); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// generateRecap asks the provider for a one-sentence recap of the turn's final
// answer. Provider-shaped exactly like generateSessionTitle: system + a single
// user turn, no tools.
func generateRecap(ctx context.Context, provider zeroruntime.Provider, answer string) (string, error) {
	if provider == nil {
		return "", errors.New("no provider configured")
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", errors.New("no answer to recap")
	}
	request := zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{
			{Role: zeroruntime.MessageRoleSystem, Content: recapSystemPrompt},
			{Role: zeroruntime.MessageRoleUser, Content: "Assistant's final answer:\n\n" + cutRunes(answer, recapMaxAnswerChars) + "\n\nOne-sentence recap:"},
		},
	}
	stream, err := provider.StreamCompletion(ctx, request)
	if err != nil {
		return "", err
	}
	collected := zeroruntime.CollectStreamWithOptions(ctx, stream, zeroruntime.CollectOptions{})
	if collected.Error != "" {
		return "", errors.New(collected.Error)
	}
	recap := cleanGeneratedRecap(collected.Text)
	if recap == "" {
		return "", errors.New("model returned no usable recap")
	}
	return recap, nil
}

// generateRecapCmd builds the background command that generates a recap for the
// turn, off the Update goroutine (mirrors generateSessionTitleCmd).
func (m model) generateRecapCmd(runID int, answer string) tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), recapTimeout)
		defer cancel()
		recap, err := generateRecap(ctx, provider, answer)
		return recapGeneratedMsg{runID: runID, recap: recap, err: err}
	}
}

// maybeRecapTurn fires a one-shot recap generation after a successful turn when
// recaps are enabled, a provider exists, the turn did real work, and the final
// answer is long enough to benefit from compression. It is a no-op otherwise,
// and the per-run gate prevents a double-fire for the same turn.
func (m model) maybeRecapTurn(runID int, rows []transcriptRow) (model, tea.Cmd) {
	var finalAnswer string
	toolCalls := 0
	for _, row := range rows {
		if row.kind == rowToolCall {
			toolCalls++
		}
		if row.kind == rowAssistant && row.final {
			finalAnswer = row.text
		}
	}
	if !m.recapsEnabled || m.provider == nil || toolCalls == 0 ||
		utf8.RuneCountInString(strings.TrimSpace(finalAnswer)) < recapMinAnswerChars {
		return m, nil
	}
	if m.recappedRuns[runID] {
		return m, nil
	}
	if m.recappedRuns == nil {
		m.recappedRuns = map[int]bool{}
	}
	m.recappedRuns[runID] = true
	return m, m.generateRecapCmd(runID, finalAnswer)
}

// handleRecapGenerated appends the finished recap as a footnote row. A failed or
// empty generation is silent (and releases the per-run gate so a future turn can
// recap normally).
func (m model) handleRecapGenerated(msg recapGeneratedMsg) (model, tea.Cmd) {
	// Drop a recap whose run is no longer the latest: a newer turn has started, so
	// appending now would land the recap on the wrong (current) conversation.
	if msg.runID != m.runID {
		delete(m.recappedRuns, msg.runID)
		return m, nil
	}
	recap := strings.TrimSpace(msg.recap)
	if msg.err != nil || recap == "" {
		delete(m.recappedRuns, msg.runID)
		return m, nil
	}
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowRecap, text: recap})
	return m, nil
}

// handleConfigCommand applies a "/config <setting> <value>" toggle and returns a
// status string. Currently only "recaps on|off". A failed persist surfaces an
// error instead of falsely reporting success.
func (m model) handleConfigCommand(arg string) (model, string) {
	switch arg {
	case "recaps on", "recaps off":
		m.recapsEnabled = arg == "recaps on"
		if err := m.persistRecapsEnabled(); err != nil {
			return m, "Config\nFailed to save recaps preference: " + err.Error()
		}
		return m, "Config\nrecaps: " + onOff(m.recapsEnabled)
	default:
		return m, "Config\nUnknown setting: " + arg + " (use: recaps on|off)"
	}
}

// persistRecapsEnabled writes the recap preference to the user config, mirroring
// persistFavoriteModels. A no-op when there is no user config path.
func (m model) persistRecapsEnabled() error {
	if strings.TrimSpace(m.userConfigPath) == "" {
		return nil
	}
	_, err := config.SetRecapsEnabled(m.userConfigPath, m.recapsEnabled)
	return err
}
