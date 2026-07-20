package cli

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type execStageProviderOptions struct {
	Files       []schemas.FileChange
	TestFiles   []schemas.FileChange
	Usage       zeroruntime.Usage
	Reasoning   string
	FailTool    string
	FailMessage string
}

type execStageAwareProvider struct {
	mu        sync.Mutex
	images    []zeroruntime.ImageBlock
	intents   []string
	submitted map[string]bool
	options   execStageProviderOptions
}

func newExecStageAwareProvider(options execStageProviderOptions) *execStageAwareProvider {
	return &execStageAwareProvider{options: options}
}

func (provider *execStageAwareProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	toolName := ""
	if len(request.Tools) > 0 {
		toolName = request.Tools[0].Name
	}
	intent := execStageIntent(request)

	provider.mu.Lock()
	provider.intents = append(provider.intents, intent)
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role == zeroruntime.MessageRoleUser {
			provider.images = append([]zeroruntime.ImageBlock(nil), request.Messages[index].Images...)
			break
		}
	}
	provider.mu.Unlock()

	if provider.options.FailTool == toolName {
		return nil, context.Canceled
	}

	args := "{}"
	switch toolName {
	case "submit_code":
		out := schemas.CodeWriterOutput{
			Files:      provider.codeFiles(),
			Language:   "go",
			Intent:     intent,
			Confidence: 0.95,
		}
		b, _ := json.Marshal(out)
		args = string(b)
	case "submit_tests":
		out := schemas.TestGeneratorOutput{
			Files:      provider.testFiles(),
			Language:   "go",
			Intent:     intent,
			Confidence: 0.9,
		}
		b, _ := json.Marshal(out)
		args = string(b)
	}

	ch := make(chan zeroruntime.StreamEvent, 8)
	select {
	case <-ctx.Done():
		close(ch)
		return ch, ctx.Err()
	default:
	}
	if provider.options.Reasoning != "" {
		ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventReasoning, Content: provider.options.Reasoning}
	}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventText, Content: intent}
	if provider.options.Usage.TotalTokens() > 0 {
		ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: provider.options.Usage}
	}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "stage_" + toolName, ToolName: toolName}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "stage_" + toolName, ArgumentsFragment: args}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "stage_" + toolName}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func (provider *execStageAwareProvider) codeFiles() []schemas.FileChange {
	if len(provider.options.Files) > 0 {
		return provider.normalizeRepeatCreates(provider.options.Files)
	}
	return provider.normalizeRepeatCreates([]schemas.FileChange{
		{Path: "go.mod", Content: "module example\n\ngo 1.22\n", ChangeType: "create"},
		{Path: "main.go", Content: "package main\n\nfunc Hello() string { return \"hello\" }\n", ChangeType: "create"},
	})
}

func (provider *execStageAwareProvider) testFiles() []schemas.FileChange {
	if len(provider.options.TestFiles) > 0 {
		return provider.normalizeRepeatCreates(provider.options.TestFiles)
	}
	return provider.normalizeRepeatCreates([]schemas.FileChange{
		{Path: "main_test.go", Content: "package main\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) {\n\tif Hello() != \"hello\" {\n\t\tt.Fatal(\"wrong greeting\")\n\t}\n}\n", ChangeType: "create"},
	})
}

// normalizeRepeatCreates makes the fixture behave like a competent model under
// AR1's fail-loud file application: the first submission of a path may create
// it, and any later submission of the same path becomes a modify, because
// create no longer silently overwrites an existing file.
func (provider *execStageAwareProvider) normalizeRepeatCreates(files []schemas.FileChange) []schemas.FileChange {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.submitted == nil {
		provider.submitted = make(map[string]bool)
	}
	out := make([]schemas.FileChange, len(files))
	for i, f := range files {
		if f.ChangeType == "create" && provider.submitted[f.Path] {
			f.ChangeType = "modify"
		}
		if f.ChangeType == "create" || f.ChangeType == "modify" {
			provider.submitted[f.Path] = true
		}
		out[i] = f
	}
	return out
}

func (provider *execStageAwareProvider) lastImages() []zeroruntime.ImageBlock {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]zeroruntime.ImageBlock(nil), provider.images...)
}

func (provider *execStageAwareProvider) seenIntents() []string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]string(nil), provider.intents...)
}

func execStageIntent(request zeroruntime.CompletionRequest) string {
	for index := len(request.Messages) - 1; index >= 0; index-- {
		message := request.Messages[index]
		if message.Role != zeroruntime.MessageRoleUser {
			continue
		}
		var payload struct {
			Intent string `json:"intent"`
		}
		if err := json.Unmarshal([]byte(message.Content), &payload); err == nil && strings.TrimSpace(payload.Intent) != "" {
			return payload.Intent
		}
		if strings.TrimSpace(message.Content) != "" {
			return message.Content
		}
	}
	return "image-only request"
}

func execStageResolvedConfig(model string, maxTurns int) config.ResolvedConfig {
	if model == "" {
		model = "echo-model"
	}
	if maxTurns == 0 {
		maxTurns = 3
	}
	return config.ResolvedConfig{
		ActiveProvider:      "echo",
		DefaultProjectTrust: "always",
		Provider: config.ProviderProfile{
			Name:         "echo",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "http://127.0.0.1/v1",
			Model:        model,
		},
		MaxTurns: maxTurns,
	}
}
