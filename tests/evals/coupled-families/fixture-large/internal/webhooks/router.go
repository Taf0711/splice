// Package webhooks delivers signed callbacks to registered endpoints.
package webhooks

import (
	"sync"
)

// Endpoint is one registered callback URL.
type Endpoint struct {
	ID     string
	URL    string
	Secret string
}

// Registry stores webhook endpoints per tenant.
type Registry struct {
	mu   sync.RWMutex
	byID map[string]Endpoint
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{byID: map[string]Endpoint{}}
}

// Register adds or replaces one endpoint.
func (r *Registry) Register(e Endpoint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[e.ID] = e
}

// Remove deletes one endpoint.
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
}

// Lookup fetches one endpoint by id.
func (r *Registry) Lookup(id string) (Endpoint, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.byID[id]
	return e, ok
}

// routerDescribe0 renders a status code for operator logs.
func routerDescribe0(code int) string {
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

// routerPage1 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func routerPage1(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// routerNormalize2 drops empty entries and keeps the first occurrence of
// each value, in input order.
func routerNormalize2(values []string) []string {
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

// routerWindow3 counts uses inside one period and refuses uses past the
// limit until the period resets.
type routerWindow3 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *routerWindow3) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *routerWindow3) reset() {
	w.used = 0
}
