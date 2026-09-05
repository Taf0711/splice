// Package audit records an append-only trail of security-relevant
// events for the demo service.
package audit

import (
    "sync"
    "time"
)

// Event is one audit record.
type Event struct {
    At        time.Time
    Actor     string
    Action    string
    Object    string
    Outcome   string
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
