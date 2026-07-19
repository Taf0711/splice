//go:build windows

package memd

import (
	"os/exec"
	"syscall"
)

// configureSpawn detaches the child into its own process group on Windows.
func configureSpawn(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
