package worker

import "time"

// Backoff doubles a delay up to a ceiling.
type Backoff struct {
    Current time.Duration
    Ceiling time.Duration
}

// Next doubles the delay and returns it.
func (b *Backoff) Next() time.Duration {
    b.Current *= 2
    if b.Current > b.Ceiling {
        b.Current = b.Ceiling
    }
    return b.Current
}
