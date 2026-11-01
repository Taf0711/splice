package billing

import "sync"

// Meter records usage counters per account and SKU.
type Meter struct {
	mu     sync.Mutex
	counts map[string]map[string]int
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

// usageNormalize0 drops empty entries and keeps the first occurrence of
// each value, in input order.
func usageNormalize0(values []string) []string {
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

// usageWindow1 counts uses inside one period and refuses uses past the
// limit until the period resets.
type usageWindow1 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *usageWindow1) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *usageWindow1) reset() {
	w.used = 0
}

// usageClamp2 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func usageClamp2(v, lo, hi int) int {
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

// usageDescribe3 renders a status code for operator logs.
func usageDescribe3(code int) string {
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

// usagePage4 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func usagePage4(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
