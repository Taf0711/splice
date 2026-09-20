#!/bin/bash
# Gold solution for mvp-02 Task A: DefaultTTL constant in the session
# package; the construction site reads it.
set -e
cd "$1"
cat > internal/session/ttl.go <<'EOF'
package session

import "time"

// DefaultTTL is the single source of truth for the default session
// lifetime. Every expiry decision reads this constant.
const DefaultTTL = 30 * time.Minute
EOF
python3 - <<'PYEOF'
path = "cmd/server/main.go"
src = open(path).read()
old = "\tstore := session.NewStore(30 * time.Minute)"
new = "\tstore := session.NewStore(session.DefaultTTL)"
assert old in src, "main.go TTL literal not found"
src = src.replace(old, new)
# time is now unused unless used elsewhere
if "time." not in src.replace('import (\n\t"log"\n\t"net/http"\n\t"time"\n', ''):
    src = src.replace('import (\n\t"log"\n\t"net/http"\n\t"time"\n)', 'import (\n\t"log"\n\t"net/http"\n)')
open(path, "w").write(src)
PYEOF
cat > internal/session/ttl_test.go <<'EOF'
package session

import (
	"testing"
	"time"
)

func TestDefaultTTL(t *testing.T) {
	if DefaultTTL != 30*time.Minute {
		t.Fatalf("DefaultTTL = %v, want 30m", DefaultTTL)
	}
}
EOF