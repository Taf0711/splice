package cli

import (
	"fmt"

	"github.com/Taf0711/splice/internal/sandbox"
)

func addConfiguredReadRoots(scope *sandbox.Scope, roots []string) error {
	for _, root := range roots {
		if _, err := scope.AddRead(root); err != nil {
			return fmt.Errorf("read root %q: %w", root, err)
		}
	}
	return nil
}
