package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestMouseWheelScrollsChatWithoutRecallingInputHistory(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width = 90
	m.height = 14
	m.mouseCapture = true
	m.inputHistory = []string{"old prompt"}
	m.historyIdx = len(m.inputHistory)
	for index := 0; index < 12; index++ {
		m.transcript = appendRow(m.transcript, rowAssistant, "message "+string(rune('A'+index)))
	}

	updated, cmd := m.Update(testMouseWheel(tea.MouseWheelUp, 0, 0))
	m = updated.(model)
	if cmd == nil {
		t.Fatal("mouse wheel that moves the scroll offset should return a clear-screen command")
	}
	if got := cmd(); got != tea.ClearScreen() {
		t.Fatalf("scroll command yielded %#v, want clear-screen message %#v", got, tea.ClearScreen())
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("mouse wheel should not recall input history, got %q", got)
	}
	if m.chatScrollOffset != chatWheelScrollLines {
		t.Fatalf("chatScrollOffset = %d, want %d", m.chatScrollOffset, chatWheelScrollLines)
	}
}

func TestMouseWheelKeepsChatLayoutCache(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width = 90
	m.height = 14
	m.headerPrinted = true
	for index := 0; index < 80; index++ {
		m.transcript = appendRow(m.transcript, rowAssistant, "message "+string(rune('A'+index%26)))
	}
	if _, ok := m.chatTranscriptViewport(); !ok || !m.chatViewport.valid {
		t.Fatal("expected a valid chat viewport cache before scrolling")
	}
	generation := m.chatLayoutGen

	for _, button := range []tea.MouseButton{tea.MouseWheelUp, tea.MouseWheelDown, tea.MouseWheelUp} {
		updated, _ := m.Update(testMouseWheel(button, 0, 0))
		m = updated.(model)
		if m.chatLayoutGen != generation {
			t.Fatalf("wheel changed chatLayoutGen from %d to %d", generation, m.chatLayoutGen)
		}
		if !m.chatViewport.valid || m.chatViewport.generation != generation {
			t.Fatalf("wheel invalidated the viewport cache: valid=%v generation=%d want=%d", m.chatViewport.valid, m.chatViewport.generation, generation)
		}
	}
}

func TestMouseWheelChangesRenderedTranscriptOffset(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width = 90
	m.height = 14
	m.headerPrinted = true
	for index := 0; index < 80; index++ {
		m.transcript = appendRow(m.transcript, rowAssistant, fmt.Sprintf("transcript row %02d", index))
	}
	before := viewString(m.View())

	updated, _ := m.Update(testMouseWheel(tea.MouseWheelUp, 0, 0))
	m = updated.(model)
	after := viewString(m.View())
	if m.chatScrollOffset == 0 {
		t.Fatal("wheel-up did not move the chat scroll offset")
	}
	if before == after {
		t.Fatal("wheel-up did not change the rendered transcript")
	}
	var newlyVisible string
	for index := 0; index < 80; index++ {
		marker := fmt.Sprintf("transcript row %02d", index)
		if !strings.Contains(before, marker) && strings.Contains(after, marker) {
			newlyVisible = marker
			break
		}
	}
	if newlyVisible == "" {
		t.Fatalf("wheel-up changed the view without revealing an older row:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestNonWheelMessagesBumpChatLayoutGeneration(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(*model) tea.Msg
		wantFlush bool
	}{
		{
			name: "append transcript row",
			prepare: func(m *model) tea.Msg {
				m.activeRunID = 1
				return agentRowMsg{runID: 1, row: transcriptRow{kind: rowAssistant, text: "new row"}}
			},
		},
		{
			name: "width resize",
			prepare: func(m *model) tea.Msg {
				return tea.WindowSizeMsg{Width: 100, Height: m.height}
			},
		},
		{
			name: "height-only resize",
			prepare: func(m *model) tea.Msg {
				return tea.WindowSizeMsg{Width: m.width, Height: m.height + 4}
			},
		},
		{
			name: "toggle detailed mode",
			prepare: func(*model) tea.Msg {
				return testKeyCtrl('o')
			},
		},
		{
			name: "flush advances frontier",
			prepare: func(m *model) tea.Msg {
				m.transcript = appendRow(m.transcript, rowAssistant, "unflushed row")
				m.flushed = 0
				m.flushedAny = false
				return composerBlinkMsg{}
			},
			wantFlush: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := newModel(context.Background(), Options{AltScreen: true})
			m.width = 90
			m.height = 14
			m.headerPrinted = true
			for index := 0; index < 20; index++ {
				m.transcript = appendRow(m.transcript, rowAssistant, "message "+string(rune('A'+index%26)))
			}
			if _, ok := m.chatTranscriptViewport(); !ok {
				t.Fatal("expected a chat viewport before the message")
			}
			generation := m.chatLayoutGen
			flushed := m.flushed

			updated, _ := m.Update(test.prepare(&m))
			next := updated.(model)
			if next.chatLayoutGen != generation+1 {
				t.Fatalf("chatLayoutGen = %d, want %d", next.chatLayoutGen, generation+1)
			}
			if test.wantFlush && next.flushed <= flushed {
				t.Fatalf("flushed = %d, want it to advance past %d", next.flushed, flushed)
			}
		})
	}
}

func TestScrollChatClampsOffsetAtTranscriptTop(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width = 90
	m.height = 14
	for index := 0; index < 40; index++ {
		m.transcript = appendRow(m.transcript, rowAssistant, "message "+string(rune('A'+index%26)))
	}
	_, maxOffset := m.chatScrollMetrics()
	if maxOffset <= chatWheelScrollLines {
		t.Fatalf("test transcript should be scrollable, maxOffset=%d", maxOffset)
	}

	m, _ = m.scrollChat(maxOffset + 100)
	if m.chatScrollOffset != maxOffset {
		t.Fatalf("scroll beyond top offset = %d, want %d", m.chatScrollOffset, maxOffset)
	}

	m.chatScrollOffset = maxOffset + 100 // Simulate an offset saved before clamping existed.
	m, _ = m.scrollChat(-chatWheelScrollLines)
	if want := maxOffset - chatWheelScrollLines; m.chatScrollOffset != want {
		t.Fatalf("scroll down from inflated offset = %d, want %d", m.chatScrollOffset, want)
	}
}

func TestScrollChatDoesNotAccumulateWhenTranscriptFits(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width = 90
	m.height = 20
	m.transcript = appendRow(m.transcript, rowAssistant, "short")

	m, _ = m.scrollChat(100)
	if m.chatScrollOffset != 0 {
		t.Fatalf("non-scrollable transcript offset = %d, want 0", m.chatScrollOffset)
	}
}

func TestMouseWheelOverWrappedComposerMovesComposerCursor(t *testing.T) {
	text := "Create a book library dashboard page with cards, filters, charts, and responsive behavior."
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width = 44
	m.height = 20
	m.mouseCapture = true
	m.input.SetValue(text)
	m.input.CursorEnd()
	startCursor := len([]rune(text))

	updated, cmd := m.Update(testMouseWheel(tea.MouseWheelUp, 0, 14))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("mouse wheel over composer should not return a command")
	}
	if next.chatScrollOffset != 0 {
		t.Fatalf("chatScrollOffset = %d, want unchanged", next.chatScrollOffset)
	}
	if got := next.currentComposerState().cursor; got >= startCursor {
		t.Fatalf("composer cursor = %d, want moved before end cursor %d", got, startCursor)
	}
}

func TestMouseWheelOnClippedFooterStatusDoesNotMoveComposerCursor(t *testing.T) {
	text := "Create a book library dashboard page with cards, filters, charts, and responsive behavior."
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width = 44
	m.height = 3
	m.mouseCapture = true
	m.input.SetValue(text)
	m.input.CursorEnd()
	startCursor := len([]rune(text))

	updated, cmd := m.Update(testMouseWheel(tea.MouseWheelUp, 0, m.height-1))
	next := updated.(model)
	// The wheel falls through to chat scroll here (the clipped footer is not
	// over the composer), which moves the scroll offset, so a clear-screen
	// command is legitimately returned. Assert it is the clear-screen message
	// rather than no command: the point of this test is that the composer
	// cursor must not move.
	if cmd == nil {
		t.Fatal("wheel that moves the scroll offset should return a clear-screen command")
	}
	if got := cmd(); got != tea.ClearScreen() {
		t.Fatalf("wheel command yielded %#v, want clear-screen message %#v", got, tea.ClearScreen())
	}
	if got := next.currentComposerState().cursor; got != startCursor {
		t.Fatalf("composer cursor = %d, want unchanged end cursor %d", got, startCursor)
	}
}

func TestAltScreenTranscriptScrollKeepsFooterFixed(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true, ProviderName: "openai", ModelName: "gpt-4.1"})
	m.width = 90
	m.height = 10
	m.gitBranch = "feat/pinned-header"
	for index := 0; index < 14; index++ {
		m.transcript = appendRow(m.transcript, rowAssistant, "message "+string(rune('A'+index)))
	}

	bottom := plainRender(t, m.View())
	if strings.Contains(bottom, "message A") {
		t.Fatalf("bottom view should start near recent history, got:\n%s", bottom)
	}
	if !strings.Contains(bottom, "describe a task for splice") || !strings.Contains(bottom, "openai") {
		t.Fatalf("bottom view should keep composer/status fixed, got:\n%s", bottom)
	}
	if !strings.Contains(bottom, "feat/pinned-header") || !strings.Contains(bottom, "gpt-4.1") {
		t.Fatalf("bottom view should keep title bar fixed, got:\n%s", bottom)
	}

	m, _ = m.scrollChat(80)
	scrolled := plainRender(t, m.View())
	if !strings.Contains(scrolled, "message A") {
		t.Fatalf("scrolled view should reveal older history, got:\n%s", scrolled)
	}
	if !strings.Contains(scrolled, "describe a task for splice") || !strings.Contains(scrolled, "openai") {
		t.Fatalf("scrolled view should keep composer/status fixed, got:\n%s", scrolled)
	}
	if !strings.Contains(scrolled, "feat/pinned-header") || !strings.Contains(scrolled, "gpt-4.1") {
		t.Fatalf("scrolled view should keep title bar fixed, got:\n%s", scrolled)
	}
}

func TestAltScreenTranscriptClampsFooterToTerminalHeight(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true, ProviderName: "openai", ModelName: "gpt-4.1"})
	m.width = 80
	m.height = 3
	m.copyStatus = "Copied!"
	m.transcript = appendRow(m.transcript, rowAssistant, "hello")

	view := plainRender(t, m.View())
	if got := len(viewLines(view)); got > m.height {
		t.Fatalf("view rendered %d lines, want at most terminal height %d:\n%s", got, m.height, view)
	}
}

func TestEmptySubmitKeepsChatScrollOffset(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width = 90
	m.height = 14
	for index := 0; index < 12; index++ {
		m.transcript = appendRow(m.transcript, rowAssistant, "message "+string(rune('A'+index)))
	}

	// Scroll up, then press Enter on an empty composer: the no-op submit must not
	// yank the viewport back to the bottom.
	m.chatScrollOffset = 7
	m.input.SetValue("")
	updated, _ := m.handleSubmit()
	m = updated.(model)
	if m.chatScrollOffset != 7 {
		t.Fatalf("empty submit changed chatScrollOffset to %d, want it left at 7", m.chatScrollOffset)
	}

	// A real submission (here a slash command) still snaps back to the bottom.
	m.chatScrollOffset = 7
	m.input.SetValue("/help")
	updated, _ = m.handleSubmit()
	m = updated.(model)
	if m.chatScrollOffset != 0 {
		t.Fatalf("real submit chatScrollOffset = %d, want 0", m.chatScrollOffset)
	}
}

func TestPageKeysScrollAltScreenTranscript(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width = 90
	m.height = 20
	for index := 0; index < 30; index++ {
		m.transcript = appendRow(m.transcript, rowAssistant, "message "+string(rune('A'+index%26)))
	}

	updated, _ := m.Update(testKey(tea.KeyPgUp))
	m = updated.(model)
	if m.chatScrollOffset != m.chatPageScrollLines() {
		t.Fatalf("page up offset = %d, want %d", m.chatScrollOffset, m.chatPageScrollLines())
	}

	updated, _ = m.Update(testKey(tea.KeyPgDown))
	m = updated.(model)
	if m.chatScrollOffset != 0 {
		t.Fatalf("page down should return to bottom, got offset %d", m.chatScrollOffset)
	}
}

func TestScrollChatEmitsClearScreenOnlyWhenOffsetChanges(t *testing.T) {
	// Keyboard path via the shared scrollChat seam. A scroll that actually
	// moves the offset returns the clear-screen message; a no-op scroll (already
	// at the top, or a zero delta) returns no command.
	scrollable := newModel(context.Background(), Options{AltScreen: true})
	scrollable.width = 90
	scrollable.height = 14
	for index := 0; index < 60; index++ {
		scrollable.transcript = appendRow(scrollable.transcript, rowAssistant, "message "+string(rune('A'+index%26)))
	}

	m, cmd := scrollable.scrollChat(5)
	if cmd == nil {
		t.Fatal("scroll that changes the offset should return a clear-screen command")
	}
	if got := cmd(); got != tea.ClearScreen() {
		t.Fatalf("keyboard scroll cmd yielded %#v, want clear-screen message %#v", got, tea.ClearScreen())
	}
	if m.chatScrollOffset == 0 {
		t.Fatal("keyboard scroll should move the offset")
	}

	// A second scroll that can no longer move the offset (reach the top) must
	// return no command. Offset counts lines below the fold, so ascent ends at
	// the transcript top.
	_, maxOffset := m.chatScrollMetrics()
	m, _ = m.scrollChat(maxOffset) // clamp at the top
	if m.chatScrollOffset == 0 {
		t.Fatal("test model should be scrolled to the transcript top")
	}
	if _, cmd = m.scrollChat(5); cmd != nil {
		t.Fatalf("scroll that does not change the offset should return no command, got %v", cmd)
	}

	// A zero delta never scrolls, so it must not return a command.
	if _, cmd = scrollable.scrollChat(0); cmd != nil {
		t.Fatalf("zero-delta scroll should return no command, got %v", cmd)
	}

	// Mouse path shares the same seam through scrollChatExtendingSelection.
	mouseModel := newModel(context.Background(), Options{AltScreen: true})
	mouseModel.width = 90
	mouseModel.height = 14
	for index := 0; index < 60; index++ {
		mouseModel.transcript = appendRow(mouseModel.transcript, rowAssistant, "message "+string(rune('A'+index%26)))
	}
	mouseMsg := testMouseWheel(tea.MouseWheelUp, 0, 0)
	next, cmd := mouseModel.scrollChatExtendingSelection(chatWheelScrollLines, mouseMsg)
	if cmd == nil {
		t.Fatal("mouse scroll that changes the offset should return a clear-screen command")
	}
	if got := cmd(); got != tea.ClearScreen() {
		t.Fatalf("mouse scroll cmd yielded %#v, want clear-screen message %#v", got, tea.ClearScreen())
	}
	if next.chatScrollOffset == 0 {
		t.Fatal("mouse scroll should move the offset")
	}
}
