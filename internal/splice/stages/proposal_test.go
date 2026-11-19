package stages

// C1/C2/C3 regression tests (handoff Section 7 C list): the compact
// proposal materializer, the write-boundary expected-base recheck, and
// the versioned-schema pairing across both arms.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
)

func cResolver(snaps map[string]ProposalSnapshot) func(string) (ProposalSnapshot, bool) {
	return func(ref string) (ProposalSnapshot, bool) {
		s, ok := snaps[ref]
		return s, ok
	}
}

func cSnapshot(path, text string) (string, ProposalSnapshot) {
	digest := contentDigest(text)
	return HandleFor(digest), ProposalSnapshot{Path: path, Version: digest, Base: text, ContentDigest: digest}
}

// TestSmallEditInLargeFileProducesSmallProposal pins the byte-shape goal:
// a one-line change in a large file is a compact proposal (old/new span),
// and the materializer hydrates it to full content with every unrelated
// byte preserved.
func TestSmallEditInLargeFileProducesSmallProposal(t *testing.T) {
	large := "line 1\nline 2\nline 3\n" + strings.Repeat("filler\n", 5000) + "last line\n"
	handle, snap := cSnapshot("big.go", large)
	ref := cResolver(map[string]ProposalSnapshot{handle: snap})
	proposal := ProposedFileChange{
		Path: "big.go", ChangeType: "modify", BaseRef: handle,
		Edits: []TextReplacement{{Old: "line 2", New: "line two edited"}},
	}
	change, err := MaterializeProposal(proposal, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(change.Content, "line two edited") {
		t.Fatal("edit not applied")
	}
	if !strings.Contains(change.Content, strings.Repeat("filler\n", 5000)) {
		t.Fatal("unrelated bytes lost")
	}
	if !strings.HasPrefix(change.Content, "line 1\n") || !strings.HasSuffix(change.Content, "last line\n") {
		t.Fatal("unrelated content at the edges lost")
	}
	// The proposal the MODEL sent is small: only the matched span, not the file.
	encoded, _ := json.Marshal(proposal)
	if len(encoded) > 500 {
		t.Fatalf("proposal bytes = %d for a one-line edit in a large file", len(encoded))
	}
}

// TestProposalVariants pins create/modify/delete/empty-new/empty-old.
func TestProposalVariants(t *testing.T) {
	handle, snap := cSnapshot("a.go", "alpha\nbeta\n")
	resolver := cResolver(map[string]ProposalSnapshot{handle: snap})

	// create: content only.
	create, err := MaterializeProposal(ProposedFileChange{Path: "n.go", ChangeType: "create", Content: strPtrC("package n\n")}, resolver)
	if err != nil || create.Content != "package n\n" || create.ChangeType != "create" {
		t.Fatalf("create: %v %+v", err, create)
	}
	// create with edits is mixed representation.
	if _, err := MaterializeProposal(ProposedFileChange{Path: "n.go", ChangeType: "create", Content: strPtrC("x"), BaseRef: handle}, resolver); err == nil {
		t.Fatal("create+base_ref must be invalid")
	}
	// modify: empty new deletes the matched span.
	mod, err := MaterializeProposal(ProposedFileChange{Path: "a.go", ChangeType: "modify", BaseRef: handle,
		Edits: []TextReplacement{{Old: "beta\n", New: ""}}}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mod.Content, "beta") {
		t.Fatalf("empty new must delete the span: %q", mod.Content)
	}
	if !strings.HasSuffix(mod.Content, "alpha\n") {
		t.Fatalf("delete span lost other lines: %q", mod.Content)
	}
	// Empty old is invalid.
	if _, err := MaterializeProposal(ProposedFileChange{Path: "a.go", ChangeType: "modify", BaseRef: handle,
		Edits: []TextReplacement{{Old: "", New: "x"}}}, resolver); err == nil {
		t.Fatal("empty old must be invalid")
	}
	// delete: base_ref only.
	del, err := MaterializeProposal(ProposedFileChange{Path: "a.go", ChangeType: "delete", BaseRef: handle}, resolver)
	if err != nil || del.ChangeType != "delete" || del.Content != "" {
		t.Fatalf("delete: %v %+v", err, del)
	}
}

// TestProposalMatchFailuresCauseNoWrite pins missing, duplicate, and
// ambiguous matches: every one fails in the materializer, before any
// filesystem call.
func TestProposalMatchFailuresCauseNoWrite(t *testing.T) {
	handle, snap := cSnapshot("a.go", "dup\ndup\nunique\n")
	resolver := cResolver(map[string]ProposalSnapshot{handle: snap})
	cases := []struct {
		name string
		p    ProposedFileChange
	}{
		{"missing", ProposedFileChange{Path: "a.go", ChangeType: "modify", BaseRef: handle,
			Edits: []TextReplacement{{Old: "absent text", New: "x"}}}},
		{"ambiguous", ProposedFileChange{Path: "a.go", ChangeType: "modify", BaseRef: handle,
			Edits: []TextReplacement{{Old: "dup", New: "x"}}}},
		{"unknown base_ref", ProposedFileChange{Path: "a.go", ChangeType: "modify", BaseRef: "nosuchhandle",
			Edits: []TextReplacement{{Old: "dup", New: "x"}}}},
	}
	for _, tc := range cases {
		if _, err := MaterializeProposal(tc.p, resolver); err == nil {
			t.Fatalf("%s: must fail before write", tc.name)
		}
	}
}

// TestProposalOverlapRejected pins the overlap rule: two edits touching
// the same span fail; the second must not target text created by the first.
func TestProposalOverlapRejected(t *testing.T) {
	handle, snap := cSnapshot("a.go", "abcdefghij\n")
	resolver := cResolver(map[string]ProposalSnapshot{handle: snap})
	overlap := ProposedFileChange{Path: "a.go", ChangeType: "modify", BaseRef: handle,
		Edits: []TextReplacement{
			{Old: "abcd", New: "ABCD"},
			{Old: "cdef", New: "CDEF"}, // overlaps the first span
		}}
	if _, err := MaterializeProposal(overlap, resolver); err == nil {
		t.Fatal("overlapping edits must be rejected")
	}
	// Non-overlapping edits on the same base are fine and deterministic.
	ok := ProposedFileChange{Path: "a.go", ChangeType: "modify", BaseRef: handle,
		Edits: []TextReplacement{
			{Old: "fgh", New: "FGH"},
			{Old: "abc", New: "ABC"},
		}}
	change, err := MaterializeProposal(ok, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if change.Content != "ABCdeFGHij\n" {
		t.Fatalf("deterministic application = %q, want ABCdeFGHij\\n", change.Content)
	}
}

// TestProposalPreservesCRLFAndUnrelatedBytes pins byte-for-byte fidelity.
func TestProposalPreservesCRLFAndUnrelatedBytes(t *testing.T) {
	base := "one\r\ntwo\r\nthree\r\n"
	handle, snap := cSnapshot("crlf.go", base)
	resolver := cResolver(map[string]ProposalSnapshot{handle: snap})
	change, err := MaterializeProposal(ProposedFileChange{Path: "crlf.go", ChangeType: "modify", BaseRef: handle,
		Edits: []TextReplacement{{Old: "two", New: "TWO"}}}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(change.Content, "\r\n") {
		t.Fatal("CRLF line endings must be preserved")
	}
	if change.Content != "one\r\nTWO\r\nthree\r\n" {
		t.Fatalf("content = %q", change.Content)
	}
}

// TestProposalCannotEditUnseenSpans pins the C1 rule: a whole-file host
// baseline the model never received does not authorize edits. The base
// registry records only DELIVERED text, so an edit in an unseen span is a
// missing match.
func TestProposalCannotEditUnseenSpans(t *testing.T) {
	reg := NewProposalBaseRegistry()
	// The model received ONLY the first three lines of a much larger file.
	bundle := &schemas.ContextBundle{Items: []schemas.ContextItem{{
		Query:   schemas.ContextQuery{QueryType: schemas.ContextReadFile},
		Payload: map[string]any{"text": "seen line 1\nseen line 2\nseen line 3\n", "raw": "seen line 1\nseen line 2\nseen line 3\n", "path": "big.go", "version": "v1", "start": 1, "end": 3},
	}}}
	reg.RecordFromBundle(bundle)
	reset := SetProposalBases(reg)
	defer reset()

	// An edit against unseen content fails (not in the delivered base).
	hidden := ProposedFileChange{Path: "big.go", ChangeType: "modify", BaseRef: HandleFor(contentDigest("seen line 1\nseen line 2\nseen line 3\n")),
		Edits: []TextReplacement{{Old: "hidden line 999", New: "evil"}}}
	if _, _, err := MaterializeProposals([]ProposedFileChange{hidden}, currentProposalSnapshot); err == nil {
		t.Fatal("editing an unseen span must fail")
	}
	// An edit against SEEN content materializes.
	seen := ProposedFileChange{Path: "big.go", ChangeType: "modify", BaseRef: HandleFor(contentDigest("seen line 1\nseen line 2\nseen line 3\n")),
		Edits: []TextReplacement{{Old: "seen line 2", New: "seen line 2 edited"}}}
	changes, _, err := MaterializeProposals([]ProposedFileChange{seen}, currentProposalSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(changes[0].Content, "seen line 2 edited") || strings.Contains(changes[0].Content, "outside the delivered") {
		t.Fatalf("seen-span edit wrong: %q", changes[0].Content)
	}
}

// TestMaterializeProposalsPreflightRejectsBadLaterProposal pins the batch
// preflight rule: a bad LATER proposal rejects the whole batch before any
// write starts.
func TestMaterializeProposalsPreflightRejectsBadLaterProposal(t *testing.T) {
	handle, snap := cSnapshot("a.go", "content\n")
	resolver := cResolver(map[string]ProposalSnapshot{handle: snap})
	batch := []ProposedFileChange{
		{Path: "good.go", ChangeType: "create", Content: strPtrC("ok")},
		{Path: "bad.go", ChangeType: "modify", BaseRef: "unknown-handle", Edits: []TextReplacement{{Old: "x", New: "y"}}},
	}
	if _, _, err := MaterializeProposals(batch, resolver); err == nil {
		t.Fatal("a bad later proposal must reject the whole batch")
	}
	// Duplicate paths fail too.
	dup := []ProposedFileChange{
		{Path: "same.go", ChangeType: "create", Content: strPtrC("a")},
		{Path: "same.go", ChangeType: "create", Content: strPtrC("b")},
	}
	if _, _, err := MaterializeProposals(dup, resolver); err == nil {
		t.Fatal("duplicate paths must fail")
	}
}

// TestExpectedBaseRecheckBlocksExternalMutation pins the C2 boundary: a
// write whose expected digest no longer matches the file (mutated after
// the proposal's base) writes NOTHING, even with overwrite and a tracker
// baseline.
func TestExpectedBaseRecheckBlocksExternalMutation(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "conflict.go")
	original := "original bytes\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := tools.NewWriteFileTool(dir)
	// The caller asserts the file holds the ORIGINAL bytes (its proposal
	// base), but an external mutation changed them.
	if err := os.WriteFile(target, []byte("externally mutated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expected := tools.HashContent([]byte(original))
	res := tool.Run(context.Background(), map[string]any{
		"path": "conflict.go", "content": "proposed content\n", "overwrite": true, "expected_base": expected,
	})
	if res.Status == tools.StatusOK {
		t.Fatalf("stale-base write must fail: %q", res.Output)
	}
	current, _ := os.ReadFile(target)
	if string(current) != "externally mutated\n" {
		t.Fatalf("failed write must not touch the file: %q", current)
	}
	// A MATCHING digest proceeds.
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	res = tool.Run(context.Background(), map[string]any{
		"path": "conflict.go", "content": "proposed content\n", "overwrite": true, "expected_base": expected,
	})
	if res.Status != tools.StatusOK {
		t.Fatalf("matching-base write must proceed: %q", res.Output)
	}
}

// TestCanonicalContentMatchesPostFormatBytes pins the C2 format-on-write
// rule: the tracker baseline records the ACTUAL final bytes after
// formatting, so a subsequent repair hash or conflict check describes
// real disk state. With formatting enabled, the file on disk differs
// from the proposed content and the tracker's recorded hash matches disk.
func TestCanonicalContentMatchesPostFormatBytes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "fmt.go")
	if err := os.WriteFile(target, []byte("package  x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	flags.SetProcess(flags.Set{flags.ToolsFormatOnWrite: true})
	t.Cleanup(func() { flags.SetProcess(nil) })
	tracker := tools.NewFileTracker()
	// Read first so the strict tracker sees a baseline (mirrors the
	// pipeline's read-before-write).
	reader := tools.NewReadFileTool(dir)
	if r := reader.Run(context.Background(), map[string]any{"path": "fmt.go"}); r.Status != tools.StatusOK {
		t.Fatalf("read: %q", r.Output)
	}
	// RunWithOptions is tool-interface-internal; exercise via a registry
	// call with the tracker in options. PermissionGranted is what a
	// pipeline stage runner sets for its own mutating tools.
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(dir))
	registry.Register(tools.NewReadFileTool(dir))
	if r := registry.RunWithOptions(context.Background(), "read_file", map[string]any{"path": "fmt.go"}, tools.RunOptions{FileTracker: tracker}); r.Status != tools.StatusOK {
		t.Fatalf("read: %q", r.Output)
	}
	res := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
		"path": "fmt.go", "content": "package  x\n\nfunc  X()  {}\n", "overwrite": true,
	}, tools.RunOptions{FileTracker: tracker, PermissionGranted: true})
	if res.Status != tools.StatusOK {
		t.Fatalf("write: %q", res.Output)
	}
	disk, _ := os.ReadFile(target)
	// The tool resolves the workspace root (macOS /var -> /private/var),
	// so the tracker's key is the resolved path. Mirror that here.
	resolvedDir, serr := filepath.EvalSymlinks(dir)
	if serr != nil {
		resolvedDir = dir
	}
	// The tracked version equals the ACTUAL disk bytes (post-format),
	// not the proposed content.
	version, tracked := tracker.Version(filepath.Join(resolvedDir, "fmt.go"))
	if !tracked {
		t.Fatal("write must baseline the file")
	}
	if version.Hash != tools.HashContent(disk) {
		t.Fatalf("tracker baseline must describe actual final bytes: tracked=%s disk=%s", version.Hash, tools.HashContent(disk))
	}
}

// TestParseCompactProposalsNormalize pins the parser migration: a
// compact/1 payload normalizes into canonical full-content FileChanges
// so repair hashes and attribution keep their full-file contract, and a
// legacy full/1 payload decodes unchanged.
func TestParseCompactProposalsNormalize(t *testing.T) {
	base := "func Old() {}\n"
	handle, snap := cSnapshot("code.go", base)
	reg := NewProposalBaseRegistry()
	reg.byHandle[handle] = snap
	reset := SetProposalBases(reg)
	defer reset()

	compact := `{"files":[{"path":"code.go","change_type":"modify","base_ref":"` + handle + `","edits":[{"old":"Old","new":"New"}]}],"language":"go","intent":"rename","confidence":0.9}`
	out, err := parseCodeWriterArgs(compact)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Files) != 1 || out.Files[0].Content != "func New() {}\n" {
		t.Fatalf("normalized content wrong: %+v", out.Files)
	}
	// Legacy form decodes as-is.
	legacy := `{"files":[{"path":"new.go","change_type":"create","content":"package new\n"}],"language":"go","intent":"add","confidence":0.9}`
	out, err = parseCodeWriterArgs(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if out.Files[0].Content != "package new\n" {
		t.Fatalf("legacy decode wrong: %+v", out.Files)
	}
}

// TestSubmitCodeAndSubmitTestsAdvertiseIdenticalSchema pins the C3 pairing
// rule: both arms (and both stages) advertise the SAME versioned files
// schema, so a comparison never measures a schema difference.
func TestSubmitCodeAndSubmitTestsAdvertiseIdenticalSchema(t *testing.T) {
	code := submitCodeToolDefinition()
	tests := testGeneratorToolDefinition()
	codeProps, _ := code.Parameters["properties"].(map[string]any)
	testProps, _ := tests.Parameters["properties"].(map[string]any)
	codeFiles := codeProps["files"]
	testFiles := testProps["files"]
	encodedCode, err1 := json.Marshal(codeFiles)
	encodedTests, err2 := json.Marshal(testFiles)
	if err1 != nil || err2 != nil {
		t.Fatal("schema marshal failed")
	}
	if string(encodedCode) != string(encodedTests) {
		t.Fatalf("writer and test-generator files schemas drifted:\n%s\n%s", encodedCode, encodedTests)
	}
	// The schema is the compact/1 protocol: edits + base_ref present.
	var filesSchema struct {
		Items struct {
			Properties map[string]any `json:"properties"`
		} `json:"items"`
	}
	if err := json.Unmarshal(encodedCode, &filesSchema); err != nil {
		t.Fatal(err)
	}
	if _, ok := filesSchema.Items.Properties["edits"]; !ok {
		t.Fatal("compact/1 schema must advertise edits")
	}
	if _, ok := filesSchema.Items.Properties["base_ref"]; !ok {
		t.Fatal("compact/1 schema must advertise base_ref")
	}
}

// TestRepairHashesStillWork pins the C1 non-migration: hydrated canonical
// content keeps the full-file contract. writerContentHashes and
// writerAuthoredTestFiles live in the splice package (internal, tested
// there via repair_progress_test.go); here we pin that the hydrated
// FileChange the materializer produces is byte-identical to what those
// consumers expect, using the same hashing definition.
func TestRepairHashesStillWork(t *testing.T) {
	content := "package authored\n\nfunc Helper() {}\n"
	proposal := ProposedFileChange{Path: "authored_test.go", ChangeType: "create", Content: strPtrC(content)}
	changes, digests, err := MaterializeProposals([]ProposedFileChange{proposal}, func(string) (ProposalSnapshot, bool) { return ProposalSnapshot{}, false })
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Content != content {
		t.Fatalf("hydrated content drifted: %q", changes[0].Content)
	}
	if digests["authored_test.go"] != tools.HashContent([]byte(content)) {
		t.Fatal("the digest handed to the write boundary must be over the full canonical content")
	}
}

func strPtrC(s string) *string { return &s }

// incidentSource is a 100 KB Python file with one edit target near the top.
func incidentSource() string {
	var b strings.Builder
	b.WriteString("def f():\n    return 1\n")
	for b.Len() < 100*1024 {
		b.WriteString("x = 0  # filler line for the 100 KB file\n")
	}
	return b.String()
}

// wholeFileBundle builds a view that delivers a whole file, with the end
// line the read tool reports for it.
func wholeFileBundle(path, content string) *schemas.ContextBundle {
	return &schemas.ContextBundle{Items: []schemas.ContextItem{{
		Summary: "source",
		Payload: map[string]any{
			"path": path, "version": "v1", "text": content, "raw": content,
			"start": 1, "end": rawLineCount(content),
		},
	}}}
}

// bundleWithRaw builds one fulfilled source view: the text the model saw
// and the raw bytes the host registered as the base.
func bundleWithRaw(path, text, raw string) *schemas.ContextBundle {
	return &schemas.ContextBundle{Items: []schemas.ContextItem{{
		Summary: "source",
		Payload: map[string]any{
			"path": path, "version": "v1", "text": text, "raw": raw,
			"start": 1, "end": countLines(text),
		},
	}}}
}

// TestTruncatedRawBaseIsRefusedBeforeAnyWrite reproduces the E05 data loss.
// The old raw read returned the first 60 KB plus a spill notice. A one-line
// edit composed on that base must be refused, and the file must stay
// byte-identical.
func TestTruncatedRawBaseIsRefusedBeforeAnyWrite(t *testing.T) {
	dir := t.TempDir()
	original := incidentSource()
	path := filepath.Join(dir, "function.py")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	truncatedRaw := original[:60*1024] + "\n[splice] output truncated, full output saved to /tmp/spill.txt (grep or read_file it instead of re-running)\n"
	reg := NewProposalBaseRegistry()
	reg.RecordFromBundle(bundleWithRaw("function.py", original, truncatedRaw))
	proposals := []ProposedFileChange{{
		Path: "function.py", ChangeType: "modify", BaseRef: "function.py",
		Edits: []TextReplacement{{Old: "    return 1", New: "    return 2"}},
	}}
	changes, _, err := MaterializeProposals(proposals, reg.Resolve)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	_, err = applyFileChanges(context.Background(), dir, changes, registryRunTool(t, dir))
	after, _ := os.ReadFile(path)
	if string(after) != original {
		t.Fatalf("file changed to %d bytes (original %d); an edit composed on a truncated base reached disk", len(after), len(original))
	}
	if err == nil {
		t.Fatal("a change composed on a truncated base must be refused")
	}
}

// TestCorrectRawBaseModifyAppliesOnlyEditedLines is the guard for the normal
// path: a complete read applies, and only the edited line changes.
func TestCorrectRawBaseModifyAppliesOnlyEditedLines(t *testing.T) {
	dir := t.TempDir()
	original := incidentSource()
	path := filepath.Join(dir, "function.py")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewProposalBaseRegistry()
	reg.RecordFromBundle(bundleWithRaw("function.py", original, original))
	proposals := []ProposedFileChange{{
		Path: "function.py", ChangeType: "modify", BaseRef: "function.py",
		Edits: []TextReplacement{{Old: "    return 1", New: "    return 2"}},
	}}
	changes, _, err := MaterializeProposals(proposals, reg.Resolve)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(changes) != 1 || changes[0].BaseDigest != tools.HashContent([]byte(original)) {
		t.Fatalf("materialized change must carry the base file digest, got %+v", changes)
	}
	if _, err := applyFileChanges(context.Background(), dir, changes, registryRunTool(t, dir)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	after, _ := os.ReadFile(path)
	want := strings.Replace(original, "    return 1", "    return 2", 1)
	if string(after) != want {
		t.Fatalf("applied file differs from the one-line edit (got %d bytes, want %d)", len(after), len(want))
	}
}

// TestDeleteRefusedWhenBaseChanged pins the delete rule: a delete composed
// on a base the file no longer has is refused, and the file stays.
func TestDeleteRefusedWhenBaseChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.py")
	if err := os.WriteFile(path, []byte("current bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewProposalBaseRegistry()
	reg.RecordFromBundle(bundleWithRaw("old.py", "seen bytes\n", "seen bytes\n"))
	changes, _, err := MaterializeProposals([]ProposedFileChange{{Path: "old.py", ChangeType: "delete", BaseRef: "old.py"}}, reg.Resolve)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if _, err := applyFileChanges(context.Background(), dir, changes, registryRunTool(t, dir)); err == nil {
		t.Fatal("a delete composed on a stale base must be refused")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("refused delete removed the file: %v", err)
	}
}

// TestSnapshotWithoutContentDigestIsRefused pins the no-bytes rule: a modify
// or delete whose base has no content digest cannot be checked against the
// file, so it is refused before any write.
func TestSnapshotWithoutContentDigestIsRefused(t *testing.T) {
	snaps := map[string]ProposalSnapshot{"h1": {Path: "x.py", Version: "v", Base: "a = 1\n"}}
	resolve := cResolver(snaps)
	modify := ProposedFileChange{Path: "x.py", ChangeType: "modify", BaseRef: "h1", Edits: []TextReplacement{{Old: "a = 1", New: "a = 2"}}}
	if _, _, err := MaterializeProposals([]ProposedFileChange{modify}, resolve); err == nil {
		t.Fatal("a modify on a base with no content digest must be refused")
	}
	del := ProposedFileChange{Path: "x.py", ChangeType: "delete", BaseRef: "h1"}
	if _, _, err := MaterializeProposals([]ProposedFileChange{del}, resolve); err == nil {
		t.Fatal("a delete on a base with no content digest must be refused")
	}
}

// TestFormatContextBundleNeverPromptsHostOnlyRaw pins the host-only rule: a
// context item with no text field must not carry its raw bytes into the
// prompt through the payload fallback.
func TestFormatContextBundleNeverPromptsHostOnlyRaw(t *testing.T) {
	secretLine := `TOKEN = "sk-proj-abcdefghijklmnop1234"`
	bundle := &schemas.ContextBundle{Items: []schemas.ContextItem{{
		Query:   schemas.ContextQuery{QueryType: schemas.ContextReadFile},
		Summary: "Read secret.py.",
		Payload: map[string]any{"path": "secret.py", "version": "v1", "raw": "import os\n" + secretLine + "\n"},
	}}}
	for _, block := range formatContextBundle(bundle) {
		if strings.Contains(block, "sk-proj-abcdefghijklmnop1234") {
			t.Fatalf("host-only raw bytes reached the prompt: %q", block)
		}
	}
}

// legacyApply runs one full-content (legacy) submission through the parser
// and the apply step, as the code writer does.
func legacyApply(t *testing.T, dir, files string) error {
	t.Helper()
	stream := dStream(`{"intent":"legacy","language":"python","confidence":0.9,"files":` + files + `}`)
	output, err := parseCodeWriterOutput(stream, dir)
	if err != nil {
		return err
	}
	_, err = applyFileChanges(context.Background(), dir, output.Files, registryRunTool(t, dir))
	return err
}

// TestLegacyFullContentChangesFollowTheBaseRules pins G1. A full-content
// entry with no base_ref must meet the same rules as a compact proposal:
// a partial view or no view refuses it, and the file stays unchanged.
func TestLegacyFullContentChangesFollowTheBaseRules(t *testing.T) {
	dir := t.TempDir()
	big := incidentSource()
	path := filepath.Join(dir, "big.py")
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewProposalBaseRegistry()
	reg.RecordFromBundle(bundleWithRaw("big.py", big[:5000], big))
	reset := SetProposalBases(reg)
	defer reset()

	probes := map[string]string{
		"modify": `[{"path":"big.py","change_type":"modify","content":"print('x')\\n"}]`,
		"create": `[{"path":"big.py","change_type":"create","content":"print('x')\\n"}]`,
		"delete": `[{"path":"big.py","change_type":"delete"}]`,
	}
	for name, files := range probes {
		err := legacyApply(t, dir, files)
		after, _ := os.ReadFile(path)
		if err == nil {
			t.Fatalf("legacy %s of a partly seen file must be refused (file now %d bytes)", name, len(after))
		}
		if string(after) != big {
			t.Fatalf("legacy %s changed the file to %d bytes", name, len(after))
		}
	}
	// A create of a new file that was never read still works.
	if err := legacyApply(t, dir, `[{"path":"fresh.py","change_type":"create","content":"print('new')\\n"}]`); err != nil {
		t.Fatalf("create of a new file must still work: %v", err)
	}
	// A legacy modify of a file whose whole content was delivered applies.
	small := "def f():\n    return 1\n"
	smallPath := filepath.Join(dir, "small.py")
	if err := os.WriteFile(smallPath, []byte(small), 0o644); err != nil {
		t.Fatal(err)
	}
	reg2 := NewProposalBaseRegistry()
	reg2.RecordFromBundle(wholeFileBundle("small.py", small))
	reset2 := SetProposalBases(reg2)
	defer reset2()
	if err := legacyApply(t, dir, `[{"path":"small.py","change_type":"modify","content":"def f():\n    return 2\n"}]`); err != nil {
		t.Fatalf("legacy modify of a fully delivered file must apply: %v", err)
	}
	if got, _ := os.ReadFile(smallPath); string(got) != "def f():\n    return 2\n" {
		t.Fatalf("legacy modify wrote %q", got)
	}
}

// TestHostWriteAllowsReemitUntilDiskChanges pins the host-write record: a
// file the host wrote may be re-emitted, and the digest guard still refuses
// the re-emit once anything else changed the file.
func TestHostWriteAllowsReemitUntilDiskChanges(t *testing.T) {
	defer SetHostWrites(NewHostWriteLedger())()
	dir := t.TempDir()
	if _, err := applyFileChanges(context.Background(), dir, []schemas.FileChange{{Path: "hw.py", ChangeType: "create", Content: "v1\n"}}, registryRunTool(t, dir)); err != nil {
		t.Fatalf("host write: %v", err)
	}
	if err := legacyApply(t, dir, `[{"path":"hw.py","change_type":"create","content":"v2\n"}]`); err != nil {
		t.Fatalf("re-emit of a file the host wrote must apply: %v", err)
	}
	path := filepath.Join(dir, "hw.py")
	if got, _ := os.ReadFile(path); string(got) != "v2\n" {
		t.Fatalf("re-emit wrote %q", got)
	}
	if err := os.WriteFile(path, []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := legacyApply(t, dir, `[{"path":"hw.py","change_type":"create","content":"v3\n"}]`); err == nil {
		t.Fatal("a re-emit after an outside change to the file must be refused")
	}
	if got, _ := os.ReadFile(path); string(got) != "external\n" {
		t.Fatalf("refused re-emit changed the file to %q", got)
	}
}

// TestHostWriteRecordsDoNotCollideAcrossDirectories: the same relative path in
// two work directories keeps separate records.
func TestHostWriteRecordsDoNotCollideAcrossDirectories(t *testing.T) {
	defer SetHostWrites(NewHostWriteLedger())()
	dirA := t.TempDir()
	if _, err := applyFileChanges(context.Background(), dirA, []schemas.FileChange{{Path: "main.go", ChangeType: "create", Content: "a\n"}}, registryRunTool(t, dirA)); err != nil {
		t.Fatalf("host write in dirA: %v", err)
	}
	dirB := t.TempDir()
	if err := legacyApply(t, dirB, `[{"path":"main.go","change_type":"create","content":"b\n"}]`); err != nil {
		t.Fatalf("create of a new file in another directory must apply: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dirB, "main.go")); string(got) != "b\n" {
		t.Fatalf("dirB file = %q", got)
	}
}

// TestCreateOverUnseenFileStillRefused: a file the host did not write, and
// the model never saw, is not replaced by a create.
func TestCreateOverUnseenFileStillRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pre.py")
	if err := os.WriteFile(path, []byte("pre\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := legacyApply(t, dir, `[{"path":"pre.py","change_type":"create","content":"new\n"}]`); err == nil {
		t.Fatal("a create over a file the model never saw must be refused")
	}
	if got, _ := os.ReadFile(path); string(got) != "pre\n" {
		t.Fatalf("refused create changed the file to %q", got)
	}
}

// TestLegacyModifyOfMissingFileCreatesIt: a modify of a path that does not
// exist has no bytes to lose, so it applies as a create.
func TestLegacyModifyOfMissingFileCreatesIt(t *testing.T) {
	dir := t.TempDir()
	if err := legacyApply(t, dir, `[{"path":"gone.go","change_type":"modify","content":"package gone\n"}]`); err != nil {
		t.Fatalf("modify of a missing file must apply as a create: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "gone.go")); string(got) != "package gone\n" {
		t.Fatalf("file = %q", got)
	}
}

// TestHostWriteRecordsEndWithTheirRun pins H1. A host write in run 1 must not
// let run 2 re-emit the same path, which run 2 never read.
func TestHostWriteRecordsEndWithTheirRun(t *testing.T) {
	dir := t.TempDir()
	reset1 := SetHostWrites(NewHostWriteLedger())
	if _, err := applyFileChanges(context.Background(), dir, []schemas.FileChange{{Path: "x.py", ChangeType: "create", Content: "v1\n"}}, registryRunTool(t, dir)); err != nil {
		t.Fatalf("run 1 write: %v", err)
	}
	reset1()
	defer SetHostWrites(NewHostWriteLedger())()
	if err := legacyApply(t, dir, `[{"path":"x.py","change_type":"create","content":"v2\n"}]`); err == nil {
		t.Fatal("run 2 must not overwrite a file it never read, because run 1 wrote it")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "x.py")); string(got) != "v1\n" {
		t.Fatalf("run 2 changed the file to %q", got)
	}
}

// TestTestGeneratorLegacyFollowsBaseRules pins the test generator's legacy
// branch: a full-content test file with no base is refused when the model never
// saw the file.
func TestTestGeneratorLegacyFollowsBaseRules(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep_test.go"), []byte("package keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := `{"files":[{"path":"keep_test.go","change_type":"modify","content":"package keep\n\n// rewritten\n"}],"language":"go","intent":"i","confidence":0.9}`
	if _, err := parseTestGeneratorOutput(collectedWithArgs(testGeneratorToolName, args), dir); err == nil {
		t.Fatal("a legacy test-file modify of a file the model never saw must be refused")
	}
}

// TestHostDeleteForgetsItsRecord: after the host deletes a file, a same-bytes
// file that appears from outside is not the host's write, so a re-emit of it is
// refused.
func TestHostDeleteForgetsItsRecord(t *testing.T) {
	defer SetHostWrites(NewHostWriteLedger())()
	dir := t.TempDir()
	if _, err := applyFileChanges(context.Background(), dir, []schemas.FileChange{{Path: "d.py", ChangeType: "create", Content: "v1\n"}}, registryRunTool(t, dir)); err != nil {
		t.Fatalf("host write: %v", err)
	}
	if _, err := applyFileChanges(context.Background(), dir, []schemas.FileChange{{Path: "d.py", ChangeType: "delete"}}, registryRunTool(t, dir)); err != nil {
		t.Fatalf("host delete: %v", err)
	}
	path := filepath.Join(dir, "d.py")
	if err := os.WriteFile(path, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := legacyApply(t, dir, `[{"path":"d.py","change_type":"create","content":"v2\n"}]`); err == nil {
		t.Fatal("a file that appeared after the host deleted its record must not be re-emitted")
	}
	if got, _ := os.ReadFile(path); string(got) != "v1\n" {
		t.Fatalf("refused re-emit changed the file to %q", got)
	}
}
