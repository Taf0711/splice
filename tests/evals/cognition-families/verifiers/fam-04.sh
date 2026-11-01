# fam-04: AdminSessionView reuses Session's explicit JSON tag convention
# (ID, User, Created, LastSeen) plus Admin, and adminSessionsHandler serves
# it. Behavioral probe marshals the view and checks the exact envelope.
cat > probe_view_test.go <<'EOF'
package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProbeAdminSessionView(t *testing.T) {
	at := time.Unix(1000, 0)
	view := AdminSessionView{ID: "s1", User: "alice", Created: at, LastSeen: at, Admin: true}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Session's explicit tag convention: unchanged names, now explicit.
	for _, key := range []string{"ID", "User", "Created", "LastSeen", "Admin"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("AdminSessionView JSON lost key %q: %v", key, payload)
		}
	}
	if payload["Admin"] != true {
		t.Fatalf("Admin = %v, want true", payload["Admin"])
	}
	// The wire keys must be EXACTLY the tagged names (no admin, no lastSeen).
	raw := string(data)
	if strings.Contains(raw, `"admin"`) || strings.Contains(raw, `"lastSeen"`) || strings.Contains(raw, `"id"`) {
		t.Fatalf("tag convention broken: %s", raw)
	}
}
EOF
go test -count=1 . ; rc=$? ; rm -f probe_view_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
# Structural: the view lives in admin.go and Session itself kept the
# explicit tags the convention requires.
grep -q 'AdminSessionView' admin.go || exit 1
grep -Eq 'json:"ID"' session.go || exit 1
grep -Eq 'json:"User"' session.go || exit 1
grep -Eq 'json:"Created"' session.go || exit 1
grep -Eq 'json:"LastSeen"' session.go || exit 1
exit 0