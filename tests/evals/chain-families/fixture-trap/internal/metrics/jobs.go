package metrics

import (
	"fmt"

	"demo/internal/registry"
)

// JobCounters maps each job to its value for this package.
var JobCounters = map[string]string{
	"send-digest":    "digest_runs_total",
	"prune-sessions": "session_prunes_total",
}

func init() {
	registry.Register("job", func(item string) error {
		if JobCounters[item] == "" {
			return fmt.Errorf("job %q: no counter is exported for it", item)
		}
		return nil
	})
}
