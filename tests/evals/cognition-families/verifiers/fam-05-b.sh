# fam-05 target check (Task B): new admin handlers map store errors through
# the SAME mapStoreError table. The task does not name the handler, so this
# check is NAME-AGNOSTIC: any function other than the pre-existing
# sessionHandler that returns http.HandlerFunc and routes through
# mapStoreError satisfies it. Behavioral: the table maps the known sentinel.
# Structural: no hand-coded store-failure status number outside mapStoreError
# itself. Runs with CWD = the arm directory (the fixture copy).
cat > probe_errmap_b_test.go <<'EOF'
package main

import "testing"

func TestProbeAdminErrorMapping(t *testing.T) {
	// The shared table exists and maps the known sentinel.
	if got := mapStoreError(ErrNotFound); got != 404 {
		t.Fatalf("mapStoreError(ErrNotFound) = %d, want 404", got)
	}
}
EOF
go test -count=1 . ; rc=$? ; rm -f probe_errmap_b_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
# Structural: the table exists.
grep -Eq 'func mapStoreError' main.go admin.go || exit 1
# Structural (name-agnostic): at least one function besides sessionHandler
# and mapStoreError returns an http.HandlerFunc AND references mapStoreError.
found=0
for f in *.go; do
  [ -f "$f" ] || continue
  if awk '
    /^func / {
      if (name != "" && handler && usesmap && name != "sessionHandler" && name != "mapStoreError") found=1
      name=$2; sub(/\(.*/, "", name); handler=0; usesmap=0
    }
    name != "" && name != "mapStoreError" && name != "sessionHandler" {
      if ($0 ~ /http\.HandlerFunc/) handler=1
      if ($0 ~ /mapStoreError/) usesmap=1
    }
    END {
      if (name != "" && handler && usesmap && name != "sessionHandler" && name != "mapStoreError") found=1
      exit(found == 1 ? 0 : 1)
    }
  ' "$f"; then found=1; fi
done
[ "$found" = 1 ] || exit 1
# Structural: no store-failure status is hand-coded outside mapStoreError.
for f in *.go; do
  [ -f "$f" ] || continue
  if awk '
    /func mapStoreError/ { inmap=1 }
    inmap && /^}/ { inmap=0; next }
    !inmap && /http\.Error\(/ && /40[049]|429/ { print FILENAME; exit 1 }
    END { exit(inline == 1 ? 1 : 0) }
  ' "$f"; then :; else exit 1; fi
done
exit 0
