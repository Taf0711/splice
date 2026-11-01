package flags

import (
	"fmt"
	"regexp"
	"strings"
)

// EnvVar is the environment variable that carries a flag list.
const EnvVar = "SPLICE_FLAGS"

// namePattern matches dotted lower-case flag names: area.name.subname.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// ValidateName checks a flag name against the naming pattern.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("feature flag name is required")
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid feature flag name %q: expected lower-case dotted words like area.name", name)
	}
	return nil
}

// ParseList parses the comma-list form shared by the SPLICE_FLAGS environment
// variable and the --flags command-line override.
//
// Grammar, one item per entry:
//
//	name        enable the flag (short form)
//	-name       disable the flag
//	name=1      enable the flag (explicit)
//	name=0      disable the flag (explicit)
//
// Accepted booleans are 1/0, true/false, and on/off. An item that repeats a
// name already seen in the same list is an error, not last-wins: silent
// last-wins is the kind of default this package exists to refuse.
func ParseList(raw string) (map[string]bool, error) {
	out := make(map[string]bool)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, enabled, err := parseItem(item)
		if err != nil {
			return nil, err
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("feature flag %q is set twice in one list", name)
		}
		out[name] = enabled
	}
	return out, nil
}

// parseItem parses one list entry into a name and value.
func parseItem(item string) (string, bool, error) {
	name := item
	enabled := true
	if rest, ok := strings.CutPrefix(name, "-"); ok {
		name = rest
		enabled = false
	}
	if rawName, rawValue, ok := strings.Cut(name, "="); ok {
		name = rawName
		parsed, err := parseBool(rawValue)
		if err != nil {
			return "", false, fmt.Errorf("feature flag %q: %w", name, err)
		}
		enabled = parsed
	}
	if err := ValidateName(name); err != nil {
		return "", false, err
	}
	return name, enabled, nil
}

// parseBool accepts the boolean spellings the flag surface documents.
func parseBool(raw string) (bool, error) {
	switch raw {
	case "1", "true", "on":
		return true, nil
	case "0", "false", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q: expected 1/0, true/false, or on/off", raw)
	}
}
