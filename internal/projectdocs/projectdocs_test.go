package projectdocs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/projectfile"
)

func TestLoadPrefersAgentsOverClaudeAtEachDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(root, "pkg")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(root, "CLAUDE.md"): "root claude",
		filepath.Join(root, "AGENTS.md"): "root agents",
		filepath.Join(leaf, "CLAUDE.md"): "leaf claude",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := Load(leaf)
	if !strings.Contains(got, "root agents") || !strings.Contains(got, "leaf claude") {
		t.Fatalf("Load = %q", got)
	}
	if strings.Contains(got, "root claude") {
		t.Fatalf("AGENTS.md did not win over CLAUDE.md at root: %q", got)
	}
}

func TestLoadFromNeverLoadsInstructionFilesOutsideRepo(t *testing.T) {
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(outside, []byte("AWS_SECRET=outside-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "AGENTS.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got := LoadFrom(repo, repo)
	if strings.Contains(got, "outside-secret") {
		t.Fatalf("instruction symlink to a file outside the repo was loaded: %q", got)
	}
}

func TestLoadFromRefusesOversizedInstructionFile(t *testing.T) {
	repo := t.TempDir()
	big := "# Guidelines\n" + strings.Repeat("x", projectfile.MaxBytes)
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadFrom(repo, repo); got != "" {
		t.Fatalf("oversized instruction file must not load, got %d bytes", len(got))
	}
}
