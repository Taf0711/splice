# fam-05 precursor check (Task A): mapStoreError is declared in main.go as
# the single store-error-to-status table and the /session handler routes its
# store-error branch through it. Behavioral: a missing session answers 404
# through the table; the unknown-error default is 500. Structural: the table
# lives in main.go and sessionHandler does not hand-code the store-failure
# status. Runs with CWD = the arm directory (the fixture copy).
cat > probe_errmap_a_test.go <<'EOF'
package main

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeStoreErrorMappingTable(t *testing.T) {
	// The shared table exists and maps the known sentinel.
	if got := mapStoreError(ErrNotFound); got != 404 {
		t.Fatalf("mapStoreError(ErrNotFound) = %d, want 404", got)
	}
	// Anything else is 500 (the fixture only has ErrNotFound today).
	if got := mapStoreError(errors.New("unexpected")); got != 500 {
		t.Fatalf("mapStoreError(unknown) = %d, want 500", got)
	}
	// The /session handler answers a missing session through the table.
	handler := sessionHandler(NewStore(time.Minute))
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest("GET", "/session?id=missing", nil))
	if rec.Code != 404 {
		t.Fatalf("session handler status = %d, want 404 via mapStoreError", rec.Code)
	}
}
EOF
go test -count=1 . ; rc=$? ; rm -f probe_errmap_a_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
# Structural: the table is declared in main.go (the task's stated location).
grep -Eq 'func mapStoreError' main.go || exit 1
# Structural: sessionHandler references the table and no store-failure status
# is hand-coded in its body (the POST 400 is input validation, not a store
# error, and is intentionally allowed).
if awk '
  /func sessionHandler/ { insess=1 }
  insess && /^}/ { insess=0; next }
  insess && /http\.Error\(/ && /StatusNotFound|40[049]|429/ { print FILENAME; exit 1 }
' main.go; then :; else exit 1; fi
awk '/func sessionHandler/,/^}/' main.go | grep -q 'mapStoreError' || exit 1
exit 0
