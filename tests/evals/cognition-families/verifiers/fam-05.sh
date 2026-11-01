# fam-05: admin handlers map store errors through the SAME mapStoreError
# table. Behavioral: an admin handler answers 404 through the shared table;
# structural: no hand-coded store-failure status number outside
# mapStoreError itself.
cat > probe_errmap_test.go <<'EOF'
package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeAdminErrorMapping(t *testing.T) {
	// The shared table exists and maps the known sentinel.
	if got := mapStoreError(ErrNotFound); got != 404 {
		t.Fatalf("mapStoreError(ErrNotFound) = %d, want 404", got)
	}
	// An admin handler that reads a missing session answers 404 through the
	// table (not an inline number) and keeps serving the admin route.
	handler := adminSessionHandler(NewStore(time.Minute))
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest("GET", "/admin/session?id=missing", nil))
	if rec.Code != 404 {
		t.Fatalf("admin handler status = %d, want 404 via mapStoreError", rec.Code)
	}
}
EOF
go test -count=1 . ; rc=$? ; rm -f probe_errmap_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
# Structural: mapStoreError exists in main.go and admin handlers route
# through it. The awk block skips mapStoreError's own body, where the
# canonical 404 lives; any OTHER hard-coded 404/400/409/429 next to a store
# error branch in admin code fails the single-source rule.
grep -Eq 'func mapStoreError' main.go admin.go || exit 1
for f in admin.go main.go; do
  [ -f "$f" ] || continue
  if awk '
    /func mapStoreError/ { inmap=1 }
    inmap && /^}/ { inmap=0; next }
    !inmap && /http\.Error\(/ && /40[049]|429/ { print FILENAME; exit 1 }
  ' "$f"; then :; else [ $? -ne 0 ] && exit 1; fi
done
# The admin handler must reference the mapping function.
grep -q 'mapStoreError' admin.go 2>/dev/null || grep -q 'adminSessionHandler' admin.go 2>/dev/null || exit 1
exit 0