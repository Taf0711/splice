package v2

import "fmt"

// VerifyRoutes compares routes captured from the running system with the
// manifest routes locked before inference. It is the runner pre-execution
// assertion and does not copy routes from configuration.
func VerifyRoutes(actual []StageRoute, m Manifest, k TraceLookupKey) error {
	if err := k.Validate(); err != nil {
		return fmt.Errorf("route verification lookup: %w", err)
	}
	if err := m.Validate(); err != nil {
		return fmt.Errorf("route verification manifest: %w", err)
	}
	expected := make(map[string]StageRoute, len(m.StageRoutes))
	for _, route := range m.StageRoutes {
		expected[route.Stage] = route
	}
	observed := make(map[string]StageRoute, len(actual))
	for _, route := range actual {
		if err := route.Validate(); err != nil {
			return fmt.Errorf("observed route: %w", err)
		}
		if _, duplicate := observed[route.Stage]; duplicate {
			return fmt.Errorf("route drift stage=%q observed duplicate route=%+v", route.Stage, route)
		}
		observed[route.Stage] = route
	}
	for stage, want := range expected {
		got, ok := observed[stage]
		if !ok {
			return fmt.Errorf("route drift stage=%q expected_route=%+v observed_route=<absent>", stage, want)
		}
		if want.Provider != got.Provider {
			return fmt.Errorf("route drift stage=%q field=provider expected=%q observed=%q expected_route=%+v observed_route=%+v", stage, want.Provider, got.Provider, want, got)
		}
		if want.Model != got.Model {
			return fmt.Errorf("route drift stage=%q field=model expected=%q observed=%q expected_route=%+v observed_route=%+v", stage, want.Model, got.Model, want, got)
		}
		if want.ReasoningEffort != got.ReasoningEffort {
			return fmt.Errorf("route drift stage=%q field=reasoning_effort expected=%q observed=%q expected_route=%+v observed_route=%+v", stage, want.ReasoningEffort, got.ReasoningEffort, want, got)
		}
	}
	for stage, got := range observed {
		if _, ok := expected[stage]; !ok {
			return fmt.Errorf("route drift stage=%q expected_route=<absent> observed_route=%+v", stage, got)
		}
	}
	return nil
}
