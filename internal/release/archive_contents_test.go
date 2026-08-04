package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyArchiveContentsRequiredEntries(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		write   func(*testing.T, string, []string)
		archive string
		entries []string
	}{
		{
			name:    "tar gz",
			goos:    "linux",
			write:   writeTestTarGz,
			archive: "splice-v0.1.0-linux-x64.tar.gz",
			entries: []string{"splice", "splice-memd"},
		},
		{
			name:    "zip",
			goos:    "windows",
			write:   writeTestZip,
			archive: "splice-v0.1.0-windows-x64.zip",
			entries: []string{"splice.exe", "splice-memd.exe"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), tt.archive)
			tt.write(t, archivePath, tt.entries)
			if err := VerifyArchiveContents(archivePath, tt.goos); err != nil {
				t.Fatalf("VerifyArchiveContents returned error: %v", err)
			}
		})
	}
}

func TestVerifyArchiveContentsMissingSidecar(t *testing.T) {
	// Regression test: a released archive's contents had never been checked.
	archivePath := filepath.Join(t.TempDir(), "splice-v0.1.0-linux-x64.tar.gz")
	writeTestTarGz(t, archivePath, []string{"splice"})

	err := VerifyArchiveContents(archivePath, "linux")
	if err == nil {
		t.Fatal("VerifyArchiveContents returned nil, want missing sidecar error")
	}
	if !strings.Contains(err.Error(), `missing required entry "splice-memd"`) {
		t.Fatalf("error = %q, want missing sidecar", err)
	}
	if !strings.Contains(err.Error(), `actual entries: [splice]`) {
		t.Fatalf("error = %q, want actual archive entries", err)
	}
}

func TestVerifyArchiveContentsMissingMainBinary(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "splice-v0.1.0-linux-x64.tar.gz")
	writeTestTarGz(t, archivePath, []string{"splice-memd"})

	err := VerifyArchiveContents(archivePath, "linux")
	if err == nil {
		t.Fatal("VerifyArchiveContents returned nil, want missing main binary error")
	}
	if !strings.Contains(err.Error(), `missing required entry "splice"`) {
		t.Fatalf("error = %q, want missing main binary", err)
	}
	if !strings.Contains(err.Error(), `actual entries: [splice-memd]`) {
		t.Fatalf("error = %q, want actual archive entries", err)
	}
}

func TestVerifyArchiveContentsAllowsExtraEntries(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "splice-v0.1.0-windows-x64.zip")
	writeTestZip(t, archivePath, []string{"splice.exe", "splice-memd.exe", "README.txt", "helpers/tool"})

	if err := VerifyArchiveContents(archivePath, "windows"); err != nil {
		t.Fatalf("VerifyArchiveContents returned error: %v", err)
	}
}

func writeTestZip(t *testing.T, path string, entries []string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	writer := zip.NewWriter(file)
	for _, name := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			_ = file.Close()
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := entry.Write([]byte("fixture")); err != nil {
			_ = file.Close()
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		t.Fatalf("close zip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}
}

func writeTestTarGz(t *testing.T, path string, entries []string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create tar.gz: %v", err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range entries {
		contents := []byte("fixture")
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(contents))}); err != nil {
			_ = file.Close()
			t.Fatalf("create tar entry %s: %v", name, err)
		}
		if _, err := tarWriter.Write(contents); err != nil {
			_ = file.Close()
			t.Fatalf("write tar entry %s: %v", name, err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		_ = file.Close()
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		_ = file.Close()
		t.Fatalf("close gzip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close tar.gz file: %v", err)
	}
}
