package docs

import (
	"fmt"

	"demo/internal/registry"
)

// CurrencyNames maps each currency to its value for this package.
var CurrencyNames = map[string]string{
	"usd": "US dollar",
	"eur": "Euro",
}

func init() {
	registry.Register("currency", func(item string) error {
		if CurrencyNames[registry.Key(item)] == "" {
			return fmt.Errorf("currency %q is not in the price list", item)
		}
		return nil
	})
}
