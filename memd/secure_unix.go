//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// ensurePrivateDir creates dir with mode 0700 and re-Chmods it to 0700 so
// an existing directory with looser permissions is tightened.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	return nil
}

// setPrivateUmask sets the process umask to 077 so files created while the
// server starts are only accessible to the owner. It returns a function
// that restores the previous umask.
func setPrivateUmask() func() {
	old := syscall.Umask(0o077)
	return func() { syscall.Umask(old) }
}

// tightenDBPermissions restricts the database file and its WAL/SHM
// companions to the owner. Missing WAL/SHM files are ignored because SQLite
// creates them lazily.
func tightenDBPermissions(dbPath string) error {
	if err := os.Chmod(dbPath, 0o600); err != nil {
		return fmt.Errorf("chmod db %s: %w", dbPath, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		p := dbPath + suffix
		if err := os.Chmod(p, 0o600); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("chmod %s: %w", p, err)
		}
	}
	return nil
}
