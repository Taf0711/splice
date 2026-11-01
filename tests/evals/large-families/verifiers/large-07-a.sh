# large-07 A: Add (s *Store) UsedBytes() int64 in the storage package: the total siz
mkdir -p internal/storage
cat > internal/storage/probe_usage_test.go <<'EOF'
package storage

import "testing"

func TestProbeUsedBytes(t *testing.T) {
	s := NewStore()
	if got := s.UsedBytes(); got != 0 {
		t.Fatalf("empty UsedBytes = %d", got)
	}
	s.Put([]byte("abcd"))
	s.Put([]byte("xyz"))
	if got := s.UsedBytes(); got != 7 {
		t.Fatalf("UsedBytes = %d, want 7", got)
	}
}
EOF
go test -count=1 ./internal/storage/ ; rc=$?
rm -f internal/storage/probe_usage_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
