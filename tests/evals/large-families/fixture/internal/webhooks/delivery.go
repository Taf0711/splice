package webhooks

import "sync"

// Attempt records one delivery try.
type Attempt struct {
    EndpointID string
    Succeeded  bool
}

// Log keeps delivery attempts.
type Log struct {
    mu       sync.Mutex
    attempts []Attempt
}

// Record appends one attempt.
func (l *Log) Record(a Attempt) {
    l.mu.Lock()
    l.attempts = append(l.attempts, a)
}

// Successes counts successful deliveries.
func (l *Log) Successes() int {
    l.mu.Lock()
    defer l.mu.Unlock()
    n := 0
    for _, a := range l.attempts {
        if a.Succeeded {
            n++
        }
    }
    return n
}
