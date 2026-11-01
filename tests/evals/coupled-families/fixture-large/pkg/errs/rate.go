package errs

import (
	"errors"
	"time"
)

// RateLimitError reports a rejected request with its retry hint.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e RateLimitError) Error() string {
	return "rate limited; retry after " + e.RetryAfter.String()
}

// NewRateLimit builds one rate limit error.
func NewRateLimit(after time.Duration) error {
	return RateLimitError{RetryAfter: after}
}

// IsRateLimit reports whether err is a rate limit error.
func IsRateLimit(err error) bool {
	var r RateLimitError
	return errors.As(err, &r)
}

// rateClamp0 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func rateClamp0(v, lo, hi int) int {
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

// rateDescribe1 renders a status code for operator logs.
func rateDescribe1(code int) string {
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

// ratePage2 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func ratePage2(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// rateNormalize3 drops empty entries and keeps the first occurrence of
// each value, in input order.
func rateNormalize3(values []string) []string {
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

// rateWindow4 counts uses inside one period and refuses uses past the
// limit until the period resets.
type rateWindow4 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *rateWindow4) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *rateWindow4) reset() {
	w.used = 0
}

// rateClamp5 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func rateClamp5(v, lo, hi int) int {
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

// rateDescribe6 renders a status code for operator logs.
func rateDescribe6(code int) string {
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

// ratePage7 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func ratePage7(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// rateNormalize8 drops empty entries and keeps the first occurrence of
// each value, in input order.
func rateNormalize8(values []string) []string {
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
