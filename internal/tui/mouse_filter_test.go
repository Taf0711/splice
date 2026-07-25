package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestMouseEventFilterThrottlesMotionNotWheel(t *testing.T) {
	base := time.Unix(0, 0)
	// Wheel events are not throttled, so they never call now(); only motion
	// events advance the clock. These three times are for the three motion
	// events below.
	times := []time.Time{
		base,                            // first motion (primes the clock)
		base.Add(5 * time.Millisecond),  // inside window, dropped
		base.Add(15 * time.Millisecond), // boundary, passes
	}
	index := 0
	filter := newMouseEventFilter(func() time.Time {
		current := times[index]
		index++
		return current
	}, 15*time.Millisecond)

	// Wheel events always pass through: each carries a discrete scroll delta,
	// so dropping one loses input and makes trackpad scroll feel laggy. Wheel
	// events do not call now() and do not prime the motion clock.
	if got := filter(nil, testMouseWheel(tea.MouseWheelDown, 0, 0)); got == nil {
		t.Fatal("first wheel event should pass through")
	}
	if got := filter(nil, testMouseWheel(tea.MouseWheelUp, 0, 0)); got == nil {
		t.Fatal("second wheel event should pass through (wheel is not throttled)")
	}
	// Motion is idempotent (latest hover position wins), so it is safe to drop
	// inside the window. The window starts at the first motion event.
	if got := filter(nil, tea.MouseMotionMsg(tea.Mouse{X: 1, Y: 1})); got == nil {
		t.Fatal("first motion event should pass through (primes the motion clock)")
	}
	if got := filter(nil, tea.MouseMotionMsg(tea.Mouse{X: 2, Y: 2})); got != nil {
		t.Fatal("motion event inside throttle window should be dropped")
	}
	if got := filter(nil, tea.MouseMotionMsg(tea.Mouse{X: 3, Y: 3})); got == nil {
		t.Fatal("mouse event at throttle boundary should pass through")
	}
}

func TestMouseEventFilterDoesNotThrottleKeyboard(t *testing.T) {
	called := false
	filter := newMouseEventFilter(func() time.Time {
		called = true
		return time.Unix(0, 0)
	}, 15*time.Millisecond)

	msg := testKey('x')
	if got := filter(nil, msg); got != msg {
		t.Fatalf("keyboard event = %#v, want original message", got)
	}
	if called {
		t.Fatal("keyboard events should not touch the mouse throttle clock")
	}
}
