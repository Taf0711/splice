# large-05 A: Add a Welcome Template variable in the notifications package (Greeting
mkdir -p internal/notifications
cat > internal/notifications/probe_welcome_test.go <<'EOF'
package notifications

import "testing"

func TestProbeRenderFor(t *testing.T) {
	m := RenderFor(Welcome, "ada")
	if m.Kind != KindEmail || m.To != "ada" || m.Subject != "your account is ready" {
		t.Fatalf("RenderFor = %+v", m)
	}
	if m.Body != Welcome.Render("ada") {
		t.Fatalf("Body = %q, want the rendered template", m.Body)
	}
	if Welcome.Footer != "demo service team" {
		t.Fatalf("Welcome footer = %q", Welcome.Footer)
	}
}
EOF
go test -count=1 ./internal/notifications/ ; rc=$?
rm -f internal/notifications/probe_welcome_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
