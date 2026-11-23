//go:build !unix

package tempgc

import "os"

// ownedByCurrentUser skips the ownership check on platforms without Unix
// user IDs.
func ownedByCurrentUser(os.FileInfo) bool {
	return true
}
