package anthropic

import "testing"

// xhigh and max are enum members this provider family is the reason for, but
// they fell through to the default budget of 0, and a zero budget leaves the
// request without thinking enabled at all. Asking for the most reasoning turned
// reasoning off.
func TestThinkingBudgetForEffortClampsTopTiers(t *testing.T) {
	for _, test := range []struct {
		effort string
		want   int
	}{
		{effort: "minimal", want: minThinkingBudget},
		{effort: "low", want: 4096},
		{effort: "medium", want: 10000},
		{effort: "high", want: 24000},
		{effort: "xhigh", want: 24000},
		{effort: "max", want: 24000},
		{effort: "XHigh", want: 24000},
		{effort: "  max  ", want: 24000},
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
