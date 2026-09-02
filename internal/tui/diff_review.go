// diff_review.go is the GAP-G diff review viewport (contract §11, Pen frame
// tOwI1): a viewport-scrolled, terminal-native diff of the exited lane's
// worktree against its base branch. It reuses the file drill-in's proven swap
// mechanism — while active, the transcript body swaps to the diff block and
// the title bar swaps to a one-line nav bar, so the scroll engine, viewport,
// and mouse hit-tests stay consistent without new geometry.
//
// Architecture fence (§3): the renderer never computes or mutates diffs. The
// diff text comes from the worktrees runner (tuiDiffCapture), the approve-all
// action dispatches the review's Accept seam (applyWorktreeReview), and a
// hunk rejection emits a runtime intervention notice; the pane never edits
// files itself. UI is a projection.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"

	"github.com/Taf0711/splice/internal/worktrees"
)

const (
	// diffViewMaxBlockLines caps the rendered diff body so a giant diff
	// cannot freeze a render (same protection as fileViewMaxLines).
	diffViewMaxBlockLines = 4000
	// diffViewMaxFiles caps the per-file stat list; the overflow row says
	// how many more exist rather than growing without bound.
	diffViewMaxFiles = 64
	// diffViewScrollStep is the lines per scroll step inside the hunk body.
	diffViewScrollStep = 3
	// diffViewHeaderLines is the fixed height of the stats list + blank line
	// above the hunk viewport. Scroll clamping keys on it so the header
	// stays pinned while hunks scroll.
	diffViewHeaderLines = 2
)

// diffViewState manages the diff review surface for one worktree. Modeled on
// fileViewState: when active, the transcript body swaps to the diff block.
type diffViewState struct {
	active  bool
	wt      worktrees.Result // the lane's worktree (Name, Path, RepoRoot, SourceBranch)
	base    string           // base ref the diff is taken against
	text    string           // full diff body (raw patch, from tuiDiffCapture)
	err     string           // non-empty when the diff could not be produced
	files   []diffFileInfo   // per-file stats parsed from the same text
	hunkTop int              // first visible line of the hunk body
	// parentScrollOffset preserves the chat scroll position so closing the
	// view returns to the same spot (mirrors fileViewState).
	parentScrollOffset int
}

// diffFileInfo is one file's stat row.
type diffFileInfo struct {
	path   string
	adds   int
	dels   int
	binary bool
}

// openDiffReview activates the diff view for a worktree. The capture runs in
// a tea.Cmd (off the UI loop); the first frame shows "Capturing diff…" and
// the diffCapturedMsg fills it. A failed capture renders an honest error
// block with the recovery path, never a bare line (§16).
func (m model) openDiffReview(wt worktrees.Result) (model, tea.Cmd) {
	if strings.TrimSpace(wt.Path) == "" {
		return m, nil
	}
	if m.diffView.active && m.diffView.wt.Name == wt.Name {
		return m, nil
	}
	parent := 0
	if m.diffView.active {
		parent = m.diffView.parentScrollOffset
	} else {
		parent = m.chatScrollOffset
	}
	m.diffView = diffViewState{
		active:             true,
		wt:                 wt,
		base:               diffBaseRef(wt),
		hunkTop:            0,
		parentScrollOffset: parent,
	}
	m.chatScrollOffset = 0
	m = m.clearHover()
	return m, diffCaptureCmd(wt)
}

// exitDiffReview deactivates the view and restores the chat scroll position.
func (m model) exitDiffReview() model {
	if !m.diffView.active {
		return m
	}
	m.chatScrollOffset = m.diffView.parentScrollOffset
	m.diffView = diffViewState{}
	m = m.clearHover()
	return m
}

// diffBaseRef picks the diff base: the worktree's recorded source branch when
// present, else the repo's default branch. The three-dot form
// (<base>...HEAD) gives the merge-base diff, which is what a review wants:
// the branch's changes only, not main's own drift.
func diffBaseRef(wt worktrees.Result) string {
	if b := strings.TrimSpace(wt.SourceBranch); b != "" {
		return b
	}
	return "main"
}

// tuiDiffCapture runs `git diff --no-color <base>...HEAD` inside the worktree
// and returns the raw patch text. It is a seam var so tests can replace it;
// the default is worktrees.GitCapture, keeping every git exec on the
// package's single runner path.
var tuiDiffCapture = func(ctx context.Context, wt worktrees.Result) (string, error) {
	res, err := worktrees.GitCapture(ctx, nil, wt.Path,
		"diff", "--no-color", diffBaseRef(wt)+"...HEAD")
	if err != nil {
		return "", err
	}
	return res.Stdout, nil
}

// diffCapturedMsg lands the captured diff on the model. It carries the lane
// name so a stale capture (view closed or reopened mid-flight) is dropped.
type diffCapturedMsg struct {
	lane string
	res  string
	err  error
}

// diffCaptureCmd runs the capture off the UI loop.
func diffCaptureCmd(wt worktrees.Result) tea.Cmd {
	return func() tea.Msg {
		text, err := tuiDiffCapture(context.Background(), wt)
		return diffCapturedMsg{lane: wt.Name, res: text, err: err}
	}
}

// handleDiffCaptured applies a capture result to the active view. Stale
// results (lane mismatch) are dropped: the view belongs to another lane.
func (m model) handleDiffCaptured(msg diffCapturedMsg) model {
	if !m.diffView.active || m.diffView.wt.Name != msg.lane {
		return m
	}
	if msg.err != nil {
		m.diffView.err = msg.err.Error()
		m.diffView.text = ""
		m.diffView.files = nil
		return m
	}
	m.diffView.err = ""
	m.diffView.text = msg.res
	m.diffView.files = diffFileStats(msg.res)
	m.diffView.hunkTop = 0
	return m
}

// diffFileStats parses unified-diff headers into per-file add/del counts.
// Deterministic code over the diff text: the stats must match exactly what
// is rendered (one source of truth), and this avoids a second git call.
// Binary files are counted, not line-parsed. The current file accumulates
// as a VALUE and is appended once at the next header (or at the end), so no
// element is ever double-appended.
func diffFileStats(diff string) []diffFileInfo {
	var files []diffFileInfo
	var cur diffFileInfo
	var haveCur bool
	flush := func() {
		if haveCur {
			files = append(files, cur)
			haveCur = false
		}
	}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			cur = diffFileInfo{path: diffFilePath(strings.TrimPrefix(line, "diff --git "))}
			haveCur = true
		case !haveCur:
			// Header lines before the first file header.
		case strings.HasPrefix(line, "+++ "):
			if p := strings.TrimSpace(strings.TrimPrefix(line, "+++ ")); p != "/dev/null" {
				cur.path = strings.TrimPrefix(p, "b/")
			}
		case strings.HasPrefix(line, "Binary files "), strings.HasPrefix(line, "GIT binary patch"):
			cur.binary = true
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"):
			// Guard headers; the specific prefixes above already matched.
		case strings.HasPrefix(line, "+"):
			cur.adds++
		case strings.HasPrefix(line, "-"):
			cur.dels++
		}
	}
	flush()
	// Drop empty artifacts and sort by total churn, most-churned first —
	// the review order the frame shows (retry.go +42-7 before client.go).
	out := make([]diffFileInfo, 0, len(files))
	for _, f := range files {
		if f.path == "" {
			continue
		}
		out = append(out, f)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			a := out[j-1].adds + out[j-1].dels
			b := out[j].adds + out[j].dels
			if b > a {
				out[j-1], out[j] = out[j], out[j-1]
			} else {
				break
			}
		}
	}
	if len(out) > diffViewMaxFiles {
		out = out[:diffViewMaxFiles]
	}
	return out
}

// diffFilePath extracts the a-side path from a `diff --git a/x b/y` tail.
func diffFilePath(tail string) string {
	// The tail is `a/<path> b/<path>`; the a-side ends before " b/".
	if i := strings.Index(tail, " b/"); i >= 0 {
		return strings.TrimPrefix(tail[:i], "a/")
	}
	return strings.TrimSpace(tail)
}

// diffHunks splits the diff body into hunks, each hunk keeping its @@ header
// line. Text before the first @@ (rare) is attached to hunk zero so nothing
// is silently dropped.
func diffHunks(diff string) []string {
	lines := strings.Split(diff, "\n")
	var hunks []string
	var cur []string
	for _, line := range lines {
		if strings.HasPrefix(line, "@@ ") && len(cur) > 0 {
			hunks = append(hunks, strings.Join(cur, "\n"))
			cur = []string{line}
			continue
		}
		cur = append(cur, line)
	}
	if len(cur) > 0 {
		hunks = append(hunks, strings.Join(cur, "\n"))
	}
	return hunks
}

// openDiffReviewForHandoff resolves the diff target the same way the handoff
// keys do (pending handoff lane via the active worktree) and opens the view.
func (m model) openDiffReviewForHandoff() (model, tea.Cmd) {
	if m.pendingHandoff != nil && m.activeWorktree != nil && m.activeWorktree.Name == m.pendingHandoff.lane {
		return m.openDiffReview(*m.activeWorktree)
	}
	if m.activeWorktree != nil && strings.TrimSpace(m.activeWorktree.Path) != "" {
		return m.openDiffReview(*m.activeWorktree)
	}
	return m, nil
}

// diffViewNavBar is the one-line nav bar that replaces the title bar while
// the diff view is active (the same swap fileViewNavBar uses; both route
// through pinnedTitleBar so frame geometry never desyncs). Header format
// from Pen frame tOwI1 and the Devin-model contract: `DIFF — <lane> vs
// <base>` with the position readout (`N files · hunk X of Y`) right. Keys
// live in the hint bar at the bottom of the block, not here — a header that
// truncates mid-key advertises a binding the user cannot read.
func (m model) diffViewNavBar(width int) string {
	dv := m.diffView
	left := zeroTheme.accent.Render("DIFF — " + dv.wt.Name + " vs " + dv.base)
	files := len(dv.files)
	current, total := diffHunkPosition(dv.text, dv.hunkTop)
	position := fmt.Sprintf("%d files", files)
	if total > 0 {
		position += fmt.Sprintf(" · hunk %d of %d", current, total)
	}
	right := zeroTheme.faint.Render(position)
	return fitStyledLine(joinHeaderLine(left, right, width), width)
}

// diffHunkPosition counts the @@ hunk headers in the diff text and reports
// which hunk the current window top sits at (1-based), plus the total.
// Header-only diffs report 0/0 and the nav omits the position segment.
func diffHunkPosition(text string, top int) (current, total int) {
	if strings.TrimSpace(text) == "" {
		return 0, 0
	}
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			total++
			if i <= top {
				current = total
			}
		}
	}
	// A window top in the preamble before the first hunk still reads as
	// hunk 1 — "hunk 0" is meaningless to a user.
	if total > 0 && current == 0 {
		current = 1
	}
	return current, total
}

// diffViewHintBar renders the diff block's bottom keymap, dropping whole
// segments by priority under width pressure (DoD 18; the same pattern
// modelPickerHintBar uses). optional is ordered least- to most-essential so
// the loop sheds "open" first and navigation last; scroll and esc never
// drop. Never ellipsis-truncates: a half-visible key hint is worse than none.
func diffViewHintBar(width int) string {
	optional := []string{"^e copy hunk", "o open", "j reject hunk", "a approve", "n next file"}
	core := []string{"↑↓ scroll", "esc close"}
	for drop := 0; drop <= len(optional); drop++ {
		parts := append([]string{core[0]}, optional[drop:]...)
		parts = append(parts, core[1:]...)
		bar := strings.Join(parts, "  ·  ")
		if width <= 0 || lipgloss.Width(bar) <= width {
			return bar
		}
	}
	return strings.Join(core, "  ·  ")
}

// diffStatMarker picks the file stat row's marker from its change shape:
// additions-only [+], deletions-only [-], mixed [~]. ASCII tier (DoD 24).
func diffStatMarker(f diffFileInfo) string {
	switch {
	case f.binary:
		return "[ ]"
	case f.adds > 0 && f.dels == 0:
		return "[+]"
	case f.dels > 0 && f.adds == 0:
		return "[-]"
	default:
		return "[~]"
	}
}

// renderDiffReview renders the whole diff block for the transcript body swap.
// Layout per the frame: file stat list, blank separator, then the hunk body
// window (hunkTop..hunkTop+budget). The header rows stay pinned; only the
// hunk body scrolls.
func (m model) renderDiffReview(width int) string {
	dv := m.diffView
	if dv.err != "" {
		lines := []string{
			zeroTheme.red.Render("[!] Could not produce the diff"),
			"  " + zeroTheme.faint.Render(dv.err),
			"  " + zeroTheme.faint.Render("Recovery: git -C "+dv.wt.Path+" log --oneline -1 (check the worktree and base branch exist)"),
		}
		return styledBlock(width, lines, zeroTheme.cardErr)
	}
	if dv.text == "" {
		return zeroTheme.faint.Render("Capturing diff…")
	}
	if width <= 0 {
		return ""
	}

	hunks := diffHunks(dv.text)

	// File stat list: churn-ordered rows with ASCII markers, then one
	// blank line before the hunk body.
	var head []string
	for _, f := range dv.files {
		if f.binary {
			head = append(head, fmt.Sprintf("  %s %s  (binary)", diffStatMarker(f), f.path))
			continue
		}
		head = append(head, fmt.Sprintf("  %s %s  +%d -%d", diffStatMarker(f), f.path, f.adds, f.dels))
	}
	if len(head) == 0 {
		head = append(head, "  "+zeroTheme.faint.Render("no file changes — the lane produced no diff against "+dv.base))
	}

	// Body window: visible slice of the joined hunks under the line budget.
	body := strings.Join(hunks, "\n")
	bodyLines := strings.Split(body, "\n")
	if len(bodyLines) > diffViewMaxBlockLines {
		bodyLines = bodyLines[:diffViewMaxBlockLines]
	}
	maxTop := len(bodyLines) - 1
	if maxTop < 0 {
		maxTop = 0
	}
	if dv.hunkTop > maxTop {
		dv.hunkTop = maxTop
	}
	if dv.hunkTop < 0 {
		dv.hunkTop = 0
	}
	budget := diffViewMaxBlockLines
	if b := m.diffBodyBudget(); b > 0 && b < budget {
		budget = b
	}
	end := dv.hunkTop + budget
	if end > len(bodyLines) {
		end = len(bodyLines)
	}
	window := bodyLines[dv.hunkTop:end]
	var more string
	if end < len(bodyLines) {
		more = zeroTheme.faint.Render(fmt.Sprintf("… %d more lines (↑↓ scroll, n next file)", len(bodyLines)-end))
	}

	// The hunk body renders raw (ASCII +/- per the contract); the frame's
	// stat rows carry the emphasis. The keymap bar sits at the bottom of
	// the block (the video-derived pattern: header carries position, the
	// hint bar carries keys) and drops whole segments under pressure.
	var rows []string
	rows = append(rows, head...)
	rows = append(rows, "")
	rows = append(rows, window...)
	if more != "" {
		rows = append(rows, more)
	}
	rows = append(rows, zeroTheme.faint.Render(diffViewHintBar(width)))
	return strings.Join(rows, "\n")
}

// diffBodyBudget returns the hunk-body line budget for the current frame,
// derived from the terminal height so the block never pushes the viewport
// into unbounded growth. Conservative: tall terminals still cap at
// diffViewMaxBlockLines via the caller.
func (m model) diffBodyBudget() int {
	if m.height <= 0 {
		return 0
	}
	b := m.height - diffViewHeaderLines - len(m.diffView.files) - 4
	if b < 10 {
		b = 10
	}
	return b
}

// handleDiffReviewKey dispatches the diff view's keys while it is active.
// Returns handled=false for anything else so the main switch keeps
// processing (the same contract as handleHandoffKey). Runtime actions go
// through the review's seams; the pane never mutates files.
func (m model) handleDiffReviewKey(msg tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
	if !m.diffView.active {
		return false, m, nil
	}
	if m.noBlockingModal() {
		// Letter actions fire only with an empty composer (the same guard
		// the file drill-in's d/f keys use), so mid-sentence typing is
		// never hijacked into a runtime action. Letters arrive as Key.Code
		// with empty Text — dispatch on keyCode, not keyText.
		if m.composerValue() == "" {
			switch {
			case keyCode(msg) == 'n':
				return true, m.nextDiffFile(), nil
			case keyCode(msg) == 'a':
				return m.approveDiffAll()
			case keyCode(msg) == 'j':
				return true, m.rejectDiffHunk(), nil
			case keyCode(msg) == 'o':
				return m.openDiffInEditor()
			case keyCtrl(msg, 'e'):
				next, cmd := m.copyDiffHunk()
				return true, next, cmd
			}
		}
		switch {
		case keyIs(msg, tea.KeyEsc):
			return true, m.exitDiffReview(), nil
		case keyIs(msg, tea.KeyDown):
			return true, m.scrollDiffView(diffViewScrollStep), nil
		case keyIs(msg, tea.KeyUp):
			return true, m.scrollDiffView(-diffViewScrollStep), nil
		case keyIs(msg, tea.KeyPgDown):
			return true, m.scrollDiffView(m.diffBodyBudget()), nil
		case keyIs(msg, tea.KeyPgUp):
			return true, m.scrollDiffView(-m.diffBodyBudget()), nil
		}
	}
	return false, m, nil
}

// scrollDiffView moves the hunk-body window and clamps to the body range.
func (m model) scrollDiffView(delta int) model {
	if !m.diffView.active || m.diffView.text == "" {
		return m
	}
	body := strings.Join(diffHunks(m.diffView.text), "\n")
	lines := strings.Split(body, "\n")
	top := m.diffView.hunkTop + delta
	if top < 0 {
		top = 0
	}
	maxTop := len(lines) - 1
	if maxTop < 0 {
		maxTop = 0
	}
	if top > maxTop {
		top = maxTop
	}
	m.diffView.hunkTop = top
	return m
}

// nextDiffFile scrolls the window to the next file's first hunk (the [N]
// action). The file list is churn-ordered; the body keeps diff order, so the
// lookup walks the body for the next file header after the current top.
func (m model) nextDiffFile() model {
	if !m.diffView.active || m.diffView.text == "" {
		return m
	}
	body := strings.Join(diffHunks(m.diffView.text), "\n")
	lines := strings.Split(body, "\n")
	for i := m.diffView.hunkTop + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "diff --git ") {
			m.diffView.hunkTop = i
			return m
		}
	}
	// No further file: jump to the end so the "N more lines" cue clears.
	m.diffView.hunkTop = len(lines) - 1
	return m
}

// approveDiffAll dispatches the review's Accept path for the viewed lane —
// the same seam the review picker and the handoff [M] key use. The view
// closes and the review result message flows through the normal handler.
func (m model) approveDiffAll() (bool, tea.Model, tea.Cmd) {
	wt := m.diffView.wt
	if strings.TrimSpace(wt.Path) == "" {
		return true, m.exitDiffReview(), nil
	}
	msg := applyWorktreeReview(wt, worktreeReviewAccept, false, "diff review approve all")
	next := m.exitDiffReview()
	next.transcript = appendTranscriptRow(next.transcript, transcriptRow{
		kind: rowSystem, text: "Diff review: approve all dispatched for lane " + wt.Name,
	})
	return true, next, tea.Batch(func() tea.Msg { return msg })
}

// rejectDiffHunk records the rejection as a runtime intervention notice and
// returns to the run's decision path — the pane never edits files itself
// (contract §11: rejecting a hunk emits a runtime intervention). The step_back
// vocabulary matches the runtime's intervention set.
func (m model) rejectDiffHunk() model {
	wt := m.diffView.wt
	return m.appendSystemNotice("Hunk rejection recorded for lane " + wt.Name +
		" — the orchestrator decides the intervention (step_back). The diff pane never edits files; use the worktree review to act on the whole lane.")
}

// diffHunkAtWindow returns the text of the hunk the current window top sits
// in (the [E] copy target). diffHunks keeps each hunk's @@ header; the
// window top is a line offset into the joined body, so walk the hunks
// tracking their line spans. ok is false when nothing is capturable (view
// closed, empty diff, or a top still in the preamble before the first hunk).
func (m model) diffHunkAtWindow() (string, bool) {
	if !m.diffView.active || strings.TrimSpace(m.diffView.text) == "" {
		return "", false
	}
	hunks := diffHunks(m.diffView.text)
	offset := 0
	for _, hunk := range hunks {
		span := len(strings.Split(hunk, "\n"))
		if m.diffView.hunkTop < offset+span {
			if !strings.HasPrefix(hunk, "@@ ") {
				// diffHunks attaches pre-first-@@ preamble text to its zeroth
				// entry; the preamble is not a hunk, so nothing is selected.
				return "", false
			}
			return hunk, true
		}
		offset += span
	}
	return "", false
}

// diffCopiedMsg reports a hunk-copy result. chars mirrors
// transcriptCopiedMsg; err is set only when neither clipboard path landed.
type diffCopiedMsg struct {
	chars int
	err   error
}

// copyDiffHunkCmd copies hunk text off the UI loop, reusing the transcript
// selection's clipboard strategy: native OS clipboard first (works on local
// terminals with no OSC52 support), OSC52 fallback for remote sessions.
func copyDiffHunkCmd(text string) tea.Cmd {
	return func() tea.Msg {
		if err := clipboard.WriteAll(text); err != nil {
			if _, oscErr := os.Stdout.WriteString(ansi.SetSystemClipboard(text)); oscErr != nil {
				return diffCopiedMsg{err: err}
			}
		}
		return diffCopiedMsg{chars: utf8.RuneCountInString(text)}
	}
}

// copyDiffHunk is the [E] action: copy the hunk under the window top.
// Nothing is viewable -> nothing is copyable; say so rather than copying a
// stale or empty payload.
func (m model) copyDiffHunk() (tea.Model, tea.Cmd) {
	hunk, ok := m.diffHunkAtWindow()
	if !ok {
		return m.appendSystemNotice("Diff copy: no hunk at the current position."), nil
	}
	return m, copyDiffHunkCmd(hunk)
}

// diffEditorMsg surfaces the editor handoff result. Success is silent (the
// editor ran in the foreground); failure appends an honest system notice.
type diffEditorMsg struct{ err error }

// openDiffInEditor opens the worktree path in $EDITOR via tea.ExecProcess
// (the terminal is released to the editor, then the program resumes). No
// $EDITOR renders a notice with the manual command, never a silent no-op.
func (m model) openDiffInEditor() (bool, tea.Model, tea.Cmd) {
	editor := strings.TrimSpace(osEditor())
	if editor == "" {
		return true, m.appendSystemNotice("No $EDITOR set — open the worktree manually: cd " + m.diffView.wt.Path), nil
	}
	path := m.diffView.wt.Path
	c := execEditor(editor, path)
	return true, m, tea.ExecProcess(c, func(err error) tea.Msg {
		return diffEditorMsg{err: err}
	})
}

// osEditor reads the editor environment the same way interactive tools do.
func osEditor() string {
	for _, k := range []string{"EDITOR", "VISUAL"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// execEditor builds the editor command: a bare command name gets the path as
// its single argument; a command with arguments (e.g. "code -w") is split on
// spaces with the path appended last, which covers the common editor
// invocations without a shell.
func execEditor(editor, path string) *exec.Cmd {
	fields := strings.Fields(editor)
	if len(fields) == 1 {
		return exec.Command(fields[0], path)
	}
	args := append(fields[1:], path)
	return exec.Command(fields[0], args...)
}
