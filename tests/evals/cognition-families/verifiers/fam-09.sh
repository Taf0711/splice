# fam-09: /healthz reports status "ok" (precursor schema), the sessions
# key, and uptime_seconds (non-negative integer), 200 always. The sessions
# key must keep its exact name and meaning.
cat > probe_healthz_test.go <<'EOF'
package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeHealthzSchema(t *testing.T) {
	rec := httptest.NewRecorder()
	healthHandler(NewStore(time.Minute))(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("healthz status = %d, want 200", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("status = %v, want ok", payload["status"])
	}
	if _, ok := payload["sessions"]; !ok {
		t.Fatalf("sessions key lost: %v", payload)
	}
	uptime, ok := payload["uptime_seconds"]
	if !ok {
		t.Fatalf("no uptime_seconds key: %v", payload)
	}
	n, ok := uptime.(float64)
	if !ok {
		t.Fatalf("uptime_seconds = %v, want a number", uptime)
	}
	if n < 0 || n != float64(int64(n)) {
		t.Fatalf("uptime_seconds = %v, want a non-negative integer", uptime)
	}
}
EOF
go test -count=1 . ; rc=$? ; rm -f probe_healthz_test.go ; exit $rc