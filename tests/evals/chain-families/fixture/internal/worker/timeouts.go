package worker

import (
	"fmt"

	"demo/internal/registry"
)

// Timeouts maps each job to its value for this package.
var Timeouts = map[string]int{
	"send-digest":    300,
	"prune-sessions": 60,
}

func init() {
	registry.Register("job", func(item string) error {
		if Timeouts[item] == 0 {
			return fmt.Errorf("job %q would run without a deadline", item)
		}
		return nil
	})
}
