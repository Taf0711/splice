package gemini

import "testing"

// xhigh and max fell through to the default budget of 0, which leaves thinking
// disabled, so requesting the most reasoning produced none.
func TestThinkingBudgetForEffortClampsTopTiers(t *testing.T) {
	for _, test := range []struct {
		effort string
		want   int
	}{
		{effort: "minimal", want: 1024},
		{effort: "low", want: 4096},
		{effort: "medium", want: 8192},
		{effort: "high", want: 24576},
		{effort: "xhigh", want: 24576},
		{effort: "max", want: 24576},
		{effort: "MAX", want: 24576},
		{effort: "none", want: 0},
		{effort: "", want: 0},
		{effort: "bogus", want: 0},
	} {
		t.Run(test.effort, func(t *testing.T) {
			if got := thinkingBudgetForEffort(test.effort); got != test.want {
				t.Fatalf("thinkingBudgetForEffort(%q) = %d, want %d", test.effort, got, test.want)
			}
		})
	}
}
