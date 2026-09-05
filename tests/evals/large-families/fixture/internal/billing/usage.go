package billing

import "sync"

// Meter records usage counters per account and SKU.
type Meter struct {
    mu      sync.Mutex
    counts  map[string]map[string]int
}

// NewMeter builds an empty usage meter.
func NewMeter() *Meter {
    return &Meter{counts: map[string]map[string]int{}}
}

// Record bumps one counter.
func (m *Meter) Record(accountID, sku string, n int) {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.counts[accountID] == nil {
        m.counts[accountID] = map[string]int{}
    }
    m.counts[accountID][sku] += n
}

// Snapshot returns one account's counters.
func (m *Meter) Snapshot(accountID string) map[string]int {
    m.mu.Lock()
    defer m.mu.Unlock()
    out := map[string]int{}
    for sku, n := range m.counts[accountID] {
        out[sku] = n
    }
    return out
}
