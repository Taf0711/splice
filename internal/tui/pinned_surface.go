package tui

// pinnedSurfaceEnvelope is the shared vertical budget for pinned footer
// surfaces. Today the plan panel is the only pinned surface; a future pipeline
// strip will be a second consumer. Both read their height from the same
// envelope so they degrade coherently at short terminal heights.
type pinnedSurfaceEnvelope struct {
	// available is the number of lines all pinned surfaces share, after the
	// fixed chrome (composer, idle hint, queued preview, status) and a minimum
	// transcript row are reserved. Zero means no pinned surface can render.
	available int
}

// computePinnedSurfaceEnvelope reserves the fixed footer chrome and one
// transcript row, and returns what remains for the pinned surfaces. total is
// the terminal height in lines, headerLines the pinned title bar height, and
// chromeLines the rendered height of the footer chrome below the pinned
// surfaces. The plan panel must respect this budget rather than delegate
// clipping to the transcript frame, so a short terminal never silently cuts
// the plan from the top.
func computePinnedSurfaceEnvelope(total int, headerLines int, chromeLines int) pinnedSurfaceEnvelope {
	if total <= 0 {
		// Unmeasured/headless: nothing is guaranteed to fit.
		return pinnedSurfaceEnvelope{}
	}
	reserved := headerLines + chromeLines + 1
	available := total - reserved
	if available < 0 {
		available = 0
	}
	return pinnedSurfaceEnvelope{available: available}
}

// envelopePlanBudget is the line ceiling the plan panel may occupy. It clamps
// the envelope to the historical one-third-of-screen cap so the plan never
// grows to fill freed space (that would change visible output); it only lets
// the envelope reduce the cap when short heights would otherwise clip the plan
// from the top. cap is maxInt(3, total/3).
func (e pinnedSurfaceEnvelope) planBudget(cap int) int {
	if e.available <= 0 {
		return 0
	}
	if cap < e.available {
		return cap
	}
	return e.available
}
