//go:build !windows

package tui

func setupConsoleUTF8() func() {
	return func() {}
}
