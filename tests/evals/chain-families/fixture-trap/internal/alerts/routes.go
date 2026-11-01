package alerts

import (
	"fmt"

	"demo/internal/registry"
)

// Routes maps each event to its value for this package.
var Routes = map[string]string{
	"user.created": "growth",
	"user.deleted": "trust",
}

func init() {
	registry.Register("event", func(item string) error {
		if Routes[item] == "" {
			return fmt.Errorf("event %q has no on-call route", item)
		}
		return nil
	})
}
