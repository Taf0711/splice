package stages

// Tests for the production-source context fallback: when the task text names
// no path, the writer must receive real production file contents (so modifies
// preserve live symbols) while test files stay excluded and explicit path
// mentions keep winning.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func readPaths(request schemas.ContextRequest) []string {
	paths := []string{}
	for _, query := range request.Queries {
		if query.QueryType == schemas.ContextReadFile && query.Path != nil {
			paths = append(paths, *query.Path)
		}
	}
	return paths
}

func seedFile(t *testing.T, root string, relative string, content string) {
	t.Helper()
	full := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relative, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

func TestDefaultContextRequestFallsBackToProductionSourcesWithoutAPath(t *testing.T) {
	root := t.TempDir()
	seedFile(t, root, "users.go", "package users\n\nfunc New() *Service { return &Service{} }\n")
	seedFile(t, root, "users/audit_test.go", "package users\n\n// planted trap\n")
	seedFile(t, root, "README.md", "# demo\n")

	request := defaultContextRequest("fix the spelling typo in the service", root, "go")
	paths := readPaths(request)
	if !slices.Contains(paths, "users.go") {
		t.Fatalf("fallback reads = %v, want the real production source users.go", paths)
	}
	for _, path := range paths {
		if strings.Contains(path, "_test.go") || strings.HasSuffix(path, ".md") {
			t.Fatalf("fallback must exclude tests and non-source files, got %v", paths)
		}
	}
	if len(request.Queries) == 0 || request.Queries[0].QueryType != schemas.ContextListFiles {
		t.Fatal("listing query must stay first")
	}
}

func TestDefaultContextRequestFallbackExcludesTestFilesAcrossLanguages(t *testing.T) {
	root := t.TempDir()
	seedFile(t, root, "svc.py", "class Service: pass\n")
	seedFile(t, root, "test_svc.py", "def test_x(): pass\n")
	seedFile(t, root, "svc_internal_test.py", "def test_y(): pass\n")

	request := defaultContextRequest("harden the service", root, "python")
	paths := readPaths(request)
	if len(paths) != 1 || paths[0] != "svc.py" {
		t.Fatalf("python fallback = %v, want exactly svc.py", paths)
	}

	root = t.TempDir()
	seedFile(t, root, "src/app.ts", "export const app = 1;\n")
	seedFile(t, root, "src/app.test.ts", "it('works', () => {});\n")
	seedFile(t, root, "src/routes.spec.ts", "it('routes', () => {});\n")

	request = defaultContextRequest("extend the app", root, "typescript")
	paths = readPaths(request)
	if len(paths) != 1 || paths[0] != filepath.Join("src", "app.ts") {
		t.Fatalf("typescript fallback = %v, want exactly src/app.ts", paths)
	}
}

func TestDefaultContextRequestExplicitPathMentionsStillWin(t *testing.T) {
	root := t.TempDir()
	seedFile(t, root, "named.go", "package named\n")
	seedFile(t, root, "other.go", "package other\n")

	request := defaultContextRequest("update named.go to add a helper", root, "go")
	paths := readPaths(request)
	if !slices.Contains(paths, "named.go") {
		t.Fatalf("explicit mention must be read, got %v", paths)
	}
	if slices.Contains(paths, "other.go") {
		t.Fatalf("fallback must not fire when an explicit path wins, got %v", paths)
	}
}

func TestDefaultContextRequestFallbackIsBoundedAndDeterministic(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < maxFallbackSourceFiles+4; i++ {
		seedFile(t, root, "pkg/file"+strings.Repeat("x", i)+".go", "package pkg\n")
	}
	first := readPaths(defaultContextRequest("touch every module", root, "go"))
	second := readPaths(defaultContextRequest("touch every module", root, "go"))
	if len(first) != maxFallbackSourceFiles {
		t.Fatalf("fallback files = %d, want capped at %d", len(first), maxFallbackSourceFiles)
	}
	if strings.Join(first, "\x00") != strings.Join(second, "\x00") {
		t.Fatalf("fallback order is not deterministic:\n%v\n%v", first, second)
	}
}

func TestIsTestFileNameCoversSupportedConventions(t *testing.T) {
	tests := map[string]bool{
		"users_test.go": true, "main.go": false,
		"test_users.py": true, "users_internal_test.py": true, "svc.py": false,
		"app.test.ts": true, "routes.spec.jsx": true, "app.ts": false,
	}
	for name, want := range tests {
		if got := isTestFileName(name); got != want {
			t.Fatalf("isTestFileName(%q) = %v, want %v", name, got, want)
		}
	}
}
