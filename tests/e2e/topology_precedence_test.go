package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// topologyWithCommand exports the default topology, names it, and adds one
// command node after the code writer. It returns the topology document.
func topologyWithCommand(t *testing.T, s *Scenario, name, node string, command []string) map[string]any {
	t.Helper()
	topo := exportDefault(t, s, filepath.Join(s.Root, "export-"+name+".json"))
	topo["name"] = name
	addCommandNode(topo, node, "code_writer", command, lightPlus)
	return topo
}

// importActive installs a topology document in the user library and makes it
// the active pipeline.
func importActive(t *testing.T, s *Scenario, topo map[string]any) {
	t.Helper()
	name := topo["name"].(string)
	file := filepath.Join(s.Root, "import-"+name+".json")
	writeJSON(t, file, topo)
	s.Run("pipeline", "import", file, "--yes").WantExit(t, 0)
	s.Run("pipeline", "use", name).WantExit(t, 0)
}

// countReasoning counts stream-json reasoning deltas that contain text.
func countReasoning(r Result, text string) int {
	n := 0
	for _, ev := range r.EventsOfType("reasoning") {
		if d, _ := ev["delta"].(string); strings.Contains(d, text) {
			n++
		}
	}
	return n
}

// commandNodeRan reports whether a stage event for the node reached completed.
func commandNodeRan(r Result, node string) bool {
	for _, ev := range r.EventsOfType("stage") {
		if ev["name"] == node && ev["status"] == "completed" {
			return true
		}
	}
	return false
}

// overrideWarning is the warning a --pipeline run writes over a trusted project file.
func overrideWarning(flagPath, projectPath string) string {
	return "--pipeline " + flagPath + " overrides the trusted project topology " + projectPath
}

// TestT03PipelineFlagOverridesTrustedProject is spec T-03: in a trusted
// workspace with a project topology, --pipeline wins and the override notice
// appears once. Each output mode runs in its own scenario.
func TestT03PipelineFlagOverridesTrustedProject(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"text", "stream-json"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			s := NewScenario(t)

			flagFile := filepath.Join(s.Root, "flag.json")
			writeJSON(t, flagFile, topologyWithCommand(t, s, "flagtopo", "from-flag",
				[]string{"sh", "-c", "printf flag > flag.txt"}))
			projectFile := s.WriteFile(".splice/pipeline.json", "")
			writeJSON(t, projectFile, topologyWithCommand(t, s, "projecttopo", "from-project",
				[]string{"sh", "-c", "printf project > project.txt"}))

			s.Provider.Script(StageCodeWriter, helloCodeReply())
			args := []string{"exec", "--trust", "--pipeline", flagFile}
			if mode == "stream-json" {
				args = append(args, "-o", "stream-json")
			}
			r := s.Run(append(args, t01Prompt)...)
			r.WantExit(t, 0)

			// The flag topology ran; the project topology did not.
			if got, err := os.ReadFile(filepath.Join(s.Workspace, "flag.txt")); err != nil || string(got) != "flag" {
				t.Errorf("flag.txt = %q (err %v), want the flag topology's command output", got, err)
			}
			if _, err := os.Stat(filepath.Join(s.Workspace, "project.txt")); err == nil {
				t.Errorf("project.txt exists: the project topology ran")
			}

			// The override notice appears exactly once, in the mode's channel.
			want := overrideWarning(flagFile, projectFile)
			var seen int
			if mode == "text" {
				seen = strings.Count(r.Stderr, want)
			} else {
				seen = countReasoning(r, want)
				if n := len(r.EventsOfType("final")); n != 1 {
					t.Fatalf("final events = %d, want 1", n)
				}
				if final := r.Final(t); final["topology_name"] != "flagtopo" {
					t.Errorf("topology_name = %v, want flagtopo", final["topology_name"])
				}
			}
			if seen != 1 {
				t.Errorf("override notice seen %d time(s), want 1\nstderr:\n%s", seen, r.Stderr)
			}
		})
	}
}

// TestT04PrecedenceMatrix is spec T-04: an untrusted project file is ignored
// and the active library entry runs; a trusted project file wins over the
// active library entry.
func TestT04PrecedenceMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		trust   bool
		wantRun string // the topology that runs
		wantOut string // the command output file that must exist
		noOut   string // the command output file that must not exist
	}{
		{name: "untrusted-project-vs-library", trust: false, wantRun: "libtopo", wantOut: "lib.txt", noOut: "project.txt"},
		{name: "trusted-project-vs-library", trust: true, wantRun: "projecttopo", wantOut: "project.txt", noOut: "lib.txt"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewScenario(t)

			importActive(t, s, topologyWithCommand(t, s, "libtopo", "from-library",
				[]string{"sh", "-c", "printf lib > lib.txt"}))
			projectFile := s.WriteFile(".splice/pipeline.json", "")
			writeJSON(t, projectFile, topologyWithCommand(t, s, "projecttopo", "from-project",
				[]string{"sh", "-c", "printf project > project.txt"}))

			s.Provider.Script(StageCodeWriter, helloCodeReply())
			args := []string{"exec", "-o", "stream-json"}
			if tc.trust {
				args = append(args, "--trust")
			}
			r := s.Run(append(args, t01Prompt)...)
			r.WantExit(t, 0)

			if final := r.Final(t); final["topology_name"] != tc.wantRun {
				t.Errorf("topology_name = %v, want %s", final["topology_name"], tc.wantRun)
			}
			if _, err := os.Stat(filepath.Join(s.Workspace, tc.wantOut)); err != nil {
				t.Errorf("%s missing: the expected topology did not run (%v)", tc.wantOut, err)
			}
			if _, err := os.Stat(filepath.Join(s.Workspace, tc.noOut)); err == nil {
				t.Errorf("%s exists: the other topology ran", tc.noOut)
			}

			// An untrusted project file is reported; a trusted one is not.
			ignored := countReasoning(r, "ignored untrusted project topology "+projectFile)
			if tc.trust && ignored != 0 {
				t.Errorf("trusted run reported the project file as ignored %d time(s)", ignored)
			}
			if !tc.trust && ignored != 1 {
				t.Errorf("untrusted run reported the ignored project file %d time(s), want 1", ignored)
			}
		})
	}
}

// TestT11WorktreeIgnoresUncommittedProjectTopology is spec T-11: a worktree
// run checks out committed files only, so an uncommitted trusted project
// topology is not loaded, and the notice names the topology that ran.
func TestT11WorktreeIgnoresUncommittedProjectTopology(t *testing.T) {
	t.Parallel()
	s := NewScenario(t)

	importActive(t, s, topologyWithCommand(t, s, "libtopo", "from-library",
		[]string{"sh", "-c", "printf lib > lib.txt"}))
	// Uncommitted: the file exists only in the stable workspace.
	writeJSON(t, s.WriteFile(".splice/pipeline.json", ""), topologyWithCommand(t, s, "projecttopo", "from-project",
		[]string{"sh", "-c", "printf project > project.txt"}))

	s.Provider.Script(StageCodeWriter, helloCodeReply())
	r := s.Run("exec", "--worktree", "--trust", "-o", "stream-json", t01Prompt)
	r.WantExit(t, 0)

	// The run names the library topology, and nothing from the project file.
	if n := countReasoning(r, "[topology] active pipeline from user library"); n != 1 {
		t.Errorf("active pipeline notice seen %d time(s), want 1", n)
	}
	if n := countReasoning(r, "from-project"); n != 0 {
		t.Errorf("project command node named %d time(s), want 0", n)
	}
	if strings.Contains(r.Stdout, "projecttopo") {
		t.Errorf("stdout mentions the uncommitted project topology")
	}
	if !commandNodeRan(r, "from-library") {
		t.Errorf("library command node from-library did not complete")
	}
	if final := r.Final(t); final["topology_name"] != "libtopo" {
		t.Errorf("topology_name = %v, want libtopo", final["topology_name"])
	}
}
