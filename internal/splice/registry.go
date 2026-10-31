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
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// stageRegistry maps stage names to runnable Stage implementations.
type stageRegistry map[string]stages.Stage

// buildStageRegistry creates a registry of Splice pipeline stages from agent
// options and the run's active topology. The topology is the seam that makes a
// loaded custom topology runnable: registerTopologyNodes adds one stage per
// custom node, keyed by node name. A nil topology registers the builtin stages
// only.
func buildStageRegistry(options PipelineRunConfig, workDir string, topology *schemas.PipelineTopology) (stageRegistry, error) {
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
	if err := registerTopologyNodes(r, topology); err != nil {
		return nil, err
	}
	return r, nil
}

// registerTopologyNodes adds a stage for every custom node in the topology,
// keyed by node name, and aliases a renamed builtin node under its node name.
// A custom node the build does not support yet is a hard error: a topology
// must never compile to a plan whose stage silently has no implementation.
// The caller passes the active topology, so a loaded topology reaches the
// registry through one seam.
func registerTopologyNodes(r stageRegistry, topology *schemas.PipelineTopology) error {
	if topology == nil {
		return nil
	}
	for _, node := range topology.Nodes {
		switch node.Type {
		case schemas.NodeTypeCommand:
			r[node.Name] = stages.CommandStage{Name: node.Name, Command: append([]string(nil), node.Command...)}
		case schemas.NodeTypePrompt:
			r[node.Name] = stages.PromptStage{Name: node.Name, Template: node.Prompt}
		case schemas.NodeTypeAgent:
			r[node.Name] = stages.AgentStage{
				Name:       node.Name,
				Template:   node.Prompt,
				Tools:      append([]string(nil), node.Tools...),
				MaxTurns:   node.MaxTurns,
				Permission: node.Permission,
				InputMax:   agentBudgetInput(node),
				OutputMax:  agentBudgetOutput(node),
			}
		default:
			if node.Name == node.Type {
				continue // already registered under its builtin name
			}
			builtin, ok := r[node.Type]
			if !ok {
				return fmt.Errorf("topology node %s: no builtin stage for type %q", node.Name, node.Type)
			}
			r[node.Name] = builtin
		}
	}
	return nil
}

// agentBudgetInput and agentBudgetOutput return an agent node's explicit
// budget. The schema requires the budget, so the nil case is a zero budget
// that the loop never reaches.
func agentBudgetInput(node schemas.PipelineNode) int {
	if node.Budget == nil {
		return 0
	}
	return node.Budget.InputMax
}

func agentBudgetOutput(node schemas.PipelineNode) int {
	if node.Budget == nil {
		return 0
	}
	return node.Budget.OutputMax
}

// stageOptions builds StageOptions for a named stage.
// iteration and selection provide attribution context for usage callbacks.
// attribution, when non-nil, carries the orchestrator's per-invocation
// classification (repair ordinal, context round, spend source); each emitted
// usage snapshot stamps it onto the record so the ledger can join spend by
// source without re-deriving it.
func stageOptions(name string, iteration int, selection agent.ModelSelection, options PipelineRunConfig, workDir string, runner ToolRunner, caps stages.Capabilities, attribution ...*agent.RequestAttribution) stages.StageOptions {
	language := detectLanguage(workDir)
	promptCacheKey := ""
	if options.SessionID != "" {
		promptCacheKey = options.SessionID + ":" + name
	}
	var onUsageResult func(zeroruntime.Usage, bool, *float64, string, bool)
	var onUsageError func(string)
	var onLegacyUsage func(zeroruntime.Usage)
	// layoutHash is the most recent request's stable-prefix layout hash. The
	// layout callback fires just before each request and the usage callback just
	// after it, and requests within a stage invocation are sequential, so the
	// usage emitted next belongs to this hash.
	layoutHash := ""
	// formatRetryNotify reclassifies the attribution cell for format-retry
	// attempts (F1). Declared here so the StageOptions literal below can
	// reference it; set only when attribution is active.
	var formatRetryNotify func(attempt int)
	var cell *agent.RequestAttribution
	if options.OnAttributedUsage != nil {
		// The closure reads the classification cell AT EMISSION TIME, not at
		// registration: an expansion round or repair re-entry reclassifies
		// the cell before its request streams, so the record carries the
		// round/ordinal the request actually belongs to.
		if len(attribution) > 0 {
			cell = attribution[0]
		}
		if cell != nil {
			formatRetryNotify = func(attempt int) {
				ordinal, round, _ := cell.Snapshot()
				cell.Set(ordinal, round, schemas.SpendSourceFormatRetry)
			}
		}
		emitAttributed := func(usage zeroruntime.Usage, reported bool, usageError string, reportedCostUSD *float64, requestID string) {
			ordinal, round, source := cell.Snapshot()
			options.OnAttributedUsage(agent.AttributedUsage{
				Usage:             usage,
				UsageReported:     reported,
				UsageError:        usageError,
				ProviderName:      selection.ProviderName,
				Model:             selection.Model,
				Stage:             name,
				Iteration:         iteration,
				ReportedCostUSD:   reportedCostUSD,
				RequestID:         requestID,
				PromptLayoutHash:  layoutHash,
				InvocationOrdinal: ordinal,
				ContextRound:      round,
				SpendSource:       source,
			})
		}
		onUsageResult = func(usage zeroruntime.Usage, reported bool, reportedCostUSD *float64, requestID string, _ bool) {
			emitAttributed(usage, reported, "", reportedCostUSD, requestID)
		}
		onUsageError = func(reason string) { emitAttributed(zeroruntime.Usage{}, true, reason, nil, "") }
	} else {
		onLegacyUsage = options.OnUsage
	}
	timeoutSeconds := caps.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = stages.DefaultTimeoutSeconds
	}
	stageOptions := stages.StageOptions{
		WorkDir:             workDir,
		ProjectInstructions: options.ProjectInstructions,
		Sandbox:             options.StageSandbox,
		Language:            language,
		PullContext:         caps.PullContext,
		RunTool:             adaptToolRunner(runner),
		RunAgent:            agentNodeRunner(name, iteration, selection, options, workDir),
		ReportActivity:      makeReportCallback(options, name),
		Stream: zeroruntime.CollectOptions{
			OnText:          options.OnText,
			OnReasoning:     options.OnReasoning,
			OnUsage:         onLegacyUsage,
			OnUsageResult:   onUsageResult,
			OnUsageError:    onUsageError,
			OnToolCallStart: options.OnToolCallStart,
			OnToolCallDelta: options.OnToolCallDelta,
			OnPromptLayout:  func(hash string) { layoutHash = hash },
		},
		Images:         append([]zeroruntime.ImageBlock(nil), options.Images...),
		RecordCommand:  makeRecordedCommandCallback(options),
		ModelOverride:  options.Model,
		PromptCacheKey: promptCacheKey,
		// F1 wiring: format retries reclassify the attribution cell so the
		// per-attempt usage events for attempts 2+ carry the format_retry
		// source. The closure runs inside callValidatedToolUse's retry loop
		// BEFORE each retry request streams; the emission-time Snapshot()
		// then reads the reclassified value.
		OnFormatRetry:  formatRetryNotify,
		TimeoutSeconds: timeoutSeconds,
	}
	return stageOptions
}

// withVerifyCommand gives a test runner stage the configured verify command
// and directory. It keys on the stage's role (its type), not its name, so a
// topology that renames the test runner still runs the configured command.
func withVerifyCommand(opts stages.StageOptions, stage stages.Stage, options PipelineRunConfig, workDir string) stages.StageOptions {
	if _, ok := stage.(stages.TestRunner); !ok || len(options.VerifyCommand) == 0 {
		return opts
	}
	opts.Command = append([]string(nil), options.VerifyCommand...)
	opts.CommandWorkDir = workDir
	if dir := strings.TrimSpace(options.VerifyDir); dir != "" {
		opts.CommandWorkDir = filepath.Join(workDir, dir)
	}
	return opts
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
		// An unknown workspace can gain a marker during the run. Only the
		// marker stats run again; the source walk waits for a marker.
		if val != "unknown" || !languageMarkerPresent(workDir) {
			return val
		}
	} else {
		languageCacheMu.Unlock()
	}

	lang := detectLanguageUncached(workDir)
	languageCacheMu.Lock()
	languageCacheValid = true
	languageCacheDir = workDir
	languageCacheVal = lang
	languageCacheMu.Unlock()

	return lang
}

// languageMarkers are the root files that name a language. A workspace with
// none of them is "unknown" until one appears (the writer may create go.mod).
var languageMarkers = []string{"go.mod", "go.work", "tsconfig.json", "package.json"}

// languageSkipDirs are never walked for Python sources: vendored code, build
// output, and tool state.
var languageSkipDirs = map[string]bool{".git": true, "node_modules": true, "target": true, "vendor": true, ".venv": true}

// pythonRootMarkers are root files that name a Python project. A root *.py
// file counts too.
var pythonRootMarkers = []string{"pyproject.toml", "setup.py", "requirements.txt"}

// rootHasPython reads the workspace root once (no recursion) and reports
// whether it holds a Python source or project file.
func rootHasPython(workDir string) bool {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".py") {
			return true
		}
		for _, marker := range pythonRootMarkers {
			if name == marker {
				return true
			}
		}
	}
	return false
}

// languageMarkerPresent stats the marker files and reads the root once. It
// costs four stats and one readdir, so an "unknown" workspace is re-checked
// without a walk.
func languageMarkerPresent(workDir string) bool {
	for _, marker := range languageMarkers {
		if _, err := os.Stat(filepath.Join(workDir, marker)); err == nil {
			return true
		}
	}
	return rootHasPython(workDir)
}

func detectLanguageUncached(workDir string) string {
	if workDir == "" {
		return "python"
	}
	if _, err := os.Stat(filepath.Join(workDir, "go.mod")); err == nil {
		return "go"
	}
	if _, err := os.Stat(filepath.Join(workDir, "go.work")); err == nil {
		return "go"
	}
	if _, err := os.Stat(filepath.Join(workDir, "tsconfig.json")); err == nil {
		return "typescript"
	}
	if _, err := os.Stat(filepath.Join(workDir, "package.json")); err == nil {
		return "javascript"
	}
	if rootHasPython(workDir) {
		return "python"
	}
	py := false
	_ = filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != workDir && languageSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
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
	// No marker matched: the workspace has no language Splice knows. Callers
	// treat "unknown" as "no language-specific check", never as Go.
	return "unknown"
}
