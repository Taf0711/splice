package splice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/stages"
)

// TestRepairReentryReadsTheFilesTheRunChanged: the repair edits the files
// the run changed, so the re-entry's first round must read them. P2b: the
// repair read only the failing test, spent both expansion rounds finding
// its own edits, and exhausted the budget.
func TestRepairReentryReadsTheFilesTheRunChanged(t *testing.T) {
	dir := t.TempDir()
	for rel, body := range map[string]string{
		"go.mod":                              "module demo\n",
		"internal/notifications/catalog.go":   "package notifications\n",
		"internal/notifications/templates.go": "package notifications\n",
		"cmd/server/catalog_test.go":          "package main\n",
	} {
		writeTestFile(t, dir, rel, body)
	}
	evidence := buildFocusedRevisionContext(focusedRepairPayload{
		Intent:       "add an invoice-due template",
		ChangedFiles: []string{"internal/notifications/catalog.go", "internal/notifications/templates.go"},
		FailureText:  "--- FAIL: TestEveryNotificationKindIsInTheCatalog\ncmd/server/catalog_test.go:17: not listed",
	}, "Fix the failing test.")
	var reads []string
	for _, q := range stages.DefaultContextRequestFor("add an invoice-due template", evidence, dir, "go").Queries {
		if q.Path != nil {
			reads = append(reads, *q.Path)
		}
	}
	got := strings.Join(reads, ",")
	for _, want := range []string{"internal/notifications/catalog.go", "internal/notifications/templates.go", "cmd/server/catalog_test.go"} {
		if !strings.Contains(got, want) {
			t.Fatalf("repair first round %v lacks %s", reads, want)
		}
	}
}

// TestRepairReentryReadsTheFailingTestFromRealGoOutput: go test names the
// failing file bare ("catalog_test.go:16:") and gives its package in the
// JSON line. P2d cpl-01: the bare name took the last read slot, the repair
// spent its first expansion finding the test, and 11 of 11 failures ran out
// of expansions one read short of the coupled file.
func TestRepairReentryReadsTheFailingTestFromRealGoOutput(t *testing.T) {
	dir := t.TempDir()
	for rel, body := range map[string]string{
		"go.mod":                              "module demo\n",
		"internal/notifications/catalog.go":   "package notifications\n",
		"internal/notifications/templates.go": "package notifications\n",
		"cmd/server/catalog_test.go":          "package main\n",
	} {
		writeTestFile(t, dir, rel, body)
	}
	stdout := `{"Action":"output","Package":"demo/cmd/server","Test":"TestEveryNotificationKindIsInTheCatalog","Output":"    catalog_test.go:1: notification kind \"invoice_due\" has a template but the service catalog does not list it\n"}`
	evidence := buildFocusedRevisionContext(focusedRepairPayload{
		Intent:       "add an invoice-due template",
		ChangedFiles: []string{"internal/notifications/catalog.go", "internal/notifications/templates.go"},
		FailureText:  `TestEveryNotificationKindIsInTheCatalog: catalog_test.go:1: notification kind "invoice_due" has a template but the service catalog does not list it`,
		Evidence:     ResolveGoDiagnostics(dir, stdout),
	}, "Fix the failing test.")
	var reads []string
	for _, q := range stages.DefaultContextRequestFor("add an invoice-due template", evidence, dir, "go").Queries {
		if q.Path != nil {
			reads = append(reads, *q.Path)
		}
	}
	if !strings.Contains(strings.Join(reads, ","), "cmd/server/catalog_test.go") {
		t.Fatalf("repair first round %v lacks the failing test", reads)
	}
}

func writeTestFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
