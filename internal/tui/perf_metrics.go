package tui

import (
	"fmt"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
)

// perfMetrics records how long View() and Update() take, so /debug can show
// real frame costs instead of guesses. Bubble Tea's eventLoop calls both from
// a single goroutine (verified against bubbletea v2.0.7), so the recorder
// needs no lock. Recording is always on: one time.Now plus one slot write per
// call costs nanoseconds against a millisecond frame budget, and jank is
// intermittent, so a flag-gated recorder would miss the frames that matter.
const perfRingSize = 512

// durationRing is a fixed-capacity ring of frame durations. count and total
// span the whole session; the buf retains only the last perfRingSize samples,
// so percentiles describe the recent window, not all history.
type durationRing struct {
	buf   []time.Duration
	idx   int
	count uint64
	total time.Duration
}

func newDurationRing() *durationRing {
	return &durationRing{buf: make([]time.Duration, perfRingSize)}
}

func (r *durationRing) record(d time.Duration) {
	r.buf[r.idx] = d
	r.idx = (r.idx + 1) % perfRingSize
	r.count++
	r.total += d
}

// summary returns count, mean, and nearest-rank p50/p95/max over the retained
// window. Called only when /debug renders, so the sort cost is fine.
func (r *durationRing) summary() (count uint64, mean, p50, p95, max time.Duration) {
	if r.count == 0 {
		return 0, 0, 0, 0, 0
	}
	mean = r.total / time.Duration(r.count)
	p50, p95, max = percentiles(r.buf, r.count)
	return r.count, mean, p50, p95, max
}

// frameTagStats holds the per-trigger view/update rings. The trigger is the
// message kind that started the frame (see tagForMsg), so /debug can answer
// "do scroll frames spike?" without conflating them with streaming frames.
type frameTagStats struct {
	views   *durationRing
	updates *durationRing
}

type perfMetrics struct {
	views   *durationRing
	updates *durationRing
	byTag   map[string]*frameTagStats
	// lastTag is set at the top of Update and read by View's deferred record,
	// so a frame's view cost is attributed to the message that triggered it.
	// Safe without a lock: eventLoop runs Update then View on one goroutine.
	lastTag string
}

var perf = newPerfMetrics()

func newPerfMetrics() *perfMetrics {
	return &perfMetrics{
		views:   newDurationRing(),
		updates: newDurationRing(),
		byTag:   make(map[string]*frameTagStats),
		lastTag: "init",
	}
}

func (p *perfMetrics) tagStats(tag string) *frameTagStats {
	s := p.byTag[tag]
	if s == nil {
		s = &frameTagStats{views: newDurationRing(), updates: newDurationRing()}
		p.byTag[tag] = s
	}
	return s
}

func (p *perfMetrics) recordView(d time.Duration) {
	p.views.record(d)
	p.tagStats(p.lastTag).views.record(d)
}

func (p *perfMetrics) recordUpdate(d time.Duration) {
	p.updates.record(d)
	p.tagStats(p.lastTag).updates.record(d)
}

// tagForMsg classifies a message into a small fixed set so per-trigger frame
// costs stay readable in /debug. Scroll vs streaming is the split that matters
// for PX3's gate; everything else collapses to "other".
func tagForMsg(msg tea.Msg) string {
	switch msg.(type) {
	case tea.MouseWheelMsg:
		return "mouse_wheel"
	case tea.MouseMotionMsg:
		return "mouse_motion"
	case dragEdgeScrollTickMsg:
		return "edge_scroll"
	case tea.KeyMsg:
		return "key"
	case tea.WindowSizeMsg:
		return "window"
	case agentTextMsg:
		return "agent_text"
	case agentReasoningMsg:
		return "agent_reasoning"
	case toolCallStreamStartMsg, toolCallStreamDeltaMsg:
		return "tool_stream"
	default:
		return "other"
	}
}

// perfSummary is a point-in-time read of the recorder. Percentiles use
// nearest-rank over the retained ring, so they describe the last
// perfRingSize calls, not the whole session.
type perfSummary struct {
	ViewCount   uint64
	UpdateCount uint64
	ViewMean    time.Duration
	UpdateMean  time.Duration
	ViewP50     time.Duration
	ViewP95     time.Duration
	ViewMax     time.Duration
	UpdateP50   time.Duration
	UpdateP95   time.Duration
	UpdateMax   time.Duration
	// ByTag is sorted worst-view-p95-first so the trigger responsible for the
	// slowest frames is at the top of the /debug table.
	ByTag []tagStat
}

type tagStat struct {
	Tag         string
	ViewCount   uint64
	UpdateCount uint64
	ViewP95     time.Duration
	ViewMax     time.Duration
	UpdateP95   time.Duration
	UpdateMax   time.Duration
}

func (p *perfMetrics) summary() perfSummary {
	s := perfSummary{}
	vc, vmean, vp50, vp95, vmax := p.views.summary()
	s.ViewCount, s.ViewMean, s.ViewP50, s.ViewP95, s.ViewMax = vc, vmean, vp50, vp95, vmax
	uc, umean, up50, up95, umax := p.updates.summary()
	s.UpdateCount, s.UpdateMean, s.UpdateP50, s.UpdateP95, s.UpdateMax = uc, umean, up50, up95, umax
	for tag, st := range p.byTag {
		vc, _, _, vp95, vmax := st.views.summary()
		uc, _, _, up95, umax := st.updates.summary()
		s.ByTag = append(s.ByTag, tagStat{
			Tag: tag, ViewCount: vc, UpdateCount: uc,
			ViewP95: vp95, ViewMax: vmax, UpdateP95: up95, UpdateMax: umax,
		})
	}
	sort.Slice(s.ByTag, func(i, j int) bool { return s.ByTag[i].ViewP95 > s.ByTag[j].ViewP95 })
	return s
}

// percentiles copies the retained samples, sorts, and picks nearest-rank
// p50/p95/max. Called only when /debug renders, so the sort cost is fine.
func percentiles(ring []time.Duration, count uint64) (p50, p95, max time.Duration) {
	n := int(count)
	if n > len(ring) {
		n = len(ring)
	}
	if n == 0 {
		return 0, 0, 0
	}
	sorted := make([]time.Duration, n)
	copy(sorted, ring[:n])
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[(n-1)/2], sorted[(n*95+99)/100-1], sorted[n-1]
}

// formatDuration renders a duration compactly for the /debug frame lines.
func formatDuration(d time.Duration) string {
	if d >= time.Millisecond {
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	}
	return fmt.Sprintf("%dµs", d.Microseconds())
}
