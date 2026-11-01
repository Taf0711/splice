package notifications

// Templates maps a notification kind to its template.
var Templates = map[string]Template{
	"password_reset": PasswordReset,
}

// RenderKind renders the template of one kind for one recipient.
func RenderKind(kind, to string) (string, bool) {
	t, ok := Templates[kind]
	if !ok {
		return "", false
	}
	return t.Render(to), true
}
