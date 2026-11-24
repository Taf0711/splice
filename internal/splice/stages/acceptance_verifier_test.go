package stages

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

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

// A tool call that ignores its context must not block the acceptance verifier
// after the per-fact deadline.
func TestAcceptanceVerifierBoundsWaitAfterDeadline(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	type outcome struct {
		out schemas.HarnessStageOutput
		err error
	}
	var called atomic.Bool
	done := make(chan outcome, 1)
	go func() {
		out, err := (AcceptanceVerifier{}).Run(context.Background(), acceptanceInput(automatedFact("hangs forever", "sleep 100")), nil, StageOptions{
			TimeoutSeconds: 1,
			RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
				called.Store(true)
				<-block
				return ToolResult{OK: true}, nil
			},
		})
		done <- outcome{out: out, err: err}
	}()

	select {
	case got := <-done:
		if !called.Load() {
			t.Fatalf("AcceptanceVerifier never called RunTool (err=%v)", got.err)
		}
		if got.err != nil {
			t.Fatalf("verifier returned error after timeout: %v; want the fact recorded as errored", got.err)
		}
		results, ok := got.out.Data["acceptance_results"].([]schemas.TestCaseResult)
		if !ok {
			t.Fatalf("setup: acceptance_results = %#v, want []schemas.TestCaseResult", got.out.Data["acceptance_results"])
		}
		if len(results) != 1 || results[0].Status != "errored" {
			t.Fatalf("results = %+v, want one errored (timed out) fact", results)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("acceptance verifier did not return within 10s after a 1s timeout")
	}
}

// On parent cancellation the verifier must not return while its tool call is
// still running.
func TestAcceptanceVerifierUnwindsToolOnCancel(t *testing.T) {
	var unwound atomic.Bool
	started := make(chan struct{})
	var startOnce sync.Once

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_, _ = (AcceptanceVerifier{}).Run(ctx, acceptanceInput(automatedFact("cancelled midway", "sleep 1")), nil, StageOptions{
			RunTool: func(runCtx context.Context, _ string, _ map[string]any) (ToolResult, error) {
				startOnce.Do(func() { close(started) })
				<-runCtx.Done()
				time.Sleep(100 * time.Millisecond)
				unwound.Store(true)
				return ToolResult{}, nil
			},
		})
	}()

	select {
	case <-started:
	case <-runDone:
		t.Fatalf("AcceptanceVerifier returned without calling RunTool")
	case <-time.After(5 * time.Second):
		cancel()
		select {
		case <-runDone:
		case <-time.After(10 * time.Second):
			t.Fatalf("setup: RunTool was not called within 5s and AcceptanceVerifier.Run did not return within 10s after cancel")
		}
		t.Fatalf("AcceptanceVerifier did not call RunTool within 5s")
	}
	cancel()
	select {
	case <-runDone:
	case <-time.After(10 * time.Second):
		t.Fatalf("AcceptanceVerifier.Run did not return within 10s after cancellation")
	}
	if !unwound.Load() {
		t.Fatalf("verifier returned while the cancelled tool call was still running")
	}
}

// A command that ignores its cancelled context must not hold the stage. Its
// fact is errored, and no later command starts while it still holds the runner.
func TestAcceptanceVerifierStopsAfterStuckCommand(t *testing.T) {
	prevGrace := acceptanceUnwindGrace
	acceptanceUnwindGrace = 100 * time.Millisecond
	t.Cleanup(func() { acceptanceUnwindGrace = prevGrace })

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	var calls atomic.Int32
	type outcome struct {
		out schemas.HarnessStageOutput
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		out, err := (AcceptanceVerifier{}).Run(context.Background(), acceptanceInput(
			automatedFact("stuck", "stuck"),
			automatedFact("after stuck", "pass"),
		), nil, StageOptions{
			TimeoutSeconds: 1,
			RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
				calls.Add(1)
				<-block
				return ToolResult{OK: true}, nil
			},
		})
		done <- outcome{out: out, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Run returned stage error: %v", got.err)
		}
		results := got.out.Data["acceptance_results"].([]schemas.TestCaseResult)
		if len(results) != 2 {
			t.Fatalf("results = %+v, want two entries", results)
		}
		if results[0].Status != "errored" || !strings.Contains(results[0].Message, "did not stop") {
			t.Fatalf("stuck fact = %+v, want errored with \"did not stop\"", results[0])
		}
		if results[1].Status != "errored" || !strings.Contains(results[1].Message, "not run") {
			t.Fatalf("second fact = %+v, want errored with \"not run\"", results[1])
		}
		if n := calls.Load(); n != 1 {
			t.Fatalf("RunTool called %d times, want exactly 1", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("acceptance verifier did not return within 5s after a 1s timeout")
	}
}

// A command that honours its context and unwinds within the grace period
// must not stop later facts: the next command still runs and can pass.
func TestAcceptanceVerifierContinuesAfterUnwoundCommand(t *testing.T) {
	var calls atomic.Int32
	output, err := (AcceptanceVerifier{}).Run(context.Background(), acceptanceInput(
		automatedFact("hangs", "hang"),
		automatedFact("passes", "pass"),
	), nil, StageOptions{
		TimeoutSeconds: 1,
		RunTool: func(ctx context.Context, _ string, args map[string]any) (ToolResult, error) {
			calls.Add(1)
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
	if calls.Load() != 2 || results[0].Status != "errored" || results[1].Status != "passed" {
		t.Fatalf("calls=%d results=%+v", calls.Load(), results)
	}
}

// acceptanceDetailRun runs the verifier with one command per fact. The output
// function decides each command's result, so a test sets each status and text.
func acceptanceDetailRun(t *testing.T, facts []schemas.AcceptanceFact, output func(command string) ToolResult) string {
	t.Helper()
	out, err := (AcceptanceVerifier{}).Run(context.Background(), acceptanceInput(facts...), nil, StageOptions{
		RunTool: func(_ context.Context, _ string, args map[string]any) (ToolResult, error) {
			return output(args["command"].(string)), nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out.Detail
}

func TestAcceptanceVerifierDetailListsFailedChecks(t *testing.T) {
	facts := []schemas.AcceptanceFact{
		automatedFact("alpha", "fail-one"),
		automatedFact("beta", "fail-two"),
		automatedFact("gamma", "pass"),
	}
	detail := acceptanceDetailRun(t, facts, func(command string) ToolResult {
		if command == "pass" {
			return ToolResult{OK: true, Output: "ok"}
		}
		return ToolResult{OK: false, Output: command + " first line\nsecond line"}
	})
	want := "Acceptance verification: 1 passed, 2 failed, 0 errored, 0 skipped.\n" +
		"- failed: alpha: fail-one first line\n" +
		"- failed: beta: fail-two first line"
	if detail != want {
		t.Fatalf("detail =\n%s\nwant\n%s", detail, want)
	}
}

func TestAcceptanceVerifierDetailCapsFailedChecks(t *testing.T) {
	var facts []schemas.AcceptanceFact
	for i := range 7 {
		facts = append(facts, automatedFact(fmt.Sprintf("check %d", i), "fail"))
	}
	detail := acceptanceDetailRun(t, facts, func(string) ToolResult {
		return ToolResult{OK: false, Output: "boom"}
	})
	lines := strings.Split(detail, "\n")
	if len(lines) != 1+acceptanceDetailMaxLines+1 {
		t.Fatalf("detail has %d lines, want summary, 5 checks, and one overflow line:\n%s", len(lines), detail)
	}
	if lines[len(lines)-1] != "- 2 more not shown" {
		t.Fatalf("last line = %q, want the overflow count", lines[len(lines)-1])
	}
	if lines[1] != "- failed: check 0: boom" {
		t.Fatalf("first check line = %q, want result order", lines[1])
	}
}

func TestAcceptanceVerifierDetailCutsLongMessage(t *testing.T) {
	long := strings.Repeat("é", 250) // 500 bytes, two bytes per rune
	detail := acceptanceDetailRun(t, []schemas.AcceptanceFact{automatedFact("long", "fail")}, func(string) ToolResult {
		return ToolResult{OK: false, Output: long}
	})
	lines := strings.Split(detail, "\n")
	message := strings.TrimPrefix(lines[1], "- failed: long: ")
	if len(message) != 200 || !utf8.ValidString(message) {
		t.Fatalf("message is %d bytes (valid UTF-8: %v), want 200 valid bytes", len(message), utf8.ValidString(message))
	}
}
