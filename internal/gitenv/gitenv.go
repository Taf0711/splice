// Package gitenv hardens the git processes that Splice starts itself. It is
// stdlib-only so that every package that runs git can import it.
//
// Splice does not disable clean and smudge filters. git-lfs depends on them,
// so a repository that uses LFS keeps working.
package gitenv

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// hardenedConfigs are the git settings every Splice git process sets. A cloned
// repository controls its own .git/config, so these stop it from running code
// through discovery (an embedded bare repository) or a core.fsmonitor command.
var hardenedConfigs = [][2]string{
	{"safe.bareRepository", "explicit"},
	{"core.fsmonitor", "false"},
}

// Env returns base with hardenedConfigs added through the
// GIT_CONFIG_COUNT/KEY_n/VALUE_n environment. Settings the caller already
// passed are kept and the new ones are appended after them. It fails when the
// caller's GIT_CONFIG_COUNT is not a count, rather than overwrite entries it
// cannot read.
func Env(base []string) ([]string, error) {
	count := 0
	env := make([]string, 0, len(base)+2*len(hardenedConfigs)+1)
	for _, entry := range base {
		if value, ok := strings.CutPrefix(entry, "GIT_CONFIG_COUNT="); ok {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("GIT_CONFIG_COUNT %q is not a count", value)
			}
			count = n
			continue
		}
		env = append(env, entry)
	}
	for _, setting := range hardenedConfigs {
		env = append(env,
			fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", count, setting[0]),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", count, setting[1]))
		count++
	}
	return append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(count)), nil
}

// Harden sets cmd.Env to the hardened environment. When cmd.Env is nil, the
// command would inherit os.Environ(), so that becomes the base. Call Harden
// after every other change to cmd.Env, so no later write drops the settings.
func Harden(cmd *exec.Cmd) error {
	base := cmd.Env
	if base == nil {
		base = os.Environ()
	}
	env, err := Env(base)
	if err != nil {
		name := "git"
		if len(cmd.Args) > 0 {
			name = cmd.Args[0]
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	cmd.Env = env
	return nil
}

// Output hardens cmd and returns its standard output, as cmd.Output does.
func Output(cmd *exec.Cmd) ([]byte, error) {
	if err := Harden(cmd); err != nil {
		return nil, err
	}
	return cmd.Output()
}

// CombinedOutput hardens cmd and returns its combined output, as
// cmd.CombinedOutput does.
func CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	if err := Harden(cmd); err != nil {
		return nil, err
	}
	return cmd.CombinedOutput()
}

// Run hardens cmd and runs it, as cmd.Run does.
func Run(cmd *exec.Cmd) error {
	if err := Harden(cmd); err != nil {
		return err
	}
	return cmd.Run()
}
