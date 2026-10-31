package catalog

import (
	"fmt"

	"demo/internal/registry"
)

func init() {
	registry.Register("job", func(item string) error {
		if JobOwners[registry.Key(item)] == "" {
			return fmt.Errorf("job %q is not in the job catalog", item)
		}
		return nil
	})
}
