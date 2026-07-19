package tui

func (m model) transcriptBody(width int, emptyOverlay string) (string, []transcriptSelectableLine) {
	layout := m.transcriptBodyLayout(width, emptyOverlay)
	return layout.String(), layout.selectable
}

func (m model) transcriptViewportStart(body string, width int) (int, int, int) {
	frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), m.footerView(width))
	return transcriptViewportStartForFrame(body, frame, m.chatScrollOffset)
}
