package stages

// Work package C1 (warm-cost handoff Section 7): the model-facing compact
// edit proposal and its shared materializer.
//
// Two layers, deliberately kept apart:
//
//   - ProposedFileChange is what the MODEL sends: create carries content
//     only; modify carries a base_ref plus text replacements matched
//     against the source the model actually received in its B1 views;
//     delete carries only a base_ref. A small edit in a large file is a
//     small proposal.
//   - FileChange keeps its canonical full-content form host-side. Repair
//     hashing (writerContentHashes), test attribution
//     (writerAuthoredTestFiles), changed-path extraction, and reporting
//     all consume complete contents and are NOT migrated to edits.
//
// The materializer is ONE shared pure function for the writer and the
// test generator. It validates deterministically, matches every
// replacement against the SAME base snapshot, applies non-overlapping
// offsets deterministically, and preserves unrelated bytes and line
// endings exactly. No fuzzy matching: a replacement that does not match
// exactly fails before any write.
//
// base_ref is a source HANDLE the host supplied in its views (short
// digest prefix); the host resolves it to a full content digest. The
// model never computes hashes.
//
// Versioning: proposals are the "compact/1" protocol; full-content files
// remain the legacy "full/1" form. Both parsers accept both; the live
// comparison advertises one versioned schema to both arms.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// ProposalProtocolVersion names the edit protocol advertised to both arms.
// The writer and test-generator schemas, prompts, and materializer all
// carry this version, and tests pair them so a drift cannot ship.
const ProposalProtocolVersion = "compact/1"

// ProposalProtocolLegacy names the historical full-content form the
// parsers still accept for stored artifacts and unsupported routes.
const ProposalProtocolLegacy = "full/1"

// maxProposalEdits bounds the replacements in one proposed file so a
// runaway proposal fails validation, not the filesystem.
const maxProposalEdits = 200

// TextReplacement is one exact-match replacement inside one base
// snapshot. Old must appear exactly once in the base text the model
// actually received (no fuzzy matching); New may be empty (a valid
// deletion of the matched span) but Old may not.
type TextReplacement struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// ProposedFileChange is the model-facing compact proposal for one file.
// Exactly one representation is valid:
//
//   - create: Content present, Edits and BaseRef absent.
//   - modify: BaseRef and one or more Edits present, Content absent.
//   - delete: BaseRef present, Edits and Content absent.
type ProposedFileChange struct {
	Path       string            `json:"path"`
	ChangeType string            `json:"change_type"`
	BaseRef    string            `json:"base_ref,omitempty"`
	Edits      []TextReplacement `json:"edits,omitempty"`
	Content    *string           `json:"content,omitempty"`
}

// Validate checks the representation rules before any filesystem work.
func (p ProposedFileChange) Validate() error {
	if p.Path == "" {
		return fmt.Errorf("proposal path is required")
	}
	switch p.ChangeType {
	case "create":
		if p.Content == nil {
			return fmt.Errorf("proposal create %s: content is required", p.Path)
		}
		if p.BaseRef != "" || len(p.Edits) > 0 {
			return fmt.Errorf("proposal create %s: mixed representation (base_ref/edits with content)", p.Path)
		}
	case "modify":
		if p.BaseRef == "" {
			return fmt.Errorf("proposal modify %s: base_ref is required", p.Path)
		}
		if p.Content != nil {
			return fmt.Errorf("proposal modify %s: mixed representation (content with base_ref/edits)", p.Path)
		}
		if len(p.Edits) == 0 {
			return fmt.Errorf("proposal modify %s: at least one edit is required", p.Path)
		}
		if len(p.Edits) > maxProposalEdits {
			return fmt.Errorf("proposal modify %s: %d edits exceed the bound of %d", p.Path, len(p.Edits), maxProposalEdits)
		}
		for i, e := range p.Edits {
			if e.Old == "" {
				return fmt.Errorf("proposal modify %s: edits[%d] old must not be empty (an empty old is ambiguous)", p.Path, i)
			}
		}
	case "delete":
		if p.BaseRef == "" {
			return fmt.Errorf("proposal delete %s: base_ref is required", p.Path)
		}
		if p.Content != nil || len(p.Edits) > 0 {
			return fmt.Errorf("proposal delete %s: mixed representation (content/edits on delete)", p.Path)
		}
	default:
		return fmt.Errorf("proposal change type must be create, modify, or delete, got %q", p.ChangeType)
	}
	return nil
}

// ProposalSnapshot is the host-side base the edits are matched against:
// the exact bytes one SourceView set represented for this file. The
// model may only edit text present in these views; a whole-file host
// baseline the model never saw does NOT authorize an edit in unseen
// content.
type ProposalSnapshot struct {
	Path    string
	Version string // full sha256 of the base bytes
	Base    string // the delivered text (concatenation of the model's views)
}

// MaterializeProposal validates one proposal against the supplied base
// snapshots and produces the canonical full-content FileChange. Purity:
// no filesystem, no I/O; the caller applies the hydrated change through
// the existing guarded applyFileChanges path.
//
// baseRefToSnapshot resolves a model-supplied base_ref handle to a
// snapshot. Matching rules, all fail-before-write:
//   - unknown base_ref: loud error (the model invented a handle);
//   - modify: every edit's Old must occur EXACTLY ONCE in the snapshot's
//     base text; zero matches is a missing match, multiple is ambiguous;
//   - overlapping replacements fail (a later replacement must not target
//     text created by an earlier one);
//   - line endings ride byte-for-byte inside the matched spans; the
//     materializer never normalizes.
func MaterializeProposal(p ProposedFileChange, baseRefToSnapshot func(baseRef string) (ProposalSnapshot, bool)) (schemas.FileChange, error) {
	if err := p.Validate(); err != nil {
		return schemas.FileChange{}, err
	}
	switch p.ChangeType {
	case "create":
		return schemas.FileChange{Path: p.Path, ChangeType: "create", Content: *p.Content}, nil
	case "delete":
		if _, ok := baseRefToSnapshot(p.BaseRef); !ok {
			return schemas.FileChange{}, fmt.Errorf("proposal delete %s: unknown base_ref %q", p.Path, p.BaseRef)
		}
		return schemas.FileChange{Path: p.Path, ChangeType: "delete"}, nil
	}

	// modify: resolve the base once and match all edits against it.
	snap, ok := baseRefToSnapshot(p.BaseRef)
	if !ok {
		return schemas.FileChange{}, fmt.Errorf("proposal modify %s: unknown base_ref %q", p.Path, p.BaseRef)
	}
	base := snap.Base

	type span struct {
		start, end int
		old, new   string
	}
	spans := make([]span, 0, len(p.Edits))
	for i, e := range p.Edits {
		count := strings.Count(base, e.Old)
		if count == 0 {
			return schemas.FileChange{}, fmt.Errorf("proposal modify %s: edits[%d] old text not found in the delivered source (no fuzzy matching; request the current source first)", p.Path, i)
		}
		if count > 1 {
			return schemas.FileChange{}, fmt.Errorf("proposal modify %s: edits[%d] old text matches %d locations; the match is ambiguous and was not applied", p.Path, i, count)
		}
		start := strings.Index(base, e.Old)
		spans = append(spans, span{start: start, end: start + len(e.Old), old: e.Old, new: e.New})
	}
	// Overlap check on character offsets: sort by start, then any
	// next.start < prev.end is an overlap. Byte-exact, deterministic.
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	for i := 1; i < len(spans); i++ {
		if spans[i].start < spans[i-1].end {
			return schemas.FileChange{}, fmt.Errorf("proposal modify %s: edits overlap in the base text (edits[%d] and a later edit touch the same span); no write was attempted", p.Path, i)
		}
	}
	// Apply in order over the ORIGINAL base. A replacement's New text is
	// never searched by a later replacement because all offsets were
	// resolved against the same snapshot first.
	var b strings.Builder
	prev := 0
	for _, s := range spans {
		b.WriteString(base[prev:s.start])
		b.WriteString(s.new)
		prev = s.end
	}
	b.WriteString(base[prev:])
	return schemas.FileChange{Path: p.Path, ChangeType: "modify", Content: b.String()}, nil
}

// MaterializeProposals is the batch form: every proposal is validated and
// hydrated BEFORE any caller starts applying writes, so a bad later
// proposal rejects the whole batch up front (the preflight rule). A
// duplicate path is a batch error. The returned digest maps path ->
// sha256 of the hydrated canonical content, for the expected-base check
// at the mutating tool boundary (C2).
func MaterializeProposals(proposals []ProposedFileChange, baseRefToSnapshot func(baseRef string) (ProposalSnapshot, bool)) ([]schemas.FileChange, map[string]string, error) {
	changes := make([]schemas.FileChange, 0, len(proposals))
	digests := make(map[string]string, len(proposals))
	seen := map[string]bool{}
	for i, p := range proposals {
		if seen[p.Path] {
			return nil, nil, fmt.Errorf("proposals[%d]: duplicate path %s (later proposals would silently overwrite earlier ones)", i, p.Path)
		}
		seen[p.Path] = true
		change, err := MaterializeProposal(p, baseRefToSnapshot)
		if err != nil {
			return nil, nil, fmt.Errorf("proposals[%d]: %w", i, err)
		}
		changes = append(changes, change)
		if change.Content != "" || change.ChangeType != "delete" {
			digests[change.Path] = HashBytes([]byte(change.Content))
		}
	}
	return changes, digests, nil
}

// HashBytes is the shared content digest (hex sha256). Exported here so
// the materializer, the tool-boundary check (C2), and the repair hashes
// all use one definition of content identity.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
