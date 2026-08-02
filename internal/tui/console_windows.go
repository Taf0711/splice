//go:build windows

package tui

import "golang.org/x/sys/windows"

const consoleUTF8CodePage = 65001

func setupConsoleUTF8() func() {
	previousOutput, err := windows.GetConsoleOutputCP()
	if err != nil {
		return func() {}
	}
	previousInput, err := windows.GetConsoleCP()
	if err != nil {
		return func() {}
	}
	if err := windows.SetConsoleOutputCP(consoleUTF8CodePage); err != nil {
		return func() {}
	}
	if err := windows.SetConsoleCP(consoleUTF8CodePage); err != nil {
		_ = windows.SetConsoleOutputCP(previousOutput)
		return func() {}
	}
	return func() {
		_ = windows.SetConsoleOutputCP(previousOutput)
		_ = windows.SetConsoleCP(previousInput)
	}
}
