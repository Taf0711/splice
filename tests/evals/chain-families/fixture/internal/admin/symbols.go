package admin

import (
	"fmt"

	"demo/internal/registry"
)

// CurrencySymbols maps each currency to its value for this package.
var CurrencySymbols = map[string]string{
	"USD": "$",
	"EUR": "€",
}

func init() {
	registry.Register("currency", func(item string) error {
		if CurrencySymbols[item] == "" {
			return fmt.Errorf("currency %q cannot be shown in the console", item)
		}
		return nil
	})
}
