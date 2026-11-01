package stages

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// The P4 live smoke failure: the compact-1 modify contract required a
// base_ref the model could not know. The host mints the handle from the
// delivered view text and never shows it; the model invented two
// handle-shaped strings, both rejected, and the modify was abandoned.
// These tests pin the repair: the delivered view's PATH resolves to the
// same snapshot, an invented handle still fails loud, and the model-facing
// wording no longer teaches the unformattable form.

func baseRefItem(path, version, text string) schemas.ContextItem {
	return schemas.ContextItem{
		Query:   schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: &path},
		Summary: "Read " + path + ".",
		Payload: map[string]any{"text": text, "path": path, "version": version},
	}
}

func baseRefFixture(t *testing.T) *ProposalBaseRegistry {
	t.Helper()
	r := NewProposalBaseRegistry()
	r.RecordFromBundle(&schemas.ContextBundle{Items: []schemas.ContextItem{
		baseRefItem("clock_test.go", "sha-clock-test", "func runClockTable(t *testing.T) {}\nfunc clockCase(tt *testing.T) clockCaseT {}\n"),
		baseRefItem("clock.go", "sha-clock", "func NewClockStore() {}\n"),
	}})
	return r
}

// TestResolveAcceptsTheDeliveredViewPath pins the path alias: the model
// cites the file exactly as its context view named it, and that resolves
// to the same snapshot the host-minted handle returns.
func TestResolveAcceptsTheDeliveredViewPath(t *testing.T) {
	r := baseRefFixture(t)
	byPath, ok := r.Resolve("clock_test.go")
	if !ok {
		t.Fatal("the delivered view's path did not resolve")
	}
	byHandle, ok := r.Resolve(HandleFor(contentDigest("func runClockTable(t *testing.T) {}\nfunc clockCase(tt *testing.T) clockCaseT {}\n")))
	if !ok {
		t.Fatal("the host-minted handle did not resolve")
	}
	if byPath.Base != byHandle.Base || byPath.Path != byHandle.Path {
		t.Fatalf("path alias resolved to a different snapshot: path=%+v handle=%+v", byPath, byHandle)
	}
}

// TestResolveStillRejectsInventedHandles is the recorded failure, kept as
// a regression: guessed handle-shaped strings resolve to nothing and fail
// loud, so a wrong citation can never silently pick a base.
func TestResolveStillRejectsInventedHandles(t *testing.T) {
	r := baseRefFixture(t)
	for _, guess := range []string{
		"clock_test.go@read_file:49-lines",
		"clock_test.go@view1",
		"never-delivered.go",
	} {
		if _, ok := r.Resolve(guess); ok {
			t.Fatalf("invented or undelivered base_ref %q resolved", guess)
		}
	}
}

// TestBaseRefWordingNamesThePath pins the model-facing contract: both the
// schema description and the prompt must teach the path form the model can
// actually produce. The recorded smoke failure taught "<handle from your
// context views>" while no view ever showed a handle.
func TestBaseRefWordingNamesThePath(t *testing.T) {
	def := submitCodeToolDefinition()
	items := def.Parameters["properties"].(map[string]any)["files"].(map[string]any)["items"].(map[string]any)
	props := items["properties"].(map[string]any)
	desc, _ := props["base_ref"].(map[string]any)["description"].(string)
	if !strings.Contains(desc, "path of the delivered view") {
		t.Fatalf("base_ref schema description still teaches the handle form: %q", desc)
	}
	if !strings.Contains(desc, "clock_test.go") {
		t.Fatalf("base_ref schema description carries no concrete path example: %q", desc)
	}
	if !strings.Contains(codeWriterSystemPrompt, "the path of the delivered view") {
		t.Fatal("prompt still does not name the path form for base_ref")
	}
	if strings.Contains(codeWriterSystemPrompt, "<handle from your context views>") {
		t.Fatal("prompt still teaches the unformattable handle placeholder")
	}
}
