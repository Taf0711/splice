package stages

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

const maxScanFiles = 50

// SecurityAuditor is the model-free security audit pipeline stage.
//
// F14b migrated it from the ambiguous StaticAnalyzerOutput to a typed
// VerificationReport. The stage runs independent VerificationCheck adapters
// (by default the Bandit security adapter), aggregates their results, and never
// calls a provider. Missing Bandit is incomplete, not clean.
type SecurityAuditor struct {
	checks []VerificationCheck
}

var _ Stage = (*SecurityAuditor)(nil)

// NewSecurityAuditor constructs a security auditor from one or more
// deterministic verification checks. Returns a named error if no checks are
// provided.
func NewSecurityAuditor(checks ...VerificationCheck) (*SecurityAuditor, error) {
	if len(checks) == 0 {
		return nil, fmt.Errorf("security auditor requires at least one verification check")
	}
	return &SecurityAuditor{checks: checks}, nil
}

func (s *SecurityAuditor) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options StageOptions) (schemas.HarnessStageOutput, error) {
	if len(s.checks) == 0 {
		return schemas.HarnessStageOutput{}, fmt.Errorf("security auditor has no verification checks configured")
	}
	workDir := options.WorkDir
	if workDir == "" {
		return schemas.HarnessStageOutput{}, fmt.Errorf("security auditor requires work_dir")
	}

	scopedToChanges := false
	changedPaths, gitErr := gitChangedSourceFiles(ctx, workDir, options)

	var paths []string
	if gitErr == nil && len(changedPaths) > 0 {
		paths = changedPaths
		scopedToChanges = true
	} else {
		paths = boundedSourceFiles(workDir)
	}
	analyzedPaths := make([]string, len(paths))
	for i, p := range paths {
		rel, _ := filepath.Rel(workDir, p)
		if rel == "" || strings.HasPrefix(rel, "..") {
			rel = p
		}
		analyzedPaths[i] = rel
	}

	options.report(fmt.Sprintf("scanning %d source file(s): %s", len(paths), formatPathList(analyzedPaths, 5)))

	var results []VerificationCheckResult
	for _, check := range s.checks {
		result, rerr := check.Run(ctx, VerificationCheckRequest{
			WorkDir:  workDir,
			Language: options.Language,
			Paths:    paths,
			Scope:    "security",
			RunTool:  options.RunTool,
		})
		if rerr != nil {
			return schemas.HarnessStageOutput{}, fmt.Errorf("verification check %s: %w", check.Name(), rerr)
		}
		results = append(results, result)
	}

	report := aggregateVerificationResults(results)

	data := map[string]any{
		"security_auditor_output": report,
		"analyzed_paths":          analyzedPaths,
		"scoped_to_changes":       scopedToChanges,
	}
	return schemas.HarnessStageOutput{
		Summary:    report.Summary,
		Detail:     report.Summary,
		Confidence: 1.0,
		Data:       data,
	}, nil
}

func boundedSourceFiles(root string) []string {
	var found []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		parts := strings.Split(rel, string(filepath.Separator))
		hidden := false
		for _, part := range parts {
			if strings.HasPrefix(part, ".") && part != "." {
				hidden = true
				break
			}
			if part == "__pycache__" || part == "node_modules" {
				hidden = true
				break
			}
		}
		if hidden {
			return nil
		}
		found = append(found, path)
		if len(found) >= maxScanFiles {
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func gitChangedSourceFiles(ctx context.Context, root string, options StageOptions) ([]string, error) {
	command := []string{"git", "diff", "--name-only", "HEAD"}
	out, err := runRecordedOutput(ctx, options, "splice.shell", map[string]any{
		"command": command,
		"cwd":     root,
		"purpose": "changed source file discovery",
	}, root, command)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		p := filepath.Join(root, line)
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	return paths, nil
}
