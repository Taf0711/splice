package splice

// This test pins F1 format-retry attribution. A typed-output contract failure
// retries the same stage round; the retry request must be billed under
// SpendSourceFormatRetry, not under the round's generation source. The
// forwarding was missing once: StageOptions.OnFormatRetry was set by the
// orchestrator but no stage passed it into callValidatedToolUse, so every
// retry silently inherited generation.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// formatRetryScriptProvider returns one malformed typed output, then a valid
// create proposal. It plays both attempts in the same stage round.
type formatRetryScriptProvider struct {
	turns int
}

func (p *formatRetryScriptProvider) StreamCompletion(_ context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.turns++
	toolName := ""
	if len(request.Tools) > 0 {
		toolName = request.Tools[0].Name
	}
	var args string
	if p.turns == 1 {
		// Malformed action envelope: the decoder cannot discriminate it, so
		// the validate callback fails and the stage retries.
		args = `{`
	} else {
		out := map[string]any{
			"files": []map[string]any{{
				"path":        "retry_added.go",
				"change_type": "create",
				"content":     "package main\n\nfunc retryAdded() int { return 1 }\n",
			}},
			"language":   "go",
			"intent":     "add a helper after the format retry",
			"confidence": 0.9,
		}
		b, err := json.Marshal(out)
		if err != nil {
			return nil, err
		}
		args = string(b)
	}
	ch := make(chan zeroruntime.StreamEvent, 8)
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 100, OutputTokens: 40}}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "1", ToolName: toolName}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "1", ArgumentsFragment: args}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "1"}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func TestFormatRetryIsBilledUnderItsOwnSpendSource(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	main := "package main\n\nfunc Hello() string { return \"hello\" }\n"
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := &formatRetryScriptProvider{}

	plan, err := BuildExecutionPlan("add a helper file")
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	cfg := PipelineConfigFromAgentOptions(agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		FileTracker:    tools.NewFileTracker(),
	})
	result, err := runExecutionPlan(context.Background(), "run-format-retry", plan, provider, cfg, nil, nil)
	if err != nil {
		t.Fatalf("runExecutionPlan: %v", err)
	}
	if provider.turns < 2 {
		t.Fatalf("provider turns = %d, want >= 2: the typed-output retry did not fire", provider.turns)
	}

	var sources []string
	for _, r := range result.UsageRecords {
		sources = append(sources, r.Stage+"="+r.SpendSource)
	}
	t.Logf("usage records: %v", sources)

	if len(result.UsageRecords) < 2 {
		t.Fatalf("usage records = %d, want >= 2 (attempt 1 and the retry)", len(result.UsageRecords))
	}
	first := result.UsageRecords[0]
	if first.SpendSource != schemas.SpendSourceGeneration {
		t.Fatalf("first record spend source = %q, want %q", first.SpendSource, schemas.SpendSourceGeneration)
	}
	retry := result.UsageRecords[1]
	if retry.SpendSource != schemas.SpendSourceFormatRetry {
		t.Fatalf("retry record spend source = %q, want %q; records = %v",
			retry.SpendSource, schemas.SpendSourceFormatRetry, sources)
	}
	if retry.ContextRound != first.ContextRound {
		t.Fatalf("retry context round = %d, want the generation round %d: a retry does not advance the round",
			retry.ContextRound, first.ContextRound)
	}
	if retry.InputTokens <= 0 {
		t.Fatalf("retry record input tokens = %d, want > 0: the usage event did not reach the ledger", retry.InputTokens)
	}
}
