package web

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	// flushInterval is the coalescing window for published events.
	flushInterval = 50 * time.Millisecond
	// clientBuffer is how many events a client may hold before it is dropped.
	clientBuffer = 32
)

// Event is one update for the browser. Events that share a Key within one flush
// interval are coalesced, and the latest one wins. Use a unique Key for events
// that must not be merged.
type Event struct {
	Key  string
	Name string // SSE event name; empty means "message"
	Data string
}

type client struct {
	ch chan Event
}

// hub fans events out to SSE clients. Publish never blocks. Each flush sends
// the pending events to every client, and drops a client whose buffer is full.
type hub struct {
	interval time.Duration

	mu      sync.Mutex
	clients map[*client]struct{}
	pending map[string]Event
	order   []string // keys in first-seen order for the current interval
	// epoch counts session changes. closeAll bumps it, and an event published
	// under an older epoch is dropped, so a stale run cannot reach a new session.
	epoch  uint64
	closed bool // set by shutdown; publish becomes a no-op
}

func newHub(interval time.Duration) *hub {
	return &hub{
		interval: interval,
		clients:  map[*client]struct{}{},
		pending:  map[string]Event{},
	}
}

// run flushes on every tick until stop closes.
func (h *hub) run(stop <-chan struct{}) {
	t := time.NewTicker(h.interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			h.flush()
		}
	}
}

func (h *hub) publish(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.addLocked(ev)
}

// publishFrom queues ev only while epoch is still current. A sink captures the
// epoch when it is created, so events from an earlier session are dropped.
func (h *hub) publishFrom(epoch uint64, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if epoch != h.epoch {
		return
	}
	h.addLocked(ev)
}

// addLocked queues ev. The caller holds h.mu.
func (h *hub) addLocked(ev Event) {
	if h.closed {
		return
	}
	if _, seen := h.pending[ev.Key]; !seen {
		h.order = append(h.order, ev.Key)
	}
	h.pending[ev.Key] = ev
}

func (h *hub) currentEpoch() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.epoch
}

func (h *hub) flush() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, key := range h.order {
		ev := h.pending[key]
		for c := range h.clients {
			select {
			case c.ch <- ev:
			default:
				close(c.ch)
				delete(h.clients, c)
			}
		}
	}
	clear(h.pending)
	h.order = h.order[:0]
}

// subscribe adds a client. The returned function removes it and is safe to call
// more than once.
func (h *hub) subscribe() (<-chan Event, func()) {
	c := &client{ch: make(chan Event, clientBuffer)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c.ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.clients[c]; ok {
			delete(h.clients, c)
			close(c.ch)
		}
	}
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// closeAll ends every client stream and drops events still pending. The pending
// events belong to the session that is ending. The epoch moves on, so a sink from
// that session cannot publish again.
func (h *hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dropLocked()
	h.epoch++
}

// shutdown closes the hub for good. Later publishes do nothing.
func (h *hub) shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dropLocked()
	h.closed = true
}

// dropLocked closes every client and clears the pending events. The caller holds h.mu.
func (h *hub) dropLocked() {
	for c := range h.clients {
		close(c.ch)
		delete(h.clients, c)
	}
	clear(h.pending)
	h.order = h.order[:0]
}

// writeEvent writes ev in SSE framing. Newlines in Name are removed, and each
// line of Data gets its own data field, so a publisher cannot inject fields.
func writeEvent(w io.Writer, ev Event) {
	name := strings.NewReplacer("\r", "", "\n", "").Replace(ev.Name)
	if name == "" {
		name = "message"
	}
	fmt.Fprintf(w, "event: %s\n", name)
	data := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(ev.Data)
	for _, line := range strings.Split(data, "\n") {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprint(w, "\n")
}
