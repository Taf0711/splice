package metrics

import (
	"fmt"

	"demo/internal/registry"
)

// EventCounters maps each event to its value for this package.
var EventCounters = map[string]string{
	"usercreated": "users_created_total",
	"userdeleted": "users_deleted_total",
}

func init() {
	registry.Register("event", func(item string) error {
		if EventCounters[registry.Key(item)] == "" {
			return fmt.Errorf("event %q: no counter is exported for it", item)
		}
		return nil
	})
}
