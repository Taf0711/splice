package tools

import (
	"fmt"
	"strings"
	"testing"
)

// makeDiffSection builds one complete `git diff` file section: header, one
// hunk, and a padding line long enough to push the section past a small budget.
func makeDiffSection(name string, padBytes int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\nindex 1111111..2222222 100644\n--- a/%s\n+++ b/%s\n@@ -1,2 +1,2 @@\n context line\n",
		name, name, name, name)
	b.WriteString("+" + strings.Repeat("x", padBytes) + "\n")
	return b.String()
}

// TestTruncateDiffStructurallyKeepsWholeFiles is the core property: the cut
// lands on a section boundary, so no kept file is left half-applied.
func TestTruncateDiffStructurallyKeepsWholeFiles(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString(makeDiffSection(fmt.Sprintf("file%d.go", i), 8000))
	}
	value := b.String()
	if len(value) <= bashOutputBudgetBytes {
		t.Fatalf("fixture must exceed the budget, got %d bytes", len(value))
	}

	out, total, truncated := truncateDiffStructurally(value, len(value), bashOutputBudgetBytes)
	if !truncated {
		t.Fatal("oversized diff must report truncation")
	}
	if total != len(value) {
		t.Fatalf("total must stay the original length, got %d want %d", total, len(value))
	}
	if len(out) > bashOutputBudgetBytes {
		t.Fatalf("emitted diff must stay within the budget: %d > %d", len(out), bashOutputBudgetBytes)
	}
	markerAt := strings.Index(out, "\n[splice] output truncated structurally:")
	if markerAt < 0 {
		t.Fatalf("structural truncation must name the cut, got %q", out)
	}
	kept := out[:markerAt]
	// The kept text must be a byte-exact prefix of the original that ends
	// exactly where the next whole section begins.
	if !strings.HasPrefix(value, kept) {
		t.Fatal("kept text must be an exact prefix of the original diff")
	}
	rest := value[len(kept):]
	if !strings.HasPrefix(rest, "diff --git ") {
		t.Fatalf("the cut must land on a section boundary, next bytes = %q", firstLine(rest))
	}
	if strings.Count(kept, "diff --git ") < 1 {
		t.Fatalf("at least one whole file must be kept, got %q", kept)
	}
	if !strings.Contains(out, "changed files omitted") {
		t.Fatalf("marker must name the omitted files, got %q", out)
	}
}

// TestTruncateDiffStructurallyFallsBackForSingleFile: one file cannot be kept
// whole under the budget, so the byte-window path is the honest option.
func TestTruncateDiffStructurallyFallsBackForSingleFile(t *testing.T) {
	value := makeDiffSection("huge.go", 40000)
	out, _, truncated := truncateDiffStructurally(value, len(value), bashOutputBudgetBytes)
	if !truncated {
		t.Fatal("oversized single-file diff must report truncation")
	}
	if !strings.Contains(out, "bytes omitted from the middle") {
		t.Fatalf("single-file diff must fall back to the byte window, got %q", out)
	}
}

// TestTruncateDiffStructurallyIgnoresNonDiff: arbitrary command output keeps
// the existing head+tail behavior and is never re-ordered.
func TestTruncateDiffStructurallyIgnoresNonDiff(t *testing.T) {
	value := strings.Repeat("a plain log line\n", 9000)
	out, _, truncated := truncateDiffStructurally(value, len(value), bashOutputBudgetBytes)
	if !truncated {
		t.Fatal("oversized non-diff output must report truncation")
	}
	if !strings.Contains(out, "bytes omitted from the middle") {
		t.Fatalf("non-diff output must use the byte window, got %q", out)
	}
	if strings.Contains(out, "structurally") {
		t.Fatalf("non-diff output must not claim a structural cut, got %q", out)
	}
}

// TestTruncateDiffStructurallyFallsBackOnCaptureGap: when bounded capture
// already dropped the middle, the retained text is two disjoint windows.
// Joining sections across that gap would present them as one contiguous patch.
func TestTruncateDiffStructurallyFallsBackOnCaptureGap(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString(makeDiffSection(fmt.Sprintf("file%d.go", i), 8000))
	}
	value := b.String()
	out, _, truncated := truncateDiffStructurally(value, len(value)+5000, bashOutputBudgetBytes)
	if !truncated {
		t.Fatal("oversized diff must report truncation")
	}
	if !strings.Contains(out, "bytes omitted from the middle") {
		t.Fatalf("a capture gap must fall back to the byte window, got %q", out)
	}
}

// TestTruncateDiffStructurallyLeavesSmallDiffAlone pins the no-op case.
func TestTruncateDiffStructurallyLeavesSmallDiffAlone(t *testing.T) {
	value := makeDiffSection("small.go", 100)
	out, total, truncated := truncateDiffStructurally(value, len(value), bashOutputBudgetBytes)
	if truncated || out != value || total != len(value) {
		t.Fatalf("a diff within budget must pass through unchanged: truncated=%v total=%d", truncated, total)
	}
}

// TestSplitDiffSectionsKeepsPreamble: a `git show` commit block precedes the
// first header, and it must stay with the first section rather than be lost.
func TestSplitDiffSectionsKeepsPreamble(t *testing.T) {
	value := "commit abc123\nAuthor: A <a@b.c>\n\n" +
		makeDiffSection("one.go", 10) + makeDiffSection("two.go", 10)
	sections := splitDiffSections(value)
	if len(sections) != 2 {
		t.Fatalf("want 2 sections, got %d", len(sections))
	}
	if !strings.HasPrefix(sections[0], "commit abc123\n") {
		t.Fatalf("preamble must attach to the first section, got %q", firstLine(sections[0]))
	}
	if !strings.HasPrefix(sections[1], "diff --git a/two.go") {
		t.Fatalf("second section must start at its own header, got %q", firstLine(sections[1]))
	}
	if joined := strings.Join(sections, ""); joined != value {
		t.Fatal("splitting must not change a single byte")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
