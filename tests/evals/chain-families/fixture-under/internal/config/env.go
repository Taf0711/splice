package config

import (
	"fmt"

	"demo/internal/registry"
)

// FlagEnv maps each flag to its value for this package.
var FlagEnv = map[string]string{
	"checkout": "FLAG_CHECKOUT",
	"darkmode": "FLAG_DARKMODE",
}

func init() {
	registry.Register("flag", func(item string) error {
		if FlagEnv[item] == "" {
			return fmt.Errorf("flag %q cannot be overridden from the environment", item)
		}
		return nil
	})
}
