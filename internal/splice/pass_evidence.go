package splice

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	udiff "github.com/aymanbagabas/go-udiff"

	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Pass evidence measures one pass's workspace change against the workspace at
// the end of the previous pass. The HEAD summary (summarizeWorkspaceChanges)
// counts every earlier edit and any dirt that existed before the run.
//
// The builder runs no git process on the host. A git workspace takes its
// changed-file list from the HEAD summary, which already ran git status in the
// stage sandbox. The HEAD text of a changed file comes from one sandboxed
// git cat-file --batch per measure. The builder writes nothing to disk.

const (
	passBuilderHead = "head"
	passBuilderPass = "pass"

	// passStatusReverted marks a file that differed from the previous pass end
	// and equals HEAD now.
	passStatusReverted = "reverted"

	// maxPassHashBytes caps the bytes hashed and read per file. A larger file
	// is marked capped and compared by size only.
	maxPassHashBytes = 8 << 20

	// binarySniffBytes is the prefix git searches for a NUL to call a file
	// binary.
	binarySniffBytes = 8000
)

var errNoPassBaseline = errors.New("no pass baseline")

// passEntry is the state of one path at the end of a pass.
type passEntry struct {
	absent     bool // no file at the path
	unreadable bool // the path could not be read: its state is unknown
	symlink    bool // the path is a symbolic link; sum covers its target text
	sum        [sha256.Size]byte
	size       int64
	capped     bool   // the file is over maxPassHashBytes; sum is not meaningful
	kept       bool   // text is kept, within the kept-content budget
	text       string // kept content
}

// passBaseline is the state the next pass is measured against. For a git
// workspace it holds the paths that differed from HEAD at the pass end. Every
// other path equals HEAD. For a non-git workspace it holds every file.
type passBaseline struct {
	files map[string]passEntry
}

// passScan is one path as scanned for a pass. hasText is set when text is
// already in memory (a carried entry); otherwise buildBaseline reads it.
type passScan struct {
	entry   passEntry
	text    string
	hasText bool
}

var errNotRegular = errors.New("not a regular file")

// openRegular opens rel under root for reading. The root keeps every path
// component, symbolic links included, inside the workspace. O_NONBLOCK keeps
// the open of a FIFO from blocking, and the check of the opened file refuses
// a path that changed to a FIFO, a device, or a directory after a Lstat.
func openRegular(root *os.Root, rel string) (*os.File, fs.FileInfo, error) {
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errNotRegular
	}
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

// scanPassPath hashes one workspace path with streamed reads. skip reports a
// path that is neither a regular file nor a symbolic link (a FIFO, a device,
// a directory). Such a path cannot be measured. A path that leaves the
// workspace through a symbolic link is unreadable.
func scanPassPath(root *os.Root, rel string) (entry passEntry, skip bool) {
	info, err := root.Lstat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return passEntry{absent: true}, false
	}
	if err != nil {
		return passEntry{unreadable: true}, false
	}
	mode := info.Mode()
	switch {
	case mode&fs.ModeSymlink != 0:
		target, err := root.Readlink(rel)
		if err != nil {
			return passEntry{unreadable: true}, false
		}
		return passEntry{symlink: true, sum: sha256.Sum256([]byte(target)), size: int64(len(target))}, false
	case mode.IsDir():
		return passEntry{}, true
	case !mode.IsRegular():
		return passEntry{}, true
	}
	f, info, err := openRegular(root, rel)
	if errors.Is(err, errNotRegular) {
		return passEntry{}, true
	}
	if err != nil {
		return passEntry{unreadable: true}, false
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxPassHashBytes+1))
	_ = f.Close()
	if err != nil {
		return passEntry{unreadable: true}, false
	}
	if n > maxPassHashBytes {
		return passEntry{capped: true, size: info.Size()}, false
	}
	entry = passEntry{size: n}
	copy(entry.sum[:], h.Sum(nil))
	return entry, false
}

// readPassText reads the current text of a scanned path for line counts. It
// returns false when the text is unknown: a capped, unreadable, or non-text
// path. Text is read only here, one file at a time.
func readPassText(root *os.Root, rel string, entry passEntry) (string, bool) {
	switch {
	case entry.absent:
		return "", true
	case entry.unreadable || entry.capped:
		return "", false
	case entry.symlink:
		target, err := root.Readlink(rel)
		return target, err == nil
	}
	f, _, err := openRegular(root, rel)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxPassHashBytes+1))
	if err != nil || len(data) > maxPassHashBytes {
		return "", false
	}
	return string(data), true
}

// buildBaseline keeps the text of each scanned file while the total kept
// content fits defaultMaxDiffBytes. It visits paths in sorted order, so the
// kept set is deterministic. A file whose size does not fit is not read.
func buildBaseline(root *os.Root, scans map[string]passScan) passBaseline {
	paths := make([]string, 0, len(scans))
	for path := range scans {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	budget := defaultMaxDiffBytes
	files := make(map[string]passEntry, len(scans))
	for _, path := range paths {
		scan := scans[path]
		entry := scan.entry
		switch {
		case entry.absent:
			entry.kept = true
		case entry.unreadable || entry.capped:
		case scan.hasText:
			if len(scan.text) <= budget {
				budget -= len(scan.text)
				entry.kept, entry.text = true, scan.text
			}
		case entry.size <= int64(budget):
			if text, ok := readPassText(root, path, entry); ok && len(text) <= budget {
				budget -= len(text)
				entry.kept, entry.text = true, text
			}
		}
		files[path] = entry
	}
	return passBaseline{files: files}
}

// carriedScan carries a previous entry forward with its kept text.
func carriedScan(prev passEntry) passScan {
	return passScan{entry: prev, text: prev.text, hasText: prev.kept && !prev.absent}
}

// entryText returns the previous text of an entry, and whether it is known.
func entryText(entry passEntry) (string, bool) {
	if entry.absent {
		return "", true
	}
	if entry.kept {
		return entry.text, true
	}
	return "", false
}

// passEntryDiffers reports whether a path changed between two states, and
// whether that answer is unknown. A path unreadable in one state differs. A
// path unreadable in both states is not shown as changed, so a locked file
// does not read as progress on every pass. A capped file is compared by size
// only.
func passEntryDiffers(prev, cur passEntry) (differs, unknown bool) {
	if prev.unreadable || cur.unreadable {
		return prev.unreadable != cur.unreadable, true
	}
	if prev.absent != cur.absent {
		return true, false
	}
	if prev.absent {
		return false, false
	}
	if prev.symlink != cur.symlink {
		return true, false
	}
	if prev.capped || cur.capped {
		return prev.size != cur.size, false
	}
	return prev.sum != cur.sum, false
}

// passAccumulator collects the changed files and line counts of one pass.
type passAccumulator struct {
	changed   []schemas.ChangedFile
	diff      strings.Builder
	truncated bool
}

// add records one changed path. A path with an unknown text is listed and the
// summary is marked truncated. A binary text counts zero lines, as git does.
// A file whose line counts do not fit the diff text left in
// defaultMaxDiffBytes is listed with no counts, and the summary is marked
// truncated. The counts of a file are never partial.
func (a *passAccumulator) add(path, status, oldText string, oldKnown bool, curText string, curKnown bool) {
	if len(a.changed) >= defaultMaxSummaryFiles {
		a.truncated = true
		return
	}
	a.changed = append(a.changed, schemas.ChangedFile{Path: path, Status: status})
	if !oldKnown || !curKnown {
		a.truncated = true
		return
	}
	if isBinaryText(oldText) || isBinaryText(curText) {
		return
	}
	// The texts are checked against the budget before the diff, so the diff
	// never runs on more than the budget.
	left := defaultMaxDiffBytes - a.diff.Len()
	if len(oldText)+len(curText) > left || !appendLineCounts(&a.diff, oldText, curText, left) {
		a.truncated = true
	}
}

// readCurText reads the current text of a changed path for line counts. It
// reads nothing when the old text is unknown, or when the old and new texts
// cannot fit the diff text left in defaultMaxDiffBytes. That file then gets no
// counts.
func (a *passAccumulator) readCurText(root *os.Root, rel string, entry passEntry, oldText string, oldKnown bool) (string, bool) {
	if !oldKnown || int64(a.diff.Len()+len(oldText))+entry.size > defaultMaxDiffBytes {
		return "", false
	}
	return readPassText(root, rel, entry)
}

// isBinaryText reports whether git would call the content binary: a NUL byte
// in its first binarySniffBytes.
func isBinaryText(text string) bool {
	if len(text) > binarySniffBytes {
		text = text[:binarySniffBytes]
	}
	return strings.IndexByte(text, 0) >= 0
}

func (a *passAccumulator) summary(isRepo bool) schemas.ChangeSummary {
	text := a.diff.String()
	if len(text) > defaultMaxDiffBytes {
		text = text[:defaultMaxDiffBytes]
		a.truncated = true
	}
	return schemas.ChangeSummary{IsRepo: isRepo, ChangedFiles: a.changed, DiffText: text, Truncated: a.truncated}
}

// appendLineCounts writes one "- " line per removed line and one "+ " line per
// added line, which countDiffLines reads exactly. The two-character prefix
// keeps content such as "++x" from looking like a file header. It writes
// nothing and returns false when the lines would be more than limit bytes.
// ponytail: line counts come from go-udiff's line diff on CRLF-normalized text.
func appendLineCounts(b *strings.Builder, before, after string, limit int) bool {
	before = strings.ReplaceAll(before, "\r\n", "\n")
	after = strings.ReplaceAll(after, "\r\n", "\n")
	edits := udiff.Lines(before, after)
	size := 0
	for _, edit := range edits {
		for _, line := range passLines(before[edit.Start:edit.End]) {
			size += len(line) + 3
		}
		for _, line := range passLines(edit.New) {
			size += len(line) + 3
		}
	}
	if size > limit {
		return false
	}
	for _, edit := range edits {
		for _, line := range passLines(before[edit.Start:edit.End]) {
			b.WriteString("- " + line + "\n")
		}
		for _, line := range passLines(edit.New) {
			b.WriteString("+ " + line + "\n")
		}
	}
	return true
}

// passLines splits text into lines, without the empty tail after a final newline.
func passLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// headBlob is one HEAD blob as read by headBlobReader.
type headBlob struct {
	text   string
	known  bool // false when the blob is over defaultMaxDiffBytes or not a blob
	absent bool // HEAD has no such path
}

// headBlobReader streams the HEAD blobs of a list of paths through one
// sandboxed git cat-file --batch process. Next returns the blobs in the order
// of the paths it was given.
type headBlobReader struct {
	cmd    *exec.Cmd
	cancel func()
	out    *bufio.Reader
	stderr *bytes.Buffer
	pos    int
	paths  []string
	// waited and waitErr hold the one Wait result. The stderr buffer is read
	// only after Wait, when git no longer writes to it.
	waited  bool
	waitErr error
}

// wait ends the batch process once and returns its result.
func (r *headBlobReader) wait() error {
	if !r.waited {
		r.waited, r.waitErr = true, r.cmd.Wait()
	}
	return r.waitErr
}

// openHeadBlobs starts the batch read for the paths. It returns nil when there
// are no paths, so no process starts.
func openHeadBlobs(ctx context.Context, workDir string, paths []string) (*headBlobReader, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	var requests strings.Builder
	for _, path := range paths {
		if strings.ContainsAny(path, "\n\r") {
			return nil, fmt.Errorf("HEAD blob read: path %q holds a line break", path)
		}
		requests.WriteString("HEAD:" + path + "\n")
	}
	cmd, done, err := prepareGitRead(ctx, workDir, "cat-file", "--batch")
	if err != nil {
		return nil, fmt.Errorf("git read refused: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		done()
		return nil, fmt.Errorf("HEAD blob read: %w", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stdin = strings.NewReader(requests.String())
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		done()
		return nil, fmt.Errorf("HEAD blob read: %w", err)
	}
	return &headBlobReader{cmd: cmd, cancel: done, out: bufio.NewReader(stdout), stderr: stderr, paths: paths}, nil
}

// Next returns the HEAD blob of the next path in the list. The caller must ask
// for the paths in the order it was given.
func (r *headBlobReader) Next() (headBlob, error) {
	if r == nil || r.pos >= len(r.paths) {
		return headBlob{}, fmt.Errorf("HEAD blob read: no path left to read")
	}
	path := r.paths[r.pos]
	r.pos++
	header, err := r.out.ReadString('\n')
	if err != nil {
		_ = r.cmd.Process.Kill()
		_ = r.wait()
		return headBlob{}, fmt.Errorf("HEAD blob read %s: %w: %s", path, err, strings.TrimSpace(r.stderr.String()))
	}
	header = strings.TrimSuffix(header, "\n")
	if strings.HasSuffix(header, " missing") {
		return headBlob{absent: true, known: true}, nil
	}
	fields := strings.Fields(header)
	if len(fields) != 3 {
		return headBlob{}, fmt.Errorf("HEAD blob read %s: unexpected header %q", path, header)
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return headBlob{}, fmt.Errorf("HEAD blob read %s: bad size in %q", path, header)
	}
	if fields[1] != "blob" || size > defaultMaxDiffBytes {
		// A submodule has no text to count, and a blob over the diff budget
		// cannot get line counts. Its bytes are still consumed so the next
		// header lines up.
		if _, err := io.CopyN(io.Discard, r.out, size+1); err != nil {
			return headBlob{}, fmt.Errorf("HEAD blob read %s: %w", path, err)
		}
		return headBlob{known: false}, nil
	}
	body := make([]byte, size+1)
	if _, err := io.ReadFull(r.out, body); err != nil {
		return headBlob{}, fmt.Errorf("HEAD blob read %s: %w", path, err)
	}
	return headBlob{text: string(body[:size]), known: true}, nil
}

// Close ends the batch process. A process that has not finished is killed. A
// failed process is an error only when the reads before it succeeded.
func (r *headBlobReader) Close() error {
	if r == nil {
		return nil
	}
	defer r.cancel()
	if r.pos < len(r.paths) {
		_ = r.cmd.Process.Kill()
	}
	if err := r.wait(); err != nil && r.pos >= len(r.paths) {
		return fmt.Errorf("git cat-file --batch: %w: %s", err, strings.TrimSpace(r.stderr.String()))
	}
	return nil
}

// passEvidenceTracker measures each pass against the workspace at the end of
// the pass before it. It lives for one run and holds no files.
type passEvidenceTracker struct {
	workDir  string
	base     passBaseline
	haveBase bool
	baseErr  error
	// records holds one pair per judged History entry, aligned with History.
	records []passEvidencePair
}

// passEvidencePair holds both builders' evidence for one pass.
// usedPass says the state read the pass builder's values, so the other record
// is the shadow.
type passEvidencePair struct {
	head     schemas.PassEvidence
	pass     schemas.PassEvidence
	usedPass bool
}

// passMeasure is one measured pass. summary and ok are the pass builder's
// result; pair holds both records.
type passMeasure struct {
	summary schemas.ChangeSummary
	ok      bool
	pair    passEvidencePair
	// next is the baseline at this pass end. commit moves the tracker to it
	// only when the pass enters History.
	next passBaseline
}

// newPassEvidenceTracker captures the baseline before the first pass. start is
// the HEAD summary at that point. The tracker never fails the run: a missing
// baseline makes every pass record a DegradedReason.
func newPassEvidenceTracker(ctx context.Context, workDir string, start schemas.ChangeSummary) *passEvidenceTracker {
	t := &passEvidenceTracker{workDir: workDir}
	t.rebase(ctx, start)
	return t
}

func (t *passEvidenceTracker) rebase(ctx context.Context, head schemas.ChangeSummary) {
	base, err := captureBaseline(ctx, t.workDir, head)
	t.base, t.haveBase, t.baseErr = base, err == nil, err
}

// captureBaseline reads the workspace state now as a pass baseline.
func captureBaseline(ctx context.Context, workDir string, head schemas.ChangeSummary) (passBaseline, error) {
	root, err := os.OpenRoot(workDir)
	if err != nil {
		return passBaseline{}, fmt.Errorf("open workspace: %w", err)
	}
	defer root.Close()
	if !isGitWorkspace(workDir) {
		scans, _ := walkPassScans(workDir, root)
		return buildBaseline(root, scans), nil
	}
	if head.DegradedReason != "" {
		return passBaseline{}, fmt.Errorf("HEAD summary degraded: %s", head.DegradedReason)
	}
	scans := make(map[string]passScan, len(head.ChangedFiles))
	for _, f := range head.ChangedFiles {
		entry, skip := scanPassPath(root, f.Path)
		if skip {
			continue
		}
		scans[f.Path] = passScan{entry: entry}
	}
	return buildBaseline(root, scans), nil
}

// measure returns the change since the previous pass end. It does not move the
// baseline: a pass that enters History calls commit, and a peek (a repair
// boundary) does not. Errors never escape: they become DegradedReason on the
// pass record.
func (t *passEvidenceTracker) measure(ctx context.Context, iteration int, head schemas.ChangeSummary) passMeasure {
	var summary schemas.ChangeSummary
	var next passBaseline
	var err error
	if t.haveBase {
		summary, next, err = t.summarize(ctx, head)
	} else {
		err = t.baseErr
		if err == nil {
			err = errNoPassBaseline
		}
	}
	var changed *bool
	m := passMeasure{}
	degraded := ""
	if err == nil {
		m.summary, m.ok = summary, true
		value := len(summary.ChangedFiles) > 0
		changed = &value
	} else {
		degraded = err.Error()
	}
	if err == nil {
		m.next = next
	}
	m.pair = passEvidencePair{
		head: evidenceRecord(passBuilderHead, iteration, head, nil, head.DegradedReason),
		pass: evidenceRecord(passBuilderPass, iteration, summary, changed, degraded),
	}
	return m
}

// commit moves the baseline to the pass end of m. A degraded pass rebases on
// the HEAD summary, so the next pass starts from the workspace now. A pass
// that does not enter History never calls commit, so its edits stay in the next
// measured change.
func (t *passEvidenceTracker) commit(ctx context.Context, m passMeasure, head schemas.ChangeSummary) {
	if m.ok {
		t.base, t.haveBase, t.baseErr = m.next, true, nil
		return
	}
	t.rebase(ctx, head)
}

func (t *passEvidenceTracker) summarize(ctx context.Context, head schemas.ChangeSummary) (schemas.ChangeSummary, passBaseline, error) {
	root, err := os.OpenRoot(t.workDir)
	if err != nil {
		return schemas.ChangeSummary{}, passBaseline{}, fmt.Errorf("open workspace: %w", err)
	}
	defer root.Close()
	if !isGitWorkspace(t.workDir) {
		return summarizeWalkPass(t.workDir, root, t.base)
	}
	return summarizeGitPass(ctx, t.workDir, root, head, t.base)
}

// summarizeGitPass compares the workspace with the previous pass end. The
// changed-file list comes from the HEAD summary. A listed path is changed when
// it was not in the previous baseline, or its digest differs. A path in the
// baseline and absent from the list reverted to HEAD. A path in neither equals
// HEAD in both passes. The HEAD texts come from one batch read.
func summarizeGitPass(ctx context.Context, workDir string, root *os.Root, head schemas.ChangeSummary, prev passBaseline) (schemas.ChangeSummary, passBaseline, error) {
	if head.DegradedReason != "" {
		return schemas.ChangeSummary{}, passBaseline{}, fmt.Errorf("HEAD summary degraded: %s", head.DegradedReason)
	}
	// The status list stops at defaultMaxSummaryFiles. A path beyond the cap
	// is not in the list, so it is carried forward and the summary is marked.
	statusCapped := len(head.ChangedFiles) >= defaultMaxSummaryFiles
	listed := make(map[string]schemas.ChangedFile, len(head.ChangedFiles))
	paths := make([]string, 0, len(head.ChangedFiles)+len(prev.files))
	for _, f := range head.ChangedFiles {
		// A path can be listed twice: "D" and "??" after git rm --cached. The
		// first entry is kept, so the HEAD text is the previous text.
		if _, dup := listed[f.Path]; dup {
			continue
		}
		listed[f.Path] = f
		paths = append(paths, f.Path)
	}
	for path := range prev.files {
		if _, ok := listed[path]; !ok {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	// The HEAD text is needed for each listed path that the previous baseline
	// lacks and that was not created. The batch returns them in this order.
	var headPaths []string
	for _, path := range paths {
		f, inList := listed[path]
		if _, inPrev := prev.files[path]; inList && !inPrev && f.Status != "created" {
			headPaths = append(headPaths, path)
		}
	}
	reader, err := openHeadBlobs(ctx, workDir, headPaths)
	if err != nil {
		return schemas.ChangeSummary{}, passBaseline{}, err
	}
	fail := func(err error) (schemas.ChangeSummary, passBaseline, error) {
		if reader != nil {
			_ = reader.Close()
		}
		return schemas.ChangeSummary{}, passBaseline{}, err
	}

	scans := make(map[string]passScan, len(head.ChangedFiles))
	acc := &passAccumulator{truncated: statusCapped}
	for _, path := range paths {
		f, inList := listed[path]
		old, inPrev := prev.files[path]
		var blob headBlob
		if inList && !inPrev && f.Status != "created" {
			if blob, err = reader.Next(); err != nil {
				return fail(err)
			}
		}
		if !inList && statusCapped {
			scans[path] = carriedScan(old)
			continue
		}
		now, skip := scanPassPath(root, path)
		if skip {
			// A FIFO, a device, or a directory has no content to compare.
			acc.truncated = true
			continue
		}
		if inList {
			scans[path] = passScan{entry: now}
		}

		var status, oldText string
		var oldKnown, changed bool
		switch {
		case inList && !inPrev:
			// Equal to HEAD before this pass. The previous text is HEAD's blob,
			// and a created file has no HEAD blob at all.
			status, changed = f.Status, true
			if f.Status == "created" {
				oldText, oldKnown = "", true
			} else {
				oldText, oldKnown = blob.text, blob.known
			}
		case !inList && inPrev:
			// Differed before this pass and equals HEAD now.
			status, changed = passStatusReverted, true
			oldText, oldKnown = entryText(old)
		default:
			var unknown bool
			changed, unknown = passEntryDiffers(old, now)
			status = f.Status
			oldText, oldKnown = entryText(old)
			if unknown {
				acc.truncated = true
			}
		}
		if !changed {
			continue
		}
		curText, curKnown := acc.readCurText(root, path, now, oldText, oldKnown)
		acc.add(path, status, oldText, oldKnown, curText, curKnown)
	}
	if err := reader.Close(); err != nil {
		return schemas.ChangeSummary{}, passBaseline{}, err
	}
	return acc.summary(true), buildBaseline(root, scans), nil
}

// summarizeWalkPass compares a non-git workspace with its previous state. A
// file is created, deleted, or modified. The listing cap stops deletion
// reporting, because a file past the cap cannot be told from a deleted one.
func summarizeWalkPass(workDir string, root *os.Root, prev passBaseline) (schemas.ChangeSummary, passBaseline, error) {
	cur, listTruncated := walkPassScans(workDir, root)
	paths := make([]string, 0, len(cur)+len(prev.files))
	for path := range cur {
		paths = append(paths, path)
	}
	for path := range prev.files {
		if _, ok := cur[path]; !ok && !listTruncated {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	acc := &passAccumulator{truncated: listTruncated}
	for _, path := range paths {
		scan, inCur := cur[path]
		old, inPrev := prev.files[path]
		switch {
		case inCur && !inPrev:
			curText, curKnown := acc.readCurText(root, path, scan.entry, "", true)
			acc.add(path, "created", "", true, curText, curKnown)
		case !inCur && inPrev:
			oldText, oldKnown := entryText(old)
			acc.add(path, "deleted", oldText, oldKnown, "", true)
		default:
			changed, unknown := passEntryDiffers(old, scan.entry)
			if unknown {
				acc.truncated = true
			}
			if !changed {
				continue
			}
			oldText, oldKnown := entryText(old)
			curText, curKnown := acc.readCurText(root, path, scan.entry, oldText, oldKnown)
			acc.add(path, "modified", oldText, oldKnown, curText, curKnown)
		}
	}
	return acc.summary(false), buildBaseline(root, cur), nil
}

// walkPassScans scans a non-git workspace with the walk rules of
// walkChangeSummary. listTruncated reports that the file cap stopped the walk.
func walkPassScans(workDir string, root *os.Root) (map[string]passScan, bool) {
	scans := map[string]passScan{}
	listTruncated := false
	_ = filepath.WalkDir(workDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipSummaryDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(workDir, path)
		if rel == "" || skipSummaryDirComponent(rel) {
			return nil
		}
		if len(scans) >= defaultMaxSummaryFiles {
			listTruncated = true
			return filepath.SkipAll
		}
		entry, skip := scanPassPath(root, rel)
		if skip {
			return nil
		}
		scans[rel] = passScan{entry: entry}
		return nil
	})
	return scans, listTruncated
}

// isGitWorkspace reports whether workDir holds a .git entry. It makes the same
// check as gitChangeSummary.
func isGitWorkspace(workDir string) bool {
	_, err := os.Stat(filepath.Join(workDir, ".git"))
	return err == nil
}

// evidenceRecord turns one builder's summary into the record the state and the trace use.
func evidenceRecord(builder string, iteration int, sum schemas.ChangeSummary, passChanged *bool, degraded string) schemas.PassEvidence {
	added, removed := countDiffLines(sum.DiffText)
	return schemas.PassEvidence{
		Iteration:      iteration,
		Builder:        builder,
		PassChanged:    passChanged,
		FilesChanged:   sortedPaths(sum.ChangedFiles),
		LinesAdded:     added,
		LinesRemoved:   removed,
		Truncated:      sum.Truncated,
		DegradedReason: degraded,
	}
}

// passNodeDrift cross-checks the pass builder's changed files against the
// nodes' claims. unclaimed holds the files the pass builder saw that no node
// reported. unseen holds the files a node claimed that the pass builder did
// not see. When the HEAD list hit its cap, a claimed path that the list does
// not hold is excluded from unseen: the builder cannot judge it.
func passNodeDrift(head schemas.ChangeSummary, passFiles []string, nodes []schemas.NodeEvidence) (unclaimed, unseen []string) {
	claimed := map[string]bool{}
	for _, node := range nodes {
		for _, path := range node.ChangedFiles {
			claimed[normalizeDriftPath(path)] = true
		}
	}
	seen := map[string]bool{}
	for _, path := range passFiles {
		path = normalizeDriftPath(path)
		seen[path] = true
		if !claimed[path] {
			unclaimed = append(unclaimed, path)
		}
	}
	listed := map[string]bool{}
	for _, f := range head.ChangedFiles {
		listed[normalizeDriftPath(f.Path)] = true
	}
	capped := len(head.ChangedFiles) >= defaultMaxSummaryFiles
	for path := range claimed {
		if !seen[path] && (!capped || listed[path]) {
			unseen = append(unseen, path)
		}
	}
	sort.Strings(unclaimed)
	sort.Strings(unseen)
	return unclaimed, unseen
}

// normalizeDriftPath puts a path in one form before the drift cross-check:
// forward slashes, no "./" prefix, no doubled or trailing separators.
func normalizeDriftPath(path string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(path))))
}

// unclaimedNotice names at most five unclaimed files and counts the rest.
func unclaimedNotice(pass int, files []string) string {
	shown := files
	if len(shown) > 5 {
		shown = shown[:5]
	}
	names := make([]string, 0, len(shown))
	for _, path := range shown {
		names = append(names, sanitizeNoticeLine(path))
	}
	text := fmt.Sprintf("[evidence] pass %d: %d changed file(s) no node reported: %s", pass, len(files), strings.Join(names, ", "))
	if more := len(files) - len(shown); more > 0 {
		text += fmt.Sprintf(", +%d more", more)
	}
	return text
}

// noteDegradedPass tells the user that the pass builder degraded for a
// flag-on measure, so the HEAD summary is the evidence for that pass.
func noteDegradedPass(options PipelineRunConfig, iteration int, m passMeasure) {
	if options.Flags.Enabled(flags.PassEvidence) && !m.ok {
		emitProgress(options, fmt.Sprintf("[evidence] pass builder degraded at iteration %d (%s); using the HEAD summary for this pass\n", iteration, sanitizeNoticeLine(m.pair.pass.DegradedReason)))
	}
}

// shadow returns the record of the builder the state did not use.
func (p passEvidencePair) shadow() schemas.PassEvidence {
	if p.usedPass {
		return p.head
	}
	return p.pass
}

// shadowList returns the shadow record of each recorded pass, then of each
// pending pass (a boundary's current pass, not yet recorded). A nil tracker
// has no recorded passes.
func (t *passEvidenceTracker) shadowList(pending ...passEvidencePair) []schemas.PassEvidence {
	var out []schemas.PassEvidence
	if t != nil {
		for _, pair := range t.records {
			out = append(out, pair.shadow())
		}
	}
	for _, pair := range pending {
		out = append(out, pair.shadow())
	}
	return out
}
