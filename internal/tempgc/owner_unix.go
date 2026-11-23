//go:build unix

package tempgc

import (
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether the entry belongs to the current user.
// It fails safe: an entry without Unix ownership data is not owned.
func ownedByCurrentUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == os.Getuid()
}
