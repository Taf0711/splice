package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// t01Prompt has no classifier keyword. The keyword classifier never returns
// standard (internal/splice/classifier.go), so this run is light: the
// default light stages plus the two command nodes. The scripted change adds
// a test, so the test runner has a real test to pass.
const t01Prompt = "add a Hello function in main.go"

var lightPlus = []string{"light", "standard", "substantial", "architectural"}

const helloMain = "package main\n\nfunc main() {}\n\n// Hello returns a greeting.\nfunc Hello() string { return \"hello\" }\n"

const helloTest = "package main\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) {\n\tif Hello() != \"hello\" {\n\t\tt.Fatal(\"wrong greeting\")\n\t}\n}\n"

// exportDefault exports the default topology and decodes it.
func exportDefault(t *testing.T, s *Scenario, path string) map[string]any {
	t.Helper()
	s.Run("pipeline", "export", "default", path).WantExit(t, 0)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var topo map[string]any
	if err := json.Unmarshal(data, &topo); err != nil {
		t.Fatalf("decode exported topology: %v\n%s", err, data)
	}
	return topo
}

// addCommandNode appends a command node and an edge from `after` to it.
func addCommandNode(topo map[string]any, name, after string, command []string, tiers []string) {
	topo["nodes"] = append(topo["nodes"].([]any), map[string]any{
		"name": name, "type": "command", "command": command, "tiers": tiers,
	})
	topo["edges"] = append(topo["edges"].([]any), map[string]any{"from": after, "to": name, "payload": "none"})
}

// TestT01CustomTopologyRunsCommandNodes is spec T-01: a library topology
// with two command nodes runs end to end and verifies.
func TestT01CustomTopologyRunsCommandNodes(t *testing.T) {
	t.Parallel()
	s := NewScenario(t)

	file := filepath.Join(s.Root, "f.json")
	topo := exportDefault(t, s, file)
	topo["name"] = "t01"
	addCommandNode(topo, "vet", "code_writer", []string{"go", "vet", "./..."}, lightPlus)
	addCommandNode(topo, "quote", "code_writer", []string{"sh", "-c", `printf '%s' "a b" > out.txt`}, lightPlus)
	writeJSON(t, file, topo)

	s.Run("pipeline", "import", file, "--yes").WantExit(t, 0)
	s.Run("pipeline", "use", "t01").WantExit(t, 0)

	s.Provider.Script(StageCodeWriter, SubmitCode(t01Prompt,
		File("main.go", "modify", helloMain),
		File("main_test.go", "create", helloTest),
	))
	r := s.Run("exec", "-o", "stream-json", t01Prompt)

	// docs/COMMANDS.md: 0 = the run completed successfully; run_end.exitCode
	// is authoritative for stream clients.
	r.WantExit(t, 0)
	ends := r.EventsOfType("run_end")
	if len(ends) != 1 || ends[0]["exitCode"] != float64(0) {
		t.Fatalf("run_end events = %v, want one with exitCode 0", ends)
	}

	final := r.Final(t)
	if final["verified"] != true {
		t.Fatalf("verified = %v, unverified_reason = %v\nstdout:\n%s", final["verified"], final["unverified_reason"], r.Stdout)
	}
	if final["topology_name"] != "t01" {
		t.Errorf("topology_name = %v, want t01", final["topology_name"])
	}

	// The run-start notices name both command nodes, once each, before any
	// stage starts.
	wantNotices := []string{
		"[topology] command node vet will run: go vet ./...\n",
		`[topology] command node quote will run: sh -c 'printf '\''%s'\'' "a b" > out.txt'` + "\n",
	}
	firstStage := len(r.Events)
	for i, ev := range r.Events {
		if ev["type"] == "stage" {
			firstStage = i
			break
		}
	}
	for _, want := range wantNotices {
		count, at := 0, -1
		for i, ev := range r.Events {
			if ev["type"] == "reasoning" && ev["delta"] == want {
				count++
				at = i
			}
		}
		if count != 1 || at > firstStage {
			t.Errorf("notice %q: seen %d time(s), last at event %d, first stage at %d", want, count, at, firstStage)
		}
	}

	// Both command nodes ran and completed (stream-json stage events).
	for _, node := range []string{"vet", "quote"} {
		var statuses []any
		for _, ev := range r.EventsOfType("stage") {
			if ev["name"] == node {
				statuses = append(statuses, ev["status"])
			}
		}
		if len(statuses) != 2 || statuses[0] != "running" || statuses[1] != "completed" {
			t.Errorf("stage %s statuses = %v, want [running completed]", node, statuses)
		}
	}

	// External checks: the quoting node wrote one argument "a b".
	out, err := os.ReadFile(filepath.Join(s.Workspace, "out.txt"))
	if err != nil || string(out) != "a b" {
		t.Errorf("out.txt = %q (err %v), want %q", out, err, "a b")
	}
	if got, err := os.ReadFile(filepath.Join(s.Workspace, "main.go")); err != nil || string(got) != helloMain {
		t.Errorf("main.go = %q (err %v), want the scripted change", got, err)
	}

	// Exactly one model request: the code writer, on the fake profile.
	reqs := s.Provider.Requests()
	if got := strings.Join(stagesOf(reqs), ","); got != StageCodeWriter || len(reqs) != 1 {
		t.Fatalf("provider requests = %v, want one %s request", reqs, StageCodeWriter)
	}
	if reqs[0].Model != FakeModel || reqs[0].Header.Get("Authorization") != "Bearer "+FakeAPIKey {
		t.Errorf("request model %q auth %q", reqs[0].Model, reqs[0].Header.Get("Authorization"))
	}
	if unused := s.Provider.Unused(); len(unused) > 0 {
		t.Errorf("unused scripted replies: %v", unused)
	}
}
