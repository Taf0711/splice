package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	zeroSandbox "github.com/Taf0711/splice/internal/sandbox"
)

// TestUnwrappedEnginePlanScrubsProviderKeys: every bash spawn that
// is not wrapped scrubs provider keys from the child environment. The nil
// engine (escalated) path and the wrapped path do. A spawn under an engine
// whose plan comes back unwrapped (policy mode disabled, or inherited sandbox
// markers) must do the same.
func TestUnwrappedEnginePlanScrubsProviderKeys(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell probe")
	}
	const key = "sk-audit3-e04-fake-provider-key"
	t.Setenv("OPENAI_API_KEY", key)
	t.Setenv("SPLICE_AUDIT_VISIBLE", "visible-e04")
	cases := []struct {
		name   string
		engine func(string) *zeroSandbox.Engine
	}{
		{name: "nil engine (control)", engine: func(string) *zeroSandbox.Engine { return nil }},
		{name: "disabled policy", engine: func(dir string) *zeroSandbox.Engine {
			return zeroSandbox.NewEngine(zeroSandbox.EngineOptions{WorkspaceRoot: dir, Policy: zeroSandbox.Policy{Mode: zeroSandbox.ModeDisabled},
				Backend: zeroSandbox.SelectBackend(zeroSandbox.BackendOptions{})})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatalf("setup: %v", err)
			}
			tool := NewBashTool(dir).(bashTool)
			res := tool.run(context.Background(), map[string]any{"command": "env > env.txt"}, RunOptions{Sandbox: tc.engine(dir)})
			data, err := os.ReadFile(filepath.Join(dir, "env.txt"))
			if err != nil || !strings.Contains(string(data), "visible-e04") {
				t.Fatalf("setup: positive control: env did not run (status=%v output=%q err=%v)", res.Status, res.Output, err)
			}
			if strings.Contains(string(data), key) {
				t.Errorf("%s: bash child environment carries OPENAI_API_KEY; the unwrapped engine plan inherits the unscrubbed process environment", tc.name)
			}
		})
	}
}
