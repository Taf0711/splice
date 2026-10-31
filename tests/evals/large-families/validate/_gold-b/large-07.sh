#!/bin/bash
# Gold solution for large-07 Task B.
set -e
cd "$1"
mkdir -p internal/storage
cat > internal/storage/put_within.go <<'GOEOF'
package storage

// PutWithin stores data only when the quota allows it.
func (s *Store) PutWithin(q Quota, data []byte) (string, error) {
	if err := q.Enforce(s.UsedBytes(), int64(len(data))); err != nil {
		return "", err
	}
	return s.Put(data), nil
}
GOEOF
