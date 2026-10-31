package catalog

import (
	"fmt"

	"demo/internal/registry"
)

func init() {
	registry.Register("event", func(item string) error {
		if EventOwners[registry.Key(item)] == "" {
			return fmt.Errorf("event %q is not in the event catalog", item)
		}
		return nil
	})
}
