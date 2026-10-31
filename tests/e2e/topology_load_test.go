package e2e

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// overEnvelopeTopology exports the default topology and adds two prompt nodes
// with a 400-token input budget each. Untiered, the trivial tier's active
// nodes total 2800 input tokens, over its 2500 envelope. With tiers set to
// lightPlus, the light envelope (21000) holds them. The tiers argument is nil
// to leave the nodes untiered.
func overEnvelopeTopology(t *testing.T, s *Scenario, name string, tiers []string) map[string]any {
	t.Helper()
	topo := exportDefault(t, s, filepath.Join(s.Root, "export-"+name+".json"))
	topo["name"] = name
	budget := map[string]any{"input_max": 400, "output_max": 100, "skippable": false}
	nodes := topo["nodes"].([]any)
	edges := topo["edges"].([]any)
	for _, n := range []string{"p1", "p2"} {
		node := map[string]any{"name": n, "type": "prompt", "prompt": "summarize", "budget": budget}
		if tiers != nil {
			node["tiers"] = tiers
		}
		nodes = append(nodes, node)
	}
	edges = append(edges,
		map[string]any{"from": "code_writer", "to": "p1", "payload": "none"},
		map[string]any{"from": "p1", "to": "p2", "payload": "none"})
	topo["nodes"], topo["edges"] = nodes, edges
	return topo
}

// noRequests fails the test when the provider received any request.
func noRequests(t *testing.T, s *Scenario) {
	t.Helper()
	if reqs := s.Provider.Requests(); len(reqs) != 0 {
		t.Errorf("provider requests = %d, want 0: %v", len(reqs), reqs)
	}
}

// fileNamed lists every file under root whose base name is name.
func fileNamed(t *testing.T, root, name string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == name {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// TestT05LoadRefusals is spec T-05: each malformed topology is refused with
// its documented message, before any provider request.
func TestT05LoadRefusals(t *testing.T) {
	t.Parallel()

	t.Run("unknown-field", func(t *testing.T) {
		t.Parallel()
		s := NewScenario(t)
		topo := exportDefault(t, s, filepath.Join(s.Root, "base.json"))
		topo["name"] = "typo"
		topo["modle"] = "x"
		file := filepath.Join(s.Root, "typo.json")
		writeJSON(t, file, topo)

		r := s.Run("exec", "--pipeline", file, t01Prompt)
		// docs/COMMANDS.md: 2 = the command or input was invalid.
		if r.ExitCode != 2 {
			t.Errorf("exit %d, want 2 per docs/COMMANDS.md (product mismatch)\nstderr:\n%s", r.ExitCode, r.Stderr)
		}
		if !strings.Contains(r.Stderr, `unknown field "modle"`) {
			t.Errorf("stderr does not name the unknown field:\n%s", r.Stderr)
		}
		noRequests(t, s)
	})

	t.Run("min-version-wins-over-unknown-field", func(t *testing.T) {
		t.Parallel()
		s := NewScenario(t)
		topo := exportDefault(t, s, filepath.Join(s.Root, "base.json"))
		topo["name"] = "newer"
		topo["modle"] = "x"
		topo["splice_min_version"] = "0.4.0"
		file := filepath.Join(s.Root, "newer.json")
		writeJSON(t, file, topo)

		r := s.Run("exec", "--pipeline", file, t01Prompt)
		if r.ExitCode != 2 {
			t.Errorf("exit %d, want 2 per docs/COMMANDS.md (product mismatch)\nstderr:\n%s", r.ExitCode, r.Stderr)
		}
		if !strings.Contains(r.Stderr, "requires Splice 0.4.0 or newer; this build is 0.3.0") {
			t.Errorf("stderr does not name the minimum version:\n%s", r.Stderr)
		}
		if strings.Contains(r.Stderr, "unknown field") {
			t.Errorf("the unknown-field error won over the min-version error:\n%s", r.Stderr)
		}
		noRequests(t, s)
	})

	t.Run("reserved-name-default", func(t *testing.T) {
		t.Parallel()
		s := NewScenario(t)
		topo := exportDefault(t, s, filepath.Join(s.Root, "base.json"))
		file := filepath.Join(s.Root, "reserved.json")
		writeJSON(t, file, topo) // name stays "default"

		r := s.Run("pipeline", "import", file, "--yes")
		if r.ExitCode == 0 {
			t.Errorf("import of a topology named default exited 0\nstdout:\n%s", r.Stdout)
		}
		if !strings.Contains(r.Stdout+r.Stderr, `pipeline name "default" is reserved`) {
			t.Errorf("output does not name the reserved name:\n%s%s", r.Stdout, r.Stderr)
		}
		if _, err := os.Stat(filepath.Join(s.ConfigDir, "pipelines", "default.json")); err == nil {
			t.Errorf("default.json was written to the library")
		}
	})

	t.Run("traversal-name-import", func(t *testing.T) {
		t.Parallel()
		s := NewScenario(t)
		topo := exportDefault(t, s, filepath.Join(s.Root, "base.json"))
		topo["name"] = "../x"
		file := filepath.Join(s.Root, "traversal.json")
		writeJSON(t, file, topo)

		r := s.Run("pipeline", "import", file, "--yes")
		if r.ExitCode == 0 {
			t.Errorf("import of ../x exited 0\nstdout:\n%s", r.Stdout)
		}
		if !strings.Contains(r.Stdout+r.Stderr, `invalid pipeline name "../x"`) {
			t.Errorf("output does not name the invalid name:\n%s%s", r.Stdout, r.Stderr)
		}
		// Nothing was written outside the library: no x.json anywhere under the
		// scenario root, and the library folder holds no entry.
		if found := fileNamed(t, s.Root, "x.json"); len(found) != 0 {
			t.Errorf("x.json written: %v", found)
		}
		if found := fileNamed(t, s.Root, "x"); len(found) != 0 {
			t.Errorf("x written: %v", found)
		}
		if entries, _ := os.ReadDir(filepath.Join(s.ConfigDir, "pipelines")); len(entries) != 0 {
			t.Errorf("library holds %d entries after a refused import", len(entries))
		}
	})

	t.Run("untiered-over-trivial-envelope", func(t *testing.T) {
		t.Parallel()
		s := NewScenario(t)
		file := filepath.Join(s.Root, "over.json")
		writeJSON(t, file, overEnvelopeTopology(t, s, "over", nil))

		r := s.Run("exec", "--pipeline", file, t01Prompt)
		// docs/COMMANDS.md: 2 = the command or input was invalid.
		if r.ExitCode != 2 {
			t.Errorf("exit %d, want 2 per docs/COMMANDS.md (product mismatch)\nstderr:\n%s", r.ExitCode, r.Stderr)
		}
		for _, want := range []string{"tier trivial", "2500 input envelope", `Set "tiers"`} {
			if !strings.Contains(r.Stderr, want) {
				t.Errorf("stderr lacks %q:\n%s", want, r.Stderr)
			}
		}
		noRequests(t, s)
	})

	t.Run("tiered-loads", func(t *testing.T) {
		t.Parallel()
		s := NewScenario(t)
		file := filepath.Join(s.Root, "tiered.json")
		writeJSON(t, file, overEnvelopeTopology(t, s, "tiered", lightPlus))

		s.Provider.Script(StageCodeWriter, helloCodeReply())
		s.Provider.Script(StagePrompt, Answer("first summary"), Answer("second summary"))
		r := s.Run("exec", "-o", "stream-json", "--pipeline", file, t01Prompt)
		r.WantExit(t, 0)
		if final := r.Final(t); final["topology_name"] != "tiered" {
			t.Errorf("topology_name = %v, want tiered", final["topology_name"])
		}
		if unused := s.Provider.Unused(); len(unused) > 0 {
			t.Errorf("unused scripted replies: %v", unused)
		}
	})
}
