package alerts

import (
	"fmt"

	"demo/internal/registry"
)

// JobPages maps each job to its value for this package.
var JobPages = map[string]string{
	"send-digest":    "growth",
	"prune-sessions": "platform",
}

func init() {
	registry.Register("job", func(item string) error {
		if JobPages[item] == "" {
			return fmt.Errorf("job %q: its failures page nobody", item)
		}
		return nil
	})
}
