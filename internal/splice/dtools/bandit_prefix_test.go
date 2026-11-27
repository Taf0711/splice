package dtools

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCompactBanditJSONSkipsProgressPrefix pins that progress text printed
// before the JSON report does not stop the report from compacting.
func TestCompactBanditJSONSkipsProgressPrefix(t *testing.T) {
	report := `{"results": [{"filename": "a.py", "line_range": [3], "issue_text": "Use of eval", "issue_severity": "HIGH", "test_id": "B307", "code": "x"}, {"filename": "b.py", "issue_text": "Low", "issue_severity": "LOW", "test_id": "B101"}], "metrics": {}}`
	raw := []byte("Working... 100% 0:00:45\n" + report)
	out := compactBanditJSON(raw)
	var got struct {
		Results []banditFinding `json:"results"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("compact output does not decode: %v: %s", err, out)
	}
	if len(got.Results) != 2 || got.Results[0].TestID != "B307" || got.Results[0].LineRange[0] != 3 {
		t.Fatalf("unexpected results: %+v", got.Results)
	}
	if strings.Contains(string(out), "Working") || strings.Contains(string(out), `"code"`) {
		t.Fatalf("compact output kept prefix or unused field: %s", out)
	}
}
