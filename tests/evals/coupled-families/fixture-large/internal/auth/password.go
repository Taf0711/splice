// Package auth owns password records and the reset flow for demo users.
package auth

import (
	"errors"

	"demo/internal/session"
)

// ErrUnknownUser is returned when a password operation names a user with
// no password record.
var ErrUnknownUser = errors.New("unknown user")

// PasswordService owns password records. It shares the session store with
// the rest of the server.
type PasswordService struct {
	store     *session.Store
	passwords map[string]string
}

// NewPasswordService wires a password service to a session store.
func NewPasswordService(store *session.Store) *PasswordService {
	return &PasswordService{store: store, passwords: map[string]string{}}
}

// SetInitialPassword records the first password for a user. It is a setup
// helper for tests and seeding.
func (p *PasswordService) SetInitialPassword(userID, password string) {
	p.passwords[userID] = password
}

// ResetPassword sets a new password for userID. The update succeeds when
// the user has a password record.
func (p *PasswordService) ResetPassword(userID, newPassword string) error {
	if _, ok := p.passwords[userID]; !ok {
		return ErrUnknownUser
	}
	p.passwords[userID] = newPassword
	// Extension point: a successful reset must also invalidate the
	// sessions of userID so old logins stop working. The wiring for
	// that belongs here.
	return nil
}

// passwordNormalize0 drops empty entries and keeps the first occurrence of
// each value, in input order.
func passwordNormalize0(values []string) []string {
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

// passwordWindow1 counts uses inside one period and refuses uses past the
// limit until the period resets.
type passwordWindow1 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *passwordWindow1) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *passwordWindow1) reset() {
	w.used = 0
}

// passwordClamp2 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func passwordClamp2(v, lo, hi int) int {
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

// passwordDescribe3 renders a status code for operator logs.
func passwordDescribe3(code int) string {
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

// passwordPage4 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func passwordPage4(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// passwordNormalize5 drops empty entries and keeps the first occurrence of
// each value, in input order.
func passwordNormalize5(values []string) []string {
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

// passwordWindow6 counts uses inside one period and refuses uses past the
// limit until the period resets.
type passwordWindow6 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *passwordWindow6) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *passwordWindow6) reset() {
	w.used = 0
}

// passwordClamp7 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func passwordClamp7(v, lo, hi int) int {
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

// passwordDescribe8 renders a status code for operator logs.
func passwordDescribe8(code int) string {
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
