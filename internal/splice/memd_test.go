package splice

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain keeps package tests away from the user's persistent memd store and
// from the user's config. An active pipeline in the real config.json would
// otherwise change which topology the run tests resolve.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "m-")
	if err != nil {
		panic(err)
	}
	configDir, err := os.MkdirTemp("", "splice-test-config-")
	if err != nil {
		_ = os.RemoveAll(dir)
		panic(err)
	}
	_ = os.Setenv("SPLICE_MEMD_SOCKET", filepath.Join(dir, "m.sock"))
	_ = os.Setenv("SPLICE_MEMD_DB", filepath.Join(dir, "m.db"))
	_ = os.Setenv("XDG_CONFIG_HOME", configDir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	_ = os.RemoveAll(configDir)
	os.Exit(code)
}
