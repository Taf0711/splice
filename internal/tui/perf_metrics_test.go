package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestPerfSummaryPercentiles(t *testing.T) {
	p := newPerfMetrics()
	for i := 1; i <= 100; i++ {
		p.recordView(time.Duration(i) * time.Millisecond)
	}
	s := p.summary()
	if s.ViewCount != 100 {
		t.Fatalf("ViewCount = %d, want 100", s.ViewCount)
	}
	if s.ViewP50 != 50*time.Millisecond {
		t.Fatalf("ViewP50 = %v, want 50ms", s.ViewP50)
	}
	if s.ViewP95 != 95*time.Millisecond {
		t.Fatalf("ViewP95 = %v, want 95ms", s.ViewP95)
	}
	if s.ViewMax != 100*time.Millisecond {
		t.Fatalf("ViewMax = %v, want 100ms", s.ViewMax)
	}
	if s.ViewMean != 50500*time.Microsecond {
		t.Fatalf("ViewMean = %v, want 50.5ms", s.ViewMean)
	}
}

func TestPerfRingWraps(t *testing.T) {
	p := newPerfMetrics()
	for i := 0; i < perfRingSize*2; i++ {
		p.recordUpdate(time.Duration(i) * time.Microsecond)
	}
	s := p.summary()
	if s.UpdateCount != uint64(perfRingSize*2) {
		t.Fatalf("UpdateCount = %d, want %d", s.UpdateCount, perfRingSize*2)
	}
	// The ring retains only the last perfRingSize samples, so the max is the
	// most recent value and percentiles cover the second half only.
	want := time.Duration(perfRingSize*2-1) * time.Microsecond
	if s.UpdateMax != want {
		t.Fatalf("UpdateMax = %v, want %v", s.UpdateMax, want)
	}
}

func TestDebugTextIncludesPerfSections(t *testing.T) {
	m := limeTestModel()
	got := m.debugText()
	for _, want := range []string{"Frames", "view: p50", "update: p50", "Render cache", "hit rate", "Transcript", "rows:", "alt screen:", "Frames by trigger"} {
		if !strings.Contains(got, want) {
			t.Fatalf("debugText missing %q, got:\n%s", want, got)
		}
	}
}

// TestPerfByTriggerTagsFrames proves the gate PX3 needs is readable: a scroll
// trigger and a streaming trigger land in separate tag buckets with separate
// p95/max, so /debug can show whether scroll frames spike relative to streaming.
func TestPerfByTriggerTagsFrames(t *testing.T) {
	p := newPerfMetrics()
	// Simulate 20 cheap streaming frames then 5 expensive scroll frames.
	p.lastTag = "agent_reasoning"
	for i := 0; i < 20; i++ {
		p.recordView(2 * time.Millisecond)
		p.recordUpdate(500 * time.Microsecond)
	}
	p.lastTag = "mouse_wheel"
	for i := 0; i < 5; i++ {
		p.recordView(18 * time.Millisecond)
		p.recordUpdate(1 * time.Millisecond)
	}
	s := p.summary()
	if len(s.ByTag) != 2 {
		t.Fatalf("ByTag len = %d, want 2", len(s.ByTag))
	}
	// Worst view-p95 first, so mouse_wheel leads.
	if s.ByTag[0].Tag != "mouse_wheel" {
		t.Fatalf("ByTag[0] = %q, want mouse_wheel", s.ByTag[0].Tag)
	}
	if s.ByTag[0].ViewMax != 18*time.Millisecond {
		t.Fatalf("mouse_wheel ViewMax = %v, want 18ms", s.ByTag[0].ViewMax)
	}
	if s.ByTag[1].Tag != "agent_reasoning" {
		t.Fatalf("ByTag[1] = %q, want agent_reasoning", s.ByTag[1].Tag)
	}
	if s.ByTag[1].ViewMax != 2*time.Millisecond {
		t.Fatalf("agent_reasoning ViewMax = %v, want 2ms", s.ByTag[1].ViewMax)
	}
	// The by-trigger table renders both tags with their view p95.
	lines := frameByTriggerLines(s.ByTag)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "mouse_wheel: view p95") {
		t.Fatalf("by-trigger lines missing mouse_wheel p95:\n%s", joined)
	}
	if !strings.Contains(joined, "agent_reasoning: view p95") {
		t.Fatalf("by-trigger lines missing agent_reasoning p95:\n%s", joined)
	}
}

func TestTagForMsgCoversScrollAndStreaming(t *testing.T) {
	cases := []struct {
		msg  tea.Msg
		want string
	}{
		{tea.MouseWheelMsg{}, "mouse_wheel"},
		{dragEdgeScrollTickMsg{}, "edge_scroll"},
		{agentReasoningMsg{}, "agent_reasoning"},
		{agentTextMsg{}, "agent_text"},
		{nil, "other"},
	}
	for _, c := range cases {
		if got := tagForMsg(c.msg); got != c.want {
			t.Fatalf("tagForMsg(%T) = %q, want %q", c.msg, got, c.want)
		}
	}
}
