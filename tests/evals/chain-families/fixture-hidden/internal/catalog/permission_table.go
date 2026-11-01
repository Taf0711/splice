package catalog

// PermissionOwners maps each permission to the team that owns it.
var PermissionOwners = map[string]string{
	"read":  "team-read",
	"write": "team-write",
}
