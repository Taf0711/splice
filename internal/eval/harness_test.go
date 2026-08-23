package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHarnessArmOrderingAndSessionID(t *testing.T) {
	taskset := TaskSet{Name: "ts", Tasks: []Task{
		{Name: "a", Prompt: "pa", Check: "true"},
		{Name: "b", Prompt: "pb", Check: "true"},
	}}

	var calls []RunInput
	h := &Harness{
		Exec: func(_ context.Context, in RunInput) (RunOutput, error) {
			if _, err := os.Stat(in.Cwd); err != nil {
				t.Errorf("cwd for %s missing during the run: %v", in.SessionID, err)
			}
			calls = append(calls, in)
			return RunOutput{Success: true, Tokens: 100, Interventions: 1}, nil
		},
		Now: func() time.Time { return time.Unix(0, 0) },
	}
	report, err := h.Run(context.Background(), taskset, "", "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(calls) != 4 {
		t.Fatalf("exec calls = %d, want 4 (2 tasks x 2 arms)", len(calls))
	}
	wantOrder := []string{"cold-a", "warm-a", "cold-b", "warm-b"}
	for i, c := range calls {
		arm := "cold"
		if c.Memory == "on" {
			arm = "warm"
		}
		got := arm + "-" + c.SessionID[len("eval-ts-"+arm+"-"):]
		if got != wantOrder[i] {
			t.Fatalf("call %d = %s, want %s", i, got, wantOrder[i])
		}
	}

	// Deterministic session ids.
	if calls[0].SessionID != "eval-ts-cold-a" || calls[1].SessionID != "eval-ts-warm-a" {
		t.Fatalf("session ids = %q, %q", calls[0].SessionID, calls[1].SessionID)
	}
	if calls[2].SessionID != "eval-ts-cold-b" || calls[3].SessionID != "eval-ts-warm-b" {
		t.Fatalf("session ids = %q, %q", calls[2].SessionID, calls[3].SessionID)
	}

	// Every pair gets fresh copies: no cwd repeats anywhere in the run.
	seen := map[string]string{}
	for _, c := range calls {
		if prev, ok := seen[c.Cwd]; ok {
			t.Fatalf("cwd %q reused by %s and %s", c.Cwd, prev, c.SessionID)
		}
		seen[c.Cwd] = c.SessionID
	}

	if report.Pairs != 2 || report.Cold.Successes != 2 || report.Warm.Successes != 2 {
		t.Fatalf("report = %#v", report)
	}
	if report.Cold.WeightedInterventions != 2 || report.Warm.WeightedInterventions != 2 {
		t.Fatalf("interventions not aggregated: %#v", report.Cold)
	}
}

func TestHarnessCheckSuccessMapping(t *testing.T) {
	taskset := TaskSet{Name: "ts", Tasks: []Task{
		{Name: "pass", Prompt: "p", Check: "true"},
		{Name: "fail", Prompt: "p", Check: "false"},
	}}
	h := &Harness{
		Exec: func(_ context.Context, in RunInput) (RunOutput, error) {
			// Success derives from the check command's exit code in production;
			// the fake mirrors that by succeeding only when the check is "true".
			return RunOutput{Success: in.Check == "true", Tokens: 100}, nil
		},
		Now: func() time.Time { return time.Unix(0, 0) },
	}
	report, err := h.Run(context.Background(), taskset, "", "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Cold.Successes != 1 || report.Warm.Successes != 1 {
		t.Fatalf("successes = cold %d warm %d, want 1/1", report.Cold.Successes, report.Warm.Successes)
	}
	if len(report.Tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(report.Tasks))
	}
	if !report.Tasks[0].ColdSuccess || report.Tasks[1].ColdSuccess {
		t.Fatalf("cold success mapping wrong: %#v", report.Tasks)
	}
}

// TestHarnessExecErrorMarksArmFailedAndCompletes pins the survival contract: a
// failed exec must not abort the eval. The failing arm takes Success=false,
// keeps its verbatim error, and its partial tokens still count as cost; every
// other pair completes and the report is produced.
func TestHarnessExecErrorMarksArmFailedAndCompletes(t *testing.T) {
	taskset := TaskSet{Name: "ts", Tasks: []Task{
		{Name: "before", Prompt: "p", Check: "true"},
		{Name: "boom", Prompt: "p", Check: "true"},
		{Name: "after", Prompt: "p", Check: "true"},
	}}
	h := &Harness{
		Exec: func(_ context.Context, in RunInput) (RunOutput, error) {
			if in.Memory == "off" && in.SessionID == "eval-ts-cold-boom" {
				return RunOutput{Success: false, Tokens: 250}, fmt.Errorf("exec run %s: exit status 4: abort_budget", in.SessionID)
			}
			return RunOutput{Success: true, Tokens: 100}, nil
		},
		Now: func() time.Time { return time.Unix(0, 0) },
	}
	report, err := h.Run(context.Background(), taskset, "", "")
	if err != nil {
		t.Fatalf("Run returned an error for a per-run failure: %v", err)
	}
	if len(report.Tasks) != 3 {
		t.Fatalf("pairs = %d, want all 3 tasks to complete", len(report.Tasks))
	}
	boom := report.Tasks[1]
	if boom.ColdSuccess || boom.WarmSuccess != true {
		t.Fatalf("boom pair = %+v, want cold failed and warm untouched-successful", boom)
	}
	if boom.ColdTokens != 250 {
		t.Fatalf("cold tokens = %d, want partial spend counted", boom.ColdTokens)
	}
	if !strings.Contains(boom.ColdError, "abort_budget") {
		t.Fatalf("cold error = %q, want the verbatim failure text", boom.ColdError)
	}
	if boom.WarmError != "" {
		t.Fatalf("warm error = %q, want empty on a healthy arm run", boom.WarmError)
	}
	if report.Cold.Successes != 2 || report.Warm.Successes != 3 {
		t.Fatalf("arm successes cold=%d warm=%d, want 2/3", report.Cold.Successes, report.Warm.Successes)
	}
}

// TestHarnessErrorsSurviveReportRenderers pins that exec failures reach both
// renderers: pe-report.json carries the full text, pe-report.md carries the
// task with a bounded line.
func TestHarnessErrorsSurviveReportRenderers(t *testing.T) {
	report := Report{Contract: ReportContractVersion, Timestamp: "t", Pairs: 1,
		Tasks: []TaskPair{{Name: "boom", ColdError: "exec run eval-x: exit status 4: abort_budget: Token budget reached."}}}
	jsonData, err := report.WriteJSON()
	if err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if !strings.Contains(string(jsonData), "abort_budget") {
		t.Fatal("pe-report.json lost the error text")
	}
	md := report.RenderMarkdown()
	if !strings.Contains(md, "## Run Errors") || !strings.Contains(md, "boom (cold)") {
		t.Fatalf("pe-report.md missing the errors section:\n%s", md)
	}
}

// TestHarnessPairLogAppendsAcrossInterruption pins incremental persistence:
// pairs land as JSONL lines as they finish, an interruption preserves both the
// previous file contents and the new completed pairs, and nothing truncates.
func TestHarnessPairLogAppendsAcrossInterruption(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "out", "pe-pairs.jsonl")
	oldPair := TaskPair{Name: "old"}
	oldLine, _ := json.Marshal(oldPair)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(logPath, append(oldLine, '\n'), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	taskset := TaskSet{Name: "ts", Tasks: []Task{
		{Name: "one", Prompt: "p", Check: "true"},
		{Name: "two", Prompt: "p", Check: "true"},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	h := &Harness{
		PairLogPath: logPath,
		Exec: func(callCtx context.Context, in RunInput) (RunOutput, error) {
			if strings.HasSuffix(in.SessionID, "-two") {
				cancel() // simulated crash mid-eval
			}
			return RunOutput{Success: true}, callCtx.Err()
		},
		Now: func() time.Time { return time.Unix(0, 0) },
	}
	if _, err := h.Run(ctx, taskset, "", ""); err == nil {
		t.Fatal("Run must surface the interruption")
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read pair log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatalf("pair log lines = %d, want the seeded line plus new pairs:\n%s", len(lines), data)
	}
	if !strings.Contains(string(data), "old") {
		t.Fatalf("previous contents were truncated: %s", data)
	}
	var firstNew TaskPair
	if err := json.Unmarshal([]byte(lines[1]), &firstNew); err != nil || firstNew.Name != "one" {
		t.Fatalf("line after seed = (%q, %v), want pair one appended", lines[1], err)
	}
}

// TestHarnessMissingTelemetryWarnsNotSilentZeros pins the telemetry contract:
// a successful run with no matching trace must surface in the markdown report
// as absent data naming the task, and a found trace must stay quiet.
func TestHarnessMissingTelemetryWarnsNotSilentZeros(t *testing.T) {
	taskset := TaskSet{Name: "ts", Tasks: []Task{
		{Name: "traced", Prompt: "p", Check: "true"},
		{Name: "blind", Prompt: "p", Check: "true"},
	}}
	h := &Harness{
		Exec: func(_ context.Context, in RunInput) (RunOutput, error) {
			return RunOutput{Success: true, Tokens: 100, TelemetryFound: strings.HasSuffix(in.SessionID, "-traced")}, nil
		},
		Now: func() time.Time { return time.Unix(0, 0) },
	}
	report, err := h.Run(context.Background(), taskset, "", "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	md := report.RenderMarkdown()
	if !strings.Contains(md, "TELEMETRY WARNING") || !strings.Contains(md, "blind (cold)") || !strings.Contains(md, "blind (warm)") {
		t.Fatalf("markdown must name the telemetry-blind tasks:\n%s", md)
	}
	if strings.Contains(md, "traced (cold)") || strings.Contains(md, "traced (warm)") {
		t.Fatalf("traced runs must not appear in the warning:\n%s", md)
	}
	jsonData, err := report.WriteJSON()
	if err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if !strings.Contains(string(jsonData), `"cold_telemetry": false`) {
		t.Fatalf("json must carry explicit telemetry flags:\n%s", jsonData)
	}
}

// TestHarnessPerTaskIsolationPinsPristineStart reproduces the run-7
// contamination failure: a task that mutates its workspace must never leak
// those edits into any later task's copy, in either arm. Task "a" rewrites
// session.go; task "b" must read back the pristine fixture bytes in both
// arms. It also pins cleanup: every pair's dirs are gone once Run returns.
func TestHarnessPerTaskIsolationPinsPristineStart(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	pristine := "package main\n\n// pristine sentinel\n"
	if err := os.WriteFile(filepath.Join(fixture, "session.go"), []byte(pristine), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	taskset := TaskSet{Name: filepath.Base(root), Dir: root, Tasks: []Task{
		{Name: "a", Prompt: "mutate", Check: "true"},
		{Name: "b", Prompt: "verify", Check: "true"},
	}}

	var bReads []string
	var allCwds []string
	h := &Harness{
		Exec: func(_ context.Context, in RunInput) (RunOutput, error) {
			allCwds = append(allCwds, in.Cwd)
			path := filepath.Join(in.Cwd, "session.go")
			switch {
			case strings.HasSuffix(in.SessionID, "-a"):
				// Task a mutates its own copy only.
				if err := os.WriteFile(path, []byte("package main // mutated by a\n"), 0o644); err != nil {
					t.Errorf("task a write: %v", err)
				}
			case strings.HasSuffix(in.SessionID, "-b"):
				got, err := os.ReadFile(path)
				if err != nil {
					t.Errorf("task b read: %v", err)
				} else if string(got) != pristine {
					t.Errorf("task %s saw contaminated fixture: %q", in.SessionID, got)
				}
				bReads = append(bReads, string(got))
			}
			return RunOutput{Success: true}, nil
		},
		Now: func() time.Time { return time.Unix(0, 0) },
	}
	if _, err := h.Run(context.Background(), taskset, "", ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(bReads) != 2 {
		t.Fatalf("task b ran %d times, want 2", len(bReads))
	}

	// Cleanup after persistence: every materialized copy is gone once Run
	// returns.
	for _, cwd := range allCwds {
		if _, err := os.Stat(cwd); !os.IsNotExist(err) {
			t.Fatalf("arm copy %s survived the run", cwd)
		}
	}
}
