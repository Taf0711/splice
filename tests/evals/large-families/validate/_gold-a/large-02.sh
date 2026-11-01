set -e
cd "$1"
cat > internal/audit/retention_enforce.go <<'GOEOF'
package audit

import "time"

// EnforceRetention drops events older than maxAge, then keeps only the
// newest maxCount events, and returns how many remain.
func (t *Trail) EnforceRetention(maxAge time.Duration, maxCount int) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock()
	kept := make([]Event, 0, len(t.events))
	for _, e := range t.events {
		if now.Sub(e.At) <= maxAge {
			kept = append(kept, e)
		}
	}
	if maxCount >= 0 && len(kept) > maxCount {
		kept = kept[len(kept)-maxCount:]
	}
	t.events = kept
	return len(kept)
}
GOEOF
