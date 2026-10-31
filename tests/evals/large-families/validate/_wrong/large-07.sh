#!/bin/bash
# Wrong Task B for large-07: re-derives the rule instead of reusing it.
set -e
cd "$1"
mkdir -p internal/storage
cat > internal/storage/put_within.go <<'GOEOF'
package storage

// PutWithin checks the incoming size alone.
func (s *Store) PutWithin(q Quota, data []byte) (string, error) {
	if int64(len(data)) > q.MaxBytes {
		return "", ErrQuotaExceeded
	}
	return s.Put(data), nil
}
GOEOF
