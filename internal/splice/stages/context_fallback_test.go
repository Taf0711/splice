package stages

// Tests for the production-source context fallback: when the task text names
// no path, the writer must receive real production file contents (so modifies
// preserve live symbols) while test files stay excluded and explicit path
// mentions keep winning.

import (
	"fmt"
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
	paths := readPaths(defaultContextRequestPreferring("Add AdminSessionView in admin.go", evidence, root, "go", nil))
	if strings.Join(paths, ",") != "admin.go,session.go" {
		t.Fatalf("repair reads = %v, want the named admin.go and the failing session.go", paths)
	}
}

func TestDefaultContextRequestFallbackIsBoundedAndDeterministic(t *testing.T) {
	// Each file defines its own identifier, so every file ranks and the
	// bound, not the ranking, decides the count. (E01c: a file ranks only on
	// a distinctive request identifier, so the old generic files no longer do.)
	root := t.TempDir()
	var names []string
	for i := 0; i < maxFallbackSourceFiles+4; i++ {
		name := fmt.Sprintf("fn%02d", i)
		names = append(names, "`"+name+"`")
		seedFile(t, root, "pkg/file"+strings.Repeat("x", i)+".go", "package pkg\n\nfunc "+name+"() {}\n")
	}
	intent := "touch every module: " + strings.Join(names, ", ")
	first := readPaths(defaultContextRequest(intent, root, "go"))
	second := readPaths(defaultContextRequest(intent, root, "go"))
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
	// E01c: a large workspace with no matching file gets no arbitrary slice.
	// This replaces the old alphabetical-slice assertion.
	if got := rankedSourcePaths(fixture, "go", "Improve the thing.", 8); len(got) != 0 {
		t.Fatalf("unranked request = %v, want no fallback sources in a large workspace", got)
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

// seedRankingFixture builds a 40-file Python tree. pkg/impl.py defines
// implemented_function and mentions _imp_ only past the first 64 KiB. Its
// companion mentions _imp_ near the top. The other 38 files hold only the
// common words Function and Symbol.
func seedRankingFixture(t *testing.T, root string) {
	t.Helper()
	pad := strings.Repeat("# filler comment line of plain text\n", 2500) // about 87 KiB
	seedFile(t, root, "pkg/impl.py", "def implemented_function(x):\n    return x\n"+pad+"_imp_ = 1\n")
	seedFile(t, root, "pkg/companion.py", "from pkg.impl import _imp_\n")
	for i := 0; i < 38; i++ {
		seedFile(t, root, fmt.Sprintf("pkg/gen%02d.py", i), "class Function(Symbol):\n    pass\n")
	}
}

// TestRankingFindsTheDefiningFileForCodeTokens pins E01c: code-form tokens
// (fenced code, snake_case) rank the defining file first, even when the
// marker sits past the old 64 KiB head. Generic files with common words do
// not rank.
func TestRankingFindsTheDefiningFileForCodeTokens(t *testing.T) {
	root := t.TempDir()
	seedRankingFixture(t, root)
	intent := "Make `implemented_function` in the Function module call `_imp_` for each Symbol.\n\n```python\nimplemented_function(x)\n_imp_\n```"
	got := rankedSourceMatches(root, "python", intent, 8)
	if len(got) == 0 || got[0] != "pkg/impl.py" {
		t.Fatalf("ranked = %v, want pkg/impl.py first", got)
	}
	for _, path := range got {
		if path != "pkg/impl.py" && path != "pkg/companion.py" {
			t.Fatalf("generic file %s ranked in %v", path, got)
		}
	}
}

// TestNoRankedFileMeansNoArbitrarySlice pins that a large workspace with no
// matching file gets no alphabetical fallback sources.
func TestNoRankedFileMeansNoArbitrarySlice(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		seedFile(t, root, fmt.Sprintf("pkg/gen%02d.py", i), "class Function(Symbol):\n    pass\n")
	}
	request := defaultContextRequest("Improve the thing.", root, "python")
	for _, path := range readPaths(request) {
		if strings.HasSuffix(path, ".py") {
			t.Fatalf("large workspace with no match read arbitrary source %s", path)
		}
	}
}

// TestRankedTokensDropSentenceFinalPeriods pins audit item 1: a word that
// ends a sentence must not keep its period as part of the token.
func TestRankedTokensDropSentenceFinalPeriods(t *testing.T) {
	got := rankedTokens("The fix lands in sympy.core.function. Keep `evalf_marker.` and _imp_.")
	for _, tok := range got {
		if strings.HasSuffix(tok, ".") {
			t.Fatalf("token %q keeps a sentence period: %v", tok, got)
		}
	}
	if !slices.Contains(got, "sympy.core.function") || !slices.Contains(got, "evalf_marker") {
		t.Fatalf("tokens = %v, want sympy.core.function and evalf_marker", got)
	}
}

// TestPackageWordTierReadsTheNamedPackage pins audit item 2: when no file
// holds a request identifier, the sources under a directory the request
// names by word are read. Both probes come from the auditor.
func TestPackageWordTierReadsTheNamedPackage(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "tests", "evals", "large-families", "fixture")
	for intent, dir := range map[string]string{
		"fix the logging in the audit package":                            "internal/audit/",
		"Make the retry backoff in the worker package cap at 30 seconds.": "internal/worker/",
	} {
		reads := readPaths(DefaultContextRequestFor(intent, "", fixture, "go"))
		found := false
		for _, path := range reads {
			if strings.HasPrefix(path, dir) {
				found = true
			}
		}
		if !found {
			t.Fatalf("intent %q reads %v, want a source under %s", intent, reads, dir)
		}
	}
}

// TestDefinitionSiteOutranksACommonMention pins audit item 3: a token that
// most files mention is dropped as generic, but the one file that defines
// it still ranks first.
func TestDefinitionSiteOutranksACommonMention(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 39; i++ {
		seedFile(t, root, fmt.Sprintf("pkg/use%02d.py", i), "x = common_helper(1)\n")
	}
	seedFile(t, root, "pkg/zdef.py", "def common_helper(x):\n    return x\n")
	got := rankedSourceMatches(root, "python", "Fix `common_helper` so it caps at 30.", 8)
	if len(got) == 0 || got[0] != "pkg/zdef.py" {
		t.Fatalf("ranked = %v, want the defining file pkg/zdef.py first", got)
	}
}

// TestDefinitionBoostOrderIsPinned pins the ranked order for each definition
// form against files that merely mention the token. F2 changed how definitions
// are found; the order must not change. Python and Go are ranked separately,
// since a request ranks one language's sources.
func TestDefinitionBoostOrderIsPinned(t *testing.T) {
	py := t.TempDir()
	seedFile(t, py, "a/def_helper.py", "def widget_build(x):\n    return x\n")
	seedFile(t, py, "b/class_user.py", "class widget_build:\n    pass\n")
	seedFile(t, py, "c/meth.py", "class T:\n    def widget_build(self):\n        return 1\n")
	for i := 0; i < 36; i++ {
		seedFile(t, py, fmt.Sprintf("f/mention%02d.py", i), "x = widget_build(1)\n")
	}
	gotPy := rankedSourceMatches(py, "python", "Fix `widget_build` so it caps at 30.", 8)
	wantPy := []string{"c/meth.py", "a/def_helper.py", "b/class_user.py"}
	if strings.Join(gotPy, ",") != strings.Join(wantPy, ",") {
		t.Fatalf("python ranked = %v, want %v", gotPy, wantPy)
	}

	gh := t.TempDir()
	seedFile(t, gh, "c/go_func.go", "package c\n\nfunc widget_build() {}\n")
	seedFile(t, gh, "d/method.go", "package d\n\nfunc (r *T) widget_build() {}\n")
	seedFile(t, gh, "e/typeset.go", "package e\n\ntype widget_build int\n")
	for i := 0; i < 36; i++ {
		seedFile(t, gh, fmt.Sprintf("f/mention%02d.go", i), "package f\n\nvar x = widget_build(1)\n")
	}
	gotGo := rankedSourceMatches(gh, "go", "Fix `widget_build` so it caps at 30.", 8)
	wantGo := []string{"d/method.go", "c/go_func.go", "e/typeset.go"}
	if strings.Join(gotGo, ",") != strings.Join(wantGo, ",") {
		t.Fatalf("go ranked = %v, want %v", gotGo, wantGo)
	}
}
