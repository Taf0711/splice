package admin

// ActionLabels are the console labels of the audit actions.
var ActionLabels = map[string]string{
	"password.reset": "Password reset",
	"auth.login":     "Sign-in",
}

// Label returns the console label of one audit action.
func Label(action string) string {
	return ActionLabels[action]
}
