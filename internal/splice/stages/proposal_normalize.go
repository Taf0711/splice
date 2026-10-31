package stages

import (
	"strings"
)

// normalizeProposal accepts the common model output shapes that the strict
// contract rejects: (a) a modify entry carrying BOTH content and base_ref,
// (b) a modify entry carrying content with NO base_ref at all, and (c) the
// hedged modify carrying content AND base_ref AND edits together. The base
// snapshot resolves via base_ref or (when omitted) via the by-path index
// of the delivered context bundle.
//
// For (a) and (b), a line-level diff between the provided content and the
// snapshot text derives exact old/new edits. For (c), the explicit edits
// are authoritative whenever every old span occurs exactly once in one of
// the snapshot's texts (the delivered view, or the raw bytes the
// materializer falls back to), and the redundant hedge content is dropped;
// only when the edits resolve against neither text does the content become
// authoritative. Derived and kept edits inherit their anchor: edits
// checked against the raw bytes are applied by the materializer's raw
// fallback, so they must be matched there too.
//
// Either way exactly one representation reaches the strict path. An empty
// or whitespace-only content is a degenerate hedge (recorded in the P4
// payloads: content set to an empty string beside real edits) and is
// never an intent to wipe the file; it is dropped before any diff can
// derive a deletion. Everything else passes through unchanged; Validate
// still rejects every other contract breach.
func normalizeProposal(p ProposedFileChange, baseRefToSnapshot func(baseRef string) (ProposalSnapshot, bool)) ProposedFileChange {
	if p.ChangeType != "modify" || p.Content == nil {
		return p
	}
	if strings.TrimSpace(*p.Content) == "" {
		// Degenerate hedge: empty content beside (possibly) real edits.
		// Diffing it against any base would derive a full-file deletion.
		p.Content = nil
		return p
	}
	var snap ProposalSnapshot
	var ok bool
	if p.BaseRef != "" {
		snap, ok = baseRefToSnapshot(p.BaseRef)
	} else {
		snap, ok = currentProposalBases.ResolveByPath(p.Path)
	}
	if !ok {
		return p // unresolvable base: Validate reports it exactly as before
	}
	editsMatchOnce := func(source string) bool {
		for _, e := range p.Edits {
			if strings.Count(source, e.Old) != 1 {
				return false
			}
		}
		return true
	}
	if len(p.Edits) > 0 {
		// Case (c): the model hedged with both representations. Keep the
		// explicit edits when they are coherent with one of the snapshot's
		// texts; the content is the redundant hedge. The materializer
		// matches against the delivered view first and falls back to the
		// raw bytes, so edits coherent with either text are usable.
		if editsMatchOnce(snap.Base) || (snap.Raw != "" && editsMatchOnce(snap.Raw)) {
			p.Content = nil
			if p.BaseRef == "" {
				p.BaseRef = HandleFor(snap.ViewDigest)
			}
			return p
		}
	}
	// The explicit edits resolve against neither text, so the content is
	// the only remaining authority. Aim the diff at the raw bytes when
	// present: the recorded model text is raw-anchored, and the derived
	// edits then ride the materializer's raw fallback. With no raw bytes
	// the delivered view is the only source.
	diffBase := snap.Base
	if snap.Raw != "" {
		diffBase = snap.Raw
	}
	edits, _ := deriveEditsFromContent(diffBase, *p.Content)
	if len(edits) == 0 {
		// Identical content: a no-op modify. Zero the mixed fields so
		// Validate reports the edit-count error (the accurate complaint),
		// not the mixed-representation one.
		p.Content = nil
		return p
	}
	p.Edits = edits
	p.Content = nil
	if p.BaseRef == "" {
		// The model omitted base_ref; fill the resolved handle so the
		// downstream baseRefToSnapshot resolves the same snapshot.
		p.BaseRef = HandleFor(snap.ViewDigest)
	}
	return p
}

// deriveEditsFromContent diffs base text against provided content line by
// line and emits one TextReplacement per changed hunk: Old = the removed
// base lines joined with \n, New = the replacement lines. The derived Old
// spans are exact base substrings by construction, so the strict
// exact-once matching downstream holds.
func deriveEditsFromContent(base, provided string) ([]TextReplacement, bool) {
	baseLines := strings.Split(base, "\n")
	provLines := strings.Split(provided, "\n")
	n, m := len(baseLines), len(provLines)

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
		for i < n && j < m && baseLines[i] == provLines[j] {
			i++
			j++
		}
		if i >= n && j >= m {
			break
		}
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
