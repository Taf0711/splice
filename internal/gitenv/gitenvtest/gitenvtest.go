// Package gitenvtest records the environment of the git processes a test
// starts. It is for tests only.
package gitenvtest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// InstallRecorder puts a fake git first on PATH. Each run of the fake appends
// its environment to the returned file, one "KEY=value" line per variable,
// and exits 0 with no output. The test sets PATH, so the recorder ends with
// the test. It skips on Windows, where the shell script cannot run.
//
// Use it when a package starts git through a function that does not return
// git's output. The recorded environment shows what git received.
func InstallRecorder(t *testing.T) (recordPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a shell script")
	}
	dir := t.TempDir()
	recordPath = filepath.Join(dir, "env.log")
	script := "#!/bin/sh\nenv >> '" + recordPath + "'\necho '---' >> '" + recordPath + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return recordPath
}

// Runs returns the environment of each git run, in order. Each run is a map
// from variable name to value. A test fails when no git ran.
func Runs(t *testing.T, recordPath string) []map[string]string {
	t.Helper()
	data, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("no git run was recorded: %v", err)
	}
	var runs []map[string]string
	current := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "---" {
			runs = append(runs, current)
			current = map[string]string{}
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			current[key] = value
		}
	}
	if len(runs) == 0 {
		t.Fatal("no git run was recorded")
	}
	return runs
}

// AssertHardened fails unless every recorded git run received the hardened
// settings. The GIT_CONFIG_* pairs are checked by key, not by index, so a
// caller's own settings do not break the check.
func AssertHardened(t *testing.T, runs []map[string]string) {
	t.Helper()
	for i, env := range runs {
		values := map[string]string{}
		for key, value := range env {
			if strings.HasPrefix(key, "GIT_CONFIG_KEY_") {
				index := strings.TrimPrefix(key, "GIT_CONFIG_KEY_")
				values[value] = env["GIT_CONFIG_VALUE_"+index]
			}
		}
		if values["core.fsmonitor"] != "false" || values["safe.bareRepository"] != "explicit" {
			t.Fatalf("git run %d lacks the hardened settings: GIT_CONFIG_COUNT=%q, settings=%v", i, env["GIT_CONFIG_COUNT"], values)
		}
	}
}
