package stages

import (
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// rangedItem is a delivered read of one line range of one file version.
func rangedItem(path string, start, end int) schemas.ContextItem {
	p := path
	return schemas.ContextItem{
		Query:   schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: &p},
		Summary: "Read " + path + ".",
		Payload: map[string]any{
			"text":    "lines for " + path,
			"path":    path,
			"version": "v1",
			"start":   start,
			"end":     end,
		},
	}
}

// TestTwoRangesOfOneFileBothFormat pins audit item E04: two different line
// ranges of one file in one bundle are two views. Both must reach the model.
func TestTwoRangesOfOneFileBothFormat(t *testing.T) {
	bundle := &schemas.ContextBundle{Items: []schemas.ContextItem{
		rangedItem("big.txt", 1, 2),
		rangedItem("big.txt", 40, 42),
	}}
	if got := formatContextBundle(bundle); len(got) != 2 {
		t.Fatalf("formatted %d view(s), want both ranges of big.txt", len(got))
	}
}
