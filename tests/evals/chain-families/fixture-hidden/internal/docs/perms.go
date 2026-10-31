package docs

import (
	"fmt"

	"demo/internal/registry"
)

// PermDocs maps each permission to its value for this package.
var PermDocs = map[string]string{
	"read":  "Read any record.",
	"write": "Change any record.",
}

func init() {
	registry.Register("permission", func(item string) error {
		if PermDocs[item] == "" {
			return fmt.Errorf("permission %q is undocumented", item)
		}
		return nil
	})
}
