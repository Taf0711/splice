package splice

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/dtools"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// stageRegistry maps stage names to runnable Stage implementations.
type stageRegistry map[string]stages.Stage

// buildStageRegistry creates a registry of Splice pipeline stages from agent options.
func buildStageRegistry(provider agent.Provider, options agent.Options, workDir string, runner ToolRunner) (stageRegistry, error) {
	language := detectLanguage(workDir)
	r := stageRegistry{
		"code_writer":    stages.CodeWriter{},
		"test_generator": stages.TestGenerator{},
		"test_runner":    stages.TestRunner{},
	}
	analyzer, err := stages.NewStaticAnalyzer(stages.DefaultQualityChecks()...)
	if err != nil {
		return nil, fmt.Errorf("build static analyzer: %w", err)
	}
	r["static_analyzer"] = analyzer
	auditor, err := stages.NewSecurityAuditor(stages.DefaultSecurityChecks()...)
	if err != nil {
		return nil, fmt.Errorf("build security auditor: %w", err)
	}
	r["security_auditor"] = auditor
	if options.Registry != nil {
		if _, ok := options.Registry.Get("bandit"); !ok {
			options.Registry.Register(dtools.NewBanditTool(workDir))
		}
		if _, ok := options.Registry.Get("gosec"); !ok {
			options.Registry.Register(dtools.NewGosecTool(workDir))
		}
		if _, ok := options.Registry.Get("sarif"); !ok {
			options.Registry.Register(dtools.NewSarifTool(workDir))
		}
	}
	_ = provider
	_ = runner
	_ = language
	return r, nil
}

// stageOptions builds StageOptions for a named stage.
func stageOptions(name string, options agent.Options, workDir string, runner ToolRunner) stages.StageOptions {
	language := detectLanguage(workDir)
	return stages.StageOptions{
		WorkDir:        workDir,
		Language:       language,
		PullContext:    name == "code_writer" || name == "test_generator",
		RunTool:        adaptToolRunner(runner),
		ReportActivity: makeReportCallback(options, name),
		Stream: zeroruntime.CollectOptions{
			OnText:          options.OnText,
			OnReasoning:     options.OnReasoning,
			OnUsage:         options.OnUsage,
			OnToolCallStart: options.OnToolCallStart,
			OnToolCallDelta: options.OnToolCallDelta,
		},
		Images:         append([]zeroruntime.ImageBlock(nil), options.Images...),
		RecordCommand:  makeRecordedCommandCallback(options),
		ModelOverride:  options.Model,
		TimeoutSeconds: 120,
	}
}

func makeReportCallback(options agent.Options, stageName string) func(string) {
	return func(message string) {
		if options.OnReasoning != nil {
			options.OnReasoning(fmt.Sprintf("[%s] %s\n", stageName, message))
		}
	}
}

func makeRecordedCommandCallback(options agent.Options) func(context.Context, string, map[string]any, func(context.Context) (stages.ToolResult, error)) (stages.ToolResult, error) {
	return func(ctx context.Context, name string, args map[string]any, run func(context.Context) (stages.ToolResult, error)) (stages.ToolResult, error) {
		call := toolCallFor(name, args)
		emitToolCall(options, call)
		result, err := run(ctx)
		if err != nil {
			result.OK = false
			if result.Output == "" {
				result.Output = err.Error()
			}
		}
		emitToolResult(options, call, ToolResult{
			OK:        result.OK,
			Output:    result.Output,
			Truncated: result.Truncated,
			Meta:      result.Meta,
		})
		return result, err
	}
}

func adaptToolRunner(runner ToolRunner) func(context.Context, string, map[string]any) (stages.ToolResult, error) {
	if runner == nil {
		return nil
	}
	return func(ctx context.Context, name string, args map[string]any) (stages.ToolResult, error) {
		res, err := runner.RunTool(ctx, name, args)
		if err != nil {
			return stages.ToolResult{}, err
		}
		return stages.ToolResult{OK: res.OK, Output: res.Output, Truncated: res.Truncated, Meta: res.Meta}, nil
	}
}

func detectLanguage(workDir string) string {
	if workDir == "" {
		return "python"
	}
	if _, err := os.Stat(filepath.Join(workDir, "go.mod")); err == nil {
		return "go"
	}
	if _, err := os.Stat(filepath.Join(workDir, "tsconfig.json")); err == nil {
		return "typescript"
	}
	if _, err := os.Stat(filepath.Join(workDir, "package.json")); err == nil {
		return "javascript"
	}
	py := false
	_ = filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".py") {
			py = true
			return filepath.SkipAll
		}
		return nil
	})
	if py {
		return "python"
	}
	return "go"
}
