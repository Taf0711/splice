package dtools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestCompactBanditJSONKeepsCheckFields pins that a large report, with
// per-file metrics and long code snippets, reduces to the fields the
// security check reads.
func TestCompactBanditJSONKeepsCheckFields(t *testing.T) {
	metrics := map[string]any{}
	for i := 0; i < 200; i++ {
		metrics[fmt.Sprintf("pkg/mod%03d.py", i)] = map[string]any{"loc": 120, "nosec": 0, "CONFIDENCE.HIGH": 3, "SEVERITY.HIGH": 1}
	}
	code := strings.Repeat("x = eval(user_input)  # padding padding\n", 60)
	results := make([]map[string]any, 0, 50)
	for i := 0; i < 50; i++ {
		results = append(results, map[string]any{
			"filename":       fmt.Sprintf("pkg/mod%03d.py", i),
			"line_range":     []int{i + 1},
			"issue_text":     "Use of eval",
			"issue_severity": "HIGH",
			"test_id":        "B307",
			"code":           code,
			"more_info":      "https://example.invalid/b307",
			"col_offset":     0,
		})
	}
	raw, err := json.Marshal(map[string]any{"errors": []any{}, "metrics": metrics, "results": results})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(raw) < 100_000 {
		t.Fatalf("fixture too small: %d bytes", len(raw))
	}
	out := compactBanditJSON(raw)
	if len(out) >= banditOutputBudget {
		t.Fatalf("compact output %d bytes, want under %d", len(out), banditOutputBudget)
	}
	if strings.Contains(string(out), `"metrics"`) || strings.Contains(string(out), `"code"`) {
		t.Fatalf("compact output kept unused fields: %.200s", out)
	}
	var got struct {
		Results        []banditFinding `json:"results"`
		OmittedResults int             `json:"omitted_results"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode compact output: %v", err)
	}
	if len(got.Results) != 50 || got.OmittedResults != 0 {
		t.Fatalf("got %d results, %d omitted; want 50, 0", len(got.Results), got.OmittedResults)
	}
	if got.Results[7].Filename != "pkg/mod007.py" || got.Results[7].LineRange[0] != 8 || got.Results[7].TestID != "B307" {
		t.Fatalf("result fields changed: %+v", got.Results[7])
	}
}

// TestCompactBanditJSONTrimsToBudget pins that an oversized report keeps
// every HIGH result and counts the lower-severity results it drops.
func TestCompactBanditJSONTrimsToBudget(t *testing.T) {
	results := make([]map[string]any, 0, 2000)
	highTotal := 0
	for i := 0; i < 2000; i++ {
		sev := "LOW"
		switch {
		case i%20 == 0:
			sev = "HIGH"
			highTotal++
		case i%3 == 0:
			sev = "MEDIUM"
		}
		results = append(results, map[string]any{
			"filename":       fmt.Sprintf("pkg/f%04d.py", i),
			"line_range":     []int{i + 1},
			"issue_text":     strings.Repeat("finding text ", 12),
			"issue_severity": sev,
			"test_id":        "B101",
		})
	}
	raw, err := json.Marshal(map[string]any{"results": results})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := compactBanditJSON(raw)
	if len(out) >= banditOutputBudget {
		t.Fatalf("compact output %d bytes, want under %d", len(out), banditOutputBudget)
	}
	var got struct {
		Results        []banditFinding `json:"results"`
		OmittedResults int             `json:"omitted_results"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode compact output: %v", err)
	}
	if got.OmittedResults <= 0 {
		t.Fatalf("omitted_results = %d, want > 0", got.OmittedResults)
	}
	if got.OmittedResults+len(got.Results) != 2000 {
		t.Fatalf("kept %d + omitted %d != 2000", len(got.Results), got.OmittedResults)
	}
	high := 0
	for _, r := range got.Results {
		if r.IssueSeverity == "HIGH" {
			high++
		}
	}
	if high != highTotal {
		t.Fatalf("kept %d HIGH findings, want all %d", high, highTotal)
	}
}

// TestCompactBanditJSONLeavesNonReportUnchanged pins that output which is
// not a Bandit report passes through, so the check reports the real error.
func TestCompactBanditJSONLeavesNonReportUnchanged(t *testing.T) {
	for _, in := range []string{
		"Traceback (most recent call last):\n  File \"bandit\", line 1\n",
		`{"results": [{"filename": "a.py", "issue_te`,
		`{"errors": []}`,
		"",
	} {
		if got := string(compactBanditJSON([]byte(in))); got != in {
			t.Fatalf("compactBanditJSON(%q) = %q, want input unchanged", in, got)
		}
	}
}
