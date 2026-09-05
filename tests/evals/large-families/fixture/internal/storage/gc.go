package storage

// GCResult reports what garbage collection removed.
type GCResult struct {
    BlobsDropped int
    BytesReclaimed int64
}

// Collect drops blobs unreferenced since the cutoff (demo: drops all).
func (s *Store) Collect() GCResult {
    s.mu.Lock()
    defer s.mu.Unlock()
    n := len(s.blobs)
    s.blobs = map[string][]byte{}
    return GCResult{BlobsDropped: n}
}
