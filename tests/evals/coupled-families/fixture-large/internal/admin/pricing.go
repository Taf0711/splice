package admin

// PricingOrder is the order in which the pricing page shows the plans.
var PricingOrder = []string{"free", "team", "scale"}

// pricingDescribe0 renders a status code for operator logs.
func pricingDescribe0(code int) string {
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

// pricingPage1 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func pricingPage1(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// pricingNormalize2 drops empty entries and keeps the first occurrence of
// each value, in input order.
func pricingNormalize2(values []string) []string {
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

// pricingWindow3 counts uses inside one period and refuses uses past the
// limit until the period resets.
type pricingWindow3 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *pricingWindow3) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *pricingWindow3) reset() {
	w.used = 0
}

// pricingClamp4 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func pricingClamp4(v, lo, hi int) int {
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

// pricingDescribe5 renders a status code for operator logs.
func pricingDescribe5(code int) string {
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

// pricingPage6 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func pricingPage6(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// pricingNormalize7 drops empty entries and keeps the first occurrence of
// each value, in input order.
func pricingNormalize7(values []string) []string {
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

// pricingWindow8 counts uses inside one period and refuses uses past the
// limit until the period resets.
type pricingWindow8 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *pricingWindow8) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *pricingWindow8) reset() {
	w.used = 0
}
