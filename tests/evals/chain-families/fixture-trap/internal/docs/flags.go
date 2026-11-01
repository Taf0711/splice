package docs

import (
	"fmt"

	"demo/internal/registry"
)

// FlagDocs maps each flag to its value for this package.
var FlagDocs = map[string]string{
	"newcheckout": "One-page checkout.",
	"darkmode":    "Dark color scheme.",
}

func init() {
	registry.Register("flag", func(item string) error {
		if FlagDocs[registry.Key(item)] == "" {
			return fmt.Errorf("flag %q is undocumented", item)
		}
		return nil
	})
}
