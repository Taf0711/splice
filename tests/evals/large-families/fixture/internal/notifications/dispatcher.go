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
