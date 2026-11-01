package projectfile

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	inside := filepath.Join(root, "config.json")
	if err := os.WriteFile(inside, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(root, "big.json")
	if err := os.WriteFile(big, bytes.Repeat([]byte("a"), MaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	atLimit := filepath.Join(root, "limit.json")
	if err := os.WriteFile(atLimit, bytes.Repeat([]byte("a"), MaxBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	inRootLink := filepath.Join(root, "link-in.json")
	outsideLink := filepath.Join(root, "link-out.json")
	nested := filepath.Join(root, "sub")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	dirLink := filepath.Join(root, "dir-link")
	devLink := filepath.Join(root, "devnull-link")
	missing := filepath.Join(root, "missing.json")

	symlinkOK := true
	if err := os.Symlink(inside, inRootLink); err != nil {
		symlinkOK = false
	}
	if symlinkOK {
		if err := os.Symlink(outsideFile, outsideLink); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(nested, dirLink); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(os.DevNull, devLink); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name    string
		path    string
		root    string
		want    string
		wantErr error
		skip    bool
		missing bool
	}{
		{name: "regular file in root", path: inside, root: root, want: `{"ok":true}`},
		{name: "regular file, no root", path: inside, root: "", want: `{"ok":true}`},
		{name: "at the size limit", path: atLimit, root: root, want: strings.Repeat("a", MaxBytes)},
		{name: "over the size limit", path: big, root: root, wantErr: ErrTooLarge},
		{name: "symlink inside root", path: inRootLink, root: root, want: `{"ok":true}`, skip: !symlinkOK},
		{name: "symlink outside root", path: outsideLink, root: root, wantErr: ErrOutsideRoot, skip: !symlinkOK},
		{name: "symlink to directory", path: dirLink, root: root, wantErr: ErrNotRegular, skip: !symlinkOK},
		{name: "directory", path: nested, root: root, wantErr: ErrNotRegular},
		{name: "char device", path: os.DevNull, root: "", wantErr: ErrNotRegular},
		{name: "symlink to char device", path: devLink, root: root, wantErr: ErrOutsideRoot, skip: !symlinkOK},
		{name: "missing file", path: missing, root: root, missing: true},
		{name: "missing file no root", path: missing, root: "", missing: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip {
				t.Skip("symlinks unavailable")
			}
			got, err := Read(tc.path, tc.root)
			if tc.missing {
				if !errors.Is(err, fs.ErrNotExist) || !os.IsNotExist(err) {
					t.Fatalf("missing file: want not-exist error, got %v", err)
				}
				return
			}
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("content mismatch: got %d bytes", len(got))
			}
		})
	}
}
