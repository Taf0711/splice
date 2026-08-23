package tui

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkStreamingInterimFrame measures one streaming frame (interimBlock)
// as accumulated text grows. Cost is O(document) per frame by design (the
// growing markdown re-renders through the stable-prefix render cache); this
// benchmark documents that scaling so a future incremental renderer can be
// validated against it.
func BenchmarkStreamingInterimFrame(b *testing.B) {
	for _, size := range []int{2_000, 10_000, 40_000} {
		var sb strings.Builder
		sb.WriteString("Here is my plan for the refactor:\n\n")
		for sb.Len() < size {
			fmt.Fprintf(&sb, "Step %d: adjust the module boundaries and re-run the suite to confirm nothing drifted.\n\n", sb.Len())
		}
		m := limeTestModel()
		m.pending = true
		m.streamingText = []byte(sb.String())
		m.fadeActive = true
		b.Run(fmt.Sprintf("text_%dB", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				out := m.interimBlock(96)
				if len(out) == 0 {
					b.Fatal("empty frame")
				}
			}
		})
	}
}

// BenchmarkPendingFrameLongTranscript measures a spinner-only frame over a
// long settled transcript in alt-screen mode — the frame the 80ms spinner tick
// drives during any active run. This is the scenario whose per-frame cost
// regressed when the body-height cache thrashed at >512 items.
func BenchmarkPendingFrameLongTranscript(b *testing.B) {
	m := limeTestModel()
	m.width, m.height = 96, 40
	m.altScreen = true
	m.pending = true
	for i := 0; i < 300; i++ {
		m.transcript = append(m.transcript,
			transcriptRow{kind: rowUser, text: fmt.Sprintf("question %d about the service behavior", i)},
			transcriptRow{kind: rowAssistant, text: fmt.Sprintf("answer %d with a fairly long explanation of what changed and why it matters for the rest of the system design", i)},
		)
	}
	m.flushed = len(m.transcript)
	m.flushedAny = true
	m.transcriptBodyHeights = newTranscriptBodyHeightCache(100_000)
	m.rebuildAltScreenSettledItems(96)
	b.ReportAllocs()
	for b.Loop() {
		v := m.transcriptView()
		if len(v) == 0 {
			b.Fatal("empty view")
		}
	}
}
