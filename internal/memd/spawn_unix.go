//go:build !windows

package memd

import (
	"os/exec"
	"syscall"
)

// configureSpawn detaches the child process into its own session on Unix
// platforms so the daemon outlives the parent terminal.
func configureSpawn(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
