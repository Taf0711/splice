package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/sandbox"
)

func TestLinuxSandboxHiddenSubcommandRoutesToHelper(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{sandbox.LinuxSandboxSubcommand, "--unknown"}, &stdout, &stderr, appDeps{})
	if code != 2 {
		t.Fatalf("exit code = %d, want Linux helper argument error", code)
	}
	if !strings.Contains(stderr.String(), sandbox.LinuxSandboxHelperName) {
		t.Fatalf("stderr = %q, want Linux helper error", stderr.String())
	}
}
