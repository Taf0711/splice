package stages

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// goSyntaxCheck is a deterministic quality adapter that parses Go source files
// and reports syntax errors. It wraps the existing go/parser logic behind the
// VerificationCheck interface so the aggregator owns report construction.
type goSyntaxCheck struct{}

func (goSyntaxCheck) Name() string     { return "go_syntax" }
func (goSyntaxCheck) Category() string { return "quality" }

func (c goSyntaxCheck) Run(ctx context.Context, req VerificationCheckRequest) (VerificationCheckResult, error) {
	var goPaths []string
	for _, p := range req.Paths {
		if strings.HasSuffix(p, ".go") {
			goPaths = append(goPaths, p)
		}
	}
	// Sort so path order (and therefore fingerprints) is stable across runs.
	sort.Strings(goPaths)
	var findings []schemas.VerificationFinding
	checked := 0
	for _, path := range goPaths {
		select {
		case <-ctx.Done():
			return VerificationCheckResult{}, ctx.Err()
		default:
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return VerificationCheckResult{}, fmt.Errorf("read %s: %w", path, rerr)
		}
		rel := relPath(path, req.WorkDir)
		fset := token.NewFileSet()
		_, perr := parser.ParseFile(fset, path, src, parser.AllErrors)
		checked++
		if perr != nil {
			findings = append(findings, schemas.VerificationFinding{
				Tool:      c.Name(),
				Authority: "deterministic",
				RuleID:    "GO_SYNTAX",
				Category:  c.Category(),
				Path:      rel,
				Line:      ptrInt(0),
				Message:   fmt.Sprintf("Go syntax error: %v", perr),
				Severity:  schemas.SeverityHigh,
			})
			// A file that does not parse cannot be gofmt-checked without noise;
			// GO_SYNTAX already covers it.
			continue
		}
		// gofmt equivalence: a file is gofmt-clean iff go/format.Source
		// reproduces its bytes exactly. This keeps the in-process Go profile
		// honest without spawning a second process.
		formatted, ferr := format.Source(src)
		if ferr == nil && !bytes.Equal(formatted, src) {
			findings = append(findings, schemas.VerificationFinding{
				Tool:      c.Name(),
				Authority: "deterministic",
				RuleID:    "GO_FORMAT",
				Category:  c.Category(),
				Path:      rel,
				Message:   "File is not gofmt-clean",
				Severity:  schemas.SeverityLow,
			})
		}
	}
	status := schemas.VerificationPassed
	summary := fmt.Sprintf("Parsed %d Go file(s), no syntax errors.", checked)
	if len(findings) > 0 {
		status = schemas.VerificationFindings
		summary = fmt.Sprintf("Parsed %d Go file(s), found %d issue(s).", checked, len(findings))
	}
	if checked == 0 {
		status = schemas.VerificationNotApplicable
		summary = "No Go files to parse."
	}
	return VerificationCheckResult{
		ToolRun: schemas.VerificationToolRun{
			Tool:     c.Name(),
			Required: true,
			Scope:    "quality",
			Status:   status,
			Summary:  summary,
		},
		Findings: findings,
	}, nil
}
