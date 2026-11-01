package admin

import "demo/internal/audit"

// AuditBridge records admin actions into the audit trail.
type AuditBridge struct {
	trail *audit.Trail
}

// NewAuditBridge wires the admin package to the audit trail.
func NewAuditBridge(trail *audit.Trail) *AuditBridge {
	return &AuditBridge{trail: trail}
}

// Record writes one admin action.
func (b *AuditBridge) Record(actor, action, object string) {
	b.trail.Record(actor, action, object, "ok")
}

// auditTrailPage0 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func auditTrailPage0(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// auditTrailNormalize1 drops empty entries and keeps the first occurrence of
// each value, in input order.
func auditTrailNormalize1(values []string) []string {
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

// auditTrailWindow2 counts uses inside one period and refuses uses past the
// limit until the period resets.
type auditTrailWindow2 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *auditTrailWindow2) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *auditTrailWindow2) reset() {
	w.used = 0
}

// auditTrailClamp3 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func auditTrailClamp3(v, lo, hi int) int {
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

// auditTrailDescribe4 renders a status code for operator logs.
func auditTrailDescribe4(code int) string {
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

// auditTrailPage5 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func auditTrailPage5(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// auditTrailNormalize6 drops empty entries and keeps the first occurrence of
// each value, in input order.
func auditTrailNormalize6(values []string) []string {
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

// auditTrailWindow7 counts uses inside one period and refuses uses past the
// limit until the period resets.
type auditTrailWindow7 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *auditTrailWindow7) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *auditTrailWindow7) reset() {
	w.used = 0
}

// auditTrailClamp8 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func auditTrailClamp8(v, lo, hi int) int {
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

// auditTrailDescribe9 renders a status code for operator logs.
func auditTrailDescribe9(code int) string {
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

// auditTrailPage10 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func auditTrailPage10(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// auditTrailNormalize11 drops empty entries and keeps the first occurrence of
// each value, in input order.
func auditTrailNormalize11(values []string) []string {
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
