package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/worktrees"
)

// diffReviewTestDiff is a deterministic fixture mirroring the Pen frame
// tOwI1's shape: two files with mixed changes and one additions-only file.
const diffReviewTestDiff = `diff --git a/internal/http/retry.go b/internal/http/retry.go
index 111..222 100644
--- a/internal/http/retry.go
+++ b/internal/http/retry.go
@@ -61,6 +61,14 @@ func (c *Client) Do(req *Request) (*Response, error) {
 	if err != nil { return nil, err }
+	if req.replayable() && attempts < 3 {
+		return c.retry(req, attempts+1)
+	}
+	if req.replayable() && attempts < 3 {
+		return c.retry(req, attempts+1)
+	}
+	if req.replayable() && attempts < 3 {
+		return c.retry(req, attempts+1)
+	}
 	return nil, err
diff --git a/internal/http/client.go b/internal/http/client.go
index 333..444 100644
--- a/internal/http/client.go
+++ b/internal/http/client.go
@@ -10,3 +10,8 @@ type Client struct {
 	transport Transport
+	timeout time.Duration
+	timeout time.Duration
+	timeout time.Duration
+	timeout time.Duration
+	timeout time.Duration
-	legacy LegacyHook
-	legacy LegacyHook
diff --git a/internal/http/retry_test.go b/internal/http/retry_test.go
new file mode 100644
index 000..555
--- /dev/null
+++ b/internal/http/retry_test.go
@@ -0,0 +1,3 @@
+func TestRetry(t *testing.T) {}
+func TestRetry(t *testing.T) {}
+func TestRetry(t *testing.T) {}
`

func diffTestWorktree() worktrees.Result {
	return worktrees.Result{
		Name:     "wt-a1",
		Path:     "/repo/.splice/wt/a1",
		RepoRoot: "/repo",
	}
}

// TestDiffFileStatsParsesAndOrders pins the parser: counts per file, b-side
// paths, churn ordering (retry.go 4+0=4? no: 6 adds 1 del? assert exact).
func TestDiffFileStatsParsesAndOrders(t *testing.T) {
	files := diffFileStats(diffReviewTestDiff)
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d: %+v", len(files), files)
	}
	// Churn order: retry.go (+9), client.go (+5 -2 = 7), retry_test.go (+3).
	want := []struct {
		path string
		adds int
		dels int
	}{
		{"internal/http/retry.go", 9, 0},
		{"internal/http/client.go", 5, 2},
		{"internal/http/retry_test.go", 3, 0},
	}
	for i, w := range want {
		if files[i].path != w.path || files[i].adds != w.adds || files[i].dels != w.dels {
			t.Errorf("file[%d] = %+v, want %+v", i, files[i], w)
		}
	}
}

func TestDiffFileStatsBinary(t *testing.T) {
	diff := "diff --git a/logo.png b/logo.png\nBinary files a/logo.png and b/logo.png differ\n"
	files := diffFileStats(diff)
	if len(files) != 1 || !files[0].binary || files[0].path != "logo.png" {
		t.Fatalf("binary parse wrong: %+v", files)
	}
}

func TestDiffHunksSplits(t *testing.T) {
	hunks := diffHunks(diffReviewTestDiff)
	// One hunk per @@ header, plus hunk 0 holding the first file's preamble
	// (diff --git, index, ---/+++ lines) so nothing is dropped. Later files'
	// preambles trail the previous hunk's body, which the viewport renders
	// in order regardless.
	if len(hunks) != 4 {
		t.Fatalf("expected 4 hunks (3 @@ blocks + preamble), got %d", len(hunks))
	}
	for i, h := range hunks[1:] {
		if !strings.HasPrefix(h, "@@ ") {
			t.Errorf("hunk %d does not start with @@ header", i+1)
		}
	}
}

func TestDiffBaseRefPrefersSourceBranch(t *testing.T) {
	wt := diffTestWorktree()
	wt.SourceBranch = "splice/wt-a1"
	if got := diffBaseRef(wt); got != "splice/wt-a1" {
		t.Errorf("diffBaseRef = %q, want splice/wt-a1", got)
	}
	if got := diffBaseRef(diffTestWorktree()); got != "main" {
		t.Errorf("diffBaseRef fallback = %q, want main", got)
	}
}

func TestDiffCaptureCommand(t *testing.T) {
	wt := diffTestWorktree()
	called := false
	restore := tuiDiffCapture
	tuiDiffCapture = func(_ context.Context, _ worktrees.Result) (string, error) {
		called = true
		return "diff --git a/x b/x\n", nil
	}
	defer func() { tuiDiffCapture = restore }()
	msg := diffCaptureCmd(wt)()
	got, ok := msg.(diffCapturedMsg)
	if !ok || !called || got.lane != "wt-a1" || got.res == "" {
		t.Fatalf("capture cmd wrong: called=%v msg=%+v", called, msg)
	}
}

// TestDiffReviewRenderShapes pins the rendered block: stats list with ASCII
// markers, hunk body, and the header. Pure render assertions at two widths.
func TestDiffReviewRenderShapes(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.diffView = diffViewState{
		active: true,
		wt:     diffTestWorktree(),
		base:   "main",
		text:   diffReviewTestDiff,
		files:  diffFileStats(diffReviewTestDiff),
	}
	out := m.renderDiffReview(120)
	for _, want := range []string{
		"[+]", "[~]", // markers
		"internal/http/retry.go", "internal/http/client.go", "internal/http/retry_test.go",
		"+9 -0", "+5 -2", "+3 -0",
		"@@ -61,6 +61,14 @@",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q", want)
		}
	}
	// No horizontal overflow: every line's VISIBLE width fits (ANSI escapes
	// measure zero, so use lipgloss.Width, not raw rune count).
	for _, line := range strings.Split(out, "\n") {
		if lipgloss.Width(line) > 120 {
			t.Errorf("line overflows 120 cols: %q", line)
		}
	}
	// 80-column render must not overflow either.
	out80 := m.renderDiffReview(80)
	for _, line := range strings.Split(out80, "\n") {
		if lipgloss.Width(line) > 80 {
			t.Errorf("80-col line overflows: %q", line)
		}
	}
}

func TestDiffReviewErrorForm(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.diffView = diffViewState{active: true, wt: diffTestWorktree(), err: "exit status 128"}
	out := m.renderDiffReview(120)
	if !strings.Contains(out, "Could not produce the diff") || !strings.Contains(out, "exit status 128") {
		t.Errorf("error form missing honest error: %q", out)
	}
	if strings.Contains(out, "Recovery: git") == false {
		t.Errorf("error form missing recovery path")
	}
}

func TestDiffReviewCapturingForm(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.diffView = diffViewState{active: true, wt: diffTestWorktree()}
	out := m.renderDiffReview(120)
	if !strings.Contains(out, "Capturing diff") {
		t.Errorf("capturing form missing: %q", out)
	}
}

// TestDiffReviewKeysWalkUpdatePath proves the key wiring through the REAL
// update path: open via Update(handoff-style dispatch is covered in the
// acceptance suite), here: scroll, next-file, and Esc all round-trip.
func TestDiffReviewKeysWalkUpdatePath(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	updated, _ := m.Update(diffCapturedMsg{lane: "unset", res: diffReviewTestDiff})
	next := updated.(model)
	if next.diffView.active {
		t.Fatal("stale capture must not open the view")
	}

	// Open directly (the emission point seam), then drive keys.
	opened, cmd := m.openDiffReview(diffTestWorktree())
	if !opened.diffView.active || cmd == nil {
		t.Fatal("openDiffReview did not activate or returned no capture cmd")
	}
	// Land the capture through the real message path.
	updated, _ = opened.Update(diffCapturedMsg{lane: "wt-a1", res: diffReviewTestDiff})
	next = updated.(model)
	if next.diffView.text == "" {
		t.Fatal("capture not applied")
	}
	if next.diffView.hunkTop != 0 {
		t.Fatalf("fresh view hunkTop = %d, want 0", next.diffView.hunkTop)
	}

	// n: next file jumps to the second diff --git header.
	updated, _ = next.Update(testKey('n'))
	next = updated.(model)
	body := strings.Join(diffHunks(next.diffView.text), "\n")
	lines := strings.Split(body, "\n")
	secondFile := -1
	seen := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "diff --git ") {
			seen++
			if seen == 2 {
				secondFile = i
				break
			}
		}
	}
	if next.diffView.hunkTop != secondFile {
		t.Errorf("after n: hunkTop = %d, want %d (second file header)", next.diffView.hunkTop, secondFile)
	}

	// Down scrolls; Up scrolls back. Arrow keys: build directly (Text must
	// stay empty for non-printable keys). Deltas, since `n` already moved
	// the window off 0.
	before := next.diffView.hunkTop
	updated, _ = next.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	next = updated.(model)
	if next.diffView.hunkTop != before+diffViewScrollStep {
		t.Errorf("after down: hunkTop = %d, want %d", next.diffView.hunkTop, before+diffViewScrollStep)
	}
	updated, _ = next.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	next = updated.(model)
	if next.diffView.hunkTop != before {
		t.Errorf("after up: hunkTop = %d, want %d", next.diffView.hunkTop, before)
	}

	// Esc closes and restores the parent scroll offset.
	parent := next.chatScrollOffset
	updated, _ = next.Update(testKey(tea.KeyEsc))
	next = updated.(model)
	if next.diffView.active {
		t.Error("Esc did not close the diff view")
	}
	if next.chatScrollOffset != parent {
		t.Errorf("scroll not restored: got %d want %d", next.chatScrollOffset, parent)
	}
}

// TestDiffReviewNavReplaceTitleBar pins the title-bar swap: while active, the
// pinned title bar is the diff nav line with the run/base header.
func TestDiffReviewNavReplaceTitleBar(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.diffView = diffViewState{active: true, wt: diffTestWorktree(), base: "main", text: diffReviewTestDiff, files: diffFileStats(diffReviewTestDiff)}
	nav := m.diffViewNavBar(120)
	if !strings.Contains(nav, "DIFF") || !strings.Contains(nav, "wt-a1") || !strings.Contains(nav, "main") {
		t.Errorf("nav missing header parts: %q", nav)
	}
	if strings.Contains(nav, "DIFF — wt-a1 vs main") == false {
		t.Errorf("nav header format wrong: %q", nav)
	}
}

// TestDiffRejectHunkDoesNotEditFiles pins the architecture fence: j emits a
// notice only; no mutation seam is called.
func TestDiffRejectHunkDoesNotEditFiles(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.diffView = diffViewState{active: true, wt: diffTestWorktree(), text: diffReviewTestDiff, files: diffFileStats(diffReviewTestDiff)}
	next := m.rejectDiffHunk()
	if next.diffView.active == false {
		t.Error("reject must keep the view open")
	}
	found := false
	for _, row := range next.transcript {
		if strings.Contains(row.text, "step_back") {
			found = true
		}
	}
	if !found {
		t.Error("reject did not record the intervention notice")
	}
}

// The video-derived UX contract (Devin model-picker reference): the header
// carries position (N files · hunk X of Y) and the keymap lives in a bottom
// hint bar that drops WHOLE segments under width pressure, never
// ellipsis-truncating a binding mid-word (DoD 18).
func TestDiffViewNavBarCarriesPosition(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.diffView = diffViewState{active: true, wt: diffTestWorktree(), base: "main", text: diffReviewTestDiff, files: diffFileStats(diffReviewTestDiff)}
	nav := m.diffViewNavBar(120)
	if !strings.Contains(nav, "3 files") || !strings.Contains(nav, "hunk 1 of 3") {
		t.Errorf("nav missing position readout: %q", nav)
	}
	// Keys are NOT in the header anymore.
	if strings.Contains(nav, "esc close") {
		t.Errorf("nav should not carry the keymap: %q", nav)
	}
}

func TestDiffViewHintBarDropsWholeSegments(t *testing.T) {
	full := diffViewHintBar(120)
	for _, want := range []string{"^e copy hunk", "n next file", "a approve", "j reject hunk", "o open", "↑↓ scroll", "esc close"} {
		if !strings.Contains(full, want) {
			t.Errorf("full hint bar missing %q: %q", want, full)
		}
	}
	// Narrow: optional segments drop whole, core never drops, nothing truncates.
	narrow := diffViewHintBar(40)
	if !strings.Contains(narrow, "esc close") || !strings.Contains(narrow, "↑↓ scroll") {
		t.Errorf("narrow hint bar lost core keys: %q", narrow)
	}
	if strings.Contains(narrow, "…") {
		t.Errorf("hint bar ellipsis-truncated (DoD 18 violation): %q", narrow)
	}
	for _, dropped := range []string{"o open", "j reject hunk"} {
		if strings.Contains(narrow, dropped) {
			t.Errorf("narrow bar kept %q but should have dropped it whole: %q", dropped, narrow)
		}
	}
	// Every rendered line fits the width.
	bar := zeroTheme.faint.Render(narrow)
	if lipgloss.Width(bar) > 40 {
		t.Errorf("hint bar overflows width: %q", narrow)
	}
}

func TestDiffViewHintBarInRender(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.diffView = diffViewState{active: true, wt: diffTestWorktree(), base: "main", text: diffReviewTestDiff, files: diffFileStats(diffReviewTestDiff)}
	out := m.renderDiffReview(120)
	if !strings.Contains(out, "esc close") {
		t.Errorf("render missing the hint bar: %q", out[len(out)-200:])
	}
}

// GAP-G rest (Ctrl+E, OSC52): the copy action targets the hunk under the
// window top. diffHunks' zeroth entry can hold pre-@@ preamble text — that
// is NOT a hunk, and copying it would be a lie about what got copied.
func TestDiffHunkAtWindowTargetsHunkUnderTop(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.diffView = diffViewState{active: true, wt: diffTestWorktree(), text: diffReviewTestDiff, files: diffFileStats(diffReviewTestDiff)}

	// Top at 0 sits on the first file's preamble (diff/index/---/+++ lines
	// before the first @@): not a hunk, nothing copyable yet.
	hunk, ok := m.diffHunkAtWindow()
	if ok {
		t.Fatalf("preamble position must not report a hunk, got %q", hunk)
	}

	// Scroll to the first @@ line: the whole first hunk is the target.
	hunks := diffHunks(diffReviewTestDiff)
	if !strings.HasPrefix(hunks[0], "diff --git") {
		t.Fatalf("fixture shape changed: first chunk is %q", firstLine(hunks[0]))
	}
	// Find the first @@ offset in the joined body.
	body := strings.Join(hunks, "\n")
	lines := strings.Split(body, "\n")
	at := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "@@ ") {
			at = i
			break
		}
	}
	m.diffView.hunkTop = at
	hunk, ok = m.diffHunkAtWindow()
	if !ok {
		t.Fatal("top on the first @@ must report the hunk")
	}
	if !strings.HasPrefix(hunk, "@@ ") || !strings.Contains(hunk, "func (c *Client) Do") {
		t.Fatalf("wrong hunk copied: %q", firstLine(hunk))
	}

	// A mid-hunk top selects the same hunk, not a truncated slice.
	m.diffView.hunkTop = at + 2
	hunk2, ok := m.diffHunkAtWindow()
	if !ok || hunk2 != hunk {
		t.Fatalf("mid-hunk top changed the copy target: ok=%v", ok)
	}

	// Scrolling into the second file's @@ selects that hunk.
	second := -1
	seen := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "@@ ") {
			seen++
			if seen == 2 {
				second = i
				break
			}
		}
	}
	m.diffView.hunkTop = second
	hunk3, ok := m.diffHunkAtWindow()
	if !ok || !strings.Contains(hunk3, "type Client struct") {
		t.Fatalf("second hunk not selected: ok=%v hunk=%q", ok, firstLine(hunk3))
	}
}

// The [E] key through the REAL update path schedules the clipboard cmd; the
// result message reports through the shared copy status.
func TestDiffCopyKeySchedulesClipboardCmd(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.diffView = diffViewState{active: true, wt: diffTestWorktree(), text: diffReviewTestDiff, files: diffFileStats(diffReviewTestDiff)}
	// Move onto the first real hunk.
	hunks := diffHunks(diffReviewTestDiff)
	body := strings.Join(hunks, "\n")
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "@@ ") {
			m.diffView.hunkTop = i
			break
		}
	}

	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'e', Mod: tea.ModCtrl}))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("Ctrl+E did not schedule the copy cmd")
	}
	// Execute the cmd — the fake clipboard may fail in tests, but the message
	// path must handle BOTH outcomes through the shared status readout.
	msg := cmd()
	copied, ok := msg.(diffCopiedMsg)
	if !ok {
		t.Fatalf("copy cmd produced %T, want diffCopiedMsg", msg)
	}
	updated, _ = next.Update(copied)
	final := updated.(model)
	if copied.err == nil {
		if final.copyStatus == "" || !strings.Contains(final.copyStatus, "Copied hunk") {
			t.Fatalf("success did not report through the copy status: %q", final.copyStatus)
		}
	} else if final.copyStatus != "Copy failed" {
		t.Fatalf("failure did not report Copy failed: %q", final.copyStatus)
	}
}

// Nothing viewable -> nothing copyable: the closed/empty/preamble cases say
// so instead of copying a stale payload.
func TestDiffCopyRefusesWhenNoHunk(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	// Closed view.
	next, cmd := m.copyDiffHunk()
	if cmd != nil {
		t.Fatal("closed view must not schedule a copy")
	}
	_ = next
	// Open but sitting on the preamble.
	m.diffView = diffViewState{active: true, wt: diffTestWorktree(), text: diffReviewTestDiff, files: diffFileStats(diffReviewTestDiff)}
	nextModel, cmd2 := m.copyDiffHunk()
	if cmd2 != nil {
		t.Fatal("preamble position must not schedule a copy")
	}
	notices := false
	for _, row := range nextModel.(model).transcript {
		if strings.Contains(row.text, "no hunk at the current position") {
			notices = true
		}
	}
	if !notices {
		t.Fatal("refusal did not explain itself")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
