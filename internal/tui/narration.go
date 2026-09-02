package tui

// narration.go (P3 GAP-L, contract §2.5/§9.6/§9.7, E2E frames): transcript
// narration classes and verbosity. The E2E frames say "the transcript already
// is the record" — so GAP-L is NOT a parallel narration store; it is a
// classifier over the EXISTING transcript rows plus a live verbosity control
// that hides/shows them. Stable identity comes from transcriptRowKey (already
// run-scoped and dedup-stable), satisfying DoD 43/44 without a second ID
// scheme.

// NarrationClass maps the 11 contract classes onto the existing row kinds.
// The class is DERIVED at render time from the row's kind/tool — no new
// pipeline events, no new persisted state (the class survives resume because
// the rows it derives from persist).
type NarrationClass int

const (
	NarrationUser NarrationClass = iota
	NarrationAgentNarration
	NarrationAgentDecision
	NarrationAgentPlanUpdate
	NarrationAgentObservation
	NarrationAgentAction
	NarrationToolActivity
	NarrationEvidence
	NarrationGate
	NarrationReceipt
	NarrationSystemNotice
)

// narrationVerbosity is the live verbosity level. Default is detailed
// (everything shows); quiet collapses tool activity to one-line summaries;
// debug adds nothing yet (reserved for raw payloads). Switching never
// restarts the session or alters runtime policy — it only re-renders
// (DoD 41/47).
type narrationVerbosity int

const (
	verbosityQuiet narrationVerbosity = iota
	verbosityNormal
	verbosityDetailed
)

// classifyNarration derives a row's narration class from its fields. Pure
// function over the row — the reducer's projection of runtime truth.
func classifyNarration(row transcriptRow) NarrationClass {
	switch row.kind {
	case rowUser:
		return NarrationUser
	case rowAssistant:
		return NarrationAgentNarration
	case rowReasoning:
		return NarrationAgentDecision
	case rowToolCall:
		return NarrationAgentAction
	case rowToolResult:
		return NarrationToolActivity
	case rowPermission, rowAskUser:
		return NarrationGate
	case rowError:
		return NarrationReceipt
	case rowSpecialist:
		return NarrationAgentObservation
	case rowSystem, rowRecap, rowWelcome:
		return NarrationSystemNotice
	default:
		return NarrationSystemNotice
	}
}

// narrationVisible reports whether a row shows at the given verbosity.
// Tool call/result pairs collapse to their result card at quiet (the result
// card already collapses long bodies — the collapse_tools contract); plain
// system chatter drops at quiet; everything else survives every level.
func narrationVisible(row transcriptRow, verbosity narrationVerbosity) bool {
	if verbosity >= verbosityDetailed {
		return true
	}
	switch classifyNarration(row) {
	case NarrationAgentAction:
		// The call row collapses into its result card (the settledRow
		// collapse rule already pairs them); at quiet, hide the standalone
		// call row.
		return false
	case NarrationToolActivity:
		return verbosity >= verbosityNormal
	case NarrationSystemNotice:
		// Welcome/recap stay; transient command chatter drops at quiet.
		return row.kind != rowSystem || verbosity >= verbosityNormal
	default:
		return true
	}
}

// next cycles quiet -> normal -> detailed -> quiet (DoD 41: live switching).
func (v narrationVerbosity) next() narrationVerbosity {
	switch v {
	case verbosityQuiet:
		return verbosityNormal
	case verbosityNormal:
		return verbosityDetailed
	default:
		return verbosityQuiet
	}
}

// label renders the status-line segment for the current verbosity. Detailed
// (the default) shows nothing — no noise for the common case.
func (v narrationVerbosity) label() string {
	switch v {
	case verbosityQuiet:
		return "· quiet"
	case verbosityNormal:
		return "· normal"
	default:
		return ""
	}
}

// narrationVisibleRow applies the model's current verbosity to one row.
func (m model) narrationVisibleRow(row transcriptRow) bool {
	return narrationVisible(row, m.narrationVerbosityLevel)
}
