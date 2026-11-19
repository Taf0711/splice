package stages

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// thirtyLines is a 30-line file whose lines carry their own numbers.
func thirtyLines() string {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("L%02d", i+1)
	}
	return strings.Join(lines, "\n")
}

// numbered renders lines the way read_file displays them: a padded line
// number, " | ", then the line.
func numbered(lines []string, from int) string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = fmt.Sprintf("%2d | %s", from+i, line)
	}
	return strings.Join(out, "\n")
}

// viewOf builds one delivered read item the way fulfillReadFile does: the
// whole-file digest and raw bytes, the view text, and the view's range.
func viewOf(path, raw, text string, start, end int, truncated bool) schemas.ContextItem {
	p := path
	return schemas.ContextItem{
		Query:     schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: &p},
		Summary:   "Read " + path + ".",
		Payload:   map[string]any{"text": text, "path": path, "version": ContentDigest(raw), "start": start, "end": end, "raw": raw},
		Truncated: truncated,
	}
}

func registryOf(items ...schemas.ContextItem) *ProposalBaseRegistry {
	r := NewProposalBaseRegistry()
	r.RecordFromBundle(&schemas.ContextBundle{Items: items})
	return r
}

func contentModify(path, content string) ProposedFileChange {
	return ProposedFileChange{Path: path, ChangeType: "modify", BaseRef: path, Content: &content}
}

// TestContentModifyOnPartialRangesFailsLoudly pins audit item F1: a whole-file
// content modify composed against two ranged views must not apply. It would
// truncate the 30-line file to the six lines the model saw.
func TestContentModifyOnPartialRangesFailsLoudly(t *testing.T) {
	raw := thirtyLines()
	r := registryOf(
		viewOf("m.py", raw, numbered([]string{"L01", "L02", "L03"}, 1), 1, 3, false),
		viewOf("m.py", raw, numbered([]string{"L20", "L21", "L22"}, 20), 20, 22, false),
	)
	_, err := MaterializeProposal(contentModify("m.py", "L01\nL02\nL03\nL20\nL21\nL22"), r.Resolve)
	if err == nil || !strings.Contains(err.Error(), "partial view of m.py") {
		t.Fatalf("err = %v, want a loud partial-view rejection; the file must not be truncated", err)
	}
}

// TestContentModifyOnTruncatedReadFailsLoudly pins F1 for a whole-file read
// that MaxChars cut short.
func TestContentModifyOnTruncatedReadFailsLoudly(t *testing.T) {
	raw := thirtyLines()
	lines := strings.Split(raw, "\n")
	lines[14] = "X15"
	r := registryOf(viewOf("m.py", raw, numbered(lines[:10], 1), 1, 30, true))
	_, err := MaterializeProposal(contentModify("m.py", strings.Join(lines, "\n")), r.Resolve)
	if err == nil || !strings.Contains(err.Error(), "partial view of m.py") {
		t.Fatalf("err = %v, want a loud partial-view rejection for a truncated read", err)
	}
}

// TestContentModifyOnCompleteViewStillWorks pins that a content modify against
// the whole file, delivered in full, still applies.
func TestContentModifyOnCompleteViewStillWorks(t *testing.T) {
	raw := thirtyLines()
	lines := strings.Split(raw, "\n")
	lines[14] = "X15"
	want := strings.Join(lines, "\n")
	r := registryOf(viewOf("m.py", raw, numbered(lines, 1), 1, 30, false))
	got, err := MaterializeProposal(contentModify("m.py", want), r.Resolve)
	if err != nil {
		t.Fatalf("a complete view must accept a content modify: %v", err)
	}
	if got.Content != want {
		t.Fatalf("content = %d bytes, want the 30-line file with the change (%d bytes)", len(got.Content), len(want))
	}
}

// TestGapSpanningEditFailsLoudly pins that an edit whose old text spans the
// gap between two ranged views fails loudly rather than applying.
func TestGapSpanningEditFailsLoudly(t *testing.T) {
	raw := thirtyLines()
	r := registryOf(
		viewOf("m.py", raw, numbered([]string{"L01", "L02", "L03"}, 1), 1, 3, false),
		viewOf("m.py", raw, numbered([]string{"L20", "L21", "L22"}, 20), 20, 22, false),
	)
	edit := ProposedFileChange{Path: "m.py", ChangeType: "modify", BaseRef: "m.py",
		Edits: []TextReplacement{{Old: " 3 | L03\n20 | L20", New: " 3 | L03\n20 | L20X"}}}
	if _, err := MaterializeProposal(edit, r.Resolve); err == nil {
		t.Fatal("an edit spanning the gap between two views must fail loudly")
	}
}

// TestCreateOverPartiallySeenFileFailsLoudly pins audit item R1: a create
// for a file the model saw only in part must not replace the file.
func TestCreateOverPartiallySeenFileFailsLoudly(t *testing.T) {
	raw := thirtyLines()
	r := registryOf(viewOf("m.py", raw, numbered([]string{"L01", "L02", "L03"}, 1), 1, 3, false))
	_, err := MaterializeProposal(ProposedFileChange{Path: "m.py", ChangeType: "create", Content: ptr("L01\nL02\nL03\nL04\nL05\nL06")}, r.Resolve)
	if err == nil || !strings.Contains(err.Error(), "seen only in part") {
		t.Fatalf("err = %v, want a loud refusal to replace a partly seen file", err)
	}
}

// TestCreateOfNewFileStillWorks pins that a create for a path with no
// snapshot still applies.
func TestCreateOfNewFileStillWorks(t *testing.T) {
	r := NewProposalBaseRegistry()
	got, err := MaterializeProposal(ProposedFileChange{Path: "new.py", ChangeType: "create", Content: ptr("x = 1\n")}, r.Resolve)
	if err != nil || got.ChangeType != "create" {
		t.Fatalf("create of a new file must apply: %v %+v", err, got)
	}
}

// TestRepairReEmitOverWhollySeenFileStillWorks pins that a create over a file
// whose whole content was delivered still applies (repair re-emit).
func TestRepairReEmitOverWhollySeenFileStillWorks(t *testing.T) {
	raw := thirtyLines()
	r := registryOf(viewOf("m.py", raw, numbered(strings.Split(raw, "\n"), 1), 1, 30, false))
	if _, err := MaterializeProposal(ProposedFileChange{Path: "m.py", ChangeType: "create", Content: ptr(raw + "\n")}, r.Resolve); err != nil {
		t.Fatalf("re-emit over a wholly delivered file must apply: %v", err)
	}
}
