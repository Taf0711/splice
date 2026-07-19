//go:build windows

package main

import "os"

// ensurePrivateDir creates dir with mode 0700. Windows does not honor Unix
// permission bits, so privacy is provided by the parent directory's ACL
// (which is created under the user's profile).
func ensurePrivateDir(dir string) error {
	return os.MkdirAll(dir, 0o700)
}

// setPrivateUmask returns a no-op restore function. Windows has no umask
// equivalent; file permissions are managed through ACLs.
func setPrivateUmask() func() {
	return func() {}
}

// tightenDBPermissions is a no-op on Windows because the database inherits
// ACLs from the user-private directory.
func tightenDBPermissions(dbPath string) error {
	return nil
}
