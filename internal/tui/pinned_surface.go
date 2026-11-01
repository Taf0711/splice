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

// pinnedSurfaceClaim is one surface's request against the shared envelope.
// Surfaces declare claims; the allocator, not the footer, decides who renders
// at how many lines. Adding a surface means adding a claim, not new inline
// budget math in the footer.
type pinnedSurfaceClaim struct {
	// name identifies the surface. It is the grant's key in the allocator
	// result.
	name string
	// lines is how many lines the surface wants.
	lines int
	// exact makes the claim all-or-nothing: a strip renders whole or not at
	// all. An exact claim that does not fit is skipped and leaves its space
	// for later claims. A flexible claim takes what remains, up to lines.
	exact bool
}

// pinnedSurfaceGrant is the allocator's decision for one claim.
type pinnedSurfaceGrant struct {
	name  string
	lines int
}

// allocatePinnedSurfaces divides the envelope among claims in slice order.
// Order is priority: earlier claims pick first. An exact claim is granted
// only when its full request fits the remaining space; otherwise it is
// skipped and the space stays available for later claims. A flexible claim
// is granted the remaining space up to its request. Zero available grants
// nothing. The result has one grant per claim, in claim order.
func allocatePinnedSurfaces(e pinnedSurfaceEnvelope, claims []pinnedSurfaceClaim) []pinnedSurfaceGrant {
	grants := make([]pinnedSurfaceGrant, 0, len(claims))
	remaining := e.available
	for _, claim := range claims {
		grant := 0
		if remaining > 0 && claim.lines > 0 {
			if claim.exact {
				if claim.lines <= remaining {
					grant = claim.lines
				}
			} else if claim.lines < remaining {
				grant = claim.lines
			} else {
				grant = remaining
			}
		}
		remaining -= grant
		grants = append(grants, pinnedSurfaceGrant{name: claim.name, lines: grant})
	}
	return grants
}
