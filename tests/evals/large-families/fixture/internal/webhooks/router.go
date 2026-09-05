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
    mu    sync.RWMutex
    byID  map[string]Endpoint
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
