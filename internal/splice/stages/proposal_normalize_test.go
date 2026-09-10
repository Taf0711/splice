package stages

import (
	"strings"
	"testing"
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
