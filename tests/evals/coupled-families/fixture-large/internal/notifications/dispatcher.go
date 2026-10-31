// Package notifications fans out in-app and email notifications.
package notifications

import "fmt"

// Kind enumerates the notification channels.
type Kind string

const (
	KindInApp Kind = "in_app"
	KindEmail Kind = "email"
)

// Message is one outgoing notification.
type Message struct {
	Kind    Kind
	To      string
	Subject string
	Body    string
}

// Dispatcher queues messages for delivery.
type Dispatcher struct {
	queued []Message
	failed int
}

// Queue appends one message.
func (d *Dispatcher) Queue(m Message) {
	if m.To == "" {
		d.failed++
		return
	}
	d.queued = append(d.queued, m)
}

// Pending reports how many messages await delivery.
func (d *Dispatcher) Pending() int { return len(d.queued) }

// Failures reports how many messages could not be queued.
func (d *Dispatcher) Failures() int { return d.failed }

// Flush drains the queue and formats each message for the transport.
func (d *Dispatcher) Flush() []string {
	out := make([]string, 0, len(d.queued))
	for _, m := range d.queued {
		out = append(out, fmt.Sprintf("%s|%s|%s", m.Kind, m.To, m.Subject))
	}
	d.queued = nil
	return out
}

// NewDispatcher builds an empty dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{}
}

// dispatcherPage0 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func dispatcherPage0(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// dispatcherNormalize1 drops empty entries and keeps the first occurrence of
// each value, in input order.
func dispatcherNormalize1(values []string) []string {
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

// dispatcherWindow2 counts uses inside one period and refuses uses past the
// limit until the period resets.
type dispatcherWindow2 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *dispatcherWindow2) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *dispatcherWindow2) reset() {
	w.used = 0
}

// dispatcherClamp3 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func dispatcherClamp3(v, lo, hi int) int {
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

// dispatcherDescribe4 renders a status code for operator logs.
func dispatcherDescribe4(code int) string {
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

// dispatcherPage5 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func dispatcherPage5(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// dispatcherNormalize6 drops empty entries and keeps the first occurrence of
// each value, in input order.
func dispatcherNormalize6(values []string) []string {
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

// dispatcherWindow7 counts uses inside one period and refuses uses past the
// limit until the period resets.
type dispatcherWindow7 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *dispatcherWindow7) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *dispatcherWindow7) reset() {
	w.used = 0
}

// dispatcherClamp8 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func dispatcherClamp8(v, lo, hi int) int {
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
