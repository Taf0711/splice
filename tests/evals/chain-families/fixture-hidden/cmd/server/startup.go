package main

import (
	"fmt"

	_ "demo/internal/admin"
	_ "demo/internal/alerts"
	_ "demo/internal/catalog"
	_ "demo/internal/config"
	_ "demo/internal/docs"
	"demo/internal/events"
	"demo/internal/flags"
	"demo/internal/jobs"
	_ "demo/internal/metrics"
	"demo/internal/perms"
	"demo/internal/registry"
	_ "demo/internal/worker"
)

// validate runs the startup checks every package registered.
func validate() error {
	if err := registry.Run("event", events.Kinds); err != nil {
		return fmt.Errorf("startup: %w", err)
	}
	if err := registry.Run("job", jobs.Types); err != nil {
		return fmt.Errorf("startup: %w", err)
	}
	if err := registry.Run("flag", flags.Names); err != nil {
		return fmt.Errorf("startup: %w", err)
	}
	if err := registry.Run("permission", perms.Names); err != nil {
		return fmt.Errorf("startup: %w", err)
	}
	return nil
}
