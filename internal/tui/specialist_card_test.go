package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/streamjson"
)

func TestSpecialistTrackerStartAndComplete(t *testing.T) {
	var tracker specialistTracker
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	tracker.start("worker", "fix oauth tests", "session-123", now)

	all := tracker.all()
	if len(all) != 1 {
		t.Fatalf("expected 1 specialist, got %d", len(all))
	}
	if all[0].name != "worker" {
		t.Errorf("name = %q, want worker", all[0].name)
	}
	if all[0].status != specialistRunning {
		t.Errorf("status = %v, want specialistRunning", all[0].status)
	}
	if !tracker.hasRunning() {
		t.Error("tracker should have running specialist")
	}

	tracker.complete("session-123", specialistCompleted, 0, "", now.Add(45*time.Second))

	info, ok := tracker.getBySessionID("session-123")
	if !ok {
		t.Fatal("specialist not found after complete")
	}
	if info.status != specialistCompleted {
		t.Errorf("status = %v, want specialistCompleted", info.status)
	}
	if tracker.hasRunning() {
		t.Error("tracker should not have running specialist after completion")
	}
}

func TestSpecialistTrackerIncrementToolCount(t *testing.T) {
	var tracker specialistTracker
	now := time.Now()

	tracker.start("worker", "task", "s1", now)
	tracker.incrementToolCount("s1")
	tracker.incrementToolCount("s1")
	tracker.incrementToolCount("s1")

	info, _ := tracker.getBySessionID("s1")
	if info.toolCount != 3 {
		t.Errorf("toolCount = %d, want 3", info.toolCount)
	}
}

func TestSpecialistTrackerAddTokens(t *testing.T) {
	var tracker specialistTracker
	now := time.Now()

	tracker.start("worker", "task", "s1", now)
	tracker.addTokens("s1", 1000)
	tracker.addTokens("s1", 500)

	info, _ := tracker.getBySessionID("s1")
	if info.tokenCount != 1500 {
		t.Errorf("tokenCount = %d, want 1500", info.tokenCount)
	}
}

func TestSpecialistTrackerClear(t *testing.T) {
	var tracker specialistTracker
	tracker.start("worker", "task", "s1", time.Now())
	tracker.clear()

	if len(tracker.all()) != 0 {
		t.Error("tracker should be empty after clear")
	}
}

func TestSpecialistTrackerDuplicateStart(t *testing.T) {
	var tracker specialistTracker
	now := time.Now()

	tracker.start("worker", "task1", "s1", now)
	tracker.start("worker", "task2", "s1", now.Add(5*time.Second))

	all := tracker.all()
	if len(all) != 1 {
		t.Errorf("duplicate start should update, not add: got %d", len(all))
	}
	if all[0].description != "task2" {
		t.Errorf("description = %q, want task2", all[0].description)
	}
}

func TestSpecialistStatusString(t *testing.T) {
	tests := []struct {
		status specialistStatus
		want   string
	}{
		{specialistRunning, "running"},
		{specialistCompleted, "completed"},
		{specialistError, "error"},
	}
	for _, tt := range tests {
		if got := specialistStatusString(tt.status); got != tt.want {
			t.Errorf("specialistStatusString(%v) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestParseSpecialistStatus(t *testing.T) {
	tests := []struct {
		input string
		want  specialistStatus
	}{
		{"running", specialistRunning},
		{"completed", specialistCompleted},
		{"error", specialistError},
		{"unknown", specialistError},
		{"", specialistError},
	}
	for _, tt := range tests {
		if got := parseSpecialistStatus(tt.input); got != tt.want {
			t.Errorf("parseSpecialistStatus(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{100, "100"},
		{1000, "1,000"},
		{1840, "1,840"},
		{5210, "5,210"},
		{1000000, "1,000,000"},
	}
	for _, tt := range tests {
		if got := formatTokenCount(tt.input); got != tt.want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatSpecialistElapsed(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "1s"},
		{5 * time.Second, "5s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m"},
		{65 * time.Second, "1m5s"},
		{125 * time.Second, "2m5s"},
	}
	for _, tt := range tests {
		if got := formatSpecialistElapsed(tt.d); got != tt.want {
			t.Errorf("formatSpecialistElapsed(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestRenderSpecialistCard(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4"})
	m.width = 80
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now.Add(18 * time.Second) }

	info := specialistInfo{
		name:           "worker",
		description:    "fix oauth tests",
		childSessionID: "s1",
		status:         specialistRunning,
		startedAt:      now,
		toolCount:      3,
		tokenCount:     1840,
	}

	got := m.renderSpecialistCard(info, 80)
	if got == "" {
		t.Fatal("expected non-empty specialist card")
	}
	plain := ansiPattern.ReplaceAllString(got, "")
	// Left-rule: every line starts with │.
	for _, line := range strings.Split(plain, "\n") {
		if !strings.HasPrefix(line, "│") {
			t.Errorf("card line %q should start with │ (left-rule)", line)
		}
	}
	// No rounded border characters.
	for _, ch := range "╭╮╰╯" {
		if strings.ContainsRune(plain, ch) {
			t.Errorf("card must not contain rounded border %q", ch)
		}
	}
	// No hint line.
	if strings.Contains(plain, "[Enter]") || strings.Contains(plain, "view subchat") {
		t.Error("card must not contain the old [Enter] view subchat hint")
	}
	// Content present.
	if !strings.Contains(plain, "worker") {
		t.Error("card should contain specialist name")
	}
}

func TestRenderSpecialistCardOmitsZeroTokens(t *testing.T) {
	// Until usage is bridged from the child, tokenCount stays 0; the card must show
	// the (now-wired) tool-call count without advertising a misleading "0 tokens" (M18).
	m := newModel(t.Context(), Options{ModelName: "gpt-4"})
	m.width = 80
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now.Add(5 * time.Second) }

	info := specialistInfo{
		name:           "worker",
		description:    "do work",
		childSessionID: "s1",
		status:         specialistRunning,
		startedAt:      now,
		toolCount:      2,
		tokenCount:     0,
	}
	plain := ansiPattern.ReplaceAllString(m.renderSpecialistCard(info, 80), "")
	if !strings.Contains(plain, "2 tool calls") {
		t.Errorf("card should show the wired tool-call count, got:\n%s", plain)
	}
	if strings.Contains(plain, "tokens") {
		t.Errorf("card must omit the token segment when tokenCount is 0, got:\n%s", plain)
	}
}

func TestSpecialistTrackerIncrementToolCountByCallID(t *testing.T) {
	// The progress handler increments by the tool-call id while the entry is still
	// keyed by it (before completion reconciles it to the session id) (M18).
	var tracker specialistTracker
	tracker.start("worker", "desc", "call-1", time.Now())
	tracker.incrementToolCount("call-1")
	tracker.incrementToolCount("call-1")
	info, ok := tracker.getBySessionID("call-1")
	if !ok || info.toolCount != 2 {
		t.Fatalf("toolCount via call id = %d (found=%v), want 2", info.toolCount, ok)
	}
}

func TestParseTaskCallArgs(t *testing.T) {
	name, desc := parseTaskCallArgs(`{"name":"worker","description":"fix tests"}`)
	if name != "worker" {
		t.Errorf("name = %q, want worker", name)
	}
	if desc != "fix tests" {
		t.Errorf("description = %q, want 'fix tests'", desc)
	}

	// Fall back to prompt when description is missing
	name2, desc2 := parseTaskCallArgs(`{"name":"explorer","prompt":"map the codebase"}`)
	if name2 != "explorer" {
		t.Errorf("name = %q, want explorer", name2)
	}
	if desc2 != "map the codebase" {
		t.Errorf("description = %q, want 'map the codebase'", desc2)
	}
}

func TestRenderLeftRuleCard(t *testing.T) {
	lines := []string{"header line", "body line"}
	got := renderLeftRuleCard(40, lines, zeroTheme.accent)
	if got == "" {
		t.Fatal("expected non-empty left-rule card")
	}
	plain := ansiPattern.ReplaceAllString(got, "")
	// Left rule present on every line.
	for _, line := range strings.Split(plain, "\n") {
		if !strings.HasPrefix(line, "│") {
			t.Errorf("line %q should start with left rule │", line)
		}
	}
	// No rounded border characters.
	for _, ch := range "╭╮╰╯" {
		if strings.ContainsRune(plain, ch) {
			t.Errorf("left-rule card must not contain %q", ch)
		}
	}
	// Two input lines → two output lines.
	if len(strings.Split(plain, "\n")) != 2 {
		t.Errorf("expected 2 lines, got %d", len(strings.Split(plain, "\n")))
	}
}

func TestRenderSpecialistSummary(t *testing.T) {
	// Empty → no output.
	if got := renderSpecialistSummary(nil, "⠙"); got != "" {
		t.Errorf("empty specialists should produce empty string, got %q", got)
	}

	specialists := []specialistInfo{
		{status: specialistRunning, tokenCount: 1840},
		{status: specialistCompleted, tokenCount: 5210},
	}
	got := renderSpecialistSummary(specialists, "⠙")
	if got == "" {
		t.Fatal("expected non-empty summary for 2 specialists")
	}
	if !strings.Contains(got, "2 specialists") {
		t.Errorf("summary should contain total count, got %q", got)
	}
	if !strings.Contains(got, "1 running") {
		t.Errorf("summary should contain running count, got %q", got)
	}
	if !strings.Contains(got, "1 done") {
		t.Errorf("summary should contain completed count, got %q", got)
	}
	if !strings.Contains(got, "7,050") {
		t.Errorf("summary should contain total tokens, got %q", got)
	}
	// No errors → no error segment.
	if strings.Contains(got, "error") {
		t.Errorf("summary should omit errors when splice, got %q", got)
	}

	// With an error.
	specialists = append(specialists, specialistInfo{status: specialistError, tokenCount: 100})
	got = renderSpecialistSummary(specialists, "⠙")
	if !strings.Contains(got, "1 error") {
		t.Errorf("summary should contain error count, got %q", got)
	}

	// Pluralization: two errored specialists → "2 errors", not "2 error".
	specialists = append(specialists, specialistInfo{status: specialistError, tokenCount: 50})
	got = renderSpecialistSummary(specialists, "⠙")
	if !strings.Contains(got, "2 errors") {
		t.Errorf("summary should pluralize errors, got %q", got)
	}
	if strings.Contains(got, "2 error\n") || strings.Contains(got, "2 error\t") ||
		strings.HasSuffix(got, "2 error") || strings.Contains(got, "2 error·") ||
		strings.Contains(got, "2 error ") {
		t.Errorf("summary should not contain singular '2 error', got %q", got)
	}
}

func TestRenderSpecialistCardWithProgress(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4"})
	m.width = 80
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now.Add(18 * time.Second) }

	info := specialistInfo{
		name:           "worker",
		description:    "fix tests",
		childSessionID: "s1",
		status:         specialistRunning,
		startedAt:      now,
		toolCount:      3,
		tokenCount:     1840,
		currentTool:    "read_file",
		currentDetail:  "internal/tui/model.go",
	}

	got := m.renderSpecialistCard(info, 80)
	if got == "" {
		t.Fatal("expected non-empty card")
	}
	plain := ansiPattern.ReplaceAllString(got, "")
	if !strings.Contains(plain, "↳ read_file") {
		t.Errorf("card should contain progress line with tool name, got:\n%s", plain)
	}
	if !strings.Contains(plain, "internal/tui/model.go") {
		t.Errorf("card should contain progress detail, got:\n%s", plain)
	}
}

func TestSpecialistTrackerAddToolCallList(t *testing.T) {
	var tracker specialistTracker
	now := time.Now()

	tracker.start("design", "plan the feature", "s1", now)
	tracker.addToolCall("s1", "read_file", "internal/splice/run.go")
	tracker.addToolCall("s1", "web_search", "how does zero handle tool registry")
	tracker.addToolCall("s1", "write_file", "internal/splice/plan.go")

	info, _ := tracker.getBySessionID("s1")
	if len(info.toolCalls) != 3 {
		t.Fatalf("toolCalls len = %d, want 3", len(info.toolCalls))
	}
	if info.toolCalls[0].name != "read_file" || info.toolCalls[0].detail != "internal/splice/run.go" {
		t.Errorf("toolCalls[0] = %+v", info.toolCalls[0])
	}
	if info.toolCalls[1].name != "web_search" || info.toolCalls[1].detail != "how does zero handle tool registry" {
		t.Errorf("toolCalls[1] = %+v", info.toolCalls[1])
	}
	// Order preserved.
	if info.toolCalls[2].name != "write_file" {
		t.Errorf("toolCalls[2].name = %q, want write_file", info.toolCalls[2].name)
	}
}

func TestSpecialistCardListsEveryToolCall(t *testing.T) {
	m := transcriptViewTestModel()
	m.width = 100
	info := specialistInfo{
		name:      "design",
		status:    specialistCompleted,
		toolCount: 2,
		toolCalls: []specialistToolCall{
			{name: "web_search", detail: "deepseek v4 pricing"},
			{name: "write_file", detail: "docs/pricing.md"},
		},
	}
	got := plainRender(t, m.renderSpecialistCard(info, 60))
	for _, want := range []string{"↳ web_search deepseek v4 pricing", "↳ write_file docs/pricing.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("card missing %q; got:\n%s", want, got)
		}
	}
}

func TestToolCallSummaryWebSearchQuery(t *testing.T) {
	for _, name := range []string{"web_search", "websearch", "search_web", "web-search"} {
		got := toolCallSummary(streamjson.Event{Name: name, Args: map[string]any{"query": "splice tool registry"}})
		if !strings.Contains(got, "splice tool registry") {
			t.Errorf("%s summary = %q, want the query", name, got)
		}
	}
}

func TestAddToolCallSanitizesAndBounds(t *testing.T) {
	var tracker specialistTracker
	now := time.Now()
	tracker.start("design", "task", "s1", now)

	// A child detail with a terminal escape, OSC, and newline must be reduced
	// to a single clean line before it reaches the transcript.
	dirty := "\x1b[31mred\x1b[0m OSC \x1b]8;;http://x\x07link\x1b]8;;\x07\nline two"
	tracker.addToolCall("s1", "web_search", dirty)
	info, _ := tracker.getBySessionID("s1")
	if len(info.toolCalls) != 1 {
		t.Fatalf("toolCalls len = %d, want 1", len(info.toolCalls))
	}
	got := info.toolCalls[0].detail
	if strings.ContainsAny(got, "\x1b\n\x07\r") {
		t.Fatalf("detail still carries terminal controls/newlines: %q", got)
	}
	if strings.Contains(got, "line") && strings.Contains(got, "two") && !strings.Contains(got, "line two") {
		// Newline was dropped, so the following text joins the same line; that is
		// the sanitizer's safe single-line contract (no injected rows), not a bug.
		t.Logf("newline collapsed into one line: %q", got)
	}

	// More than specialistToolCap calls: only the newest 8 are retained, and
	// toolCount keeps the true total for the fold count. Production increments
	// toolCount alongside addToolCall in the same progress message.
	for i := 0; i < 12; i++ {
		tracker.addToolCall("s1", "bash", "step")
		tracker.incrementToolCount("s1")
	}
	info, _ = tracker.getBySessionID("s1")
	if len(info.toolCalls) != specialistToolCap {
		t.Fatalf("retained toolCalls = %d, want %d", len(info.toolCalls), specialistToolCap)
	}
	if info.toolCount != 12 {
		t.Fatalf("toolCount = %d, want 12 (all 12 calls counted)", info.toolCount)
	}
	if info.toolCalls[specialistToolCap-1].detail != "step" {
		t.Fatalf("newest call not retained as the tail")
	}
	// The dirty call from earlier was dropped by the cap (it is the oldest).
	if info.toolCalls[0].detail != "step" {
		t.Fatalf("oldest retained call should be a step call, got %q", info.toolCalls[0].detail)
	}
}

func TestSearchArgSummaryPluralAndURL(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"web_search plural queries", map[string]any{"queries": []any{"go 1.25", "splice"}}, "go 1.25, splice"},
		{"fetch_content url", map[string]any{"url": "https://example.com/doc"}, "https://example.com/doc"},
		{"fetch_content urls array", map[string]any{"urls": []string{"https://a.com"}}, "https://a.com"},
		{"singular query", map[string]any{"query": "deepseek pricing"}, "deepseek pricing"},
		{"no query", map[string]any{"path": "/tmp/x"}, ""},
	}
	for _, tc := range cases {
		got, ok := searchArgSummary(tc.args)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: got %q, want none", tc.name, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("%s: got %q ok=%v, want %q", tc.name, got, ok, tc.want)
		}
	}
}

func TestIsSearchToolNameTokenBased(t *testing.T) {
	positives := []string{"web_search", "websearch", "fetch_content", "search_web", "query_planner", "webfetch"}
	negatives := []string{"webhook_dispatch", "submit_findings", "findings_report", "update", "bash"}
	for _, name := range positives {
		if !isSearchToolName(name) {
			t.Errorf("%s should be a search/fetch tool name", name)
		}
	}
	for _, name := range negatives {
		if isSearchToolName(name) {
			t.Errorf("%s must NOT be treated as a search tool (substring false positive)", name)
		}
	}
}

func TestToolCallSummaryNegativeNameMatching(t *testing.T) {
	// A non-search tool whose args happen to carry a "q" must not surface it as
	// a search purpose.
	got := toolCallSummary(streamjson.Event{Name: "webhook_dispatch", Args: map[string]any{"q": "https://hooks.example.com/x"}})
	if got != "" {
		t.Errorf("webhook_dispatch summary = %q, want empty", got)
	}
}
