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
	return HandleFor(digest), ProposalSnapshot{Path: path, Version: digest, Base: text}
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
		Payload: map[string]any{"text": "seen line 1\nseen line 2\nseen line 3\n", "path": "big.go", "version": "v1", "start": 1, "end": 3},
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
	t.Setenv("SPLICE_FORMAT_ON_WRITE", "gofmt")
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
	code := submitCodeToolDefinition(false)
	tests := testGeneratorToolDefinition(false)
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
