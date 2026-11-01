package registry

import (
	"strings"
	"unicode"
)

// Check reports why an item cannot be served, or nil.
type Check func(item string) error

var checks = map[string][]Check{}

// Register adds a startup check for one kind of item (event, flag, job,
// currency). Packages register their checks in init.
func Register(kind string, c Check) {
	checks[kind] = append(checks[kind], c)
}

// Run checks every item of one kind and returns the first failure.
func Run(kind string, items []string) error {
	for _, item := range items {
		for _, c := range checks[kind] {
			if err := c(item); err != nil {
				return err
			}
		}
	}
	return nil
}

// Key is the lookup form of an item: its letters and digits.
func Key(item string) string {
	var b strings.Builder
	for _, r := range item {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
