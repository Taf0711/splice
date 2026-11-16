package stages

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// AcceptanceVerifier runs the runnable acceptance facts carried by a plan.
// It is model-free. Each fact gets an independent timeout so one hung check
// cannot consume the budget for all later checks.
type AcceptanceVerifier struct{}

var _ Stage = AcceptanceVerifier{}

const defaultAcceptanceTimeoutSeconds = 30

// acceptanceUnwindGrace bounds how long the verifier waits for a cancelled
// acceptance command to return. It is a var so tests can shorten it.
var acceptanceUnwindGrace = 2 * time.Second

func (AcceptanceVerifier) Capabilities() Capabilities {
	return Capabilities{ModelFree: true, TimeoutSeconds: defaultAcceptanceTimeoutSeconds, Description: "verifying acceptance criteria"}
}

func (AcceptanceVerifier) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options StageOptions) (schemas.HarnessStageOutput, error) {
	timeout := options.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultAcceptanceTimeoutSeconds
	}

	results := make([]schemas.TestCaseResult, 0, len(input.AcceptanceFacts))
	// commandStuck is set when a command ignored its cancelled context. Its
	// goroutine still holds the tool runner, so no later command may start.
	commandStuck := false
	for _, fact := range input.AcceptanceFacts {
		command := ""
		if fact.VerificationCommand != nil {
			command = *fact.VerificationCommand
		}
		if !fact.AutomatedVerification || strings.TrimSpace(command) == "" {
			results = append(results, schemas.TestCaseResult{
				Name:    fact.Statement,
				Status:  "skipped",
				Message: "no automated verification command",
			})
			continue
		}
		if commandStuck {
			results = append(results, schemas.TestCaseResult{
				Name:    fact.Statement,
				Status:  "errored",
				Message: "not run: an earlier acceptance command is still running",
			})
			continue
		}
		if options.RunTool == nil {
			results = append(results, schemas.TestCaseResult{
				Name:    fact.Statement,
				Status:  "errored",
				Message: "bash tool runner is unavailable",
			})
			continue
		}

		start := time.Now()
		factCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		args := map[string]any{
			"command":    command,
			"cwd":        options.WorkDir,
			"timeout_ms": timeout * 1000,
		}
		run := func(runCtx context.Context) (ToolResult, error) {
			return options.RunTool(runCtx, "bash", args)
		}

		type commandOutcome struct {
			result ToolResult
			err    error
		}
		outcomes := make(chan commandOutcome, 1)
		go func() {
			var outcome commandOutcome
			if options.RecordCommand != nil {
				outcome.result, outcome.err = options.RecordCommand(factCtx, "splice.acceptance", args, run)
			} else {
				outcome.result, outcome.err = run(factCtx)
			}
			outcomes <- outcome
		}()
		var toolResult ToolResult
		var err error
		timedOut := false
		returned := false
		select {
		case outcome := <-outcomes:
			toolResult, err = outcome.result, outcome.err
			returned = true
		case <-factCtx.Done():
			timedOut = factCtx.Err() == context.DeadlineExceeded
			err = factCtx.Err()
		}
		if factCtx.Err() == context.DeadlineExceeded {
			timedOut = true
		}
		cancel()
		// The command may still be running. Its context is cancelled, so wait
		// for it to unwind before starting the next fact, so one criterion's
		// command never overlaps another's: RunTool passes through the
		// permission gate and the command ledger, and neither expects to be
		// entered twice at once. The wait is bounded by acceptanceUnwindGrace.
		if !returned {
			grace := time.NewTimer(acceptanceUnwindGrace)
			select {
			case <-outcomes:
				returned = true
			case <-grace.C:
			}
			grace.Stop()
		}
		durationMs := int(time.Since(start).Milliseconds())

		result := schemas.TestCaseResult{Name: fact.Statement, DurationMs: durationMs}
		switch {
		case ctx.Err() != nil:
			return schemas.HarnessStageOutput{}, ctx.Err()
		case timedOut && !returned:
			result.Status = "errored"
			result.Message = fmt.Sprintf("acceptance command timed out after %ds and did not stop within %s", timeout, acceptanceUnwindGrace)
			commandStuck = true
		case timedOut:
			result.Status = "errored"
			result.Message = fmt.Sprintf("acceptance command timed out after %ds", timeout)
		case err != nil:
			result.Status = "errored"
			result.Message = err.Error()
		case toolResult.OK:
			result.Status = "passed"
			result.Message = toolResult.Output
		default:
			result.Status = "failed"
			result.Message = toolResult.Output
			if result.Message == "" {
				result.Message = "acceptance command failed"
			}
		}
		results = append(results, result)
	}

	passed, failed, errored, skipped := 0, 0, 0, 0
	for _, result := range results {
		switch result.Status {
		case "passed":
			passed++
		case "failed":
			failed++
		case "errored":
			errored++
		case "skipped":
			skipped++
		}
	}
	confidence := 1.0
	if failed > 0 || errored > 0 {
		confidence = 0.5
	}
	summary := fmt.Sprintf("Acceptance verification: %d passed, %d failed, %d errored, %d skipped.", passed, failed, errored, skipped)
	detail := summary
	return schemas.HarnessStageOutput{
		Summary:    summary,
		Detail:     detail,
		Confidence: confidence,
		Data: map[string]any{
			"acceptance_results": results,
		},
	}, nil
}
