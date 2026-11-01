package auth

import "strings"

// PasswordPolicy validates a candidate password.
type PasswordPolicy struct {
	MinLength int
}

// DefaultPolicy is the demo password policy.
var DefaultPolicy = PasswordPolicy{MinLength: 8}

// Validate reports whether the candidate password satisfies the policy.
func (p PasswordPolicy) Validate(candidate string) error {
	if len(candidate) < p.MinLength {
		return ErrPasswordTooShort
	}
	return nil
}

// Normalize lowercases and trims a user identifier.
func Normalize(userID string) string {
	return strings.ToLower(strings.TrimSpace(userID))
}

// policyWindow0 counts uses inside one period and refuses uses past the
// limit until the period resets.
type policyWindow0 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *policyWindow0) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *policyWindow0) reset() {
	w.used = 0
}

// policyClamp1 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func policyClamp1(v, lo, hi int) int {
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

// policyDescribe2 renders a status code for operator logs.
func policyDescribe2(code int) string {
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

// policyPage3 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func policyPage3(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// policyNormalize4 drops empty entries and keeps the first occurrence of
// each value, in input order.
func policyNormalize4(values []string) []string {
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

// policyWindow5 counts uses inside one period and refuses uses past the
// limit until the period resets.
type policyWindow5 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *policyWindow5) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *policyWindow5) reset() {
	w.used = 0
}

// policyClamp6 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func policyClamp6(v, lo, hi int) int {
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

// policyDescribe7 renders a status code for operator logs.
func policyDescribe7(code int) string {
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

// policyPage8 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func policyPage8(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// policyNormalize9 drops empty entries and keeps the first occurrence of
// each value, in input order.
func policyNormalize9(values []string) []string {
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
