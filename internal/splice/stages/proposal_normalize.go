package stages

import (
	"strings"
)

// normalizeProposal accepts the common model output shape that the strict
// contract rejects: a modify entry carrying BOTH content and base_ref. The
// base snapshot resolves the base_ref; a line-level diff between the base
// text and the provided content derives exact old/new edits, which then
// flow through the standard strict path (exact-once matching, delivered-
// views span check, byte-for-byte application). Everything else passes
// through unchanged; Validate still rejects every other contract breach.
func normalizeProposal(p ProposedFileChange, baseRefToSnapshot func(baseRef string) (ProposalSnapshot, bool)) ProposedFileChange {
	if p.ChangeType != "modify" || p.Content == nil || p.BaseRef == "" || len(p.Edits) > 0 {
		return p
	}
	snap, ok := baseRefToSnapshot(p.BaseRef)
	if !ok {
		return p // unknown base_ref: Validate reports it exactly as before
	}
	edits, _ := deriveEditsFromContent(snap.Base, *p.Content)
	if len(edits) == 0 {
		// Identical content: a no-op modify. Zero the mixed fields so
		// Validate reports the edit-count error (the accurate complaint),
		// not the mixed-representation one.
		p.Content = nil
		return p
	}
	p.Edits = edits
	p.Content = nil
	return p
}

// deriveEditsFromContent diffs base text against provided content line by
// line and emits one TextReplacement per changed hunk: Old = the removed
// base lines joined with \n, New = the replacement lines. ok is false when
// the texts are identical (nothing to derive). The derived Old spans are
// exact base substrings by construction, so the strict exact-once matching
// downstream holds.
func deriveEditsFromContent(base, provided string) ([]TextReplacement, bool) {
	baseLines := strings.Split(base, "\n")
	provLines := strings.Split(provided, "\n")
	n, m := len(baseLines), len(provLines)

	// LCS table over lines (bounded by the edit bound via hunk count later).
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if baseLines[i] == provLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var edits []TextReplacement
	i, j := 0, 0
	for i < n || j < m {
		// Equal lines advance.
		for i < n && j < m && baseLines[i] == provLines[j] {
			i++
			j++
		}
		if i >= n && j >= m {
			break
		}
		// One changed hunk: consume differing base lines and provided lines.
		bStart, pStart := i, j
		for i < n && (j >= m || baseLines[i] != provLines[j]) {
			i++
		}
		for j < m && (i >= n || baseLines[i] != provLines[j]) {
			j++
		}
		oldText := strings.Join(baseLines[bStart:i], "\n")
		newText := strings.Join(provLines[pStart:j], "\n")
		if oldText == newText {
			continue
		}
		edits = append(edits, TextReplacement{Old: oldText, New: newText})
	}
	return edits, len(edits) > 0
}
