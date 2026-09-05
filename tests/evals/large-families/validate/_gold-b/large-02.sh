set -e
cd "$1"
cat > internal/audit/retention_deficit.go <<'GOEOF'
package audit

import "time"

// RetentionDeficit reports how many events the next enforcement would
// drop, without dropping them. It reuses the enforcement cutoff and cap
// rules so the two can never diverge.
func RetentionDeficit(t *Trail, maxAge time.Duration, maxCount int) int {
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
	return len(t.events) - len(kept)
}
GOEOF
