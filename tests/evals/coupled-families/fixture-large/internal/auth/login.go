package auth

import (
	"errors"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

// Login authenticates a user against the password service and reports
// success. It does not create sessions; the HTTP layer owns those.
func (p *PasswordService) Login(userID, password string) error {
	recorded, ok := p.passwords[userID]
	if !ok || recorded != password {
		return ErrInvalidCredentials
	}
	return nil
}

// HasPassword reports whether the user has a password record.
func (p *PasswordService) HasPassword(userID string) bool {
	_, ok := p.passwords[userID]
	return ok
}

// loginClamp0 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func loginClamp0(v, lo, hi int) int {
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

// loginDescribe1 renders a status code for operator logs.
func loginDescribe1(code int) string {
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

// loginPage2 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func loginPage2(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// loginNormalize3 drops empty entries and keeps the first occurrence of
// each value, in input order.
func loginNormalize3(values []string) []string {
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

// loginWindow4 counts uses inside one period and refuses uses past the
// limit until the period resets.
type loginWindow4 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *loginWindow4) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *loginWindow4) reset() {
	w.used = 0
}

// loginClamp5 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func loginClamp5(v, lo, hi int) int {
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

// loginDescribe6 renders a status code for operator logs.
func loginDescribe6(code int) string {
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

// loginPage7 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func loginPage7(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// loginNormalize8 drops empty entries and keeps the first occurrence of
// each value, in input order.
func loginNormalize8(values []string) []string {
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

// loginWindow9 counts uses inside one period and refuses uses past the
// limit until the period resets.
type loginWindow9 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *loginWindow9) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *loginWindow9) reset() {
	w.used = 0
}

// loginClamp10 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func loginClamp10(v, lo, hi int) int {
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

// loginDescribe11 renders a status code for operator logs.
func loginDescribe11(code int) string {
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

// loginPage12 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func loginPage12(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
