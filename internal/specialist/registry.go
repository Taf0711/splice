package specialist

import (
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/tools"
)

// RegisterTools registers the specialist tools for the supplied roster. The
// roster is passed by the startup caller so specialist.Load runs only once.
// The variadic form keeps direct callers that do not need the gate compatible;
// those callers receive the legacy Task description without fabricated names.
func RegisterTools(registry *tools.Registry, executor Executor, rosters ...[]Manifest) (*Runtime, error) {
	runtime := executor.BackgroundRuntime
	if runtime == nil {
		runtime = NewRuntime(RuntimeOptions{
			Manager:     executor.BackgroundManager,
			ManagerFunc: executor.BackgroundManagerFunc,
		})
	}
	toolExecutor := executor
	toolExecutor.BackgroundRuntime = runtime
	toolExecutor.BackgroundManagerFunc = runtime.Manager
	var roster []Manifest
	legacyRegistration := len(rosters) == 0
	if !legacyRegistration {
		roster = rosters[0]
	}
	if legacyRegistration || len(roster) > 0 {
		if legacyRegistration {
			registry.Register(NewTaskTool(toolExecutor))
		} else {
			registry.Register(NewTaskToolWithSpecialists(toolExecutor, specialistNames(roster)))
		}
		registry.Register(newOutputToolWithManagerFunc(runtime.Manager, executor.SessionStore))
		registry.Register(newStopToolWithManagerFunc(runtime.Manager))
	}
	registry.Register(NewGenerateTool(NewStorage(executor.Paths)))
	return runtime, nil
}

func specialistNames(roster []Manifest) []string {
	names := make([]string, 0, len(roster))
	seen := make(map[string]struct{}, len(roster))
	for _, manifest := range roster {
		name := strings.TrimSpace(manifest.Metadata.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
