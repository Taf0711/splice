package billing

// Plan is one subscription tier.
type Plan struct {
	Name         string
	MonthlyCents int64
	Seats        int
}

// Catalog lists the available subscription plans.
var Catalog = []Plan{
	{Name: "free", MonthlyCents: 0, Seats: 1},
	{Name: "team", MonthlyCents: 2900, Seats: 10},
	{Name: "scale", MonthlyCents: 9900, Seats: 100},
}

// Lookup returns the plan by name.
func Lookup(name string) (Plan, bool) {
	for _, p := range Catalog {
		if p.Name == name {
			return p, true
		}
	}
	return Plan{}, false
}

// plansPage0 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func plansPage0(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// plansNormalize1 drops empty entries and keeps the first occurrence of
// each value, in input order.
func plansNormalize1(values []string) []string {
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

// plansWindow2 counts uses inside one period and refuses uses past the
// limit until the period resets.
type plansWindow2 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *plansWindow2) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *plansWindow2) reset() {
	w.used = 0
}

// plansClamp3 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func plansClamp3(v, lo, hi int) int {
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

// plansDescribe4 renders a status code for operator logs.
func plansDescribe4(code int) string {
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

// plansPage5 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func plansPage5(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// plansNormalize6 drops empty entries and keeps the first occurrence of
// each value, in input order.
func plansNormalize6(values []string) []string {
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

// plansWindow7 counts uses inside one period and refuses uses past the
// limit until the period resets.
type plansWindow7 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *plansWindow7) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *plansWindow7) reset() {
	w.used = 0
}
