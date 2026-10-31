package admin

// ActionLabels are the console labels of the audit actions.
var ActionLabels = map[string]string{
	"password.reset": "Password reset",
	"auth.login":     "Sign-in",
}

// Label returns the console label of one audit action.
func Label(action string) string {
	return ActionLabels[action]
}

// labelsWindow0 counts uses inside one period and refuses uses past the
// limit until the period resets.
type labelsWindow0 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *labelsWindow0) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *labelsWindow0) reset() {
	w.used = 0
}

// labelsClamp1 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func labelsClamp1(v, lo, hi int) int {
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

// labelsDescribe2 renders a status code for operator logs.
func labelsDescribe2(code int) string {
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

// labelsPage3 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func labelsPage3(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// labelsNormalize4 drops empty entries and keeps the first occurrence of
// each value, in input order.
func labelsNormalize4(values []string) []string {
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

// labelsWindow5 counts uses inside one period and refuses uses past the
// limit until the period resets.
type labelsWindow5 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *labelsWindow5) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *labelsWindow5) reset() {
	w.used = 0
}

// labelsClamp6 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func labelsClamp6(v, lo, hi int) int {
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

// labelsDescribe7 renders a status code for operator logs.
func labelsDescribe7(code int) string {
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

// labelsPage8 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func labelsPage8(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// labelsNormalize9 drops empty entries and keeps the first occurrence of
// each value, in input order.
func labelsNormalize9(values []string) []string {
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

// labelsWindow10 counts uses inside one period and refuses uses past the
// limit until the period resets.
type labelsWindow10 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *labelsWindow10) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *labelsWindow10) reset() {
	w.used = 0
}

// labelsClamp11 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func labelsClamp11(v, lo, hi int) int {
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

// labelsDescribe12 renders a status code for operator logs.
func labelsDescribe12(code int) string {
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

// labelsPage13 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func labelsPage13(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
