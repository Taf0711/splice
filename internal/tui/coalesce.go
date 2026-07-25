package tui

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// streamCoalesceInterval is roughly one 60fps frame. Assistant-text deltas that
// arrive within this window are merged into a single agentTextMsg, so the render
// rate decouples from the token rate: a fast local model (100+ tok/s) no longer
// forces 100+ full Update→View cycles (each re-parsing the growing markdown) per
// second. Rendering stays smooth regardless of provider speed.
const streamCoalesceInterval = 16 * time.Millisecond

// textCoalescer batches agentTextMsg and agentReasoningMsg deltas before
// forwarding them to the Bubble Tea program. Any OTHER message flushes the
// pending buffer first, so ordering between streamed prose / reasoning and
// tool-call / row / usage messages is preserved. The turn's final
// agentResponseMsg does not pass through here (it is a tea.Cmd return, not a
// sink message), but the model drops deltas whose runID is no longer active,
// so a flush that races just past end-of-turn is harmless.
//
// Sink messages originate from the single agent goroutine and so arrive
// serially; the only concurrent caller is the flush timer. The mutex guards the
// buffer/timer AND is held across the downstream forward, so a timer-fired flush
// can never overtake a concurrent non-text message: whoever holds the lock
// drains and forwards atomically, and the other caller blocks until it is done.
type textCoalescer struct {
	forward func(tea.Msg) // downstream sink (external sink + program.Send)
	// afterFunc schedules fn to run after one frame interval and returns a
	// stoppable timer. Defaults to a real time.AfterFunc(streamCoalesceInterval, …);
	// tests swap in a controllable timer so flush timing is deterministic instead of
	// racing the 16ms wall clock.
	afterFunc func(fn func()) coalesceTimer

	mu    sync.Mutex
	buf   []byte
	runID int
	kind  byte // 0=empty, 1=text, 2=reasoning
	timer coalesceTimer
}

// coalesceTimer is the subset of *time.Timer the coalescer needs. Abstracted
// behind afterFunc so a test can substitute a timer it controls.
type coalesceTimer interface {
	Stop() bool
}

func newTextCoalescer(forward func(tea.Msg)) *textCoalescer {
	return &textCoalescer{
		forward: forward,
		afterFunc: func(fn func()) coalesceTimer {
			return time.AfterFunc(streamCoalesceInterval, fn)
		},
	}
}

const (
	coalesceEmpty     byte = 0
	coalesceText      byte = 1
	coalesceReasoning byte = 2
)

// send is the coalescing entry point installed as the RuntimeMessageSink.
func (c *textCoalescer) send(msg tea.Msg) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var delta string
	var msgRunID int
	var msgKind byte

	switch msg := msg.(type) {
	case agentTextMsg:
		delta = msg.delta
		msgRunID = msg.runID
		msgKind = coalesceText
	case agentReasoningMsg:
		delta = msg.delta
		msgRunID = msg.runID
		msgKind = coalesceReasoning
	default:
		// Non-stream message: flush whatever is buffered first (preserving order),
		// then forward it — both under the lock so nothing can interleave.
		c.drainAndForwardLocked()
		c.forward(msg)
		return
	}

	// A delta whose kind or runID differs from what is buffered flushes the old
	// buffer first, preserving arrival order across kind/run switches.
	if len(c.buf) > 0 && (c.kind != msgKind || msgRunID != c.runID) {
		c.drainAndForwardLocked()
	}
	c.kind = msgKind
	c.runID = msgRunID
	c.buf = append(c.buf, delta...)
	if c.timer == nil {
		c.timer = c.afterFunc(c.flush)
	}
}

// flush forwards any buffered text as one agentTextMsg. Runs on the timer
// goroutine; the lock it takes serializes it against send so its output can't be
// reordered around a concurrent non-text message.
func (c *textCoalescer) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drainAndForwardLocked()
}

// drainAndForwardLocked forwards any buffered delta as one agentTextMsg or
// agentReasoningMsg (depending on the current kind) and stops the timer, all
// while the caller holds c.mu — so a flush and any non-stream forward are
// strictly ordered and never interleave. A no-op when nothing is buffered.
// string(c.buf) copies, so reusing the backing array via [:0] is safe.
func (c *textCoalescer) drainAndForwardLocked() {
	if len(c.buf) == 0 {
		return
	}
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	delta := string(c.buf)
	kind := c.kind
	c.buf = c.buf[:0]
	c.kind = coalesceEmpty
	switch kind {
	case coalesceText:
		c.forward(agentTextMsg{runID: c.runID, delta: delta})
	case coalesceReasoning:
		c.forward(agentReasoningMsg{runID: c.runID, delta: delta})
	}
}
