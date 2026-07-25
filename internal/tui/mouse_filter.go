package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const mouseEventThrottleInterval = 15 * time.Millisecond

// mouseEventFilter throttles MouseMotionMsg only. Wheel events carry a
// discrete scroll delta, so dropping one loses input and makes trackpad
// momentum scroll feel laggy (the terminal's synchronized-output frame
// pacing, Mode 2026 default in bubbletea v2.0.7, already bounds the visible
// redraw rate). Motion is idempotent, the latest hover position wins, so it
// is safe to drop.
func mouseEventFilter() func(tea.Model, tea.Msg) tea.Msg {
	return newMouseEventFilter(time.Now, mouseEventThrottleInterval)
}

func newMouseEventFilter(now func() time.Time, minInterval time.Duration) func(tea.Model, tea.Msg) tea.Msg {
	var last time.Time
	return func(_ tea.Model, msg tea.Msg) tea.Msg {
		switch msg.(type) {
		case tea.MouseMotionMsg:
			current := now()
			if !last.IsZero() && current.Sub(last) < minInterval {
				return nil
			}
			last = current
		}
		return msg
	}
}
