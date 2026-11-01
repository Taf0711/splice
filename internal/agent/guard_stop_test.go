package agent

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// guardStopProvider returns the same empty turn forever, so the no-output guard
// stops the loop.
type guardStopProvider struct{}

func (guardStopProvider) StreamCompletion(context.Context, zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	ch := make(chan zeroruntime.StreamEvent, 1)
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

// TestNoOutputGuardStopIsUnchangedForInteractiveRuns pins that the guard stop
// keeps its interactive behavior when the completion signal is off: the run
// returns the canned answer as success, with no incomplete mark and no stop
// reason. Headless runs are covered by the agent node tests.
func TestNoOutputGuardStopIsUnchangedForInteractiveRuns(t *testing.T) {
	result, err := Run(context.Background(), "x", guardStopProvider{}, Options{
		MaxTurns: 8, Registry: tools.NewRegistry(), PermissionMode: PermissionModeAuto,
	})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if result.Incomplete || result.StopReason != "" {
		t.Fatalf("interactive guard stop = incomplete %v reason %q, want unchanged", result.Incomplete, result.StopReason)
	}
	if result.FinalAnswer == "" {
		t.Fatal("interactive guard stop returned no answer")
	}
}
