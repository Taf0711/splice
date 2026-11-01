package admin

import (
	"fmt"

	"demo/internal/registry"
)

// FlagOwners maps each flag to its value for this package.
var FlagOwners = map[string]string{
	"new_checkout": "payments",
	"dark_mode":    "web",
}

func init() {
	registry.Register("flag", func(item string) error {
		if FlagOwners[item] == "" {
			return fmt.Errorf("flag %q has no owning team", item)
		}
		return nil
	})
}
