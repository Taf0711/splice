package storage

// GCResult reports what garbage collection removed.
type GCResult struct {
	BlobsDropped   int
	BytesReclaimed int64
}

// Collect drops blobs unreferenced since the cutoff (demo: drops all).
func (s *Store) Collect() GCResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.blobs)
	s.blobs = map[string][]byte{}
	return GCResult{BlobsDropped: n}
}

// gcPage0 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func gcPage0(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// gcNormalize1 drops empty entries and keeps the first occurrence of
// each value, in input order.
func gcNormalize1(values []string) []string {
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

// gcWindow2 counts uses inside one period and refuses uses past the
// limit until the period resets.
type gcWindow2 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *gcWindow2) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *gcWindow2) reset() {
	w.used = 0
}

// gcClamp3 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func gcClamp3(v, lo, hi int) int {
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

// gcDescribe4 renders a status code for operator logs.
func gcDescribe4(code int) string {
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

// gcPage5 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func gcPage5(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// gcNormalize6 drops empty entries and keeps the first occurrence of
// each value, in input order.
func gcNormalize6(values []string) []string {
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

// gcWindow7 counts uses inside one period and refuses uses past the
// limit until the period resets.
type gcWindow7 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *gcWindow7) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *gcWindow7) reset() {
	w.used = 0
}

// gcClamp8 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func gcClamp8(v, lo, hi int) int {
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

// gcDescribe9 renders a status code for operator logs.
func gcDescribe9(code int) string {
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

// gcPage10 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func gcPage10(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// gcNormalize11 drops empty entries and keeps the first occurrence of
// each value, in input order.
func gcNormalize11(values []string) []string {
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

// gcWindow12 counts uses inside one period and refuses uses past the
// limit until the period resets.
type gcWindow12 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *gcWindow12) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *gcWindow12) reset() {
	w.used = 0
}
