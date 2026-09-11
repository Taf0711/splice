package stages

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Mixed representation (content + base_ref on a modify): the common model
// output shape. Normalization derives exact edits from the base snapshot,
// so the proposal flows through the standard strict path.
func TestNormalizeMixedModifyDerivesEdits(t *testing.T) {
	base := "package audit\n\nfunc Apply(trail *Trail) int {\n\treturn 0\n}\n"
	snap := ProposalSnapshot{Path: "internal/audit/retention.go", Base: base}
	provided := "package audit\n\nfunc Apply(trail *Trail) int {\n\treturn 1\n}\n"
	p := ProposedFileChange{
		Path:       "internal/audit/retention.go",
		ChangeType: "modify",
		BaseRef:    "snap-1",
		Content:    &provided,
	}
	got, err := MaterializeProposal(p, func(string) (ProposalSnapshot, bool) { return snap, true })
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if got.ChangeType != "modify" {
		t.Fatalf("change type = %q", got.ChangeType)
	}
	// The hydrated content must equal the model's provided content exactly.
	if got.Content != provided {
		t.Fatalf("hydrated content mismatch:\n%q\nwant\n%q", got.Content, provided)
	}
}

// Identical content (a pure no-op modify) derives zero edits and fails
// validation with the at-least-one-edit error, not the mixed error.
func TestNormalizeMixedModifyIdenticalDerivesNothing(t *testing.T) {
	base := "package audit\n\nfunc Apply() {}\n"
	snap := ProposalSnapshot{Path: "x.go", Base: base}
	provided := base
	p := ProposedFileChange{
		Path: "internal/x.go", ChangeType: "modify", BaseRef: "h", Content: &provided,
	}
	_, err := MaterializeProposal(p, func(string) (ProposalSnapshot, bool) { return snap, true })
	if err == nil {
		t.Fatal("identical content must derive zero edits and fail validation (at least one edit required)")
	}
	if !strings.Contains(err.Error(), "at least one edit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// The derived old spans must appear exactly once in the base: a rewrite of
// every line of a single-occurrence block satisfies that by construction.
func TestNormalizeMixedModifyPreservesExactOnce(t *testing.T) {
	base := "alpha\nbeta\ngamma\n"
	snap := ProposalSnapshot{Path: "f.txt", Base: base}
	provided := "alpha\nBETA\ngamma\n"
	p := ProposedFileChange{
		Path: "f.txt", ChangeType: "modify", BaseRef: "h", Content: &provided,
	}
	got, err := MaterializeProposal(p, func(string) (ProposalSnapshot, bool) { return snap, true })
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if !strings.Contains(got.Content, "BETA") || strings.Contains(got.Content, "beta") {
		t.Fatalf("derived edit did not apply: %q", got.Content)
	}
}

// The model also omits base_ref entirely (the gpt-5.6-sol smoke failure):
// content-only modify with no base_ref at all. Normalization resolves the
// base by path from the delivered bundle registry.
func TestNormalizeModifyWithoutBaseRefResolvesByPath(t *testing.T) {
	base := "package audit\n\nfunc Apply() int {\n\treturn 0\n}\n"
	provided := "package audit\n\nfunc Apply() int {\n\treturn 1\n}\n"
	p := ProposedFileChange{
		Path: "internal/audit/retention.go", ChangeType: "modify", Content: &provided,
	}
	RecordProposalBases(nil) // reset
	// Record the delivered bundle the way the runner does.
	bundle := &schemas.ContextBundle{Items: []schemas.ContextItem{{
		Query:   schemas.ContextQuery{QueryType: "read_file"},
		Summary: "internal/audit/retention.go",
		Payload: map[string]any{"text": base, "path": "internal/audit/retention.go", "version": "rev-1"},
	}}}
	RecordProposalBases(bundle)
	got, err := MaterializeProposal(p, currentProposalSnapshot)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if got.Content != provided {
		t.Fatalf("hydrated mismatch:\n%q", got.Content)
	}
}

func mustRawText(text string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"text": text, "path": "internal/audit/retention.go"})
	return json.RawMessage(b)
}

// TestProposalBaseRegistryResolvesPathBaseRef pins the compatibility path:
// a model may echo the delivered view path as base_ref instead of the short
// content handle. The path must resolve only when it was actually delivered;
// unknown paths still fail loudly.
func TestProposalBaseRegistryResolvesPathBaseRef(t *testing.T) {
	base := "package audit\n\nfunc Apply() {}\n"
	reg := NewProposalBaseRegistry()
	reg.RecordFromBundle(&schemas.ContextBundle{Items: []schemas.ContextItem{{
		Query:   schemas.ContextQuery{QueryType: "read_file"},
		Summary: "internal/audit/retention.go",
		Payload: map[string]any{
			"text":    base,
			"path":    "internal/audit/retention.go",
			"version": "rev-1",
			"raw":     base,
		},
	}}})
	if _, ok := reg.Resolve("internal/audit/retention.go"); !ok {
		t.Fatal("delivered path must resolve as a base_ref compatibility alias")
	}
	if _, ok := reg.Resolve("internal/audit/not-delivered.go"); ok {
		t.Fatal("undelivered path must remain an unknown base_ref")
	}
}

// TestNormalizeCompactEditRawSourceFallback covers models that write exact
// edits against raw source text while the delivered view is read_file
// display output. The old text is absent from the numbered view but occurs
// exactly once in the raw bytes, so hydration must use raw directly.
func TestNormalizeCompactEditRawSourceFallback(t *testing.T) {
	raw := "package audit\n\nfunc Apply() {}\n"
	view := " 1 | package audit\n 2 | \n 3 | func Apply() {}\n"
	reg := NewProposalBaseRegistry()
	restore := SetProposalBases(reg)
	defer restore()
	bundle := &schemas.ContextBundle{Items: []schemas.ContextItem{{
		Query:   schemas.ContextQuery{QueryType: "read_file"},
		Summary: "internal/audit/retention.go",
		Payload: map[string]any{
			"text":    view,
			"path":    "internal/audit/retention.go",
			"version": "rev-1",
			"raw":     raw,
		},
	}}}
	reg.RecordFromBundle(bundle)
	regHandle := ""
	reg.mu.Lock()
	for path, handle := range reg.byPath {
		if path == "internal/audit/retention.go" {
			regHandle = handle
		}
	}
	reg.mu.Unlock()
	if regHandle == "" {
		t.Fatal("delivered path did not receive a handle")
	}
	provided := "package audit\n\nfunc Apply() int { return 1 }\n"
	old := ProposedFileChange{
		Path:       "internal/audit/retention.go",
		ChangeType: "modify",
		BaseRef:    regHandle,
		Edits: []TextReplacement{{
			Old: "func Apply() {}",
			New: "func Apply() int { return 1 }",
		}},
	}
	got, err := MaterializeProposal(old, currentProposalSnapshot)
	if err != nil {
		t.Fatalf("materialize raw fallback: %v", err)
	}
	if got.Content != provided {
		t.Fatalf("raw fallback content mismatch:\n%q\nwant\n%q", got.Content, provided)
	}
}
