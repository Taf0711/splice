package eval

import (
	"context"
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

	// Warm runs share one copy across tasks; cold shares a different copy.
	if calls[1].Cwd != calls[3].Cwd {
		t.Fatalf("warm arm must share one copy: %q vs %q", calls[1].Cwd, calls[3].Cwd)
	}
	if calls[0].Cwd != calls[2].Cwd {
		t.Fatalf("cold arm must share one copy: %q vs %q", calls[0].Cwd, calls[2].Cwd)
	}
	if calls[0].Cwd == calls[1].Cwd {
		t.Fatalf("cold and warm arms must be distinct copies")
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
