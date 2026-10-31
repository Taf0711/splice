# large-07 B: Add (s *Store) PutWithin(q Quota, data []byte) (string, error) in the 
mkdir -p internal/storage
cat > internal/storage/probe_put_within_test.go <<'EOF'
package storage

import (
	"errors"
	"testing"
)

func TestProbePutWithin(t *testing.T) {
	s := NewStore()
	q := Quota{MaxBytes: 10}
	if _, err := s.PutWithin(q, []byte("123456")); err != nil {
		t.Fatalf("first put: %v", err)
	}
	key, err := s.PutWithin(q, []byte("abcdef"))
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("second put err = %v, want ErrQuotaExceeded", err)
	}
	if key != "" || s.UsedBytes() != 6 {
		t.Fatalf("a refused put must store nothing: key=%q used=%d", key, s.UsedBytes())
	}
}
EOF
go test -count=1 ./internal/storage/ ; rc=$?
rm -f internal/storage/probe_put_within_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
