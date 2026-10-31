package audit

const (
	ActionPasswordReset = "password.reset"
	ActionLogin         = "auth.login"
)

// Actions lists every audit action the service records.
var Actions = []string{ActionPasswordReset, ActionLogin}
