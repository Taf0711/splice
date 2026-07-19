package stages

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

const (
	defaultStaticMaxFiles = 20
	defaultStaticMaxBytes = 32_000
	maxSourceFileBytes    = 1_000_000
)

var languageExtensions = map[string][]string{
	"python":     {".py"},
	"typescript": {".ts", ".tsx"},
	"javascript": {".js", ".jsx"},
	"go":         {".go"},
}

var defaultIgnoreDirs = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true,
	".splice": true, ".zero": true, "vendor": true, "dist": true, "build": true,
}

// StaticAnalyzer is the model-free static analysis pipeline stage.
//
// F14b migrated it from the ambiguous StaticAnalyzerOutput to a typed
// VerificationReport. The stage runs independent VerificationCheck adapters,
// aggregates their results, and never calls a provider. A supplied provider
// or model fixture is ignored. Missing coverage (unsupported language,
// unavailable tool) is reported as incomplete, not clean.
type StaticAnalyzer struct {
	checks []VerificationCheck
}

var _ Stage = (*StaticAnalyzer)(nil)

// NewStaticAnalyzer constructs a static analyzer from one or more
// deterministic verification checks. Returns a named error if no checks are
// provided, so a misconfigured registry fails loudly instead of silently
// reporting clean.
func NewStaticAnalyzer(checks ...VerificationCheck) (*StaticAnalyzer, error) {
	if len(checks) == 0 {
		return nil, fmt.Errorf("static analyzer requires at least one verification check")
	}
	return &StaticAnalyzer{checks: checks}, nil
}

func (s *StaticAnalyzer) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options StageOptions) (schemas.HarnessStageOutput, error) {
	if len(s.checks) == 0 {
		return schemas.HarnessStageOutput{}, fmt.Errorf("static analyzer has no verification checks configured")
	}
	workDir := options.WorkDir
	language := options.language("python")

	scoped, err := gitScopedFiles(ctx, workDir, language, options)
	if err != nil {
		scoped = nil
	}
	var paths []string
	scopedToChanges := false
	if len(scoped) > 0 {
		paths = scoped
		scopedToChanges = true
	} else {
		paths = workspaceFiles(workDir, language, defaultStaticMaxFiles)
	}

	options.report(fmt.Sprintf("inspecting %d file(s)", len(paths)))

	// Deterministic subprocess checks are bounded so a slow linter or compiler
	// cannot hang the pipeline. Sort paths so check input order is stable.
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	sort.Strings(paths)

	var results []VerificationCheckResult
	for _, check := range s.checks {
		result, rerr := check.Run(runCtx, VerificationCheckRequest{
			WorkDir:  workDir,
			Language: language,
			Paths:    paths,
			Scope:    "quality",
			RunTool:  options.RunTool,
		})
		if rerr != nil {
			return schemas.HarnessStageOutput{}, fmt.Errorf("verification check %s: %w", check.Name(), rerr)
		}
		results = append(results, result)
	}

	report := aggregateVerificationResults(results)
	if report.Status == schemas.VerificationNotApplicable {
		report = markIncompleteLanguage(report, language)
	}

	relPaths := make([]string, len(paths))
	for i, p := range paths {
		rel, _ := filepath.Rel(workDir, p)
		if rel == "" || strings.HasPrefix(rel, "..") {
			rel = p
		}
		relPaths[i] = rel
	}

	data := map[string]any{
		"static_analyzer_output": report,
		"analyzed_paths":         relPaths,
		"scoped_to_changes":      scopedToChanges,
	}
	return schemas.HarnessStageOutput{
		Summary:    report.Summary,
		Detail:     report.Summary,
		Confidence: 1.0,
		Data:       data,
	}, nil
}

// markIncompleteLanguage converts an all-not-applicable report into an
// incomplete report when the detected language has no registered check. This
// prevents unsupported languages from being reported as clean.
func markIncompleteLanguage(report schemas.VerificationReport, language string) schemas.VerificationReport {
	report.Status = schemas.VerificationIncomplete
	report.Complete = false
	report.Summary = fmt.Sprintf("No verification check available for language: %s", language)
	report.Tools = append(report.Tools, schemas.VerificationToolRun{
		Tool:     "language_profile",
		Required: true,
		Scope:    "quality",
		Status:   schemas.VerificationIncomplete,
		Summary:  fmt.Sprintf("Missing language profile: %s", language),
	})
	return report
}

func workspaceFiles(workspace, language string, maxFiles int) []string {
	extensions := languageExtensions[language]
	if extensions == nil {
		return nil
	}
	var candidates []string
	_ = filepath.WalkDir(workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(workspace, path)
		parts := strings.Split(rel, string(filepath.Separator))
		for _, part := range parts {
			if defaultIgnoreDirs[part] {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		for _, ext := range extensions {
			if strings.HasSuffix(path, ext) {
				info, err := d.Info()
				if err == nil && info.Size() <= maxSourceFileBytes {
					candidates = append(candidates, path)
				}
				break
			}
		}
		if len(candidates) >= maxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	return candidates
}

func gitScopedFiles(ctx context.Context, workspace, language string, options StageOptions) ([]string, error) {
	if workspace == "" {
		return nil, fmt.Errorf("no workspace")
	}
	extensions := languageExtensions[language]
	if extensions == nil {
		return nil, fmt.Errorf("no workspace")
	}
	command := []string{"git", "diff", "--name-only", "HEAD"}
	out, err := runRecordedOutput(ctx, options, "splice.shell", map[string]any{
		"command": command,
		"cwd":     workspace,
		"purpose": "changed file discovery",
	}, workspace, command)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		for _, ext := range extensions {
			if strings.HasSuffix(line, ext) {
				p := filepath.Join(workspace, line)
				if _, err := os.Stat(p); err == nil {
					paths = append(paths, p)
				}
				break
			}
		}
	}
	return paths, nil
}
