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
// or model fixture is ignored. A check that applies but cannot run (tool
// unavailable) is reported as incomplete. When no check applies (unknown
// language, nothing to analyze) the report is not applicable: the stage is
// skipped, not failed and not clean.
type StaticAnalyzer struct {
	checks []VerificationCheck
}

var _ Stage = (*StaticAnalyzer)(nil)

func (*StaticAnalyzer) Capabilities() Capabilities {
	return Capabilities{ModelFree: true, Description: "checking code quality"}
}

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

	scoped, changedFound, err := gitScopedFiles(ctx, workDir, language, options)
	if err != nil {
		scoped = nil
		changedFound = false
	}
	var paths []string
	scopedToChanges := false
	truncated := false
	switch {
	case len(scoped) > 0:
		paths = scoped
		scopedToChanges = true
	case changedFound:
		// Changes exist, but none is in the analyzed language. Analyze
		// nothing: never walk unrelated files.
		scopedToChanges = true
	default:
		paths, truncated = workspaceFiles(workDir, language, defaultStaticMaxFiles)
	}
	if scopedToChanges && len(paths) == 0 {
		summary := fmt.Sprintf("no changed %s files to analyze", language)
		return schemas.HarnessStageOutput{
			Summary:    summary,
			Detail:     summary,
			Confidence: 1.0,
			Data: map[string]any{
				"static_analyzer_output": schemas.VerificationReport{
					Status:   schemas.VerificationNotApplicable,
					Complete: true,
					Summary:  summary,
					Tools:    []schemas.VerificationToolRun{},
					Findings: []schemas.VerificationFinding{},
				},
				"analyzed_paths":    []string{},
				"scoped_to_changes": true,
				"truncated":         false,
			},
		}, nil
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
			Sandbox:  options.Sandbox,
		})
		if rerr != nil {
			return schemas.HarnessStageOutput{}, fmt.Errorf("verification check %s: %w", check.Name(), rerr)
		}
		results = append(results, result)
	}

	if truncated {
		results = append([]VerificationCheckResult{{ToolRun: truncatedCoverageRun("quality", len(paths), defaultStaticMaxFiles)}}, results...)
	}
	report := aggregateVerificationResults(results)

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
		"truncated":              truncated,
	}
	return schemas.HarnessStageOutput{
		Summary:    report.Summary,
		Detail:     report.Summary,
		Confidence: 1.0,
		Data:       data,
	}, nil
}

// workspaceFiles returns up to maxFiles eligible files under workspace.
// truncated is true when at least one more eligible file exists beyond the cap.
func workspaceFiles(workspace, language string, maxFiles int) (files []string, truncated bool) {
	extensions := languageExtensions[language]
	if extensions == nil {
		return nil, false
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
		if len(candidates) > maxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	if len(candidates) > maxFiles {
		return candidates[:maxFiles], true
	}
	return candidates, false
}

// gitScopedFiles returns the changed or untracked files in the analyzed
// language that exist on disk. changed reports whether git found any changed or
// untracked file at all, whatever its language. A failed discovery returns an
// error and no list.
func gitScopedFiles(ctx context.Context, workspace, language string, options StageOptions) (paths []string, changed bool, err error) {
	if workspace == "" {
		return nil, false, fmt.Errorf("no workspace")
	}
	extensions := languageExtensions[language]
	if extensions == nil {
		return nil, false, fmt.Errorf("no workspace")
	}
	lines, err := gitChangedFileLines(ctx, workspace, "changed file discovery", options)
	if err != nil {
		return nil, false, err
	}
	for _, line := range lines {
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
	return paths, len(lines) > 0, nil
}
