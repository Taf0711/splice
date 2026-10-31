package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestT09EmptyPromptAnswer is spec T-09: a prompt node that returns no text
// fails the run, the error names the node, and the error does not describe a
// ledger mismatch.
func TestT09EmptyPromptAnswer(t *testing.T) {
	t.Parallel()
	s := NewScenario(t)

	topo := exportDefault(t, s, filepath.Join(s.Root, "base.json"))
	topo["name"] = "prompt"
	budget := map[string]any{"input_max": 400, "output_max": 100, "skippable": false}
	topo["nodes"] = append(topo["nodes"].([]any), map[string]any{
		"name": "ask", "type": "prompt", "prompt": "summarize the change", "budget": budget,
	})
	topo["edges"] = append(topo["edges"].([]any), map[string]any{"from": "code_writer", "to": "ask", "payload": "none"})
	file := filepath.Join(s.Root, "prompt.json")
	writeJSON(t, file, topo)

	// The empty answer fails each iteration; the run stops after two.
	s.Provider.Script(StageCodeWriter, helloCodeReply(), helloCodeReply())
	s.Provider.Script(StagePrompt, Answer(""), Answer(""))

	r := s.Run("exec", "-o", "stream-json", "--pipeline", file, t01Prompt)
	// docs/COMMANDS.md: 4 = the run ended without a complete result.
	r.WantExit(t, 4)

	errs := r.EventsOfType("error")
	if len(errs) != 1 {
		t.Fatalf("error events = %d, want 1\nstdout:\n%s", len(errs), r.Stdout)
	}
	msg, _ := errs[0]["message"].(string)
	if !strings.Contains(msg, "prompt node ask") {
		t.Errorf("error does not name the prompt node: %q", msg)
	}
	if strings.Contains(strings.ToLower(r.Stdout+r.Stderr), "ledger") {
		t.Errorf("output mentions a ledger mismatch:\n%s", msg)
	}
	if unused := s.Provider.Unused(); len(unused) > 0 {
		t.Errorf("unused scripted replies: %v", unused)
	}
}

// TestT10TrivialTierDefaultRun is spec T-10: a trivial run with the default
// topology completes, and it does not claim verification. The reason names the
// tier.
func TestT10TrivialTierDefaultRun(t *testing.T) {
	t.Parallel()
	s := NewScenario(t)

	// "typo" is a trivial keyword and the request is short.
	s.Provider.Script(StageCodeWriter, helloCodeReply())
	r := s.Run("exec", "-o", "stream-json", "fix a typo in main.go")
	// docs/COMMANDS.md: 0 = completed. A run that verified nothing still exits 0.
	r.WantExit(t, 0)
	ends := r.EventsOfType("run_end")
	if len(ends) != 1 || ends[0]["exitCode"] != float64(0) {
		t.Errorf("run_end events = %v, want one with exitCode 0", ends)
	}

	final := r.Final(t)
	if final["tier"] != "trivial" {
		t.Errorf("tier = %v, want trivial", final["tier"])
	}
	if final["verified"] != false {
		t.Errorf("verified = %v, want false", final["verified"])
	}
	reason, _ := final["unverified_reason"].(string)
	if !strings.Contains(reason, "trivial") {
		t.Errorf("unverified_reason = %q, want it to name the trivial tier", reason)
	}
}

// TestT12TextModeNoticesPrintOnce is spec T-12: a text mode run prints each
// [topology] notice exactly once on stderr. The run has a flag topology, two
// command nodes, and an ignored untrusted project file, so it has several
// notices to repeat.
func TestT12TextModeNoticesPrintOnce(t *testing.T) {
	t.Parallel()
	s := NewScenario(t)

	topo := exportDefault(t, s, filepath.Join(s.Root, "base.json"))
	topo["name"] = "notices"
	addCommandNode(topo, "vet", "code_writer", []string{"go", "vet", "./..."}, lightPlus)
	addCommandNode(topo, "quote", "code_writer", []string{"sh", "-c", `printf '%s' "a b" > out.txt`}, lightPlus)
	file := filepath.Join(s.Root, "notices.json")
	writeJSON(t, file, topo)
	// An untrusted project file is never parsed, so an invalid one is safe here.
	projectFile := s.WriteFile(".splice/pipeline.json", "{")

	s.Provider.Script(StageCodeWriter, helloCodeReply())
	r := s.Run("exec", "--pipeline", file, t01Prompt)
	r.WantExit(t, 0)

	counts := map[string]int{}
	for _, line := range strings.Split(r.Stderr, "\n") {
		if strings.Contains(line, "[topology]") {
			counts[line]++
		}
	}
	wantLines := []string{
		"[splice] [topology] warning: ignored untrusted project topology " + projectFile,
		"[splice] [topology] active pipeline from --pipeline",
		"[splice] [topology] command node vet will run: go vet ./...",
		`[splice] [topology] command node quote will run: sh -c 'printf '\''%s'\'' "a b" > out.txt'`,
	}
	for _, want := range wantLines {
		if counts[want] != 1 {
			t.Errorf("notice %q printed %d time(s), want 1", want, counts[want])
		}
	}
	for line, n := range counts {
		if n != 1 {
			t.Errorf("notice %q printed %d time(s), want 1", line, n)
		}
	}
}
