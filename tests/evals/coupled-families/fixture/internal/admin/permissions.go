package admin

import "demo/internal/auth"

// RolePermissions grants each role its permissions in the admin console.
var RolePermissions = map[auth.Role][]string{
	auth.RoleViewer: {"read"},
	auth.RoleEditor: {"read", "write"},
}

// Can reports whether a role holds a permission.
func Can(role auth.Role, permission string) bool {
	for _, p := range RolePermissions[role] {
		if p == permission {
			return true
		}
	}
	return false
}
