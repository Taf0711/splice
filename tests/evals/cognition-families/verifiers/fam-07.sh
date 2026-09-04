# fam-07: ONE table-driven TestSessionLookupTable in lookup_table_test.go
# covering /session GET 200/404/405 through a fresh mux with a controllable
# clock. Structural: exactly one such function, one table.
cat > probe_lookup_test.go <<'EOF'
package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeLookupTableShim(t *testing.T) {
	// The target test must exist and run; this shim just proves the mux
	// wiring the table drives.
	store := NewStore(time.Minute)
	store.now = func() time.Time { return time.Unix(1000, 0) }
	store.Create("live", "ada")
	mux := newSessionMux(store)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/session?id=live", nil))
	if rec.Code != 200 {
		t.Fatalf("live lookup = %d, want 200", rec.Code)
	}
}
EOF
go test -count=1 . ; rc=$? ; rm -f probe_lookup_test.go
if [ [ $rc -ne 0 ] ]; then exit $rc; fi
# Structural: the table test exists, in the pinned file, exactly once.
[ -f lookup_table_test.go ] || exit 1
count=$(grep -c 'func TestSessionLookupTable' lookup_table_test.go)
if [ "$count" != "1" ]; then exit 1; fi
# One table, not three separate test functions.
funcs=$(grep -c '^func Test' lookup_table_test.go)
if [ "$funcs" != "1" ]; then exit 1; fi
grep -q 'cases := \[\]struct\|tests := \[\]struct\|tt.name\|tc.name' lookup_table_test.go || exit 1
# The three statuses are covered in that table.
grep -q '200' lookup_table_test.go || exit 1
grep -q '404' lookup_table_test.go || exit 1
grep -q '405' lookup_table_test.go || exit 1
exit 0