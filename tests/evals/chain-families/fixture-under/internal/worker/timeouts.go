package worker

import (
	"fmt"

	"demo/internal/registry"
)

// Timeouts maps each job to its value for this package.
var Timeouts = map[string]string{
	"digest": "300s",
	"prune":  "60s",
}

func init() {
	registry.Register("job", func(item string) error {
		if Timeouts[item] == "" {
			return fmt.Errorf("job %q would run without a deadline", item)
		}
		return nil
	})
}
