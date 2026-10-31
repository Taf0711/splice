package openai

import (
	"encoding/json"
	"testing"
)

// Usage chunks use the shape OpenRouter sends on a streamed request with
// usage accounting on. The paid, non-BYOK chunk was captured live.
func TestReportedUsageCostFromRecordedChunks(t *testing.T) {
	cases := []struct {
		name string
		json string
		want *float64
	}{
		{
			name: "paid non-BYOK chunk reports its charge",
			json: `{"prompt_tokens":33,"completion_tokens":8,"total_tokens":41,"cost":0.0000195,"is_byok":false,"cost_details":{"upstream_inference_cost":0.0000195}}`,
			want: f(0.0000195),
		},
		{
			name: "BYOK chunk with cost 0 reports the upstream inference cost",
			json: `{"prompt_tokens":33,"completion_tokens":8,"cost":0,"is_byok":true,"cost_details":{"upstream_inference_cost":0.0000195}}`,
			want: f(0.0000195),
		},
		{
			name: "BYOK chunk without an upstream cost keeps the reported zero",
			json: `{"prompt_tokens":33,"completion_tokens":8,"cost":0,"is_byok":true}`,
			want: f(0),
		},
		{
			name: "non-BYOK zero cost is returned as reported for the ledger to judge",
			json: `{"prompt_tokens":33,"completion_tokens":8,"cost":0,"is_byok":false,"cost_details":{"upstream_inference_cost":0.5}}`,
			want: f(0),
		},
		{
			name: "chunk without a cost field reports nothing",
			json: `{"prompt_tokens":33,"completion_tokens":8}`,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var u usage
			if err := json.Unmarshal([]byte(tc.json), &u); err != nil {
				t.Fatal(err)
			}
			got := reportedUsageCost(u)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("reported cost = %v, want none", *got)
			case tc.want != nil && (got == nil || *got != *tc.want):
				t.Fatalf("reported cost = %v, want %v", got, *tc.want)
			}
		})
	}
}

func f(v float64) *float64 { return &v }

// A later usage chunk with a zero cost must not overwrite a real charge seen
// earlier in the same stream. A later non-zero charge still replaces it.
func TestKeepReportedCostOrderIndependent(t *testing.T) {
	cases := []struct {
		name     string
		previous *float64
		next     *float64
		want     *float64
	}{
		{"zero after real charge keeps the charge", f(0.000246696), f(0), f(0.000246696)},
		{"real charge replaces an earlier one", f(0.1), f(0.2), f(0.2)},
		{"absent later chunk keeps the charge", f(0.3), nil, f(0.3)},
		{"zero first is replaced by a real charge", f(0), f(0.4), f(0.4)},
		{"first chunk sets the value", nil, f(0.5), f(0.5)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := keepReportedCost(tc.previous, tc.next)
			if got == nil || *got != *tc.want {
				t.Fatalf("keepReportedCost = %v, want %v", got, *tc.want)
			}
		})
	}
}
