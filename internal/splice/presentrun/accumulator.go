package presentrun

import (
	"fmt"

	"github.com/Taf0711/splice/internal/presentation"
)

// Accumulator owns the presentation.State lifecycle for one pipeline run. It
// consumes the same events the pipeline already emits, derives snapshots via
// the reducer, and counts what happened to every event.
//
// Error policy (the checkpoint's one deliberate asymmetry): presentation
// emission is observability, not control flow. An event the reducer refuses
// is counted and logged through the caller's warning path, but it NEVER
// aborts or crashes the run. The accumulator keeps the last good state, so
// a malformed event degrades the presentation only, never the pipeline.
// This is correct because the snapshot stream is a projection of run truth
// for human consumption; the pipeline's own typed schemas and validation are
// the control path and stay fail-closed. Silently dropping an event would be
// wrong, which is why every refusal is counted and surfaced.
type Accumulator struct {
	state   presentation.State
	applied int
	skipped int
	errors  int
	onWarn  func(msg string)
}

// New creates an accumulator. onWarn is the warning sink for refused events;
// it may be nil (counters still update, nothing is logged).
func New(onWarn func(msg string)) *Accumulator {
	return &Accumulator{onWarn: onWarn}
}

// Apply feeds one presentation event into the reducer. A refused event is
// counted and logged; the accumulated state stays at its last good value.
func (a *Accumulator) Apply(event presentation.StreamEventLike) {
	if event == nil {
		a.errors++
		a.warn("presentrun: refusing nil presentation event")
		return
	}
	next, err := presentation.Apply(a.state, event)
	if err != nil {
		a.errors++
		a.warn(fmt.Sprintf("presentrun: ignoring presentation event: %v", err))
		return
	}
	a.applied++
	a.state = next
}

// Skip counts an event the caller chose not to feed (for example a
// trajectory decision that is not representable as an intervention).
func (a *Accumulator) Skip() {
	a.skipped++
}

// Snapshot returns the current accumulated state. It always passes
// presentation Validate for legal event sequences.
func (a *Accumulator) Snapshot() presentation.State {
	return a.state
}

// Counts returns (applied, skipped, errors) for the run so far.
func (a *Accumulator) Counts() (applied, skipped, errors int) {
	return a.applied, a.skipped, a.errors
}

func (a *Accumulator) warn(msg string) {
	if a.onWarn != nil {
		a.onWarn(msg)
	}
}
