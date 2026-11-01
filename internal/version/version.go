// Package version carries the Splice build version and compares version
// strings. It has no dependencies, so every layer can read the build version
// without an import cycle.
package version

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is the Splice build version. The release workflow sets it at link
// time with -ldflags. A source build keeps the default, which is not a release
// version, so a minimum-version check skips instead of blocking local work.
var Version = "dev"

var semverPattern = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+].*)?$`)

// SatisfiesMin reports whether current is at least min. An empty min passes.
// A malformed min fails loud, so a declared requirement is never decorative.
// A current value that is not a release version, such as "dev" or "", passes,
// so a source build is not blocked.
func SatisfiesMin(current, min string) (bool, error) {
	min = strings.TrimSpace(min)
	if min == "" {
		return true, nil
	}
	required, err := parse(min)
	if err != nil {
		return false, fmt.Errorf("invalid minimum version %q: %w", min, err)
	}
	have, err := parse(current)
	if err != nil {
		return true, nil
	}
	return compare(have, required) >= 0, nil
}

type parts [3]int

func parse(value string) (parts, error) {
	match := semverPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return parts{}, fmt.Errorf("not a release version: %q", value)
	}
	var result parts
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(match[i+1])
		if err != nil {
			return parts{}, fmt.Errorf("not a release version: %q", value)
		}
		result[i] = n
	}
	return result, nil
}

func compare(left, right parts) int {
	for i := range left {
		switch {
		case left[i] > right[i]:
			return 1
		case left[i] < right[i]:
			return -1
		}
	}
	return 0
}
