package webhooks

// Events maps each webhook event type to its human description.
var Events = map[string]string{
	"invoice.paid": "An invoice was paid",
}

// Describe returns the description of one event type.
func Describe(event string) (string, bool) {
	d, ok := Events[event]
	return d, ok
}

// eventsClamp0 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func eventsClamp0(v, lo, hi int) int {
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

// eventsDescribe1 renders a status code for operator logs.
func eventsDescribe1(code int) string {
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

// eventsPage2 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func eventsPage2(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// eventsNormalize3 drops empty entries and keeps the first occurrence of
// each value, in input order.
func eventsNormalize3(values []string) []string {
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

// eventsWindow4 counts uses inside one period and refuses uses past the
// limit until the period resets.
type eventsWindow4 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *eventsWindow4) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *eventsWindow4) reset() {
	w.used = 0
}

// eventsClamp5 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func eventsClamp5(v, lo, hi int) int {
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

// eventsDescribe6 renders a status code for operator logs.
func eventsDescribe6(code int) string {
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

// eventsPage7 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func eventsPage7(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// eventsNormalize8 drops empty entries and keeps the first occurrence of
// each value, in input order.
func eventsNormalize8(values []string) []string {
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

// eventsWindow9 counts uses inside one period and refuses uses past the
// limit until the period resets.
type eventsWindow9 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *eventsWindow9) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *eventsWindow9) reset() {
	w.used = 0
}

// eventsClamp10 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func eventsClamp10(v, lo, hi int) int {
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

// eventsDescribe11 renders a status code for operator logs.
func eventsDescribe11(code int) string {
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
