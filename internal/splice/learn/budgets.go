// Package learn holds the deterministic learning layer. It is pure functions
// over typed structs: no provider, no model, no I/O except the injected
// TraceQuerier. The Regression Discipline governs its tests: fabricated trace
// corpora, property tests, and adversarial guard fixtures.
//
// DELIBERATE DEFERRALS (LN2 scope):
//   - The kept-rate rollback guard is deferred: the verdict corpus is too
//     sparse yet to distinguish regression from noise, and the survivorship
//     guard already covers the budget-too-low direction.
//   - LN3 learned trajectory weights and PC3 exemplar retrieval are out of
//     scope for this slice.
package learn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Floor is the minimum completed-run corpus size for a bucket before the
// fitter calibrates. Below it the fitter refuses loudly with provenance naming
// n and the floor.
const Floor = 20

// shortHashBytes is the "short form" length of a hash: the first 8 bytes of
// sha256 rendered as 16 hex characters.
const shortHashBytes = 8

// queryLimit bounds each fitter query so a warm repo cannot produce an
// unbounded fetch. It is far above Floor.
const queryLimit = 1000

// Hash returns a short sha256 hex of the concatenated parts, separated by NUL
// so ("a","bc") and ("ab","c") never collide. It is the deterministic key
// builder for prompt, tool, and topology fingerprints.
func Hash(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil)[:shortHashBytes])
}

// BucketKey is the six-tuple that identifies a calibration bucket. Staleness
// is key mismatch, never time: a key change opens a fresh bucket and the old
// bucket freezes intact.
type BucketKey struct {
	RepoRoot        string
	Stage           string
	PromptHash      string
	Model           string
	ToolFingerprint string
	TopologyHash    string
}

// BudgetFit is the fitted per-stage budget plus its provenance.
type BudgetFit struct {
	InputMax   int
	OutputMax  int
	Calibrated bool
	Provenance string
}

// TraceQuerier fetches decoded traces. The memd client satisfies it; tests use
// fabricated corpora.
type TraceQuerier interface {
	QueryTraces(ctx context.Context, filter schemas.TraceQueryFilter) ([]schemas.TraceQueryResult, error)
}

// FitBudget calibrates a per-stage token budget from completed-run history for
// the exact bucket key and memory status. It never fails the run: an error is
// returned to the caller, which maps it to defaults with a "fit error"
// provenance.
//
// Verdict is irrelevant to cost fitting and is never consulted. Cache hits are
// already real tokens in the ledger, so no special-casing is needed.
func FitBudget(ctx context.Context, q TraceQuerier, key BucketKey, memoryStatus string, staticDefault schemas.StageBudget) (BudgetFit, error) {
	// A legacy/partial key never fits: refuse with the same provenance as a
	// below-floor bucket so an empty key is never silently treated as a match.
	if key.PromptHash == "" || key.ToolFingerprint == "" || key.TopologyHash == "" || key.Model == "" {
		return BudgetFit{
			InputMax:   staticDefault.InputMax,
			OutputMax:  staticDefault.OutputMax,
			Calibrated: false,
			Provenance: fmt.Sprintf("budget not calibrated: 0/%d for this key", Floor),
		}, nil
	}

	completed, err := q.QueryTraces(ctx, schemas.TraceQueryFilter{
		RepoRoot: key.RepoRoot,
		Status:   "completed",
		Limit:    queryLimit,
	})
	if err != nil {
		return BudgetFit{}, fmt.Errorf("query completed traces: %w", err)
	}

	var inputs, outputs []int
	for _, result := range completed {
		if in, out, ok := matchStage(&result.Trace, key, memoryStatus); ok {
			inputs = append(inputs, in)
			outputs = append(outputs, out)
		}
	}

	if len(inputs) < Floor {
		return BudgetFit{
			InputMax:   staticDefault.InputMax,
			OutputMax:  staticDefault.OutputMax,
			Calibrated: false,
			Provenance: fmt.Sprintf("budget not calibrated: %d/%d for this key", len(inputs), Floor),
		}, nil
	}

	fit := BudgetFit{
		InputMax:   p80(inputs),
		OutputMax:  p80(outputs),
		Calibrated: true,
		Provenance: fmt.Sprintf("calibrated from %d runs, p80", len(inputs)),
	}

	// Survivorship guard: budget-aborted runs vote too. If the same bucket
	// aborted on token budget, raise the fit to cover the largest abort so a
	// too-low budget does not silently select out the expensive runs.
	aborted, err := q.QueryTraces(ctx, schemas.TraceQueryFilter{
		RepoRoot: key.RepoRoot,
		Status:   "aborted",
		Limit:    queryLimit,
	})
	if err != nil {
		return BudgetFit{}, fmt.Errorf("query aborted traces: %w", err)
	}
	var abortInputs, abortOutputs []int
	for _, result := range aborted {
		if !strings.Contains(strings.ToLower(result.Trace.Outcome.AbortReason), "abort_budget") {
			continue
		}
		if in, out, ok := matchStage(&result.Trace, key, memoryStatus); ok {
			abortInputs = append(abortInputs, in)
			abortOutputs = append(abortOutputs, out)
		}
	}
	if len(abortInputs) > 0 {
		maxIn := maxInt(abortInputs)
		maxOut := maxInt(abortOutputs)
		if maxIn > fit.InputMax || maxOut > fit.OutputMax {
			if maxIn > fit.InputMax {
				fit.InputMax = maxIn
			}
			if maxOut > fit.OutputMax {
				fit.OutputMax = maxOut
			}
			fit.Provenance += fmt.Sprintf("; raised for %d budget aborts", len(abortInputs))
		}
	}

	// Sanity clamp: the fit must stay within [0.5x, 2.0x] of the static
	// default so a pathological corpus cannot produce a nonsensical budget.
	clamped := false
	if lo, hi := staticDefault.InputMax/2, staticDefault.InputMax*2; fit.InputMax < lo {
		fit.InputMax = lo
		clamped = true
	} else if fit.InputMax > hi {
		fit.InputMax = hi
		clamped = true
	}
	if lo, hi := staticDefault.OutputMax/2, staticDefault.OutputMax*2; fit.OutputMax < lo {
		fit.OutputMax = lo
		clamped = true
	} else if fit.OutputMax > hi {
		fit.OutputMax = hi
		clamped = true
	}
	if clamped {
		fit.Provenance += "; clamped to [0.5x, 2.0x] of default"
	}

	return fit, nil
}

// matchStage reports whether a trace belongs to the exact bucket key and memory
// status, and returns the matched stage's authoritative input/output tokens.
// A trace with empty key fields is a legacy trace: it never matches a full key.
func matchStage(trace *schemas.RunOutcome, key BucketKey, memoryStatus string) (input, output int, ok bool) {
	if trace == nil {
		return 0, 0, false
	}
	if trace.RepoRoot != key.RepoRoot {
		return 0, 0, false
	}
	if trace.ToolFingerprint != key.ToolFingerprint || trace.TopologyHash != key.TopologyHash {
		return 0, 0, false
	}
	if trace.Memory.Status != memoryStatus {
		return 0, 0, false
	}
	for _, stage := range trace.Stages {
		if stage.Name != key.Stage {
			continue
		}
		if stage.PromptHash != key.PromptHash {
			continue
		}
		if DerefString(stage.Model) != key.Model {
			continue
		}
		return stage.TokensInput, stage.TokensOutput, true
	}
	return 0, 0, false
}

// DerefString returns the string a pointer points to, or "" when nil.
func DerefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// p80 returns the 80th percentile (nearest-rank) of a sorted copy of values.
// Percentiles, never means, keep one outlier run from skewing the fit.
func p80(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	rank := int(math.Ceil(0.8*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

func maxInt(values []int) int {
	if len(values) == 0 {
		return 0
	}
	m := values[0]
	for _, v := range values[1:] {
		if v > m {
			m = v
		}
	}
	return m
}
