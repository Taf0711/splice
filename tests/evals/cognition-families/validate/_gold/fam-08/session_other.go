//go:build !unix

package main

// platformNote reports the platform family. The non-unix side of the
// build-tag split.
func platformNote() string {
	return "other"
}
