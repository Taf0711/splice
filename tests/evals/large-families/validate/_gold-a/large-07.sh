#!/bin/bash
# Gold solution for large-07 Task A.
set -e
cd "$1"
mkdir -p internal/storage
cat > internal/storage/usage.go <<'GOEOF'
package storage

// UsedBytes totals the size of every stored blob.
func (s *Store) UsedBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int64
	for _, b := range s.blobs {
		total += int64(len(b))
	}
	return total
}
GOEOF
