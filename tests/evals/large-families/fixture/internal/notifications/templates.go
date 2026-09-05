package notifications

// Template renders one notification body from its parts.
type Template struct {
    Greeting string
    Action   string
    Footer   string
}

// Render fills the template for one recipient.
func (t Template) Render(to string) string {
    return t.Greeting + " " + to + ": " + t.Action + "\n" + t.Footer
}

// PasswordReset is the template for password reset mail.
var PasswordReset = Template{
    Greeting: "Hello",
    Action:   "your password was reset; reply if this was not you",
    Footer:   "demo service security team",
}
