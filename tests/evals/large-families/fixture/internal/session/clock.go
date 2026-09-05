package session

import "time"

// Clock abstracts time for tests and scheduling.
type Clock interface {
    Now() time.Time
}

// SystemClock reads the wall clock.
type SystemClock struct{}

// Now returns time.Now.
func (SystemClock) Now() time.Time { return time.Now() }
