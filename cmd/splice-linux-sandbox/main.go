//go:build linux

package main

import (
	"os"

	"github.com/Taf0711/splice/internal/sandbox"
)

func main() {
	os.Exit(sandbox.RunLinuxSandboxHelper(os.Args[1:], os.Stderr))
}
