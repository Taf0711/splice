package audit

import "time"

// RetentionPolicy bounds how long audit events are kept.
type RetentionPolicy struct {
    MaxAge   time.Duration
    MaxCount int
}

// DefaultRetention keeps 30 days of events capped at ten thousand.
var DefaultRetention = RetentionPolicy{MaxAge: 30 * 24 * time.Hour, MaxCount: 10000}

// Apply drops events outside the retention policy and returns how
// many remain.
func Apply(t *Trail, p RetentionPolicy, now time.Time) int {
    kept := t.Since(now.Add(-p.MaxAge))
    if len(kept) > p.MaxCount {
        kept = kept[len(kept)-p.MaxCount:]
    }
    return len(kept)
}
