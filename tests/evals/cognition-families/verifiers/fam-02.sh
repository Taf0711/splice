# fam-02: every expiry decision reads the single sessionTTL constant.
# Behavioral: a store wired from the constant drops a session just past the
# TTL (the probe never calls Get before the expiry check, because Get
# refreshes LastSeen and would reset the clock). Structural: the constant
# exists, main wires NewStore from it, and no competing duration literal
# sits at the call site.
cat > probe_ttl_test.go <<'EOF'
package main

import (
	"testing"
	"time"
)

func TestProbeSingleTTLSource(t *testing.T) {
	// sessionTTL must exist as a package-level constant with the fixture's
	// configured value.
	if sessionTTL != 30*time.Minute {
		t.Fatalf("sessionTTL = %v, want 30m", sessionTTL)
	}
	base := time.Unix(1000, 0)
	store := NewStore(sessionTTL)
	store.now = func() time.Time { return base }
	store.Create("a", "alice")
	// Jump past the TTL WITHOUT an intermediate Get: Get refreshes
	// LastSeen, which would mask the expiry decision under test.
	store.now = func() time.Time { return base.Add(31 * time.Minute) }
	if _, err := store.Get("a"); err == nil {
		t.Fatal("session survived past sessionTTL; expiry does not read the configured TTL")
	}
	// The configured value is honored at the boundary: just under TTL is
	// still live on a fresh store.
	fresh := NewStore(sessionTTL)
	fresh.now = func() time.Time { return base }
	fresh.Create("b", "bob")
	fresh.now = func() time.Time { return base.Add(29 * time.Minute) }
	if _, err := fresh.Get("b"); err != nil {
		t.Fatalf("live at 29m under sessionTTL: %v", err)
	}
}
EOF
go test -count=1 . ; rc=$? ; rm -f probe_ttl_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
# Structural: sessionTTL is declared once as a const and referenced.
grep -Eq 'const[[:space:]]+sessionTTL' session.go main.go || exit 1
# No competing duration literal wired into NewStore in main.go: the single
# source of truth means main passes sessionTTL, never a fresh literal.
if grep -Eq 'NewStore\([^)]*time\.Minute' main.go; then exit 1; fi
exit 0