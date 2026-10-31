package presentrun

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/presentation"
)

// The functions in this file turn graph view model facts into words. The
// terminal view and the web viewer both call them, so the two renderers
// cannot word the same fact in two ways.

// HumanCount renders a count compactly: 999, 1.2K, 3M.
func HumanCount(n int) string {
	if n < 0 {
		n = 0
	}
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 1_000_000:
		return humanCountScaled(float64(n)/1000, "K")
	default:
		return humanCountScaled(float64(n)/1_000_000, "M")
	}
}

func humanCountScaled(value float64, suffix string) string {
	text := fmt.Sprintf("%.1f%s", value, suffix)
	return strings.Replace(text, ".0"+suffix, suffix, 1)
}

// CostText renders a node's cost. A node with an unpriced request shows a
// partial cost or "cost n/a", never a false zero. It is empty when the node
// has no cost to show.
func CostText(node GraphNode) string {
	if node.CostUSD <= 0 && !node.CostUnpriced {
		return ""
	}
	if node.CostUSD <= 0 {
		return "cost n/a"
	}
	formatted, err := modelregistry.FormatCostUSD(node.CostUSD)
	if err != nil {
		return "cost n/a"
	}
	if node.CostUnpriced {
		return formatted + " partial"
	}
	return formatted
}

// StatusText is the status phrase of a node in a live graph: the status
// word, or why the node does not run, and the pass when it is not the first.
func StatusText(node GraphNode) string {
	status := "not started"
	switch {
	case node.Removed != "":
		status = "removed: " + node.Removed
	case len(node.SkippedAt) > 0:
		status = "skipped at " + strings.Join(node.SkippedAt, ", ")
	case node.NotRun:
		status = "not run"
	case node.Interrupted:
		status = "stopped when the run ended"
	case node.Status != "":
		status = presentation.StatusMarker(node.Status, presentation.GlyphTierASCII).Word
	}
	if node.Iteration > 0 {
		status += fmt.Sprintf(" · pass %d", node.Iteration+1)
	}
	return status
}

// CommandResult names a command node's result from its status: a command
// node passes only on exit status 0, and the summary carries the failure
// text.
func CommandResult(live bool, node GraphNode) string {
	switch {
	case !live:
		return "no run yet"
	case node.Removed != "":
		return "not run: " + node.Removed
	case len(node.SkippedAt) > 0:
		return "not run at this tier"
	case node.Interrupted:
		return "stopped when the run ended"
	case node.Status == presentation.NodeStatusComplete:
		return "succeeded (exit status 0)"
	case node.Status == presentation.NodeStatusFailed:
		return "failed; the summary names the cause"
	case node.Status == presentation.NodeStatusRunning:
		return "running"
	case node.Status == presentation.NodeStatusSkipped:
		return "skipped"
	case node.NotRun:
		return "not run; the run ended first"
	}
	return "not run yet"
}

// ChangedFilesText names up to five changed paths and counts the rest. A node
// that completed with no changes reads "none".
func ChangedFilesText(files []string) string {
	if len(files) == 0 {
		return "none"
	}
	shown := files
	if len(shown) > 5 {
		shown = shown[:5]
	}
	text := strings.Join(shown, ", ")
	if more := len(files) - len(shown); more > 0 {
		text += fmt.Sprintf(", +%d more", more)
	}
	return text
}

// RouteText renders provider/model (effort).
func RouteText(provider, model, effort string) string {
	text := model
	if provider != "" {
		text = provider + "/" + model
	}
	if effort != "" {
		text += " (" + effort + ")"
	}
	return text
}
