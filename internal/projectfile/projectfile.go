// Package projectfile reads files that a workspace supplies, such as config,
// hook, plugin, and instruction files. A cloned repository can carry symlinks
// and very large files, so every read is bounded and contained.
package projectfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxBytes caps one file read. A larger file is refused, not truncated.
const MaxBytes = 1 << 20

var (
	// ErrOutsideRoot means the file resolves to a path outside the root.
	ErrOutsideRoot = errors.New("file resolves outside the workspace root")
	// ErrNotRegular means the file is not a regular file (a device, FIFO, or directory).
	ErrNotRegular = errors.New("file is not a regular file")
	// ErrTooLarge means the file is larger than MaxBytes.
	ErrTooLarge = errors.New("file exceeds the size limit")
)

// Read returns the contents of path. Symlinks are resolved first. When root is
// not empty, the resolved file must sit inside the resolved root. The file must
// be a regular file no larger than MaxBytes. A missing file returns an error
// for which errors.Is(err, fs.ErrNotExist) is true. Pass root "" for files the
// user owns, such as the user config.
func Read(path, root string) ([]byte, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err // *fs.PathError: os.IsNotExist must still match
	}
	if root != "" {
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(resolvedRoot, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("%s (resolves to %s): %w", path, resolved, ErrOutsideRoot)
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %w", path, ErrNotRegular)
	}
	if info.Size() > MaxBytes {
		return nil, fmt.Errorf("%s is %d bytes; the limit is %d bytes: %w", path, info.Size(), MaxBytes, ErrTooLarge)
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("%s grew past %d bytes while reading: %w", path, MaxBytes, ErrTooLarge)
	}
	return data, nil
}
