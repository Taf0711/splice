package stages

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func acceptanceInput(facts ...schemas.AcceptanceFact) schemas.HarnessStageInput {
	return schemas.HarnessStageInput{
		RunID:           "run-1",
		StageName:       "acceptance_verifier",
		Sequence:        1,
		PlanTier:        schemas.TierLight,
		RequestIntent:   "verify the task",
		AcceptanceFacts: facts,
	}
}

func automatedFact(statement, command string) schemas.AcceptanceFact {
	return schemas.AcceptanceFact{Statement: statement, AutomatedVerification: true, VerificationCommand: &command}
}

func TestAcceptanceVerifierPassingFact(t *testing.T) {
	var gotName string
	output, err := (AcceptanceVerifier{}).Run(context.Background(), acceptanceInput(automatedFact("returns 42", "test -f result")), nil, StageOptions{
		RunTool: func(_ context.Context, name string, args map[string]any) (ToolResult, error) {
			gotName = name
			if args["command"] != "test -f result" {
				t.Fatalf("command = %#v, want verbatim command", args["command"])
			}
			return ToolResult{OK: true, Output: "ok"}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	results := output.Data["acceptance_results"].([]schemas.TestCaseResult)
	if gotName != "bash" || len(results) != 1 || results[0].Name != "returns 42" || results[0].Status != "passed" {
		t.Fatalf("tool=%q results=%+v", gotName, results)
	}
}

func TestAcceptanceVerifierFailureDoesNotFailStage(t *testing.T) {
	output, err := (AcceptanceVerifier{}).Run(context.Background(), acceptanceInput(automatedFact("is correct", "false")), nil, StageOptions{
		RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
			return ToolResult{OK: false, Output: "exit code 1"}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run returned stage error: %v", err)
	}
	results := output.Data["acceptance_results"].([]schemas.TestCaseResult)
	if results[0].Status != "failed" || !strings.Contains(output.Summary, "1 failed") {
		t.Fatalf("results=%+v summary=%q", results, output.Summary)
	}
}

func TestAcceptanceVerifierCommandlessFactIsSkipped(t *testing.T) {
	calls := 0
	output, err := (AcceptanceVerifier{}).Run(context.Background(), acceptanceInput(
		schemas.AcceptanceFact{Statement: "documented behavior"},
	), nil, StageOptions{
		RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
			calls++
			return ToolResult{OK: true}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	results := output.Data["acceptance_results"].([]schemas.TestCaseResult)
	if calls != 0 || results[0].Status != "skipped" {
		t.Fatalf("calls=%d results=%+v, want skipped without execution", calls, results)
	}
}

func TestAcceptanceVerifierIndependentFactTimeouts(t *testing.T) {
	// Regression test for the shared-timeout failure mode: a hung first fact
	// must not starve the later facts of their own timeout and execution.
	// The counter is guarded because a timed-out fact's command is only
	// abandoned if the stage forgets to wait for it — the mutex keeps this test
	// reporting that as a failed assertion rather than as a data race.
	var mu sync.Mutex
	calls := 0
	output, err := (AcceptanceVerifier{}).Run(context.Background(), acceptanceInput(
		automatedFact("hangs", "hang"),
		automatedFact("passes", "pass"),
		automatedFact("also passes", "pass-2"),
	), nil, StageOptions{
		TimeoutSeconds: 1,
		RunTool: func(ctx context.Context, _ string, args map[string]any) (ToolResult, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			if args["command"] == "hang" {
				<-ctx.Done()
				return ToolResult{}, ctx.Err()
			}
			return ToolResult{OK: true}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	results := output.Data["acceptance_results"].([]schemas.TestCaseResult)
	if calls != 3 || results[0].Status != "errored" || results[1].Status != "passed" || results[2].Status != "passed" {
		t.Fatalf("calls=%d results=%+v", calls, results)
	}
}

func TestAcceptanceVerifierRecordsEveryCommand(t *testing.T) {
	recorded := 0
	output, err := (AcceptanceVerifier{}).Run(context.Background(), acceptanceInput(automatedFact("works", "echo works")), nil, StageOptions{
		RunTool: func(_ context.Context, name string, _ map[string]any) (ToolResult, error) {
			if name != "bash" {
				t.Fatalf("tool name = %q, want bash", name)
			}
			return ToolResult{OK: true}, nil
		},
		RecordCommand: func(ctx context.Context, name string, args map[string]any, run func(context.Context) (ToolResult, error)) (ToolResult, error) {
			recorded++
			if name != "splice.acceptance" || args["command"] != "echo works" {
				t.Fatalf("recorded command name=%q args=%v", name, args)
			}
			return run(ctx)
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if recorded != 1 || output.Data["acceptance_results"].([]schemas.TestCaseResult)[0].Status != "passed" {
		t.Fatalf("recorded=%d output=%v", recorded, output.Data)
	}
}

// A fact that times out leaves its command still running. Abandoning it lets the
// next fact's command start while the previous one is live, so two commands
// enter RunTool at once — and RunTool passes through the permission gate and the
// command ledger, neither of which expects to be entered twice concurrently.
func TestAcceptanceVerifierRunsOneCommandAtATime(t *testing.T) {
	var mu sync.Mutex
	live := 0
	maxLive := 0

	_, err := (AcceptanceVerifier{}).Run(context.Background(), acceptanceInput(
		automatedFact("hangs", "hang"),
		automatedFact("passes", "pass"),
		automatedFact("also passes", "pass-2"),
	), nil, StageOptions{
		TimeoutSeconds: 1,
		RunTool: func(ctx context.Context, _ string, args map[string]any) (ToolResult, error) {
			mu.Lock()
			live++
			if live > maxLive {
				maxLive = live
			}
			mu.Unlock()
			defer func() {
				mu.Lock()
				live--
				mu.Unlock()
			}()
			if args["command"] == "hang" {
				<-ctx.Done()
				// Unwind slowly. A real command does not vanish the instant its
				// context is cancelled, and without that delay the abandoned
				// goroutine finishes before the next fact starts, hiding the
				// overlap this test exists to catch.
				time.Sleep(50 * time.Millisecond)
				return ToolResult{}, ctx.Err()
			}
			return ToolResult{OK: true}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if maxLive > 1 {
		t.Fatalf("%d acceptance commands ran at once, want never more than 1", maxLive)
	}
}
