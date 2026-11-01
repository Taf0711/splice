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

// errorsWindow0 counts uses inside one period and refuses uses past the
// limit until the period resets.
type errorsWindow0 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *errorsWindow0) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *errorsWindow0) reset() {
	w.used = 0
}

// errorsClamp1 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func errorsClamp1(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// errorsDescribe2 renders a status code for operator logs.
func errorsDescribe2(code int) string {
	switch {
	case code < 0:
		return "invalid"
	case code == 0:
		return "idle"
	case code < 10:
		return "active"
	case code < 100:
		return "degraded"
	default:
		return "saturated"
	}
}

// errorsPage3 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func errorsPage3(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// errorsNormalize4 drops empty entries and keeps the first occurrence of
// each value, in input order.
func errorsNormalize4(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// errorsWindow5 counts uses inside one period and refuses uses past the
// limit until the period resets.
type errorsWindow5 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *errorsWindow5) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *errorsWindow5) reset() {
	w.used = 0
}

// errorsClamp6 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func errorsClamp6(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// errorsDescribe7 renders a status code for operator logs.
func errorsDescribe7(code int) string {
	switch {
	case code < 0:
		return "invalid"
	case code == 0:
		return "idle"
	case code < 10:
		return "active"
	case code < 100:
		return "degraded"
	default:
		return "saturated"
	}
}

// errorsPage8 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func errorsPage8(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// errorsNormalize9 drops empty entries and keeps the first occurrence of
// each value, in input order.
func errorsNormalize9(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// errorsWindow10 counts uses inside one period and refuses uses past the
// limit until the period resets.
type errorsWindow10 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *errorsWindow10) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *errorsWindow10) reset() {
	w.used = 0
}

// errorsClamp11 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func errorsClamp11(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// errorsDescribe12 renders a status code for operator logs.
func errorsDescribe12(code int) string {
	switch {
	case code < 0:
		return "invalid"
	case code == 0:
		return "idle"
	case code < 10:
		return "active"
	case code < 100:
		return "degraded"
	default:
		return "saturated"
	}
}
