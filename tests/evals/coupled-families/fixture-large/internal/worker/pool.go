// Package worker runs background jobs from an in-memory queue.
package worker

import "sync"

// Job is one unit of background work.
type Job struct {
	Name string
	Run  func() error
}

// Pool executes jobs serially with retry accounting.
type Pool struct {
	mu        sync.Mutex
	completed int
	failed    int
}

// Submit runs one job immediately and records the outcome.
func (p *Pool) Submit(j Job) {
	if err := j.Run(); err != nil {
		p.mu.Lock()
		p.failed++
		p.mu.Unlock()
		return
	}
	p.mu.Lock()
	p.completed++
	p.mu.Unlock()
}

// Stats reports completed and failed job counts.
func (p *Pool) Stats() (completed, failed int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.completed, p.failed
}

// poolNormalize0 drops empty entries and keeps the first occurrence of
// each value, in input order.
func poolNormalize0(values []string) []string {
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

// poolWindow1 counts uses inside one period and refuses uses past the
// limit until the period resets.
type poolWindow1 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *poolWindow1) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *poolWindow1) reset() {
	w.used = 0
}

// poolClamp2 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func poolClamp2(v, lo, hi int) int {
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

// poolDescribe3 renders a status code for operator logs.
func poolDescribe3(code int) string {
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

// poolPage4 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func poolPage4(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
