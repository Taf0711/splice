# fam-08: build-tag files exist with the right tags, platformNote answers
# per-GOOS, and the go.mod toolchain line stays at the pinned version.
cat > probe_platform_test.go <<'EOF'
package main

import "testing"

func TestProbePlatformNote(t *testing.T) {
	// On the test host (a unix OS in CI and eval) the unix path answers.
	// The note must be non-empty either way; the build-tag split is
	// verified structurally below.
	note := platformNote()
	if note == "" {
		t.Fatal("platformNote() returned an empty string")
	}
}
EOF
go test -count=1 . ; rc=$? ; rm -f probe_platform_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
# Structural: both files with the exact build tags.
grep -Eq '^//go:build unix' session_unix.go || exit 1
grep -Eq '^//go:build !unix' session_other.go || exit 1
grep -q 'platformNote' session_unix.go || exit 1
grep -q 'platformNote' session_other.go || exit 1
# The opposite-GOOS side must still compile.
GOOS=windows go vet . || exit 1
GOOS=windows go build . || exit 1
# The pinned toolchain line from precursor-08 stays explicit in go.mod.
grep -Eq '^go 1\.26' go.mod || exit 1
exit 0