package config

import (
	"fmt"

	"demo/internal/registry"
)

// MinorUnits maps each currency to its value for this package.
var MinorUnits = map[string]int{
	"USD": 2,
	"EUR": 2,
}

func init() {
	registry.Register("currency", func(item string) error {
		if MinorUnits[item] == 0 {
			return fmt.Errorf("currency %q has no rounding rule", item)
		}
		return nil
	})
}
