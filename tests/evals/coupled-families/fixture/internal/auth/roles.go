package auth

// Role names what a signed-in user may do.
type Role string

const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
)

// Roles lists every role a user can hold.
var Roles = []Role{RoleViewer, RoleEditor}
