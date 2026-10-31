package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type loopFinalProvider struct{}

func (loopFinalProvider) StreamCompletion(context.Context, zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	stream := make(chan zeroruntime.StreamEvent, 3)
	stream <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventText, Content: "loop complete"}
	stream <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 100, OutputTokens: 20}}
	stream <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(stream)
	return stream, nil
}

func TestRunExecLoopFinalEventIsPipelineResult(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr strings.Builder
	exitCode := runWithDeps([]string{"exec", "--cwd", root, "--loop", "--output-format", "stream-json", "finish the task"}, &stdout, &stderr, appDeps{
		getwd:          func() (string, error) { return root, nil },
		userConfigPath: func() (string, error) { return root + "/config.json", nil },
		resolveConfig: func(_ string, overrides config.Overrides) (config.ResolvedConfig, error) {
			return execStageResolvedConfig(overrides.Provider.Model, 2), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return loopFinalProvider{}, nil
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("loop exit = %d, stderr=%s, stdout=%s", exitCode, stderr.String(), stdout.String())
	}
	var finalText string
	for _, line := range strings.Split(stdout.String(), "\n") {
		var event struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && event.Type == "final" {
			finalText = event.Text
		}
	}
	if finalText == "" {
		t.Fatalf("stream did not contain a final event: %s", stdout.String())
	}
	var result schemas.PipelineResult
	if err := json.Unmarshal([]byte(finalText), &result); err != nil {
		t.Fatalf("decode loop final: %v\n%s", err, finalText)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("loop result invalid: %v", err)
	}
	if result.Status != "completed" || result.TotalTokensInput != 100 || result.TotalTokensOutput != 20 {
		t.Fatalf("loop result = %#v, want completed totals 100/20", result)
	}
	if len(result.UsageRecords) != 1 || result.UsageRecords[0].Stage != "agent_loop" || result.UsageRecords[0].SpendSource != schemas.SpendSourceGeneration {
		t.Fatalf("loop usage records = %#v", result.UsageRecords)
	}
	if result.FinalOutput["memory_status"] != "off" {
		t.Fatalf("memory status = %#v, want off", result.FinalOutput["memory_status"])
	}
}
