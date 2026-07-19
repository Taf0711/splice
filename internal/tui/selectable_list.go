package tui

type selectableListItem struct {
	Label       string
	Description string
}

type selectableListOptions struct {
	Items      []selectableListItem
	Selected   int
	Width      int
	MaxVisible int
}

// selectableListAnchorRow is how many rows above the cursor the window
// keeps visible. 1 means the window scrolls as soon as the cursor moves
// past the first visible row, keeping one row of context above.
const selectableListAnchorRow = 1

func selectableListStart(total, maxVisible, selected int) int {
	if total <= maxVisible {
		return 0
	}
	start := selected - selectableListAnchorRow
	return clampInt(start, 0, total-maxVisible)
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
