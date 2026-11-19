package splice

// B1 regression tests: typed source views, range validation, the guarded
// reader seam, aggregate request bounds, and cache accounting.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/hooks"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/tools"
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

// TestSourceReaderRealRegistryHostSeam pins the end-to-end seam path with
// a REAL registry: ToolRunnerSourceReader reaches the raw bytes through
// RegistryToolRunner.RunHostSeamTool, the Deny tool's only executable
// path, and the tool's guard set (path scoping) still applies.
func TestSourceReaderRealRegistryHostSeam(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.go"), []byte("package real\n\nfunc Real() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewScopedRawFileReadTool(root, nil))
	runner := NewRegistryToolRunner(registry)
	reader := ToolRunnerSourceReader{Inner: runner}
	snap, err := reader.ReadSource(context.Background(), "real.go")
	if err != nil {
		t.Fatalf("host-seam read through real registry: %v", err)
	}
	if !strings.Contains(string(snap.Raw), "func Real()") {
		t.Fatalf("snapshot bytes wrong: %q", snap.Raw)
	}
	if snap.SHA256 == "" {
		t.Fatal("snapshot digest must be computed")
	}
	// Path scoping survives: an outside-root read fails even through the
	// seam.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "escape.go"), []byte("package escape\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadSource(context.Background(), filepath.Join(outside, "escape.go")); err == nil {
		t.Fatal("host-seam marker must not bypass path scoping")
	}
	// And a model-initiated call on the same runner is still rejected:
	// the marker lives on the entry point, not the tool.
	res, _ := runner.RunTool(context.Background(), "raw_file_read", map[string]any{"path": "real.go"})
	if res.OK {
		t.Fatalf("model-surface RunTool executed a host-seam-only tool: %q", res.Output)
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

// fixedHostSeamRunner returns one canned result for every call.
type fixedHostSeamRunner struct{ res ToolResult }

func (f fixedHostSeamRunner) RunTool(context.Context, string, map[string]any) (ToolResult, error) {
	return f.res, nil
}

func (f fixedHostSeamRunner) RunHostSeamTool(context.Context, string, map[string]any) (ToolResult, error) {
	return f.res, nil
}

// TestSourceReaderFailsOnIncompleteResult pins defense in depth: a seam
// result that was truncated or redacted never becomes a snapshot, so no
// base can be built from bytes that are not the file.
func TestSourceReaderFailsOnIncompleteResult(t *testing.T) {
	cases := map[string]ToolResult{
		"truncated": {OK: true, Output: "partial bytes", Truncated: true},
		"redacted":  {OK: true, Output: "x = 1", Redacted: true},
	}
	for name, res := range cases {
		reader := ToolRunnerSourceReader{Inner: fixedHostSeamRunner{res: res}}
		snap, err := reader.ReadSource(context.Background(), "a.py")
		if err == nil {
			t.Fatalf("%s: ReadSource built a snapshot %q from an incomplete result", name, snap.Raw)
		}
	}
}

// TestSourceReaderExactBytesThroughRealRegistry pins the exact-bytes rule
// through the real seam: a 300 KB file comes back byte for byte.
func TestSourceReaderExactBytesThroughRealRegistry(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("value = 1\n", 30_000) // about 300 KB
	if err := os.WriteFile(filepath.Join(root, "big.py"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewScopedRawFileReadTool(root, nil))
	reader := ToolRunnerSourceReader{Inner: NewRegistryToolRunner(registry)}
	snap, err := reader.ReadSource(context.Background(), "big.py")
	if err != nil {
		t.Fatalf("read big.py through the seam: %v", err)
	}
	if string(snap.Raw) != content {
		t.Fatalf("seam returned %d bytes, want the file's %d exact bytes", len(snap.Raw), len(content))
	}
	if snap.SHA256 != tools.HashContent([]byte(content)) {
		t.Fatal("snapshot digest does not match the file bytes")
	}
}

// TestSourceReaderNeverReturnsRedactedBytes pins the redaction rule: a file
// with a secret-looking token either reads back exactly or fails. It never
// returns the redaction marker in place of the secret.
func TestSourceReaderNeverReturnsRedactedBytes(t *testing.T) {
	root := t.TempDir()
	content := "import os\nTOKEN = \"sk-proj-abcdefghijklmnop1234\"\nprint(os.name)\n"
	if err := os.WriteFile(filepath.Join(root, "secret.py"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewScopedRawFileReadTool(root, nil))
	reader := ToolRunnerSourceReader{Inner: NewRegistryToolRunner(registry)}
	snap, err := reader.ReadSource(context.Background(), "secret.py")
	if err == nil && string(snap.Raw) != content {
		t.Fatalf("seam built a base from redacted bytes: %q", snap.Raw)
	}
}

// TestRedactedSourceModifyNamesTheSecret pins the user-facing outcome for a
// file that holds secret-looking text. The seam fails closed; the modify
// must say why, and name the file.
func TestRedactedSourceModifyNamesTheSecret(t *testing.T) {
	root := t.TempDir()
	content := "import os\nTOKEN = \"sk-proj-abcdefghijklmnop1234\"\nprint(os.name)\n"
	if err := os.WriteFile(filepath.Join(root, "secret.py"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewScopedReadFileTool(root, nil))
	tools.RegisterPipelineSourceReader(registry, root, nil)
	runner := NewRegistryToolRunner(registry)
	path := "secret.py"
	item, err := fulfillQuery(context.Background(), schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: &path}, runner)
	if err != nil {
		t.Fatalf("fulfill read_file: %v", err)
	}
	reg := stages.NewProposalBaseRegistry()
	reg.RecordFromBundle(&schemas.ContextBundle{Items: []schemas.ContextItem{item}})
	proposal := stages.ProposedFileChange{
		Path: "secret.py", ChangeType: "modify", BaseRef: "secret.py",
		Edits: []stages.TextReplacement{{Old: "print(os.name)", New: "print(os.sep)"}},
	}
	_, _, err = stages.MaterializeProposals([]stages.ProposedFileChange{proposal}, reg.Resolve)
	if err == nil {
		t.Fatal("a modify of a file that holds a secret must be refused")
	}
	if !strings.Contains(err.Error(), "secret.py") || !strings.Contains(err.Error(), "looks like a secret") {
		t.Fatalf("refusal must name the path and the secret: %v", err)
	}
}

// TestRedactedSourceCreateIsRefused pins the create rule: a create over a
// file whose bytes were withheld would write the redaction marker into the
// file, so it is refused. A create of a new file still works.
func TestRedactedSourceCreateIsRefused(t *testing.T) {
	root := t.TempDir()
	content := "import os\nTOKEN = \"sk-proj-abcdefghijklmnop1234\"\nprint(os.name)\n"
	path := filepath.Join(root, "secret.py")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewScopedReadFileTool(root, nil))
	tools.RegisterPipelineSourceReader(registry, root, nil)
	name := "secret.py"
	item, err := fulfillQuery(context.Background(), schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: &name}, NewRegistryToolRunner(registry))
	if err != nil {
		t.Fatalf("fulfill read_file: %v", err)
	}
	reg := stages.NewProposalBaseRegistry()
	reg.RecordFromBundle(&schemas.ContextBundle{Items: []schemas.ContextItem{item}})
	redactedView := "import os\nTOKEN = \"[REDACTED]\"\nprint(os.sep)\n"
	create := stages.ProposedFileChange{Path: "secret.py", ChangeType: "create", Content: &redactedView}
	if _, _, err := stages.MaterializeProposals([]stages.ProposedFileChange{create}, reg.Resolve); err == nil {
		t.Fatal("a create over a redacted file must be refused")
	} else if !strings.Contains(err.Error(), "looks like a secret") {
		t.Fatalf("refusal must give the secret reason: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != content {
		t.Fatalf("file changed on disk: %q", after)
	}
	fresh := "print('new file')\n"
	newFile := stages.ProposedFileChange{Path: "fresh.py", ChangeType: "create", Content: &fresh}
	if _, _, err := stages.MaterializeProposals([]stages.ProposedFileChange{newFile}, reg.Resolve); err != nil {
		t.Fatalf("create of a new file must still work: %v", err)
	}
}

// TestRedactedSourceDeleteIsRefused pins the delete rule for a file whose
// bytes were withheld: the delete is refused with the secret reason, and the
// file stays on disk.
func TestRedactedSourceDeleteIsRefused(t *testing.T) {
	root := t.TempDir()
	content := "import os\nTOKEN = \"sk-proj-abcdefghijklmnop1234\"\nprint(os.name)\n"
	path := filepath.Join(root, "secret.py")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewScopedReadFileTool(root, nil))
	tools.RegisterPipelineSourceReader(registry, root, nil)
	name := "secret.py"
	item, err := fulfillQuery(context.Background(), schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: &name}, NewRegistryToolRunner(registry))
	if err != nil {
		t.Fatalf("fulfill read_file: %v", err)
	}
	reg := stages.NewProposalBaseRegistry()
	reg.RecordFromBundle(&schemas.ContextBundle{Items: []schemas.ContextItem{item}})
	del := stages.ProposedFileChange{Path: "secret.py", ChangeType: "delete", BaseRef: "secret.py"}
	if _, _, err := stages.MaterializeProposals([]stages.ProposedFileChange{del}, reg.Resolve); err == nil {
		t.Fatal("a delete of a file that holds a secret must be refused")
	} else if !strings.Contains(err.Error(), "looks like a secret") {
		t.Fatalf("refusal must give the secret reason: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != content {
		t.Fatal("refused delete changed the file")
	}
}

// seamRunnerWith builds the stage runner over a registry with the raw
// reader, with optional agent options (hooks, result callbacks).
func seamRunnerWith(t *testing.T, root string, opts agent.Options) ToolRunner {
	t.Helper()
	registry := tools.NewRegistry()
	tools.RegisterPipelineSourceReader(registry, root, nil)
	opts.Cwd = root
	opts.Registry = registry
	return newAgentToolRunner(PipelineConfigFromAgentOptions(opts), root)
}

// TestRawReadEventCarriesSummaryNotBytes pins G2: a host-seam raw read emits
// a bounded summary to the event stream (path, byte count, digest), never the
// file's full bytes. The reader still gets the exact bytes.
func TestRawReadEventCarriesSummaryNotBytes(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("value = 1\n", 30_000) // about 300 KB
	if err := os.WriteFile(filepath.Join(root, "big.py"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var events []agent.ToolResult
	runner := seamRunnerWith(t, root, agent.Options{OnToolResult: func(res agent.ToolResult) { events = append(events, res) }})
	snap, err := ToolRunnerSourceReader{Inner: runner}.ReadSource(context.Background(), "big.py")
	if err != nil || string(snap.Raw) != content {
		t.Fatalf("seam read: err=%v, %d bytes", err, len(snap.Raw))
	}
	for _, ev := range events {
		if len(ev.Output) > len(content)/10 {
			t.Fatalf("raw read event carries %d bytes of file output, want a summary", len(ev.Output))
		}
	}
}

// TestHookFeedbackNeverEntersARawRead pins G3: an after-tool hook still runs
// on a raw read, but its feedback is not added to the bytes the reader gets.
func TestHookFeedbackNeverEntersARawRead(t *testing.T) {
	root := t.TempDir()
	content := "package x\n"
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "ran.txt")
	hook := hooks.Definition{ID: "feed", Event: hooks.EventAfterTool, Matcher: "raw_file_read", Command: "/bin/sh", Args: []string{"-c", `echo HOOK-FEEDBACK; echo ran > "$1"`, "hook", marker}, Enabled: true}
	dispatcher := hooks.NewDispatcher(hooks.DispatcherOptions{Config: hooks.Config{Enabled: true, Hooks: []hooks.Definition{hook}}, Cwd: t.TempDir()})
	runner := seamRunnerWith(t, root, agent.Options{Hooks: dispatcher})
	snap, err := ToolRunnerSourceReader{Inner: runner}.ReadSource(context.Background(), "x.go")
	if err != nil {
		t.Fatalf("seam read: %v", err)
	}
	if string(snap.Raw) != content {
		t.Fatalf("hook feedback entered the raw read: %q", snap.Raw)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("after-tool hook did not run on the raw read: %v", err)
	}
}
