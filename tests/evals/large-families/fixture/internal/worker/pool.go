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
