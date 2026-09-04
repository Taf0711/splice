package splice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workspaceFixture is a deterministic two-file Go workspace used by resolver
// tests: store.go declares Store with one method, main.go uses it.
func workspaceFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"store.go": "package fixture\n\ntype Store struct {\n\tName string\n}\n\nfunc (s *Store) Save(v string) error {\n\treturn nil\n}\n\nfunc (s *Store) Load() string {\n\treturn \"\"\n}\n\nfunc newSessionStore(d string) *Store {\n\treturn &Store{}\n}\n",
		"main.go":  "package fixture\n\nfunc run() error {\n\tstore := newSessionStore(\"x\")\n\treturn store.Save(\"v\")\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestResolveGoDiagnosticsUndefinedSymbol(t *testing.T) {
	workspace := workspaceFixture(t)
	output := "# example.com/fixture [example.com/fixture.test]\n" +
		"./main_test.go:9:2: undefined: newSessionMux\n"

	evidence := ResolveGoDiagnostics(workspace, output)

	foundSymbol := false
	for _, symbol := range evidence.Symbols {
		if symbol == "newSessionMux" {
			foundSymbol = true
		}
	}
	if !foundSymbol {
		t.Fatalf("symbols = %v, want newSessionMux", evidence.Symbols)
	}
	// Negative fact: the symbol really is absent from the workspace.
	negative := false
	for _, lookup := range evidence.Lookups {
		if strings.Contains(lookup, "newSessionMux: not found in workspace") {
			negative = true
		}
	}
	if !negative {
		t.Fatalf("lookups = %v, want a not-found fact for newSessionMux", evidence.Lookups)
	}
	// The workspace's real API surface is listed so the writer can resolve
	// the hallucinated symbol against actual declarations.
	surface := false
	for _, lookup := range evidence.Lookups {
		if strings.Contains(lookup, "workspace top-level declarations:") && strings.Contains(lookup, "newSessionStore") {
			surface = true
		}
	}
	if !surface {
		t.Fatalf("lookups = %v, want the workspace declaration surface", evidence.Lookups)
	}
}

func TestResolveGoDiagnosticsMissingMethod(t *testing.T) {
	workspace := workspaceFixture(t)
	output := "./main.go:12:9: store.Commit undefined (type *Store has no field or method Commit)\n"

	evidence := ResolveGoDiagnostics(workspace, output)

	foundMethod := false
	for _, symbol := range evidence.Symbols {
		if symbol == "Commit" {
			foundMethod = true
		}
	}
	if !foundMethod {
		t.Fatalf("symbols = %v, want Commit", evidence.Symbols)
	}
	// The method set of the real receiver type is enumerated.
	methodSet := strings.Join(evidence.Lookups, "\n")
	for _, want := range []string{"methods: Load, Save", "fields: Name"} {
		if !strings.Contains(methodSet, want) {
			t.Fatalf("lookups missing %q:\n%s", want, methodSet)
		}
	}
}

func TestResolveGoDiagnosticsFailingTestNames(t *testing.T) {
	workspace := t.TempDir()
	output := strings.Join([]string{
		`{"Action":"run","Package":"x","Test":"TestFails"}`,
		`{"Action":"output","Package":"x","Test":"TestFails","Output":"--- FAIL: TestFails (0.00s)"}`,
		`{"Action":"fail","Package":"x","Test":"TestFails","Elapsed":0}`,
		`{"Action":"fail","Package":"x","Elapsed":0.1}`,
		`{"ImportPath":"x [x.test]","Action":"build-output","Output":"./a.go:3:9: undefined: Zap"}`,
		`{"Action":"build-fail"}`,
	}, "\n")

	evidence := ResolveGoDiagnostics(workspace, output)
	joined := strings.Join(evidence.Symbols, ",")
	if !strings.Contains(joined, "TestFails") {
		t.Fatalf("symbols = %v, want the failing test name", evidence.Symbols)
	}
	if !strings.Contains(joined, "Zap") {
		t.Fatalf("symbols = %v, want the build-error symbol", evidence.Symbols)
	}
}

func TestResolveGoDiagnosticsCannotUse(t *testing.T) {
	workspace := t.TempDir()
	output := "./main.go:20:5: cannot use client (variable of type *Client) as *Conn value\n"
	evidence := ResolveGoDiagnostics(workspace, output)
	fact := strings.Join(evidence.Facts, "\n")
	if !strings.Contains(fact, "cannot use client") {
		t.Fatalf("facts = %v, want the cannot-use line", evidence.Facts)
	}
}

func TestResolveGoDiagnosticsFileExcerpts(t *testing.T) {
	workspace := workspaceFixture(t)
	output := "./main.go:5:8: undefined: newSessionStore\n"
	evidence := ResolveGoDiagnostics(workspace, output)
	found := false
	for _, fact := range evidence.Facts {
		if strings.HasPrefix(fact, "main.go:") && strings.Contains(fact, "store.Save") {
			found = true
		}
	}
	if !found {
		t.Fatalf("facts = %v, want a main.go excerpt at the named line", evidence.Facts)
	}
}

func TestResolveGoDiagnosticsFallbackPassthrough(t *testing.T) {
	workspace := t.TempDir()
	output := "panic: something exploded\n\ngoroutine 1 [running]:"
	evidence := ResolveGoDiagnostics(workspace, output)
	if len(evidence.Facts) != 1 || !strings.Contains(evidence.Facts[0], "panic: something exploded") {
		t.Fatalf("facts = %v, want the raw diagnostic passthrough", evidence.Facts)
	}
	if len(evidence.Lookups) != 0 || len(evidence.Symbols) != 0 {
		t.Fatalf("fallback must not invent lookups or symbols: %+v", evidence)
	}
}

func TestResolveGoDiagnosticsEmptyOutput(t *testing.T) {
	evidence := ResolveGoDiagnostics(t.TempDir(), "   ")
	if len(evidence.Symbols)+len(evidence.Files)+len(evidence.Facts)+len(evidence.Lookups) != 0 {
		t.Fatalf("empty output must produce an empty bundle: %+v", evidence)
	}
}
