#!/bin/bash
# Gold solution for large-05 Task A.
set -e
cd "$1"
mkdir -p internal/notifications
cat > internal/notifications/welcome.go <<'GOEOF'
package notifications

// Welcome is the template for new-account mail.
var Welcome = Template{
	Greeting: "Welcome",
	Action:   "your account is ready",
	Footer:   "demo service team",
}

// RenderFor builds the email Message for one recipient from a template.
func RenderFor(t Template, to string) Message {
	return Message{Kind: KindEmail, To: to, Subject: t.Action, Body: t.Render(to)}
}
GOEOF
