//go:build windows

package sandbox

import (
	"os/exec"
	"strconv"
	"time"
)

// processWaitDelay bounds how long Wait blocks for the I/O pipes to drain
// after the process exited or Cancel ran.
const processWaitDelay = 2 * time.Second

// HardenProcessLifetime makes command killable as a process tree. When the
// context ends, Cancel kills the command and every child process (taskkill
// /T). WaitDelay is the backstop: if a process still holds the I/O pipes,
// Wait stops after the delay. The bash tool applies the same rules.
func HardenProcessLifetime(command *exec.Cmd) {
	command.WaitDelay = processWaitDelay
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		_ = exec.Command("taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(command.Process.Pid)).Run()
		return nil
	}
}
