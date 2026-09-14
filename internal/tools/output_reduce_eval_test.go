package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Output-reduction evaluation.
//
// This is a deterministic, offline evaluation of the native reducers. It runs
// against real captured tool output, so a result is reproducible and costs no
// provider tokens.
//
// Method:
//   - Fixed corpus: testdata/reduction/*.log, captured from real commands.
//   - Fixed expectations: each case declares "reduce" or "refuse".
//   - Preservation check: every declared signal substring must survive.
//   - Order check: the output must be an in-order subsequence of the input.
//   - Idempotence check: a second pass must change nothing.
//   - Baseline: head-and-tail truncation at the bash emit budget (32 KiB), the
//     behaviour the reducer replaces.
//
// The test always prints a Markdown report. It writes JSON only when
// SPLICE_REDUCE_EVAL_JSON names an output path, so a normal `go test` run has no
// side effects.

// evalBaselineBytes is the bash emit budget per stream, the baseline this
// reducer replaces.
const evalBaselineBytes = 32 * 1024

type reduceEvalCase struct {
	Name   string
	File   string
	Expect string // "reduce" or "refuse"
	Keep   []string
	Drop   []string
	Note   string
}

type reduceEvalResult struct {
	Name          string  `json:"name"`
	File          string  `json:"file"`
	Expect        string  `json:"expect"`
	Observed      string  `json:"observed"`
	BytesIn       int     `json:"bytes_in"`
	BytesOut      int     `json:"bytes_out"`
	LinesIn       int     `json:"lines_in"`
	LinesOut      int     `json:"lines_out"`
	BytesSavedPct float64 `json:"bytes_saved_pct"`
	BaselineBytes int     `json:"baseline_bytes"`
	BaselineTrunc bool    `json:"baseline_truncated"`
	SignalsKept   int     `json:"signals_kept"`
	SignalsTotal  int     `json:"signals_total"`
	OrderOK       bool    `json:"order_ok"`
	Idempotent    bool    `json:"idempotent"`
	Pass          bool    `json:"pass"`
	Note          string  `json:"note,omitempty"`
}

func reduceEvalCases() []reduceEvalCase {
	return []reduceEvalCase{
		{
			Name:   "go-test-pass",
			File:   "go-test-pass.log",
			Expect: "reduce",
			Keep: []string{
				"ok  \tgithub.com/Taf0711/splice/internal/eval\t",
				"ok  \tgithub.com/Taf0711/splice/internal/eval/v2\t",
				"PASS",
			},
			Drop: []string{"--- PASS:", "=== RUN"},
		},
		{
			Name:   "go-test-fail",
			File:   "go-test-fail.log",
			Expect: "reduce",
			Keep: []string{
				"--- FAIL: TestWithSubs (0.00s)",
				"--- FAIL: TestWithSubs/bad (0.00s)",
				"--- FAIL: TestTable/b (0.00s)",
				"    thing_test.go:13: got 3, want 4",
				"    thing_test.go:31: table case b failed",
				"--- SKIP: TestSkipped (0.00s)",
				"=== PAUSE TestParallel",
				"=== CONT  TestParallel",
				"--- PASS: NotARealTest (0.00s)",
				"fake detail line that must survive",
				"FAIL\tgtfixture\t0.340s",
			},
			Drop: []string{"--- PASS: TestPassOne", "=== RUN   TestPassOne", "=== RUN   TestTable/a"},
		},
		{
			Name:   "go-compile-error",
			File:   "go-compile-error.log",
			Expect: "refuse",
			Keep:   []string{"cannot use", "[build failed]"},
		},
		{
			Name:   "go-bench",
			File:   "go-bench.log",
			Expect: "reduce",
			Keep:   []string{"BenchmarkThing-8", "BenchmarkOther-8", "ns/op", "ok  \tgtbench"},
			Drop:   []string{"--- PASS: TestAlpha", "=== RUN   TestAlpha"},
			Note:   "a -v benchmark run carries passing-test blocks; the reducer removes those and keeps every benchmark number",
		},
		{
			Name:   "git-diff",
			File:   "git-diff.log",
			Expect: "refuse",
			Keep:   []string{"diff --git", "@@", "+++ b/"},
			Note:   "open gap: a diff has no passing-test blocks, so the reducer refuses and head+tail still drops content",
		},
		{
			Name:   "git-status",
			File:   "git-status.log",
			Expect: "refuse",
			Keep:   []string{" M .gitignore", "?? plans/TOOL_PERMISSION_AUDIT_2026-09-10.md"},
		},
	}
}

// isSubsequence reports whether every output line appears in the input, in
// order. A reducer may remove lines; it may never reorder or rewrite them.
func isSubsequence(in, out []string) bool {
	i := 0
	for _, line := range out {
		found := false
		for i < len(in) {
			if in[i] == line {
				found = true
				i++
				break
			}
			i++
		}
		if !found {
			return false
		}
	}
	return true
}

// TestReduceEval is the evaluation. It fails when a case breaks its declared
// expectation, loses a signal, reorders content, or stops being idempotent.
func TestReduceEval(t *testing.T) {
	cases := reduceEvalCases()
	results := make([]reduceEvalResult, 0, len(cases))
	totalIn, totalOut, totalBaseline := 0, 0, 0

	for _, tc := range cases {
		path := filepath.Join("testdata", "reduction", tc.File)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("case %s: read %s: %v", tc.Name, path, err)
		}
		in := string(raw)

		out, changed := reduceNativeOutput(in)
		observed := "refuse"
		if changed {
			observed = "reduce"
		}

		res := reduceEvalResult{
			Name:          tc.Name,
			File:          tc.File,
			Expect:        tc.Expect,
			Observed:      observed,
			BytesIn:       len(in),
			BytesOut:      len(out),
			LinesIn:       strings.Count(in, "\n") + 1,
			LinesOut:      strings.Count(out, "\n") + 1,
			BaselineBytes: len(headTailBudget(in, evalBaselineBytes)),
			BaselineTrunc: len(in) > evalBaselineBytes,
			SignalsTotal:  len(tc.Keep),
			Note:          tc.Note,
		}
		if res.BytesIn > 0 {
			res.BytesSavedPct = 100 * float64(res.BytesIn-res.BytesOut) / float64(res.BytesIn)
		}
		for _, needle := range tc.Keep {
			if strings.Contains(out, needle) {
				res.SignalsKept++
			}
		}
		res.OrderOK = isSubsequence(strings.Split(in, "\n"), strings.Split(out, "\n"))
		if changed {
			again, changedAgain := reduceNativeOutput(out)
			res.Idempotent = !changedAgain && again == out
		} else {
			res.Idempotent = true
		}

		res.Pass = res.Observed == tc.Expect &&
			res.SignalsKept == res.SignalsTotal &&
			res.OrderOK &&
			res.Idempotent
		if !res.Pass {
			t.Errorf("case %s failed: expect=%s observed=%s signals=%d/%d order=%v idempotent=%v",
				tc.Name, res.Expect, res.Observed, res.SignalsKept, res.SignalsTotal, res.OrderOK, res.Idempotent)
		}
		for _, needle := range tc.Drop {
			if strings.Contains(out, needle) {
				t.Errorf("case %s: redundancy %q survived reduction", tc.Name, needle)
			}
		}

		totalIn += res.BytesIn
		totalOut += res.BytesOut
		totalBaseline += res.BaselineBytes
		results = append(results, res)
	}

	passed := 0
	for _, r := range results {
		if r.Pass {
			passed++
		}
	}

	// ---- Markdown report ----
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n## Output-reduction eval\n\n")
	fmt.Fprintf(&b, "cases: %d   passed: %d/%d   bytes in: %d   out: %d   baseline (head+tail 32 KiB): %d\n\n",
		len(results), passed, len(results), totalIn, totalOut, totalBaseline)
	fmt.Fprintf(&b, "| case | expect | observed | bytes in | bytes out | saved | baseline | signals | order | idem | verdict |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | --- | --- | --- |\n")
	for _, r := range results {
		verdict := "pass"
		if !r.Pass {
			verdict = "FAIL"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d | %.1f%% | %d%s | %d/%d | %v | %v | %s |\n",
			r.Name, r.Expect, r.Observed, r.BytesIn, r.BytesOut, r.BytesSavedPct,
			r.BaselineBytes, truncMark(r.BaselineTrunc), r.SignalsKept, r.SignalsTotal,
			r.OrderOK, r.Idempotent, verdict)
	}
	fmt.Print(b.String())

	if out := os.Getenv("SPLICE_REDUCE_EVAL_JSON"); out != "" {
		payload := map[string]any{
			"eval":          "native-output-reduction",
			"version":       1,
			"deterministic": true,
			"baseline":      "head+tail truncation at 32 KiB (bashOutputBudgetBytes)",
			"cases":         results,
			"totals": map[string]any{
				"cases":           len(results),
				"passed":          passed,
				"bytes_in":        totalIn,
				"bytes_out":       totalOut,
				"bytes_baseline":  totalBaseline,
				"bytes_saved_pct": 100 * float64(totalIn-totalOut) / float64(totalIn),
			},
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			t.Fatalf("marshal report: %v", err)
		}
		if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
			t.Fatalf("write report: %v", err)
		}
		t.Logf("wrote %s", out)
	}
}

func truncMark(truncated bool) string {
	if truncated {
		return " (truncated)"
	}
	return ""
}

// headTailBudget reproduces the current bash emit truncation so the eval can
// report what the reducer replaces.
func headTailBudget(text string, cap int) string {
	if len(text) <= cap {
		return text
	}
	head := cap / 2
	tail := cap - head
	return utf8Prefix(text, head) + utf8Suffix(text, tail)
}
