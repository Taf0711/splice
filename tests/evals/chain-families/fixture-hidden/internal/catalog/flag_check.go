package catalog

import (
	"fmt"

	"demo/internal/registry"
)

func init() {
	registry.Register("flag", func(item string) error {
		if FlagOwners[registry.Key(item)] == "" {
			return fmt.Errorf("flag %q is not in the flag catalog", item)
		}
		return nil
	})
}
