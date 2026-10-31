package audit

import (
	"fmt"

	"demo/internal/registry"
)

// Labels maps each event to its value for this package.
var Labels = map[string]string{
	"user.created": "User created",
	"user.deleted": "User deleted",
}

func init() {
	registry.Register("event", func(item string) error {
		if Labels[item] == "" {
			return fmt.Errorf("event %q: the audit trail cannot render it", item)
		}
		return nil
	})
}
