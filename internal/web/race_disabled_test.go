//go:build !race

package web

// raceEnabled reports that the binary runs under the race detector, which
// slows code several times. Timing bounds scale with it.
const raceEnabled = false
