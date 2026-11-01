// Package errs holds small error helpers for the demo module. It is not
// part of the session flow. It exists so the layout mirrors a real
// service.
package errs

import (
	"errors"
	"fmt"
)

// New makes a plain error with the given text.
var New = errors.New

// Is reports whether err matches target.
var Is = errors.Is

// Describe returns a wrapped error that names the failing input. It
// returns nil when err is nil.
func Describe(err error, what string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", what, err)
}
