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
	// A named path is read first. In a large workspace the fallback does not
	// add an arbitrary slice of other sources.
	root := t.TempDir()
	seedFile(t, root, "named.go", "package named\n")
	for i := 0; i < maxFallbackSourceFiles+2; i++ {
		seedFile(t, root, "pkg/other"+strings.Repeat("x", i)+".go", "package pkg\n")
	}
	paths := readPaths(defaultContextRequest("update named.go to add a helper", root, "go"))
	if len(paths) != 1 || paths[0] != "named.go" {
		t.Fatalf("large workspace reads = %v, want only named.go", paths)
	}
}

// TestDefaultContextRequestNamedCreateTargetStillReadsSources is the D0
// regression (plans/SPEC_JEV_DECISION_LAYER_2026-09-25.md): "Add X in
// admin.go" named only the file to create, so the old rule read the missing
// admin.go and none of the sources the task depends on.
func TestDefaultContextRequestNamedCreateTargetStillReadsSources(t *testing.T) {
	root := t.TempDir()
	seedFile(t, root, "session.go", "package main\n")
	seedFile(t, root, "main.go", "package main\n")
	seedFile(t, root, "session_test.go", "package main\n")
	seedFile(t, root, "go.mod", "module demo\n")
	request := defaultContextRequest("Add AdminSessionView in admin.go reusing the Session tags", root, "go")
	paths := readPaths(request)
	want := []string{"main.go", "session.go", "go.mod"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("reads = %v, want %v", paths, want)
	}
	if !strings.Contains(request.Reason, "do not exist yet, so the task creates them: admin.go") {
		t.Fatalf("reason must name the create target: %q", request.Reason)
	}
}

func TestDefaultContextRequestSmallWorkspaceAddsSourcesToNamedPath(t *testing.T) {
	root := t.TempDir()
	seedFile(t, root, "named.go", "package named\n")
	seedFile(t, root, "other.go", "package named\n")
	paths := readPaths(defaultContextRequest("update named.go to add a helper", root, "go"))
	if strings.Join(paths, ",") != "named.go,other.go" {
		t.Fatalf("small workspace reads = %v, want named.go first, then other.go", paths)
	}
}

func TestDefaultContextRequestReadsTestsOnlyWhenTheRequestMentionsTests(t *testing.T) {
	root := t.TempDir()
	seedFile(t, root, "clock.go", "package main\n")
	seedFile(t, root, "clock_test.go", "package main\n")
	paths := readPaths(defaultContextRequest("Add a table-driven test that reuses the clock table style", root, "go"))
	if !slices.Contains(paths, "clock_test.go") {
		t.Fatalf("a request about tests must read the test files, got %v", paths)
	}
	paths = readPaths(defaultContextRequest("Harden the clock", root, "go"))
	if slices.Contains(paths, "clock_test.go") {
		t.Fatalf("a request that does not mention tests must not read test files (trap-test guard), got %v", paths)
	}
}

func TestDefaultContextRequestReadsFilesNamedByRepairEvidence(t *testing.T) {
	root := t.TempDir()
	seedFile(t, root, "admin.go", "package main\n")
	for i := 0; i < maxFallbackSourceFiles+2; i++ {
		seedFile(t, root, "pkg/f"+strings.Repeat("x", i)+".go", "package pkg\n")
	}
	seedFile(t, root, "session.go", "package main\n")
	evidence := "build demo [demo.test]: ./session.go:91:30: undefined: adminUser"
	paths := readPaths(defaultContextRequestWithEvidence("Add AdminSessionView in admin.go", evidence, root, "go"))
	if strings.Join(paths, ",") != "admin.go,session.go" {
		t.Fatalf("repair reads = %v, want the named admin.go and the failing session.go", paths)
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

// TestFallbackReadsThePackageTheRequestNames: large-01-write names no path,
// only "the billing Service" and "DunningLevel". The alphabetical fallback
// read cmd/, admin/, audit/, and auth/, and the model exhausted its
// expansion rounds in 8 of 9 attempts finding the billing package.
func TestFallbackReadsThePackageTheRequestNames(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "tests", "evals", "large-families", "fixture")
	intent := "Add RecordDunningNotice(accountID string) DunningLevel on the billing Service: it advances and returns that account's dunning level, keeping per-account levels in the service. DunningLevel and its levels (with Next()) already exist in the billing package. Add a regression test for advancement and account isolation."
	var reads []string
	for _, q := range DefaultContextRequestFor(intent, "", fixture, "go").Queries {
		if q.QueryType == schemas.ContextReadFile && q.Path != nil {
			reads = append(reads, *q.Path)
		}
	}
	joined := strings.Join(reads, ",")
	t.Logf("first round: %v", reads)
	for _, want := range []string{"internal/billing/invoice.go", "internal/billing/dunning.go"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("first round %v lacks %s", reads, want)
		}
	}
	for _, unwanted := range []string{"internal/admin/", "internal/auth/", "cmd/"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("first round %v reads unrelated %s", reads, unwanted)
		}
	}
	// A request that names nothing in the workspace keeps the old slice.
	if got := rankedSourcePaths(fixture, "go", "Improve the thing.", 8); strings.Join(got, ",") != strings.Join(productionSourcePaths(fixture, "go"), ",") {
		t.Fatalf("unranked request = %v, want the alphabetical slice", got)
	}
}

// TestMemoryFilesAddToThePackageTheRequestNames: large-01-read is in the
// audit package; memory offers the billing files that the related write
// changed. The audit sources must still be read, before memory's files.
func TestMemoryFilesAddToThePackageTheRequestNames(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "tests", "evals", "large-families", "fixture")
	intent := "Add LatestDunningEvent(trail *Trail, action string) *Event in the audit package: return the most recent audit event with that action, or nil when none exists."
	memory := []string{"internal/billing/dunning.go", "internal/billing/invoice.go"}
	var reads []string
	for _, q := range DefaultContextRequestPreferring(intent, "", fixture, "go", memory).Queries {
		if q.QueryType == schemas.ContextReadFile && q.Path != nil {
			reads = append(reads, *q.Path)
		}
	}
	index := func(p string) int { return slices.Index(reads, p) }
	if index("internal/audit/log.go") < 0 || index("internal/billing/dunning.go") < 0 {
		t.Fatalf("first round %v must hold both the audit sources and memory's files", reads)
	}
	if index("internal/audit/log.go") > index("internal/billing/dunning.go") {
		t.Fatalf("first round %v puts memory's files before the package the request names", reads)
	}
}
