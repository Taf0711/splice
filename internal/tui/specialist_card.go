// specialist_card.go renders specialist/subagent cards in the transcript.
//
// A specialist card summarises one spawned sub-agent (worker, explorer, code
// review, ...): its name, task description, elapsed time, tool-call count, and
// token usage. The SpecialistTracker holds the live state that the transcript
// view consults each render; the session store feeds it via start/complete/
// incrementToolCount/addTokens as specialist events arrive.
package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/streamjson"
)

// specialistStatus is the lifecycle state of a single specialist invocation.
type specialistStatus int

const (
	specialistRunning specialistStatus = iota
	specialistCompleted
	specialistError
)

// specialistToolCap is the number of per-call lines the transcript card shows
// and the tracker retains (newest kept). Shared by the tracker (memory bound)
// and the renderer (visual bound); the full list lives in the subchat drill-in.
const specialistToolCap = 8

// specialistToolCall is one visible tool call a specialist made: the tool
// name plus a short "for what" detail (the resolved argument hint, e.g. a
// file path or a search query).
type specialistToolCall struct {
	name   string
	detail string
}

// specialistInfo is the rendered view of one specialist invocation.
type specialistInfo struct {
	name           string
	description    string
	childSessionID string
	status         specialistStatus
	startedAt      time.Time
	completedAt    time.Time
	exitCode       int
	errorMsg       string
	toolCount      int // number of tool calls made by this specialist
	tokenCount     int // total tokens consumed
	currentTool    string
	currentDetail  string
	toolCalls      []specialistToolCall // every tool call, in order; verbose listing
}

// specialistTracker holds the live state for every specialist the parent agent
// has spawned in the current turn. Lookups are by childSessionID.
type specialistTracker struct {
	specialists []specialistInfo
}

// start adds a new specialist entry. If childSessionID already exists the
// existing entry is updated in place (so a duplicate start event is idempotent).
func (t *specialistTracker) start(name, description, childSessionID string, now time.Time) {
	for index := range t.specialists {
		if t.specialists[index].childSessionID == childSessionID {
			t.specialists[index].name = name
			t.specialists[index].description = description
			t.specialists[index].status = specialistRunning
			t.specialists[index].startedAt = now
			t.specialists[index].completedAt = time.Time{}
			t.specialists[index].exitCode = 0
			t.specialists[index].errorMsg = ""
			return
		}
	}
	t.specialists = append(t.specialists, specialistInfo{
		name:           name,
		description:    description,
		childSessionID: childSessionID,
		status:         specialistRunning,
		startedAt:      now,
	})
}

// complete marks the specialist with childSessionID as finished, recording the
// terminal status, exit code, and any error message. Specialists that are not
// tracked are ignored.
func (t *specialistTracker) complete(childSessionID string, status specialistStatus, exitCode int, errorMsg string, now time.Time) {
	for index := range t.specialists {
		if t.specialists[index].childSessionID == childSessionID {
			t.specialists[index].status = status
			t.specialists[index].exitCode = exitCode
			t.specialists[index].errorMsg = errorMsg
			t.specialists[index].completedAt = now
			return
		}
	}
}

// incrementToolCount bumps the tool-call counter for the specialist with
// childSessionID. Unknown specialists are ignored.
func (t *specialistTracker) incrementToolCount(childSessionID string) {
	for index := range t.specialists {
		if t.specialists[index].childSessionID == childSessionID {
			t.specialists[index].toolCount++
			return
		}
	}
}

// addTokens adds tokens to the running total for the specialist with
// childSessionID. Unknown specialists are ignored.
func (t *specialistTracker) addTokens(childSessionID string, tokens int) {
	for index := range t.specialists {
		if t.specialists[index].childSessionID == childSessionID {
			t.specialists[index].tokenCount += tokens
			return
		}
	}
}

// setCurrentTool updates the live tool-call progress for the specialist with
// childSessionID. Used by specialistProgressMsg to show ↳ toolName detail.
// The detail is sanitized so child-provided terminal controls or newlines
// cannot leak into the sidebar line.
func (t *specialistTracker) setCurrentTool(childSessionID, toolName, detail string) {
	detail = sanitizeToolCallDetail(detail)
	for index := range t.specialists {
		if t.specialists[index].childSessionID == childSessionID {
			t.specialists[index].currentTool = toolName
			t.specialists[index].currentDetail = detail
			return
		}
	}
}

// addToolCall appends one tool call (name + purpose) to the specialist's
// running list. Each progress message is one tool call, so the list grows in
// execution order and the transcript card can show every tool the specialist
// ran, not just the latest. The list keeps only the newest specialistToolCap
// entries so a long child run cannot grow memory without bound; the dropped
// earlier count is derived from toolCount at render time. The detail is
// sanitized before storage so raw child arguments cannot inject terminal
// sequences or unruled lines into the transcript.
func (t *specialistTracker) addToolCall(childSessionID, toolName, detail string) {
	detail = sanitizeToolCallDetail(detail)
	for index := range t.specialists {
		if t.specialists[index].childSessionID == childSessionID {
			toolCalls := append(t.specialists[index].toolCalls, specialistToolCall{name: toolName, detail: detail})
			if len(toolCalls) > specialistToolCap {
				toolCalls = toolCalls[len(toolCalls)-specialistToolCap:]
			}
			t.specialists[index].toolCalls = toolCalls
			return
		}
	}
}

// sanitizeToolCallDetail strips terminal control sequences and newlines from
// a child-provided tool detail and bounds its length, so one line of the
// transcript or sidebar cannot carry escape injection or grow unbounded.
func sanitizeToolCallDetail(detail string) string {
	cleaned := strings.TrimSpace(sanitizeTerminalOutput(detail, false))
	return truncateRunes(cleaned, 120)
}

// clear resets the tracker to an empty state.
func (t *specialistTracker) clear() {
	t.specialists = nil
}

// reconcileSessionID rewrites the childSessionID of the entry currently keyed
// by oldID to newID. This bridges the tool-call-ID (used as a temporary key at
// specialist start time) to the real session ID (known only when the child
// process reports it on completion). No-op if oldID is not found.
func (t *specialistTracker) reconcileSessionID(oldID, newID string) {
	for index := range t.specialists {
		if t.specialists[index].childSessionID == oldID {
			t.specialists[index].childSessionID = newID
			return
		}
	}
}

// getBySessionID returns the info for childSessionID and whether it was found.
func (t *specialistTracker) getBySessionID(childSessionID string) (specialistInfo, bool) {
	for index := range t.specialists {
		if t.specialists[index].childSessionID == childSessionID {
			return t.specialists[index], true
		}
	}
	return specialistInfo{}, false
}

// all returns a copy of the specialists slice so callers may iterate without
// the underlying array mutating underneath them.
func (t *specialistTracker) all() []specialistInfo {
	if len(t.specialists) == 0 {
		return nil
	}
	out := make([]specialistInfo, len(t.specialists))
	copy(out, t.specialists)
	return out
}

// hasRunning reports whether any tracked specialist is still running.
func (t *specialistTracker) hasRunning() bool {
	for index := range t.specialists {
		if t.specialists[index].status == specialistRunning {
			return true
		}
	}
	return false
}

// specialistStatusString returns the lowercase human label for a status.
func specialistStatusString(s specialistStatus) string {
	switch s {
	case specialistRunning:
		return "running"
	case specialistCompleted:
		return "completed"
	case specialistError:
		return "error"
	default:
		return "error"
	}
}

// parseSpecialistStatus maps the status string carried by specialist events to
// the internal specialistStatus enum. Unknown values default to error so a
// malformed event never reads as a silent success.
func parseSpecialistStatus(s string) specialistStatus {
	switch s {
	case "running":
		return specialistRunning
	case "completed":
		return specialistCompleted
	default:
		return specialistError
	}
}

// parseTaskCallArgs extracts the specialist name and description from a Task
// tool call's JSON arguments. The name comes from the "name" field and the
// description from the "description" field (falling back to "prompt").
func parseTaskCallArgs(rawArgs string) (name, description string) {
	name = firstArgValue(rawArgs, []string{"name"})
	description = firstArgValue(rawArgs, []string{"description", "prompt"})
	return name, description
}

// formatTokenCount renders an integer token count with comma thousands
// separators: 1840 -> "1,840", 5210 -> "5,210".
func formatTokenCount(n int) string {
	if n < 0 {
		n = -n
	}
	digits := strconv.Itoa(n)
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	first := len(digits) % 3
	if first > 0 {
		b.WriteString(digits[:first])
		if len(digits) > first {
			b.WriteByte(',')
		}
	}
	for i := first; i < len(digits); i += 3 {
		b.WriteString(digits[i : i+3])
		if i+3 < len(digits) {
			b.WriteByte(',')
		}
	}
	return b.String()
}

// formatSpecialistElapsed renders a duration as the compact "Ns" / "NmNs" form
// shown on specialist card headers (e.g. 18s, 45s, 1m5s). Durations under a
// second round up to 1s so a freshly started card never shows "0s".
func formatSpecialistElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int(d.Seconds())
	if seconds < 1 {
		return "1s"
	}
	if seconds < 60 {
		return strconv.Itoa(seconds) + "s"
	}
	minutes := seconds / 60
	remainder := seconds % 60
	if remainder == 0 {
		return strconv.Itoa(minutes) + "m"
	}
	return strconv.Itoa(minutes) + "m" + strconv.Itoa(remainder) + "s"
}

// renderSpecialistCard renders one specialist as a left-rule card of the given
// width: a status-tinted │ on the left, no top/right/bottom borders. The card
// has a header (icon + name + description + elapsed) and a body line (status +
// tool calls + tokens). An optional error detail line is shown when the
// specialist errored. The whole card is mouse-clickable to drill into its
// subchat (wired in transcript_selection.go); no text hint is rendered.
// Widths below the minimum are clamped to 30.
func (m model) renderSpecialistCard(info specialistInfo, width int) string {
	if width < 30 {
		width = 30
	}

	// Elapsed: live while running, frozen at completion once the specialist is
	// done.
	var elapsed time.Duration
	if info.status == specialistRunning {
		elapsed = m.now().Sub(info.startedAt)
	} else if !info.completedAt.IsZero() {
		elapsed = info.completedAt.Sub(info.startedAt)
	} else {
		elapsed = m.now().Sub(info.startedAt)
	}
	elapsedStr := formatSpecialistElapsed(elapsed)

	// Description truncation. The header reserves room for the icon, the name,
	// the two " · " separators, the elapsed string, and a safety margin. Clamp
	// to splice so very long names never underflow.
	descMax := width - len(info.name) - 25
	if descMax < 0 {
		descMax = 0
	}
	description := truncateRunes(info.description, descMax)

	// Header line: icon + name + " · " + description + " · " + elapsed.
	var header string
	switch info.status {
	case specialistRunning:
		icon := m.spinnerGlyph()
		header = zeroTheme.accent.Render(fmt.Sprintf("%s%s · %s · %s", icon, info.name, description, elapsedStr))
	case specialistCompleted:
		header = zeroTheme.green.Render(fmt.Sprintf("✓ %s · %s · %s", info.name, description, elapsedStr))
	case specialistError:
		header = zeroTheme.red.Render(fmt.Sprintf("✗ %s · %s · %s", info.name, description, elapsedStr))
	default:
		header = zeroTheme.accent.Render(fmt.Sprintf("• %s · %s · %s", info.name, description, elapsedStr))
	}

	// Body line: "  status · N tool calls · M,NNN tokens".
	toolLabel := "tool calls"
	statusLabel := specialistStatusString(info.status)
	if info.status == specialistError {
		statusLabel = fmt.Sprintf("error (exit code %d)", info.exitCode)
	}
	// The token total is only populated when usage was bridged from the child; omit
	// the segment when it is splice rather than advertise a misleading "0 tokens" (M18).
	bodyText := fmt.Sprintf("  %s · %d %s", statusLabel, info.toolCount, toolLabel)
	if info.tokenCount > 0 {
		bodyText += fmt.Sprintf(" · %s tokens", formatTokenCount(info.tokenCount))
	}
	var body string
	if info.status == specialistError {
		body = zeroTheme.red.Render(bodyText)
	} else {
		body = zeroTheme.muted.Render(bodyText)
	}
	// Surface the otherwise-invisible drill-in affordance: a left-click or Enter on
	// the card opens its subchat (transcript_selection.go). A faint hint makes that
	// discoverable instead of hidden; it truncates first on narrow cards.
	body += zeroTheme.faint.Render("   · enter to open")

	lines := []string{header, body}

	// Verbose per-call listing: every tool the specialist ran, in order, each with
	// its purpose (resolved arg hint). While running the trailing entry is the
	// in-flight call; after completion it is the last one it made. The list means
	// a specialist's web search (or any tool) stays visible in the transcript
	// instead of flashing past and being lost. The tracker retains only the
	// newest specialistToolCap entries (memory bound), so the fold count is
	// derived from toolCount; the full list lives in the subchat drill-in.
	calls := info.toolCalls
	if len(calls) == 0 && info.status == specialistRunning && info.currentTool != "" {
		// A running specialist whose first progress msg has not landed yet still
		// reports currentTool; render it so the card is not empty.
		calls = []specialistToolCall{{name: info.currentTool, detail: info.currentDetail}}
	}
	if hidden := info.toolCount - len(info.toolCalls); hidden > 0 {
		// Calls dropped from the retained tail (bounded retention): fold them
		// into a count line so the card stays compact on long runs.
		lines = append(lines, zeroTheme.faint.Render(fmt.Sprintf("  · · · +%d earlier", hidden)))
	}
	for _, tc := range calls {
		line := "  ↳ " + tc.name
		if strings.TrimSpace(tc.detail) != "" {
			line += " " + zeroTheme.faint.Render(tc.detail)
		}
		lines = append(lines, zeroTheme.muted.Render(line))
	}

	// Optional error detail line.
	if info.status == specialistError && strings.TrimSpace(info.errorMsg) != "" {
		errMax := width - 4
		if errMax < 1 {
			errMax = 1
		}
		errMsg := truncateRunes(strings.TrimSpace(info.errorMsg), errMax)
		lines = append(lines, zeroTheme.red.Render("  "+errMsg))
	}

	// Left-rule card: status-tinted │ on the left, no box borders.
	rule := specialistBorderStyle(info.status)
	return renderLeftRuleCard(width, lines, rule)
}

// specialistBorderStyle picks the card border style for a specialist status:
// the running tint while in flight, the error tint on failure, and the default
// line once completed cleanly.
func specialistBorderStyle(status specialistStatus) lipgloss.Style {
	switch status {
	case specialistRunning:
		return zeroTheme.cardRun
	case specialistError:
		return zeroTheme.cardErr
	default:
		return zeroTheme.line
	}
}

// specialistTitleFor returns the display title (name + " · " + description) for
// the specialist with the given childSessionID, for the subchat nav bar. Returns
// "" when the specialist is not found in the tracker. Falls back to the
// specialist info carried by transcript rows when the tracker has been cleared.
func (m model) specialistTitleFor(childSessionID string) string {
	info, ok := m.specialists.getBySessionID(childSessionID)
	if ok {
		return info.name + " · " + info.description
	}
	for _, row := range m.transcript {
		if row.kind == rowSpecialist && row.specialistInfo != nil && row.specialistInfo.childSessionID == childSessionID {
			return row.specialistInfo.name + " · " + row.specialistInfo.description
		}
	}
	return ""
}

// toolCallSummary extracts a short detail string from a stream-json tool_call
// event's arguments, for the live progress line in specialist cards.
func toolCallSummary(event streamjson.Event) string {
	args, ok := event.Args.(map[string]any)
	if !ok {
		return ""
	}
	switch event.Name {
	case "read_file", "read_minified_file", "list_directory", "write_file", "edit_file":
		if path, ok := args["path"].(string); ok {
			return truncateRunes(path, 50)
		}
	case "grep":
		if pattern, ok := args["pattern"].(string); ok {
			return truncateRunes(pattern, 40)
		}
	case "glob":
		if pattern, ok := args["pattern"].(string); ok {
			return truncateRunes(pattern, 40)
		}
	case "bash", "exec_command":
		if cmd, ok := args["command"].(string); ok {
			return truncateRunes(singleLineToolHeadText(cmd), 40)
		}
		if cmd, ok := args["cmd"].(string); ok {
			return truncateRunes(singleLineToolHeadText(cmd), 40)
		}
	case "write_stdin":
		sessionID := toolCallIntArg(args, "session_id")
		chars, _ := args["chars"].(string)
		switch {
		case chars == "":
			return fmt.Sprintf("poll session %d", sessionID)
		case chars == "\x03":
			return fmt.Sprintf("interrupt session %d", sessionID)
		default:
			return fmt.Sprintf("send input to session %d", sessionID)
		}
	case "update_plan":
		return "plan"
	}
	// Search and fetch tools (MCP-loaded web_search, fetch_content, ...) carry
	// the purpose under a query/queries or url/urls key. Unknown names fall
	// through here so the summary shows what the search was FOR instead of
	// nothing. Name matching is whole-token (isSearchToolName), so unrelated
	// tools like webhook_dispatch or submit_findings never surface a stray arg.
	if isSearchToolName(event.Name) {
		if q, ok := searchArgSummary(args); ok {
			return truncateRunes(q, 40)
		}
	}
	return ""
}

// searchNameTokens is the set of whole name tokens that mark a tool as a
// search/fetch tool whose query or url is worth surfacing as the purpose.
type searchTokenSet map[string]bool

var searchNameTokens = searchTokenSet{
	"web": true, "search": true, "fetch": true, "query": true,
	"find": true, "browse": true, "scrape": true, "url": true,
}

// isSearchToolName reports whether a tool name looks like a search/fetch tool.
// Matching is token-based: the name is split on separators and each token is
// compared exactly, so "web_search" matches but "webhook_dispatch" and
// "submit_findings" do not (the false positives of substring matching).
// Separator-less compounds ("websearch", "webfetch") match by prefix.
func isSearchToolName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "websearch") || strings.HasPrefix(lower, "webfetch") {
		return true
	}
	for _, token := range strings.FieldsFunc(lower, func(r rune) bool {
		return r == '_' || r == '-' || r == ' ' || r == '.' || r == '/'
	}) {
		if searchNameTokens[token] {
			return true
		}
	}
	return false
}

// searchArgSummary returns the first query/url-like argument (singular or
// plural) as the "what it was for" detail for search/fetch tools. Handles the
// real schemas: query/q/search/search_query/terms (strings), queries (array of
// strings), and url/urls (string or array). ok is false when none is present.
func searchArgSummary(args map[string]any) (string, bool) {
	if value, ok := firstSearchString(args, "query", "q", "search", "search_query", "terms"); ok {
		return value, true
	}
	if value, ok := firstSearchString(args, "url", "urls"); ok {
		return value, true
	}
	if value, ok := joinStringSlice(args["queries"]); ok {
		return value, true
	}
	return "", false
}

// firstSearchString returns the first non-empty string value for the given
// keys, including a single-element []string/[]any array form.
func firstSearchString(args map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := args[key]; ok {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				return text, true
			}
			if joined, ok := joinStringSlice(value); ok {
				return joined, true
			}
		}
	}
	return "", false
}

// joinStringSlice joins a []string or []any-of-strings value into a single
// comma-separated purpose. ok is false for nil/empty or non-string slices.
func joinStringSlice(value any) (string, bool) {
	var parts []string
	switch typed := value.(type) {
	case []string:
		parts = typed
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				parts = append(parts, text)
			}
		}
	default:
		return "", false
	}
	filtered := parts[:0:0]
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	if len(filtered) == 0 {
		return "", false
	}
	return strings.Join(filtered, ", "), true
}

func toolCallIntArg(args map[string]any, key string) int {
	switch value := args[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case string:
		parsed, _ := strconv.Atoi(value)
		return parsed
	default:
		return 0
	}
}

// truncateRunes is provided by view.go; specialist_card.go relies on it for
// rune-safe description and error-message truncation.

// renderLeftRuleCard renders lines with a single status-tinted left rule and
// no other borders. Each line is prefixed with "│ " in the rule style; the
// content is padded to the given width so cards align. No top/bottom/right
// borders — lighter than styledBlock, matching the borderless inline tool
// render style used by reference TUIs.
func renderLeftRuleCard(width int, lines []string, ruleStyle lipgloss.Style) string {
	if width < 4 {
		width = 4
	}
	inner := width - 2 // "│ " takes 2 cells
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		fitted := fitStyledLine(line, inner)
		pad := strings.Repeat(" ", maxInt(0, inner-lipgloss.Width(fitted)))
		out = append(out, ruleStyle.Render("│ ")+fitted+pad)
	}
	return strings.Join(out, "\n")
}

// renderSpecialistSummary renders a one-line rollup shown above the specialist
// cards: live spinner, total/running/completed/error counts, and total tokens.
// Returns "" when there are no specialists.
func renderSpecialistSummary(specialists []specialistInfo, spinnerView string) string {
	if len(specialists) == 0 {
		return ""
	}
	running, completed, errors, totalTokens := 0, 0, 0, 0
	for _, sp := range specialists {
		totalTokens += sp.tokenCount
		switch sp.status {
		case specialistRunning:
			running++
		case specialistCompleted:
			completed++
		case specialistError:
			errors++
		}
	}
	summary := fmt.Sprintf("  %s %d specialists · %d running · %d done",
		spinnerView, len(specialists), running, completed)
	if errors > 0 {
		summary += fmt.Sprintf(" · %d error", errors)
		if errors > 1 {
			summary += "s"
		}
	}
	summary += " · " + formatTokenCount(totalTokens) + " tokens"
	// summary is "  " + spinnerView + " N specialists ...". The spinner sits
	// at byte offset 2 (after the 2-space indent), so the muted tail must skip
	// both the indent and the spinner's bytes to avoid splitting a multi-byte
	// rune and losing the indent.
	tailStart := 2 + len(spinnerView)
	return zeroTheme.accent.Render(spinnerView) + zeroTheme.muted.Render(summary[tailStart:])
}
