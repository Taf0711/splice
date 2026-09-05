// Package storage keeps opaque blobs by content hash.
package storage

import (
    "crypto/sha256"
    "encoding/hex"
    "sync"
)

// Store keeps blobs in memory.
type Store struct {
    mu    sync.RWMutex
    blobs map[string][]byte
}

// NewStore builds an empty blob store.
func NewStore() *Store {
    return &Store{blobs: map[string][]byte{}}
}

// Put stores a blob and returns its content hash.
func (s *Store) Put(data []byte) string {
    sum := sha256.Sum256(data)
    key := hex.EncodeToString(sum[:8])
    s.mu.Lock()
    s.blobs[key] = data
    s.mu.Unlock()
    return key
}

// Get fetches a blob by hash key.
func (s *Store) Get(key string) ([]byte, bool) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    b, ok := s.blobs[key]
    return b, ok
}
