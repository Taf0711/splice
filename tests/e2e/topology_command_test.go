package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// helloCodeReply is the code writer answer both command-node scenarios use.
func helloCodeReply() Reply {
	return SubmitCode(t01Prompt,
		File("main.go", "modify", helloMain),
		File("main_test.go", "create", helloTest),
	)
}

// TestT02FailingCommandNode is spec T-02: a command node that exits non-zero
// fails the pipeline. The run is not verified, the reason names the node, and
// the exit code is 4 (docs/COMMANDS.md: the run ended without a complete
// result). The unchanged failure repeats, so the cycle rule stops the run
// after two iterations.
func TestT02FailingCommandNode(t *testing.T) {
	t.Parallel()
	s := NewScenario(t)

	file := filepath.Join(s.Root, "f.json")
	topo := exportDefault(t, s, file)
	topo["name"] = "t02"
	addCommandNode(topo, "vet", "code_writer", []string{"go", "vet", "./..."}, lightPlus)
	addCommandNode(topo, "fail", "code_writer", []string{"sh", "-c", "exit 3"}, lightPlus)
	writeJSON(t, file, topo)

	s.Run("pipeline", "import", file, "--yes").WantExit(t, 0)
	s.Run("pipeline", "use", "t02").WantExit(t, 0)

	// Two iterations: the second identical failure is the cycle.
	s.Provider.Script(StageCodeWriter, helloCodeReply(), helloCodeReply())
	r := s.Run("exec", "-o", "stream-json", t01Prompt)

	// docs/COMMANDS.md: 4 = the run ended without a complete result.
	r.WantExit(t, 4)
	ends := r.EventsOfType("run_end")
	if len(ends) != 1 || ends[0]["exitCode"] != float64(4) || ends[0]["status"] != "incomplete" {
		t.Errorf("run_end events = %v, want one incomplete with exitCode 4", ends)
	}

	final := r.Final(t)
	if final["verified"] != false {
		t.Errorf("verified = %v, want false\nstdout:\n%s", final["verified"], r.Stdout)
	}
	if final["status"] != "failed" {
		t.Errorf("status = %v, want failed", final["status"])
	}
	reason, _ := final["abort_reason"].(string)
	if !strings.Contains(reason, "fail:") || !strings.Contains(reason, "sh -c 'exit 3'") {
		t.Errorf("abort_reason = %q, want it to name the fail node and its command", reason)
	}
	if !strings.Contains(reason, "repeated unchanged stage failure (stage_error: command node fail)") {
		t.Errorf("abort_reason = %q, want the stage_error failure kind and the node name", reason)
	}

	// Exactly the two code writer requests.
	if n := len(s.Provider.Requests()); n != 2 {
		t.Errorf("provider requests = %d, want 2 code writer requests", n)
	}
	if unused := s.Provider.Unused(); len(unused) > 0 {
		t.Errorf("unused scripted replies: %v", unused)
	}
}
