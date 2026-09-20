package swebenchpro

import (
	"os"
	"strings"
)

func trimSpace(s string) string { return strings.TrimSpace(s) }

// fileExists reports whether the path exists (file or directory).
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
