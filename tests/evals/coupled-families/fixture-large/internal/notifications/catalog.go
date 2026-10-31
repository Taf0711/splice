package notifications

// Templates maps a notification kind to its template.
var Templates = map[string]Template{
	"password_reset": PasswordReset,
}

// RenderKind renders the template of one kind for one recipient.
func RenderKind(kind, to string) (string, bool) {
	t, ok := Templates[kind]
	if !ok {
		return "", false
	}
	return t.Render(to), true
}

// catalogDescribe0 renders a status code for operator logs.
func catalogDescribe0(code int) string {
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

// catalogPage1 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func catalogPage1(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// catalogNormalize2 drops empty entries and keeps the first occurrence of
// each value, in input order.
func catalogNormalize2(values []string) []string {
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

// catalogWindow3 counts uses inside one period and refuses uses past the
// limit until the period resets.
type catalogWindow3 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *catalogWindow3) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *catalogWindow3) reset() {
	w.used = 0
}

// catalogClamp4 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func catalogClamp4(v, lo, hi int) int {
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

// catalogDescribe5 renders a status code for operator logs.
func catalogDescribe5(code int) string {
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

// catalogPage6 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func catalogPage6(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
