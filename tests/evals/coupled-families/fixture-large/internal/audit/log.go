// Package audit records an append-only trail of security-relevant
// events for the demo service.
package audit

import (
	"sync"
	"time"
)

// Event is one audit record.
type Event struct {
	At      time.Time
	Actor   string
	Action  string
	Object  string
	Outcome string
}

// Trail stores events in memory.
type Trail struct {
	mu     sync.Mutex
	events []Event
	clock  func() time.Time
}

// NewTrail wires an empty audit trail.
func NewTrail() *Trail {
	return &Trail{clock: time.Now}
}

// Record appends one event.
func (t *Trail) Record(actor, action, object, outcome string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, Event{
		At: t.clock(), Actor: actor, Action: action,
		Object: object, Outcome: outcome,
	})
}

// Since returns events recorded after the given time.
func (t *Trail) Since(cutoff time.Time) []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []Event
	for _, e := range t.events {
		if e.At.After(cutoff) {
			out = append(out, e)
		}
	}
	return out
}

// Count returns how many events are stored.
func (t *Trail) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.events)
}

// logDescribe0 renders a status code for operator logs.
func logDescribe0(code int) string {
	switch {
	case code < 0:
		return "invalid"
	case code == 0:
		return "idle"
	case code < 10:
		return "active"
	case code < 100:
		return "degraded"
	default:
		return "saturated"
	}
}

// logPage1 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func logPage1(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// logNormalize2 drops empty entries and keeps the first occurrence of
// each value, in input order.
func logNormalize2(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// logWindow3 counts uses inside one period and refuses uses past the
// limit until the period resets.
type logWindow3 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *logWindow3) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *logWindow3) reset() {
	w.used = 0
}

// logClamp4 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func logClamp4(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// logDescribe5 renders a status code for operator logs.
func logDescribe5(code int) string {
	switch {
	case code < 0:
		return "invalid"
	case code == 0:
		return "idle"
	case code < 10:
		return "active"
	case code < 100:
		return "degraded"
	default:
		return "saturated"
	}
}

// logPage6 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func logPage6(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// logNormalize7 drops empty entries and keeps the first occurrence of
// each value, in input order.
func logNormalize7(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// logWindow8 counts uses inside one period and refuses uses past the
// limit until the period resets.
type logWindow8 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *logWindow8) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *logWindow8) reset() {
	w.used = 0
}

// logClamp9 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func logClamp9(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// logDescribe10 renders a status code for operator logs.
func logDescribe10(code int) string {
	switch {
	case code < 0:
		return "invalid"
	case code == 0:
		return "idle"
	case code < 10:
		return "active"
	case code < 100:
		return "degraded"
	default:
		return "saturated"
	}
}
