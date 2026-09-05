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
