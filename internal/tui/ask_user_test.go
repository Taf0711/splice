package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/agent"
)

// testAskUserRequest is a two-question request used by model_test.go too.
func testAskUserRequest() agent.AskUserRequest {
	return agent.AskUserRequest{
		ToolCallID: "call_1",
		Header:     "Need a couple of details",
		Questions: []agent.AskUserQuestion{
			{Question: "Which framework?", Options: []string{"React", "Vue"}},
			{Question: "TypeScript?"},
		},
	}
}

func newAskUserModel(t *testing.T, request agent.AskUserRequest, answers *[][]string) model {
	t.Helper()
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	m.width = 96
	updated, _ := m.Update(askUserRequestMsg{
		runID:   7,
		request: request,
		answer:  func(values []string) { *answers = append(*answers, values) },
	})
	return updated.(model)
}

func askUserSingle(options []string, recommended string) agent.AskUserRequest {
	return agent.AskUserRequest{
		ToolCallID: "call_single",
		Questions:  []agent.AskUserQuestion{{Question: "Pick one", Options: options, Recommended: recommended}},
	}
}

func askUserTwoQuestions() agent.AskUserRequest {
	return agent.AskUserRequest{
		ToolCallID: "call_2q",
		Questions: []agent.AskUserQuestion{
			{Question: "Framework?", Header: "FW", Options: []string{"React", "Vue"}},
			{Question: "TypeScript?", Header: "TS", Options: []string{"Yes", "No"}},
		},
	}
}

// --- single question (no Confirm step) -------------------------------------

func TestAskUserSinglePickerDefaultsToRecommendedAndSubmits(t *testing.T) {
	var answers [][]string
	next := newAskUserModel(t, askUserSingle([]string{"Postgres", "SQLite", "MySQL"}, "SQLite"), &answers)

	if next.pendingAskUser == nil || next.pendingAskUser.states[0].typing {
		t.Fatalf("expected picker mode for a question with options, got %#v", next.pendingAskUser)
	}
	if next.pendingAskUser.states[0].cursor != 1 {
		t.Fatalf("expected cursor on the recommended option (index 1), got %d", next.pendingAskUser.states[0].cursor)
	}
	view := next.View()
	for _, want := range []string{"Postgres", "SQLite", "MySQL", "(recommended)", askUserTypeMyOwnLabel} {
		assertContains(t, view, want)
	}
	// A single question has no tab row / Confirm step.
	assertNotContains(t, view, "Confirm")

	updated, _ := next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if next.pendingAskUser != nil {
		t.Fatalf("single question should submit on selection, still pending: %#v", next.pendingAskUser)
	}
	if len(answers) != 1 || len(answers[0]) != 1 || answers[0][0] != "SQLite" {
		t.Fatalf("expected [SQLite], got %#v", answers)
	}
}

func TestAskUserSingleTypeMyOwnSubmitsTypedText(t *testing.T) {
	var answers [][]string
	next := newAskUserModel(t, askUserSingle([]string{"Postgres", "SQLite", "MySQL"}, "SQLite"), &answers)

	// Move from SQLite (1) to the "type your own" entry (index 3).
	for i := 0; i < 2; i++ {
		updated, _ := next.Update(testKey(tea.KeyDown))
		next = updated.(model)
	}
	if next.pendingAskUser.states[0].cursor != 3 {
		t.Fatalf("expected cursor on type-your-own (index 3), got %d", next.pendingAskUser.states[0].cursor)
	}
	updated, _ := next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if !next.pendingAskUser.states[0].typing {
		t.Fatal("expected 'type your own' to switch into free-text")
	}
	next.input.SetValue("CockroachDB")
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(answers) != 1 || answers[0][0] != "CockroachDB" {
		t.Fatalf("expected typed answer [CockroachDB], got %#v", answers)
	}
}

func TestAskUserTypingInPickerSwitchesToFreeText(t *testing.T) {
	var answers [][]string
	next := newAskUserModel(t, askUserSingle([]string{"Postgres", "SQLite", "MySQL"}, "SQLite"), &answers)

	updated, _ := next.Update(testKeyText("M"))
	next = updated.(model)
	if !next.pendingAskUser.states[0].typing {
		t.Fatal("a printable keystroke should switch the picker into free-text")
	}
	if next.input.Value() != "M" {
		t.Fatalf("the keystroke should be captured, got %q", next.input.Value())
	}
	next.input.SetValue("MariaDB")
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(answers) != 1 || answers[0][0] != "MariaDB" {
		t.Fatalf("expected typed answer [MariaDB], got %#v", answers)
	}
}

func TestAskUserSingleNoOptionsIsFreeText(t *testing.T) {
	var answers [][]string
	next := newAskUserModel(t, agent.AskUserRequest{
		ToolCallID: "c",
		Questions:  []agent.AskUserQuestion{{Question: "Describe the behavior"}},
	}, &answers)

	if next.pendingAskUser == nil || !next.pendingAskUser.states[0].typing {
		t.Fatalf("expected free-text for a no-options question, got %#v", next.pendingAskUser)
	}
	next.input.SetValue("free-form answer")
	updated, _ := next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(answers) != 1 || answers[0][0] != "free-form answer" {
		t.Fatalf("expected [free-form answer], got %#v", answers)
	}
}

func TestAskUserMultiSelectIsFreeTextWithSuggestions(t *testing.T) {
	var answers [][]string
	next := newAskUserModel(t, agent.AskUserRequest{
		ToolCallID: "c",
		Questions:  []agent.AskUserQuestion{{Question: "Which checks?", Options: []string{"lint", "test", "typecheck"}, MultiSelect: true}},
	}, &answers)

	if !next.pendingAskUser.states[0].typing {
		t.Fatalf("multi-select must use free-text, got %#v", next.pendingAskUser)
	}
	assertContains(t, next.View(), "suggested:")
	assertContains(t, next.View(), "lint")
	next.input.SetValue("lint, typecheck")
	updated, _ := next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(answers) != 1 || answers[0][0] != "lint, typecheck" {
		t.Fatalf("expected verbatim multi-answer, got %#v", answers)
	}
}

func TestAskUserTypeMyOwnEscReturnsToPicker(t *testing.T) {
	var answers [][]string
	next := newAskUserModel(t, askUserSingle([]string{"Postgres", "SQLite", "MySQL"}, "SQLite"), &answers)

	for i := 0; i < 2; i++ { // to type-your-own
		updated, _ := next.Update(testKey(tea.KeyDown))
		next = updated.(model)
	}
	updated, _ := next.Update(testKey(tea.KeyEnter)) // into free-text
	next = updated.(model)
	next.input.SetValue("scratch")

	updated, _ = next.Update(testKey(tea.KeyEsc)) // back to picker
	next = updated.(model)
	if next.pendingAskUser == nil {
		t.Fatal("Esc from type-your-own must not dismiss the prompt")
	}
	if next.pendingAskUser.states[0].typing {
		t.Fatal("Esc from type-your-own must return to the picker")
	}
	if len(answers) != 0 {
		t.Fatalf("Esc back to the picker must not deliver answers, got %#v", answers)
	}
	// Pick a real option from the picker.
	updated, _ = next.Update(testKey(tea.KeyUp)) // 3 -> 2 (MySQL)
	next = updated.(model)
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(answers) != 1 || answers[0][0] != "MySQL" {
		t.Fatalf("expected [MySQL] after returning to picker, got %#v", answers)
	}
}

// A single-question prompt has no tab strip / Confirm tab, so Tab / Shift+Tab must
// be no-ops (not advance into the hidden Confirm state).
func TestAskUserSingleQuestionTabIsNoOp(t *testing.T) {
	var answers [][]string
	next := newAskUserModel(t, askUserSingle([]string{"A", "B"}, "A"), &answers)
	if next.pendingAskUser.active != 0 {
		t.Fatalf("expected to start on the single question, active=%d", next.pendingAskUser.active)
	}
	updated, _ := next.Update(testKey(tea.KeyTab))
	next = updated.(model)
	if next.pendingAskUser == nil || next.pendingAskUser.active != 0 {
		t.Fatalf("Tab on a single-question prompt must be a no-op, active=%d", next.pendingAskUser.active)
	}
	updated, _ = next.Update(testKeyShift(tea.KeyTab))
	next = updated.(model)
	if next.pendingAskUser.active != 0 {
		t.Fatalf("Shift+Tab on a single-question prompt must be a no-op, active=%d", next.pendingAskUser.active)
	}
}

// --- multi-question (tabs + Confirm) ---------------------------------------

func TestAskUserMultiQuestionTabbedSubmit(t *testing.T) {
	var answers [][]string
	next := newAskUserModel(t, askUserTwoQuestions(), &answers)

	// Q1: select React (cursor 0), advances to Q2.
	updated, _ := next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if next.pendingAskUser == nil || next.pendingAskUser.active != 1 {
		t.Fatalf("expected to advance to Q2 (active=1), got %#v", next.pendingAskUser)
	}
	if len(answers) != 0 {
		t.Fatalf("must not deliver before Confirm, got %#v", answers)
	}
	// Q2: select Yes (cursor 0), advances to Confirm tab.
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if !next.pendingAskUser.onConfirmTab() {
		t.Fatalf("expected to land on the Confirm tab, active=%d", next.pendingAskUser.active)
	}
	assertContains(t, next.View(), "Review and submit")
	// Confirm: submit all.
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if next.pendingAskUser != nil {
		t.Fatalf("Confirm should submit, still pending: %#v", next.pendingAskUser)
	}
	if len(answers) != 1 || len(answers[0]) != 2 || answers[0][0] != "React" || answers[0][1] != "Yes" {
		t.Fatalf("expected [React Yes], got %#v", answers)
	}
}

func TestAskUserTabSwitchesQuestions(t *testing.T) {
	var answers [][]string
	next := newAskUserModel(t, askUserTwoQuestions(), &answers)

	if next.pendingAskUser.active != 0 {
		t.Fatalf("expected to start on Q1, got active=%d", next.pendingAskUser.active)
	}
	updated, _ := next.Update(testKey(tea.KeyTab))
	next = updated.(model)
	if next.pendingAskUser.active != 1 {
		t.Fatalf("Tab should move to Q2, got active=%d", next.pendingAskUser.active)
	}
	updated, _ = next.Update(testKey(tea.KeyTab))
	next = updated.(model)
	if !next.pendingAskUser.onConfirmTab() {
		t.Fatalf("Tab should move to the Confirm tab, got active=%d", next.pendingAskUser.active)
	}
	updated, _ = next.Update(testKey(tea.KeyTab))
	next = updated.(model)
	if next.pendingAskUser.active != 0 {
		t.Fatalf("Tab should wrap to Q1, got active=%d", next.pendingAskUser.active)
	}
	updated, _ = next.Update(testKeyShift(tea.KeyTab))
	next = updated.(model)
	if !next.pendingAskUser.onConfirmTab() {
		t.Fatalf("Shift+Tab should wrap back to the Confirm tab, got active=%d", next.pendingAskUser.active)
	}
}

func TestAskUserEscDismissDeliversPartialAnswers(t *testing.T) {
	var answers [][]string
	next := newAskUserModel(t, askUserTwoQuestions(), &answers)

	// Answer Q1, advance to Q2, then Esc (Q2 is a picker, so Esc dismisses).
	updated, _ := next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	updated, _ = next.Update(testKey(tea.KeyEsc))
	next = updated.(model)
	if next.pendingAskUser != nil {
		t.Fatalf("Esc on a picker should dismiss, still pending: %#v", next.pendingAskUser)
	}
	if len(answers) != 1 || len(answers[0]) != 2 || answers[0][0] != "React" || answers[0][1] != "" {
		t.Fatalf("expected partial [React, \"\"], got %#v", answers)
	}
	if !next.pending {
		t.Fatal("dismiss cancels only the questionnaire; the run keeps running")
	}
}

// --- rendering -------------------------------------------------------------

func TestAskUserMultiQuestionShowsTabsAndOptions(t *testing.T) {
	var answers [][]string
	next := newAskUserModel(t, agent.AskUserRequest{
		ToolCallID: "call_v",
		Questions: []agent.AskUserQuestion{
			{Question: "Which framework?", Header: "Framework", Options: []string{"React", "Vue"}},
			{Question: "TypeScript?", Header: "TypeScript"},
		},
	}, &answers)
	view := next.View()
	for _, want := range []string{"Framework", "TypeScript", "Confirm", "Which framework?", "React", "Vue"} {
		assertContains(t, view, want)
	}
}

func TestAskUserOptionDescriptionsRender(t *testing.T) {
	var answers [][]string
	next := newAskUserModel(t, agent.AskUserRequest{
		ToolCallID: "call_d",
		Questions: []agent.AskUserQuestion{{
			Question:           "Which decade?",
			Options:            []string{"1980s", "1990s"},
			OptionDescriptions: []string{"Synth-pop, hair metal", "Grunge, Britpop"},
			Recommended:        "1980s",
		}},
	}, &answers)
	view := next.View()
	for _, want := range []string{"1980s", "Synth-pop, hair metal", "1990s", "Grunge, Britpop"} {
		assertContains(t, view, want)
	}
}

// --- regressions preserved -------------------------------------------------

func TestAskUserPromptBlocksNormalSubmit(t *testing.T) {
	var answers [][]string
	next := newAskUserModel(t, askUserTwoQuestions(), &answers)
	next.input.SetValue("/help")

	updated, _ := next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if transcriptContains(next.transcript, "Available commands") {
		t.Fatalf("ask_user must capture Enter, not run commands: %#v", next.transcript)
	}
	if next.pendingAskUser == nil {
		t.Fatal("expected the prompt to remain pending after answering Q1 of 2")
	}
}

func TestAskUserRequestClearsComposerDraft(t *testing.T) {
	var answers [][]string
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	m = typeRunes(t, m, "hidden followup")
	if !m.composerActive || m.composerValue() == "" {
		t.Fatalf("setup expected an active composer draft, got active=%v value=%q", m.composerActive, m.composerValue())
	}
	updated, _ := m.Update(askUserRequestMsg{
		runID:   7,
		request: agent.AskUserRequest{ToolCallID: "c", Questions: []agent.AskUserQuestion{{Question: "Proceed?"}}},
		answer:  func(values []string) { answers = append(answers, values) },
	})
	next := updated.(model)
	if next.composerActive || next.composerValue() != "" {
		t.Fatalf("ask_user should clear the composer draft, active=%v value=%q", next.composerActive, next.composerValue())
	}
	next.input.SetValue("yes")
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(answers) != 1 || answers[0][0] != "yes" {
		t.Fatalf("expected answer to use ask_user input only, got %#v", answers)
	}
	if transcriptContains(next.transcript, "hidden followup") {
		t.Fatalf("hidden composer draft leaked into transcript: %#v", next.transcript)
	}
}

func TestAskUserRequestClearsStaleSuggestions(t *testing.T) {
	var answers [][]string
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	m.suggestions = []commandSuggestion{{Name: "/model", Desc: "Pick a model."}}
	m.suggestionIdx = 0
	m.suggestionsAreFiles = true

	updated, _ := m.Update(askUserRequestMsg{
		runID:   7,
		request: agent.AskUserRequest{ToolCallID: "c", Questions: []agent.AskUserQuestion{{Question: "Proceed?"}}},
		answer:  func(values []string) { answers = append(answers, values) },
	})
	next := updated.(model)
	if len(next.suggestions) != 0 || next.suggestionsAreFiles {
		t.Fatalf("ask_user should clear stale suggestions, got %#v files=%v", next.suggestions, next.suggestionsAreFiles)
	}
}

func TestAskUserEmptyRequestResolvesImmediately(t *testing.T) {
	var answers [][]string
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	updated, _ := m.Update(askUserRequestMsg{
		runID:   7,
		request: agent.AskUserRequest{ToolCallID: "c"},
		answer:  func(values []string) { answers = append(answers, values) },
	})
	next := updated.(model)
	if next.pendingAskUser != nil {
		t.Fatalf("an empty request must not open a prompt, got %#v", next.pendingAskUser)
	}
	if len(answers) != 1 {
		t.Fatalf("an empty request should resolve immediately, got %#v", answers)
	}
}

// --- free-text and confirm containment -------------------------------------

// longAskUserAnswer is a prose answer long enough to wrap on every card width
// under test. The expected wrapped chunks below are hand-derived from the width
// budget: card inner width (width-4) minus the free-text prefix and cursor, or
// minus the confirm summary's title prefix.
func longAskUserAnswer() string {
	return "the quick brown fox jumps over the lazy dog and keeps running through the forest toward the distant hills"
}

func askUserFreeTextModel(t *testing.T, width int, request agent.AskUserRequest, input string) (model, *[][]string) {
	t.Helper()
	var answers [][]string
	next := newAskUserModel(t, request, &answers)
	next.width = width
	next.height = 30
	next.input.SetValue(input)
	return next, &answers
}

// assertRenderedLinesFit fails if any rendered line is wider than width. It
// strips styling first so the check measures display cells, not ANSI bytes.
func assertRenderedLinesFit(t *testing.T, view any, width int) {
	t.Helper()
	for i, line := range strings.Split(plainRender(t, view), "\n") {
		if got := lipgloss.Width(plainRender(t, line)); got > width {
			t.Fatalf("line %d width %d > %d: %q", i, got, width, plainRender(t, line))
		}
	}
}

// assertAnswerWordsVisible proves the wrapped display did not lose or reorder
// any word of the submitted answer (truncation would fail this check).
func assertAnswerWordsVisible(t *testing.T, view any, answer string) {
	t.Helper()
	words := strings.Fields(answer)
	plain := strings.ReplaceAll(plainRender(t, view), "▌", "") // the trailing cursor merges with the last token
	idx := 0
	for _, line := range strings.Split(plain, "\n") {
		for _, token := range strings.Fields(line) {
			if idx < len(words) && token == words[idx] {
				idx++
			}
		}
	}
	if idx < len(words) {
		t.Fatalf("answer not fully visible (%d/%d words): %q", idx, len(words), plain)
	}
}

// askUserCardInteriors returns the questionnaire card's content cells: each
// rendered line with the border chrome ("│ ... │") and right padding removed.
// Leading spaces survive so assertions can prove continuation alignment.
func askUserCardInteriors(t *testing.T, view any) map[string]bool {
	t.Helper()
	set := map[string]bool{}
	for _, line := range strings.Split(plainRender(t, view), "\n") {
		line = strings.TrimPrefix(line, "│ ")
		line = strings.TrimSuffix(line, " │")
		if s := strings.TrimRight(line, " "); s != "" {
			set[s] = true
		}
	}
	return set
}

func TestAskUserFreeTextWrapsInsideCard(t *testing.T) {
	request := agent.AskUserRequest{
		ToolCallID: "c",
		Questions:  []agent.AskUserQuestion{{Question: "Describe the behavior"}},
	}
	answer := longAskUserAnswer()
	cases := []struct {
		name      string
		width     int
		firstLine string // the hand-derived first wrapped chunk after "❯ "
		rest      string // the continuation chunk ("" when the answer fits one line)
	}{
		{"narrow", 60, "the quick brown fox jumps over the lazy dog and keeps", "running through the forest toward the distant hills"},
		{"compact", 80, "the quick brown fox jumps over the lazy dog and keeps running through the", "forest toward the distant hills"},
		{"full", 120, answer, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, answers := askUserFreeTextModel(t, tc.width, request, answer)
			view := next.footerView(tc.width)
			assertRenderedLinesFit(t, view, tc.width)

			interiors := askUserCardInteriors(t, view)
			if tc.rest != "" {
				// Continuation lines align under the first answer character ("❯ "
				// is two cells) and the cursor stays inside on the last line.
				if !interiors["❯ "+tc.firstLine] {
					t.Fatalf("first answer line missing %q: %v", "❯ "+tc.firstLine, interiors)
				}
				if !interiors["  "+tc.rest+"▌"] {
					t.Fatalf("wrapped continuation missing %q: %v", "  "+tc.rest+"▌", interiors)
				}
			} else if !interiors["❯ "+answer+"▌"] {
				t.Fatalf("single-line answer missing %q: %v", "❯ "+answer+"▌", interiors)
			}
			assertAnswerWordsVisible(t, view, answer)

			// The wrapped display must not change the submitted answer.
			updated, _ := next.Update(testKey(tea.KeyEnter))
			next = updated.(model)
			if len(*answers) != 1 || (*answers)[0][0] != answer {
				t.Fatalf("expected the exact answer %q, got %#v", answer, *answers)
			}
		})
	}
}

func TestAskUserFreeTextWrapsInsideCardAtModelWidth(t *testing.T) {
	// The same containment must hold through the full rendered View, not only
	// the footer region that owns the questionnaire.
	request := agent.AskUserRequest{
		ToolCallID: "c",
		Questions:  []agent.AskUserQuestion{{Question: "Describe the behavior"}},
	}
	next, _ := askUserFreeTextModel(t, 80, request, longAskUserAnswer())
	assertRenderedLinesFit(t, next.View(), 80)
}

func TestAskUserFreeTextHardSplitsLongUnbrokenInput(t *testing.T) {
	request := agent.AskUserRequest{
		ToolCallID: "c",
		Questions:  []agent.AskUserQuestion{{Question: "Paste the key"}},
	}
	input := strings.Repeat("a", 200)
	next, answers := askUserFreeTextModel(t, 60, request, input)
	view := next.footerView(60)
	assertRenderedLinesFit(t, view, 60)
	interiors := askUserCardInteriors(t, view)
	if !interiors["❯ "+strings.Repeat("a", 53)] {
		t.Fatalf("first wrapped line should hold 53 cells of the run: %v", interiors)
	}
	if !interiors["  "+strings.Repeat("a", 53)] {
		t.Fatalf("continuation line should hold 53 cells with a 2-cell indent: %v", interiors)
	}
	if !interiors["  "+strings.Repeat("a", 41)+"▌"] {
		t.Fatalf("cursor must stay on the final split chunk: %v", interiors)
	}
	updated, _ := next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(*answers) != 1 || (*answers)[0][0] != input {
		t.Fatalf("hard-splitting the display must not change the answer, got %#v", *answers)
	}
}

func TestAskUserFreeTextWrapsWideUnicodeByDisplayWidth(t *testing.T) {
	request := agent.AskUserRequest{
		ToolCallID: "c",
		Questions:  []agent.AskUserQuestion{{Question: "Paste the text"}},
	}
	input := strings.Repeat("絵", 60) // 60 double-width runes = 120 cells
	next, _ := askUserFreeTextModel(t, 60, request, input)
	view := next.footerView(60)
	assertRenderedLinesFit(t, view, 60)
	interiors := askUserCardInteriors(t, view)
	// 26 double-width runes (52 cells) fit the 53-cell budget; 27 would be 54.
	if !interiors["❯ "+strings.Repeat("絵", 26)] {
		t.Fatalf("expected 26 double-width runes on the first line: %v", interiors)
	}
	if !interiors["  "+strings.Repeat("絵", 26)] {
		t.Fatalf("expected 26 double-width runes on the continuation line: %v", interiors)
	}
	if !interiors["  "+strings.Repeat("絵", 8)+"▌"] {
		t.Fatalf("final partial line or cursor missing: %v", interiors)
	}
}

func TestAskUserFreeTextPreservesExistingSubmitTrim(t *testing.T) {
	request := agent.AskUserRequest{
		ToolCallID: "c",
		Questions:  []agent.AskUserQuestion{{Question: "Name it"}},
	}
	next, answers := askUserFreeTextModel(t, 80, request, "  padded answer  ")
	assertRenderedLinesFit(t, next.footerView(80), 80)
	assertAnswerWordsVisible(t, next.View(), "padded answer")

	updated, _ := next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if len(*answers) != 1 || (*answers)[0][0] != "padded answer" {
		t.Fatalf("expected the existing trimmed answer, got %#v", *answers)
	}
}

func TestAskUserConfirmAnswersWrapInsideCard(t *testing.T) {
	request := agent.AskUserRequest{
		ToolCallID: "c",
		Questions: []agent.AskUserQuestion{
			{Question: "Question one?", Header: "FW"},
			{Question: "Question two?", Options: []string{"Yes", "No"}},
		},
	}
	answer := longAskUserAnswer()
	cases := []struct {
		name          string
		width         int
		wantInteriors []string // hand-derived summary lines inside the card, including alignment
	}{
		{"narrow", 60, []string{
			"  FW: the quick brown fox jumps over the lazy dog and",
			"      keeps running through the forest toward the",
			"      distant hills",
			"  Question two?: Yes",
		}},
		{"compact", 80, []string{
			"  FW: the quick brown fox jumps over the lazy dog and keeps running through",
			"      the forest toward the distant hills",
			"  Question two?: Yes",
		}},
		{"full", 120, []string{
			"  FW: " + answer,
			"  Question two?: Yes",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var answers [][]string
			next := newAskUserModel(t, request, &answers)
			next.width = tc.width
			next.height = 30
			next.input.SetValue(answer)
			updated, _ := next.Update(testKey(tea.KeyEnter)) // Q1 free text -> Q2 picker
			next = updated.(model)
			updated, _ = next.Update(testKey(tea.KeyEnter)) // Q2 picker -> Confirm tab
			next = updated.(model)
			if !next.pendingAskUser.onConfirmTab() {
				t.Fatalf("expected the Confirm tab, got %#v", next.pendingAskUser)
			}
			view := next.footerView(tc.width)
			assertRenderedLinesFit(t, view, tc.width)

			interiors := askUserCardInteriors(t, view)
			if !interiors["Review and submit:"] {
				t.Fatalf("review header missing: %v", interiors)
			}
			// The "  FW: " summary prefix is 6 cells; continuations align under
			// the first answer character.
			for _, want := range tc.wantInteriors {
				if !interiors[want] {
					t.Fatalf("confirm summary missing %q: %v", want, interiors)
				}
			}

			// Confirm submits the exact answers.
			updated, _ = next.Update(testKey(tea.KeyEnter))
			next = updated.(model)
			if next.pendingAskUser != nil {
				t.Fatalf("Confirm should submit, still pending: %#v", next.pendingAskUser)
			}
			if len(answers) != 1 || len(answers[0]) != 2 || answers[0][0] != answer || answers[0][1] != "Yes" {
				t.Fatalf("expected [%q Yes], got %#v", answer, answers)
			}
		})
	}
}
