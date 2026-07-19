package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// TestRunExecDropsImagesOnNonVisionModelWithWarning drives a full exec run with
// an --image attachment against a custom (catalog-unknown) model id. The vision
// gate cannot confirm vision support for an unknown id, so it must warn on
// stderr and proceed text-only (exit 0), never erroring the run.
func TestRunExecDropsImagesOnNonVisionModelWithWarning(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "shot.png"), pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	exitCode, _, stderr := runExecWithStageProvider(t, root, []string{
		"exec", "--cwd", root,
		"--model", "my-custom-vision-less-model",
		"--image", "shot.png",
		"describe the screenshot",
	})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0 (drop+warn, never error), got %d: %s", exitCode, stderr)
	}
	if !strings.Contains(stderr, "does not support image input") {
		t.Fatalf("expected non-vision warning on stderr, got %q", stderr)
	}
}

func runExecWithStageProvider(t *testing.T, cwd string, args []string) (int, string, string) {
	t.Helper()

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithDeps(args, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(_ string, overrides config.Overrides) (config.ResolvedConfig, error) {
			return execStageResolvedConfig(overrides.Provider.Model, 3), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return newExecStageAwareProvider(execStageProviderOptions{}), nil
		},
	})
	return exitCode, stdout.String(), stderr.String()
}

// TestRunExecRejectsUnsupportedImageType confirms the usage-error path is wired
// into the run (a .txt sniffs as text -> unsupported, exit 2).
func TestRunExecRejectsUnsupportedImageType(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("not an image at all"), 0o600); err != nil {
		t.Fatal(err)
	}

	exitCode, _, stderr := runExecWithEcho(t, []string{
		"exec", "--cwd", root,
		"--image", "notes.txt",
		"look",
	})

	if exitCode != 2 {
		t.Fatalf("expected usage exit code 2, got %d: %s", exitCode, stderr)
	}
	if !strings.Contains(stderr, "unsupported image type") {
		t.Fatalf("expected unsupported-image-type error on stderr, got %q", stderr)
	}
}
