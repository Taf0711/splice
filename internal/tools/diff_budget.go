package tools

import (
	"fmt"
	"strings"
)

// diffMarkerSlack reserves room for the structural-truncation marker so the
// kept diff plus its marker can never exceed the byte budget.
const diffMarkerSlack = 320

// truncateDiffStructurally keeps whole changed-file sections of a git-style
// unified diff within maxBytes instead of slicing bytes at a fixed position.
//
// Head+tail truncation is position-blind. On a diff it drops an arbitrary set
// of files and cuts hunks in half, so the model sees a patch it cannot apply
// and cannot tell what was removed. This budgeter drops whole sections from the
// end, names how many files and bytes were omitted, and leaves every kept hunk
// intact.
//
// It falls back to truncateHeadTailWithTotal when the text is not a git-style
// unified diff, when only one section exists (a single file over budget cannot
// be kept whole), when the retained text is a capture head+tail whose middle
// was already dropped (total exceeds len(value), so sections would not be
// contiguous), or when even the first section exceeds the budget.
func truncateDiffStructurally(value string, total, maxBytes int) (string, int, bool) {
	if maxBytes <= 0 || total <= maxBytes {
		return value, total, false
	}
	if total != len(value) {
		// A capture gap already removed the middle. Joining sections across
		// that gap would present non-contiguous hunks as one patch.
		return truncateHeadTailWithTotal(value, total, maxBytes)
	}
	if !isGitUnifiedDiff(value) {
		return truncateHeadTailWithTotal(value, total, maxBytes)
	}
	sections := splitDiffSections(value)
	if len(sections) < 2 {
		return truncateHeadTailWithTotal(value, total, maxBytes)
	}
	budget := maxBytes - diffMarkerSlack
	if budget <= 0 {
		return truncateHeadTailWithTotal(value, total, maxBytes)
	}
	kept, used := 0, 0
	for kept < len(sections) && used+len(sections[kept]) <= budget {
		used += len(sections[kept])
		kept++
	}
	if kept == 0 {
		return truncateHeadTailWithTotal(value, total, maxBytes)
	}
	// The marker length depends on the omitted counts, so shrink the kept set
	// until the marker fits rather than assuming the slack was enough.
	for kept > 0 {
		marker := diffTruncationMarker(len(sections)-kept, len(sections), total-used)
		if used+len(marker) <= maxBytes {
			return strings.Join(sections[:kept], "") + marker, total, true
		}
		kept--
		used -= len(sections[kept])
	}
	return truncateHeadTailWithTotal(value, total, maxBytes)
}

// diffTruncationMarker names what the structural cut removed and points at the
// recovery path. It is single-line so it survives any downstream formatter.
func diffTruncationMarker(omitted, totalFiles, omittedBytes int) string {
	return fmt.Sprintf(
		"\n[splice] output truncated structurally: %d of %d changed files omitted (%d bytes) — every file above is complete; read_file the spill file for the rest\n",
		omitted, totalFiles, omittedBytes,
	)
}

// isGitUnifiedDiff reports whether value carries at least one `git diff`
// section header. Plain `diff -u` output has no `diff --git` line and is left
// to the head+tail path.
func isGitUnifiedDiff(value string) bool {
	if strings.HasPrefix(value, "diff --git ") {
		return true
	}
	return strings.Contains(value, "\ndiff --git ")
}

// splitDiffSections splits value at `diff --git` line starts. Each returned
// section keeps its own header and every hunk that follows it, in order, byte
// for byte. A preamble before the first header (a `git show` commit block, for
// example) is attached to the first section so nothing is dropped from the
// front.
func splitDiffSections(value string) []string {
	lines := strings.SplitAfter(value, "\n")
	starts := make([]int, 0, 8)
	offset := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			starts = append(starts, offset)
		}
		offset += len(line)
	}
	if len(starts) == 0 {
		return nil
	}
	if starts[0] != 0 {
		starts[0] = 0
	}
	sections := make([]string, 0, len(starts))
	for i, start := range starts {
		end := len(value)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		sections = append(sections, value[start:end])
	}
	return sections
}
