package notifications

import "sync"

// Throttle limits notifications per recipient.
type Throttle struct {
    mu    sync.Mutex
    sent  map[string]int
    limit int
}

// NewThrottle builds a per-recipient limiter.
func NewThrottle(limit int) *Throttle {
    return &Throttle{sent: map[string]int{}, limit: limit}
}

// Allow reports whether one more notification may go out.
func (t *Throttle) Allow(to string) bool {
    t.mu.Lock()
    defer t.mu.Unlock()
    if t.sent[to] >= t.limit {
        return false
    }
    t.sent[to]++
    return true
}
