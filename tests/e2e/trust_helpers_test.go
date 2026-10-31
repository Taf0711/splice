package e2e

import "path/filepath"

// touchCommand returns a command argv that creates the named marker when it
// runs. The marker path is absolute, under the scenario root.
func touchCommand(s *Scenario, name string) (string, []string) {
	return "sh", []string{"-c", "touch " + s.Marker(name)}
}

// writeProjectFile writes JSON under the workspace (for example .splice/config.json).
func writeProjectFile(s *Scenario, rel string, v any) {
	s.T.Helper()
	writeJSON(s.T, filepath.Join(s.Workspace, rel), v)
}
