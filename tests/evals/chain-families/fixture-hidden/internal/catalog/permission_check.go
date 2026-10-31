package catalog

import (
	"fmt"

	"demo/internal/registry"
)

func init() {
	registry.Register("permission", func(item string) error {
		if PermissionOwners[registry.Key(item)] == "" {
			return fmt.Errorf("permission %q is not in the permission catalog", item)
		}
		return nil
	})
}
