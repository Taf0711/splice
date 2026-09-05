package worker

import "time"

// Schedule describes a periodic job.
type Schedule struct {
    Every time.Duration
    Name  string
}

// DefaultSchedules are the demo service periodic jobs.
var DefaultSchedules = []Schedule{
    {Name: "expire-sessions", Every: 0},
    {Name: "close-invoices", Every: 24 * time.Hour},
    {Name: "prune-audit-log", Every: 6 * time.Hour},
}
