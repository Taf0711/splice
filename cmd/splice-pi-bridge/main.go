// Command splice-pi-bridge runs a Splice pipeline and exposes it to the Pi
// harness over stream-json. The Pi extension pi-adapter/splice-bridge.ts
// spawns this binary, reads the stream-json events, and renders them with Pi's
// extension UI. The extension sends typed control commands on stdin.
//
// The bridge is an adapter over the existing agent.Options callbacks. It does
// not change the core. It wires the harness seam (RunEvent, ControlCommand,
// CapabilitySet) to the real splicerun.Run pipeline.
//
// Flags:
//
//	-prompt   the request to run
//	-cwd      the working directory for the run
//	-fixture  use the deterministic fixture provider (no live model)
//
// Usage (fixture, no live model):
//
//	splice-pi-bridge -fixture -prompt "fix the spelling typo"
//
// The bridge ships one deterministic fixture path. It does not resolve a live
// model provider; a run without -fixture fails loudly instead of silently
// degrading. Terminal state is honest: every exit path emits a final event
// followed by a run_end event whose exit code is authoritative.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/harness"
	"github.com/Taf0711/splice/internal/harness/pi"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/tools"
)

// Exit codes mirror splice exec so automation can share one convention.
const (
	exitSuccess     = 0
	exitCrash       = 1
	exitUsage       = 2
	exitProvider    = 3
	exitIncomplete  = 4
	exitInterrupted = 130
)

// fixtureWaitCancelEnv turns on the deterministic test gate: the fixture
// provider holds its stream open until a cancel command is routed. It exists
// for the process-boundary tests and changes nothing by default.
const fixtureWaitCancelEnv = "SPLICE_PI_FIXTURE_WAIT_CANCEL"

func main() {
	var (
		prompt  = flag.String("prompt", "", "the request to run")
		cwd     = flag.String("cwd", ".", "working directory for the run")
		fixture = flag.Bool("fixture", false, "use the deterministic fixture provider")
	)
	flag.Parse()

	if *prompt == "" {
		fmt.Fprintln(os.Stderr, "splice-pi-bridge: -prompt is required")
		os.Exit(exitUsage)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Forward SIGINT/SIGTERM to the run context so the Pi extension's
	// /splice-cancel command (which kills the child) aborts cleanly.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
	}()

	sink := pi.NewStreamSink(os.Stdout)

	var provider agent.Provider
	if *fixture {
		provider = pi.FixtureProvider{BlockUntilCancel: os.Getenv(fixtureWaitCancelEnv) == "1"}
	} else {
		// Fail loudly: silent fallbacks in a deterministic pipeline are bugs
		// that look like features. Live provider resolution is not wired in
		// this checkpoint and must not degrade into a nil-provider crash.
		fmt.Fprintln(os.Stderr, "splice-pi-bridge: no live provider is wired; pass -fixture for the deterministic path")
		finish(sink, "error", exitProvider)
		os.Exit(exitProvider)
	}

	options := agent.Options{
		Cwd:            *cwd,
		MaxTurns:       1,
		PermissionMode: agent.PermissionModeUnsafe, // fixture: no prompt-gated tools
	}

	// The code writer applies file changes through the tool registry. Register
	// the core tools (write_file, delete_file) so a real run can land its
	// fixture output. This matches what exec does via newCoreRegistry.
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreTools(*cwd) {
		registry.Register(tool)
	}
	options.Registry = registry

	// The harness seam attaches the typed event callbacks. The orchestrator
	// emits PipelinePlanEvent and StageEvent; Wire forwards them to the sink
	// as typed RunEvents; the sink serializes them as stream-json.
	wired := harness.Wire(options, sink)

	// The Pi extension sends control commands on stdin in every mode. Route
	// them through the CapabilitySet gate: lifecycle cancel is always allowed;
	// approvals and model control stay undeclared, so those commands are
	// rejected before routing (default-denied).
	ctrl := harness.Controls{CancelRun: cancel}
	caps := harness.CapabilitySet{}
	go func() {
		_ = pi.CommandLoop(ctx, os.Stdin, os.Stderr, caps, ctrl)
	}()

	result, err := splicerun.Run(ctx, *prompt, provider, wired, nil, nil)

	// Terminal semantics live in harness.TerminalStatus: a run that completed
	// normally stays completed even when a cancel command arrives afterwards.
	status, code := pi.TerminalStatus(err, ctx.Err() != nil, result.Incomplete)
	if err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "splice-pi-bridge: %v\n", err)
	}
	if status == "success" || status == "incomplete" {
		sendFinal(sink, result.FinalAnswer)
	}
	finish(sink, status, code)
	os.Exit(code)
}

// sendFinal emits the final event with the run's answer. An empty answer is
// skipped: the completion summary already streamed as text events.
func sendFinal(sink *pi.StreamSink, answer string) {
	if answer == "" {
		return
	}
	sink.Send(harness.RunEvent{Kind: harness.RunEventFinal, Final: answer})
}

// finish emits the terminal run_end event. Exit code stays authoritative even
// if the transport already failed.
func finish(sink *pi.StreamSink, status string, code int) {
	_ = sink.Terminate(status, code)
}
