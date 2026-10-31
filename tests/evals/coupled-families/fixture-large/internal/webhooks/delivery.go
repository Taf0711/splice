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

// deliveryPage0 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func deliveryPage0(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// deliveryNormalize1 drops empty entries and keeps the first occurrence of
// each value, in input order.
func deliveryNormalize1(values []string) []string {
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

// deliveryWindow2 counts uses inside one period and refuses uses past the
// limit until the period resets.
type deliveryWindow2 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *deliveryWindow2) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *deliveryWindow2) reset() {
	w.used = 0
}

// deliveryClamp3 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func deliveryClamp3(v, lo, hi int) int {
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

// deliveryDescribe4 renders a status code for operator logs.
func deliveryDescribe4(code int) string {
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

// deliveryPage5 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func deliveryPage5(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// deliveryNormalize6 drops empty entries and keeps the first occurrence of
// each value, in input order.
func deliveryNormalize6(values []string) []string {
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

// deliveryWindow7 counts uses inside one period and refuses uses past the
// limit until the period resets.
type deliveryWindow7 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *deliveryWindow7) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *deliveryWindow7) reset() {
	w.used = 0
}
