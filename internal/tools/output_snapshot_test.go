package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

type snapshotTestTool struct{ baseTool }

func newSnapshotTestTool() snapshotTestTool {
	return snapshotTestTool{baseTool: baseTool{
		name:       "snapshot_test",
		safety:     Safety{SideEffect: SideEffectNone, Permission: PermissionAllow},
		parameters: Schema{Type: "object", AdditionalProperties: false},
	}}
}

func (tool snapshotTestTool) Run(context.Context, map[string]any) Result {
	return okResult("done")
}

func (tool snapshotTestTool) RunWithOptions(_ context.Context, _ map[string]any, options RunOptions) Result {
	options.OnToolOutput(OutputSnapshot{ToolCallID: options.ToolCallID, Output: "key=AKIAIOSFOD"})
	options.OnToolOutput(OutputSnapshot{ToolCallID: options.ToolCallID, Output: "key=AKIAIOSFODNN7EXAMPLE\n"})
	return okResult("done")
}

func TestRegistryRedactsOutputSnapshots(t *testing.T) {
	registry := NewRegistry()
	registry.Register(newSnapshotTestTool())
	var got OutputSnapshot
	calls := 0
	result := registry.RunWithOptions(context.Background(), "snapshot_test", nil, RunOptions{
		ToolCallID: "call_1",
		OnToolOutput: func(snapshot OutputSnapshot) {
			calls++
			got = snapshot
		},
	})
	if result.Status != StatusOK {
		t.Fatalf("result = %#v", result)
	}
	if calls != 1 {
		t.Fatalf("partial line reached callback: calls=%d", calls)
	}
	if got.ToolCallID != "call_1" || strings.Contains(got.Output, "AKIAIOSFODNN7EXAMPLE") || !strings.Contains(got.Output, "[REDACTED") {
		t.Fatalf("snapshot was not redacted at registry boundary: %#v", got)
	}
}

func TestOutputThrottleAllowsFirstAndOnePerInterval(t *testing.T) {
	now := time.Unix(100, 0)
	throttle := outputThrottle{minGap: 100 * time.Millisecond, now: func() time.Time { return now }}
	if !throttle.due() || throttle.due() {
		t.Fatal("first output must pass and an immediate repeat must be suppressed")
	}
	now = now.Add(99 * time.Millisecond)
	if throttle.due() {
		t.Fatal("output before the interval must be suppressed")
	}
	now = now.Add(time.Millisecond)
	if !throttle.due() {
		t.Fatal("output at the interval must pass")
	}
}

func TestBashEmitsOutputBeforeCompletion(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewBashTool(t.TempDir()))
	snapshots := make(chan OutputSnapshot, 4)
	results := make(chan Result, 1)
	go func() {
		results <- registry.RunWithOptions(context.Background(), "bash", map[string]any{
			"command": helperCommand("output-sleep"),
		}, RunOptions{
			PermissionGranted: true,
			ToolCallID:        "bash_1",
			OnToolOutput:      func(snapshot OutputSnapshot) { snapshots <- snapshot },
		})
	}()
	select {
	case snapshot := <-snapshots:
		if snapshot.ToolCallID != "bash_1" || !strings.Contains(snapshot.Output, "started") {
			t.Fatalf("unexpected live snapshot: %#v", snapshot)
		}
	case result := <-results:
		t.Fatalf("bash completed before a live snapshot: %#v", result)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for live bash output")
	}
	if result := <-results; result.Status != StatusOK {
		t.Fatalf("bash result = %#v", result)
	}
}

func TestExecCommandStopsSnapshotsBeforeReturn(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewScopedExecCommandTool(t.TempDir(), nil, newExecSessionManager()))
	var snapshots []OutputSnapshot
	result := registry.RunWithOptions(context.Background(), ExecCommandToolName, map[string]any{
		"cmd":           helperCommand("output-sleep"),
		"yield_time_ms": 30000,
	}, RunOptions{
		PermissionGranted: true,
		ToolCallID:        "exec_1",
		OnToolOutput: func(snapshot OutputSnapshot) {
			snapshots = append(snapshots, snapshot)
		},
	})
	if result.Status != StatusOK {
		t.Fatalf("exec_command result = %#v", result)
	}
	if len(snapshots) == 0 || snapshots[0].ToolCallID != "exec_1" || !strings.Contains(snapshots[0].Output, "started") {
		t.Fatalf("expected output before completion, got %#v", snapshots)
	}
	count := len(snapshots)
	time.Sleep(150 * time.Millisecond)
	if len(snapshots) != count {
		t.Fatalf("snapshots continued after return: before=%d after=%d", count, len(snapshots))
	}
}
