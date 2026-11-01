package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Rollout loop invariants (handoff section 34): N rollouts run each (task,
// arm) N times, every attempt starts from pristine fixture bytes, session
// ids stay joinable per attempt, and per-attempt rows land in the pair log.
func TestHarnessRolloutsRunEachArmNTimes(t *testing.T) {
	taskset := TaskSet{Name: "ts", Tasks: []Task{
		{Name: "a", Prompt: "pa", Check: "true"},
	}}

	var calls []RunInput
	h := &Harness{
		Rollouts: 3,
		Exec: func(_ context.Context, in RunInput) (RunOutput, error) {
			// Pin the pristine-start invariant: the arm must not carry the
			// previous attempt's edits. The fixture file is canary.txt; an
			// exec that sees content "edited" is looking at a dirty arm.
			if data, err := os.ReadFile(filepath.Join(in.Cwd, "canary.txt")); err == nil {
				if strings.Contains(string(data), "edited") {
					t.Fatalf("%s: arm not reset between attempts: %q", in.SessionID, string(data))
				}
			}
			calls = append(calls, in)
			// Simulate the agent editing the arm so the next attempt proves
			// the reset actually happened.
			_ = os.WriteFile(filepath.Join(in.Cwd, "canary.txt"), []byte("edited"), 0o644)
			return RunOutput{Success: true, Tokens: 10, TelemetryFound: true}, nil
		},
		Now: func() time.Time { return time.Unix(0, 0) },
	}
	report, err := h.Run(context.Background(), taskset, "", "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 1 task x 2 arms x 3 rollouts.
	if len(calls) != 6 {
		t.Fatalf("exec calls = %d, want 6", len(calls))
	}
	// Pairing order: cold r1, warm r1, cold r2, warm r2, cold r3, warm r3.
	wantSessions := []string{
		"eval-ts-cold-a-r1", "eval-ts-warm-a-r1",
		"eval-ts-cold-a-r2", "eval-ts-warm-a-r2",
		"eval-ts-cold-a-r3", "eval-ts-warm-a-r3",
	}
	for i, c := range calls {
		if c.SessionID != wantSessions[i] {
			t.Fatalf("call %d session = %q, want %q", i, c.SessionID, wantSessions[i])
		}
	}
	// Attempts must alternate cold/warm per rollout (paired by attempt).
	for i, c := range calls {
		want := "off"
		if i%2 == 1 {
			want = "on"
		}
		if c.Memory != want {
			t.Fatalf("call %d memory = %q, want %q", i, c.Memory, want)
		}
	}
	// Aggregates span all rollouts: 6 successes, 60 tokens.
	if report.Pairs != 3 {
		t.Fatalf("pairs = %d, want 3 (one per rollout)", report.Pairs)
	}
	if report.Cold.Successes != 3 || report.Warm.Successes != 3 {
		t.Fatalf("successes = cold %d / warm %d, want 3 / 3", report.Cold.Successes, report.Warm.Successes)
	}
	if report.Cold.Tokens != 30 || report.Warm.Tokens != 30 {
		t.Fatalf("tokens = cold %d / warm %d, want 30 / 30", report.Cold.Tokens, report.Warm.Tokens)
	}
	// Every pair row carries its attempt number (one pair per rollout,
	// holding both arms' outcomes).
	for i, pair := range report.Tasks {
		if pair.Attempt != i+1 {
			t.Fatalf("pair %d (%s) attempt = %d, want %d", i, pair.Name, pair.Attempt, i+1)
		}
	}
}

// Single-rollout default preserves the historical contract: no -r suffix on
// session ids, one pair per task.
func TestHarnessSingleRolloutKeepsLegacySessionIDs(t *testing.T) {
	taskset := TaskSet{Name: "ts", Tasks: []Task{{Name: "a", Prompt: "pa", Check: "true"}}}
	var sessions []string
	h := &Harness{
		Exec: func(_ context.Context, in RunInput) (RunOutput, error) {
			sessions = append(sessions, in.SessionID)
			return RunOutput{Success: true, Tokens: 10, TelemetryFound: true}, nil
		},
		Now: func() time.Time { return time.Unix(0, 0) },
	}
	if _, err := h.Run(context.Background(), taskset, "", ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("calls = %d, want 2", len(sessions))
	}
	if sessions[0] != "eval-ts-cold-a" || sessions[1] != "eval-ts-warm-a" {
		t.Fatalf("legacy session ids broken: %q, %q", sessions[0], sessions[1])
	}
}
