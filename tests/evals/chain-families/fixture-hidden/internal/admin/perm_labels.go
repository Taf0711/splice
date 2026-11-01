package admin

import (
	"fmt"

	"demo/internal/registry"
)

// PermLabels maps each permission to its value for this package.
var PermLabels = map[string]string{
	"read":  "Read",
	"write": "Write",
}

func init() {
	registry.Register("permission", func(item string) error {
		if PermLabels[item] == "" {
			return fmt.Errorf("permission %q has no console label", item)
		}
		return nil
	})
}
