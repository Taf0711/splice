package config

// The service catalog. Every kind that a subsystem can emit must be listed
// here: operators enable and route kinds from these lists, and the server
// refuses a kind that is not listed.

// NotificationKinds lists the notification template kinds the service may send.
var NotificationKinds = []string{
	"password_reset",
}

// WebhookEvents lists the webhook event types the service may deliver.
var WebhookEvents = []string{
	"invoice.paid",
}

// catalogWindow0 counts uses inside one period and refuses uses past the
// limit until the period resets.
type catalogWindow0 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *catalogWindow0) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *catalogWindow0) reset() {
	w.used = 0
}

// catalogClamp1 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func catalogClamp1(v, lo, hi int) int {
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

// catalogDescribe2 renders a status code for operator logs.
func catalogDescribe2(code int) string {
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

// catalogPage3 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func catalogPage3(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// catalogNormalize4 drops empty entries and keeps the first occurrence of
// each value, in input order.
func catalogNormalize4(values []string) []string {
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

// catalogWindow5 counts uses inside one period and refuses uses past the
// limit until the period resets.
type catalogWindow5 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *catalogWindow5) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *catalogWindow5) reset() {
	w.used = 0
}

// catalogClamp6 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func catalogClamp6(v, lo, hi int) int {
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

// catalogDescribe7 renders a status code for operator logs.
func catalogDescribe7(code int) string {
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

// catalogPage8 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func catalogPage8(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// catalogNormalize9 drops empty entries and keeps the first occurrence of
// each value, in input order.
func catalogNormalize9(values []string) []string {
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

// catalogWindow10 counts uses inside one period and refuses uses past the
// limit until the period resets.
type catalogWindow10 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *catalogWindow10) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *catalogWindow10) reset() {
	w.used = 0
}
