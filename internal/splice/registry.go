package splice

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/dtools"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// stageRegistry maps stage names to runnable Stage implementations.
type stageRegistry map[string]stages.Stage

// buildStageRegistry creates a registry of Splice pipeline stages from agent options.
func buildStageRegistry(options PipelineRunConfig, workDir string) (stageRegistry, error) {
	r := stageRegistry{
		"code_writer":         stages.CodeWriter{},
		"test_generator":      stages.TestGenerator{},
		"test_runner":         stages.TestRunner{},
		"acceptance_verifier": stages.AcceptanceVerifier{},
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
	return r, nil
}

// stageOptions builds StageOptions for a named stage.
// iteration and selection provide attribution context for usage callbacks.
func stageOptions(name string, iteration int, selection agent.ModelSelection, options PipelineRunConfig, workDir string, runner ToolRunner) stages.StageOptions {
	language := detectLanguage(workDir)
	promptCacheKey := ""
	if options.SessionID != "" {
		promptCacheKey = options.SessionID + ":" + name
	}
	var onUsageResult func(zeroruntime.Usage, bool, *float64)
	var onUsageError func(string)
	var onLegacyUsage func(zeroruntime.Usage)
	if options.OnAttributedUsage != nil {
		emitAttributed := func(usage zeroruntime.Usage, reported bool, usageError string, reportedCostUSD *float64) {
			options.OnAttributedUsage(agent.AttributedUsage{
				Usage:           usage,
				UsageReported:   reported,
				UsageError:      usageError,
				ProviderName:    selection.ProviderName,
				Model:           selection.Model,
				Stage:           name,
				Iteration:       iteration,
				ReportedCostUSD: reportedCostUSD,
			})
		}
		onUsageResult = func(usage zeroruntime.Usage, reported bool, reportedCostUSD *float64) {
			emitAttributed(usage, reported, "", reportedCostUSD)
		}
		onUsageError = func(reason string) { emitAttributed(zeroruntime.Usage{}, true, reason, nil) }
	} else {
		onLegacyUsage = options.OnUsage
	}
	timeoutSeconds := 120
	if name == "acceptance_verifier" {
		timeoutSeconds = 30
	}
	return stages.StageOptions{
		WorkDir:        workDir,
		Language:       language,
		PullContext:    name == "code_writer" || name == "test_generator",
		RunTool:        adaptToolRunner(runner),
		ReportActivity: makeReportCallback(options, name),
		Stream: zeroruntime.CollectOptions{
			OnText:          options.OnText,
			OnReasoning:     options.OnReasoning,
			OnUsage:         onLegacyUsage,
			OnUsageResult:   onUsageResult,
			OnUsageError:    onUsageError,
			OnToolCallStart: options.OnToolCallStart,
			OnToolCallDelta: options.OnToolCallDelta,
		},
		Images:         append([]zeroruntime.ImageBlock(nil), options.Images...),
		RecordCommand:  makeRecordedCommandCallback(options),
		ModelOverride:  options.Model,
		PromptCacheKey: promptCacheKey,
		TimeoutSeconds: timeoutSeconds,
	}
}

func makeReportCallback(options PipelineRunConfig, stageName string) func(string) {
	return func(message string) {
		if options.OnReasoning != nil {
			options.OnReasoning(fmt.Sprintf("[%s] %s\n", stageName, message))
		}
	}
}

func makeRecordedCommandCallback(options PipelineRunConfig) func(context.Context, string, map[string]any, func(context.Context) (stages.ToolResult, error)) (stages.ToolResult, error) {
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

// languageCache memoizes the last computed workDir -> language mapping.
// stageOptions calls detectLanguage once per stage per iteration with the
// same workDir every time; without this, each call re-walks the whole
// workspace when no go.mod/tsconfig.json/package.json marker is present
// (i.e. on every Python target).
//
// ponytail: single-entry cache, not keyed per workDir. A caller that
// interleaves pipeline stages across multiple distinct workspaces in one
// process (e.g. a future daemon serving concurrent runs) would thrash it
// back to a walk per call; upgrade to a small bounded map if that happens.
var (
	languageCacheMu    sync.Mutex
	languageCacheValid bool
	languageCacheDir   string
	languageCacheVal   string
)

func detectLanguage(workDir string) string {
	languageCacheMu.Lock()
	if languageCacheValid && workDir == languageCacheDir {
		val := languageCacheVal
		languageCacheMu.Unlock()
		return val
	}
	languageCacheMu.Unlock()

	lang := detectLanguageUncached(workDir)

	languageCacheMu.Lock()
	languageCacheValid = true
	languageCacheDir = workDir
	languageCacheVal = lang
	languageCacheMu.Unlock()

	return lang
}

func detectLanguageUncached(workDir string) string {
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
