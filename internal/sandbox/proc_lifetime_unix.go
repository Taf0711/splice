//go:build !windows

package sandbox

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// processWaitDelay bounds how long Wait blocks for the I/O pipes to drain
// after the process exited or Cancel ran.
const processWaitDelay = 2 * time.Second

// HardenProcessLifetime makes command killable as one unit. Setpgid puts the
// command in a new process group, and every child it starts inherits that
// group. When the context ends, Cancel kills the whole group, not only the
// direct child. WaitDelay is the backstop: if a process still holds the I/O
// pipes, Wait stops after the delay. A caller that needs a new session
// (Setsid) must clear Setpgid, because a session leader cannot change its
// process group. The bash tool applies the same rules.
func HardenProcessLifetime(command *exec.Cmd) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Setpgid = true
	command.WaitDelay = processWaitDelay
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		// A negative pid sends the signal to the process group.
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
}
