set -e
cd "$1"
cat > internal/audit/retention_deficit.go <<'GOEOF'
package audit

import "time"

// Wrong: reports the kept count instead of the drop count.
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
	return len(kept)
}
GOEOF
