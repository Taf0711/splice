package tui

import (
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestChatViewportIncludesPersistentPlanHeader(t *testing.T) {
	m := transcriptViewTestModel()
	m.altScreen = true
	m.height = 20
	m.designMode = true
	m.planPanelPersistent = true
	m.pendingPlan = &schemas.DesignPlan{Epic: "persistent plan"}
	width := m.chatColumnWidth()

	viewport, ok := m.chatTranscriptViewport()
	if !ok {
		t.Fatal("expected an alt-screen viewport")
	}
	frame := m.scrollableTranscriptFrame(m.normalTranscriptHeader(width), m.footerView(width))
	if viewport.height != frame.bodyRect.height {
		t.Fatalf("cached viewport height = %d, rendered body height = %d", viewport.height, frame.bodyRect.height)
	}
}

func TestSyncChatScrollPinsWhileScrolledUp(t *testing.T) {
	m := transcriptViewTestModel()
	m.altScreen = true
	m.height = 12
	m.transcript = []transcriptRow{{kind: rowAssistant, text: numberedLines(120)}}
	m.chatScrollOffset = 5
	if _, maxOffset := m.chatScrollMetrics(); maxOffset < m.chatScrollOffset {
		t.Fatalf("test transcript should be scrollable, maxOffset=%d", maxOffset)
	}

	m = m.syncChatScroll() // establishes the baseline, no adjustment yet
	if m.chatBodyLines == 0 {
		t.Fatal("scrolled-up baseline must be recorded")
	}
	baseOffset := m.chatScrollOffset
	baseLines := m.chatBodyLines

	// Stream more content in.
	m.transcript = append(m.transcript, transcriptRow{kind: rowAssistant, text: numberedLines(12)})
	m = m.syncChatScroll()

	grew := m.chatBodyLines - baseLines
	if grew <= 0 {
		t.Fatalf("body should have grown, base=%d now=%d", baseLines, m.chatBodyLines)
	}
	if m.chatScrollOffset != baseOffset+grew {
		t.Fatalf("offset must grow by %d to hold the read position, was %d now %d", grew, baseOffset, m.chatScrollOffset)
	}
}

func TestSyncChatScrollFollowsAtBottom(t *testing.T) {
	m := transcriptViewTestModel()
	m.altScreen = true
	m.transcript = []transcriptRow{{kind: rowAssistant, text: numberedLines(40)}}
	m.chatScrollOffset = 0
	m.chatBodyLines = 99 // stale baseline from a prior scrolled-up state

	m = m.syncChatScroll()
	if m.chatScrollOffset != 0 {
		t.Fatalf("at the bottom the viewport must keep following (offset 0), got %d", m.chatScrollOffset)
	}
	if m.chatBodyLines != 0 {
		t.Fatalf("at the bottom the pin baseline must reset to 0, got %d", m.chatBodyLines)
	}
}
