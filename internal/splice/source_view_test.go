package splice

// B1 regression tests: typed source views, range validation, the guarded
// reader seam, aggregate request bounds, and cache accounting.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// strPtr returns a pointer to s (test helper).
func b1StrPtr(s string) *string { return &s }

// fakeSourceReader is a scripted SourceReader for seam tests.
type fakeSourceReader struct {
	files map[string][]byte
	reads int
	deny  map[string]bool
}

func (f *fakeSourceReader) ReadSource(ctx context.Context, path string) (SourceSnapshot, error) {
	f.reads++
	if f.deny[path] {
		return SourceSnapshot{}, errors.New("denied by scope: " + path)
	}
	raw, ok := f.files[path]
	if !ok {
		return SourceSnapshot{}, errors.New("file not found: " + path)
	}
	return NewSourceSnapshot(path, raw), nil
}

// TestSourceViewHashChangesWithBytes pins identity: the same path with
// different bytes produces a different version digest, so a view can never
// silently claim to represent content it does not.
func TestSourceViewHashChangesWithBytes(t *testing.T) {
	first := NewSourceSnapshot("a.go", []byte("package a\n"))
	second := NewSourceSnapshot("a.go", []byte("package a // changed\n"))
	if first.SHA256 == second.SHA256 {
		t.Fatal("different bytes must produce different digests")
	}
	if first.SHA256 == "" || second.SHA256 == "" {
		t.Fatal("digests must be defined for non-nil bytes")
	}
	empty := NewSourceSnapshot("empty.go", []byte{})
	if empty.SHA256 == "" {
		t.Fatal("an empty file still has a defined digest")
	}
}

// TestSourceViewRangeAndContent pins view materialization: 1-based
// inclusive ranges, CRLF preservation, no-final-newline files, empty
// files, and EOF clamping.
func TestSourceViewRangeAndContent(t *testing.T) {
	crlf := "line one\r\nline two\r\nline three\r\n"
	snap := NewSourceSnapshot("crlf.txt", []byte(crlf))
	view, err := BuildSourceView(snap, 2, 2, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if view.Text != "line two\r" {
		// The CR stays: raw bytes in, raw bytes out. No normalization.
		t.Fatalf("CRLF file lost its carriage return: %q", view.Text)
	}
	if view.EndLine != 2 || view.StartLine != 2 || view.Version != snap.SHA256 {
		t.Fatalf("view identity wrong: %+v", view)
	}
	if view.Identity() != "crlf.txt@"+snap.SHA256+"#2-2" {
		t.Fatalf("identity = %q", view.Identity())
	}

	// No final newline: the last line still materializes.
	noNL := NewSourceSnapshot("nonl.txt", []byte("alpha\nbeta\ngamma"))
	view, err = BuildSourceView(noNL, 3, 3, "h2")
	if err != nil {
		t.Fatal(err)
	}
	if view.Text != "gamma" {
		t.Fatalf("last line without trailing newline = %q", view.Text)
	}

	// EOF clamp: end past the file end clamps to the last line.
	view, err = BuildSourceView(noNL, 2, 99, "h3")
	if err != nil {
		t.Fatal(err)
	}
	if view.EndLine != 3 || view.Text != "beta\ngamma" {
		t.Fatalf("EOF clamp wrong: end=%d text=%q", view.EndLine, view.Text)
	}

	// Empty file: any range is invalid (zero lines admit nothing).
	empty := NewSourceSnapshot("empty.txt", []byte{})
	if _, err := BuildSourceView(empty, 1, 1, "h4"); err == nil {
		t.Fatal("an empty file must not admit a range")
	}

	// Invalid ranges fail loudly.
	if _, err := BuildSourceView(noNL, 0, 2, "h5"); err == nil {
		t.Fatal("start line 0 must be invalid")
	}
	if _, err := BuildSourceView(noNL, 3, 2, "h6"); err == nil {
		t.Fatal("end before start must be invalid")
	}

	// Oversized views truncate and flag.
	huge := NewSourceSnapshot("big.txt", []byte(strings.Repeat("x", maxSourceViewBytes+1000)+"\n"))
	view, err = BuildSourceView(huge, 1, 1, "h7")
	if err != nil {
		t.Fatal(err)
	}
	if !view.Truncated || len(view.Text) != maxSourceViewBytes {
		t.Fatalf("oversized view: truncated=%v len=%d", view.Truncated, len(view.Text))
	}
}

// TestSourceReaderDeniesOutsideScope pins the seam's guard inheritance: a
// runner-level denial (extra-root absent, path confined) surfaces as an
// error naming the path, never as a silent empty snapshot.
func TestSourceReaderDeniesOutsideScope(t *testing.T) {
	reader := &fakeSourceReader{files: map[string][]byte{}, deny: map[string]bool{"../etc/passwd": true}}
	if _, err := reader.ReadSource(context.Background(), "../etc/passwd"); err == nil {
		t.Fatal("a scoped denial must reach the caller as an error")
	}
	if _, err := reader.ReadSource(context.Background(), "missing.go"); err == nil {
		t.Fatal("a missing file must error, never return an empty snapshot")
	}
	// A nil inner runner is a typed capability failure.
	var nilReader ToolRunnerSourceReader
	if _, err := nilReader.ReadSource(context.Background(), "any.go"); !errors.Is(err, errNoRawRead) {
		t.Fatalf("nil runner: %v, want errNoRawRead", err)
	}
}

// TestSourceCacheCountsPhysicalReads pins the accounting rule: cache hits
// never masquerade as skipped physical reads. HostReads counts actual seam
// acquisitions; CacheHits counts validated hits; the counters never merge.
func TestSourceCacheCountsPhysicalReads(t *testing.T) {
	reader := &fakeSourceReader{files: map[string][]byte{"a.go": []byte("package a\n")}}
	cache := newSourceCache("/workspace")
	first, err := cache.get(context.Background(), reader, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.get(context.Background(), reader, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatal("cache returned different content for unchanged file")
	}
	if cache.HostReads != 1 {
		t.Fatalf("host reads = %d, want 1 (one physical acquisition)", cache.HostReads)
	}
	if cache.CacheHits != 1 {
		t.Fatalf("cache hits = %d, want 1", cache.CacheHits)
	}
	if reader.reads != 1 {
		t.Fatalf("seam reads = %d, want 1", reader.reads)
	}
}

// TestAggregateContextRequestBounds pins the request-level bounds: too many
// queries or a too-large summed MaxChars budget is invalid even when every
// individual query is legal.
func TestAggregateContextRequestBounds(t *testing.T) {
	legal := func() schemas.ContextQuery {
		return schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: b1StrPtr("a.go"), MaxResults: 10, MaxChars: 5000}
	}
	// Within bounds.
	ok := schemas.ContextRequest{Reason: "need source", Queries: []schemas.ContextQuery{legal(), legal()}}
	if err := ok.AggregateValidate(); err != nil {
		t.Fatalf("legal request rejected: %v", err)
	}
	// Too many queries, each individually legal.
	many := schemas.ContextRequest{Reason: "need source"}
	for i := 0; i <= schemas.MaxAggregateContextQueries; i++ {
		many.Queries = append(many.Queries, legal())
	}
	if err := many.AggregateValidate(); err == nil {
		t.Fatal("aggregate query-count bound must fire")
	}
	// Sum of budgets over the aggregate byte bound.
	fat := schemas.ContextRequest{Reason: "need source", Queries: []schemas.ContextQuery{
		{QueryType: schemas.ContextReadFile, Path: b1StrPtr("a.go"), MaxResults: 10, MaxChars: 20000},
		{QueryType: schemas.ContextReadFile, Path: b1StrPtr("b.go"), MaxResults: 10, MaxChars: 20000},
		{QueryType: schemas.ContextReadFile, Path: b1StrPtr("c.go"), MaxResults: 10, MaxChars: 20000},
		{QueryType: schemas.ContextReadFile, Path: b1StrPtr("d.go"), MaxResults: 10, MaxChars: 5000},
	}}
	if err := fat.AggregateValidate(); err == nil {
		t.Fatal("aggregate byte bound must fire (65000 > 64000)")
	}
}

// TestContextQueryRangeValidation pins the range-shape rules: both ends
// together, start >= 1, end >= start.
func TestContextQueryRangeValidation(t *testing.T) {
	start, end := 5, 10
	q := schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: b1StrPtr("a.go"), MaxResults: 10, MaxChars: 1000, StartLine: &start, EndLine: &end}
	if !q.HasRange() {
		t.Fatal("HasRange must be true")
	}
	if err := q.Validate(); err != nil {
		t.Fatalf("valid range rejected: %v", err)
	}
	// One end without the other.
	onlyStart := schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: b1StrPtr("a.go"), MaxResults: 10, MaxChars: 1000, StartLine: &start}
	if err := onlyStart.Validate(); err == nil {
		t.Fatal("start_line without end_line must be invalid")
	}
	// end < start.
	badStart, badEnd := 10, 5
	bad := schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: b1StrPtr("a.go"), MaxResults: 10, MaxChars: 1000, StartLine: &badStart, EndLine: &badEnd}
	if err := bad.Validate(); err == nil {
		t.Fatal("end_line < start_line must be invalid")
	}
	// start < 1.
	zero := 0
	zeroStart := schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: b1StrPtr("a.go"), MaxResults: 10, MaxChars: 1000, StartLine: &zero, EndLine: &end}
	if err := zeroStart.Validate(); err == nil {
		t.Fatal("start_line 0 must be invalid")
	}
}
