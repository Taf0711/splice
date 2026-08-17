package eval

import (
	"strings"
	"testing"
)

func in(pairs, coldSucc, warmSucc, coldTok, warmTok, coldInt, warmInt int) DecisionInput {
	return DecisionInput{
		Pairs: pairs,
		Cold:  ArmStats{Successes: coldSucc, Tokens: coldTok, WeightedInterventions: coldInt},
		Warm:  ArmStats{Successes: warmSucc, Tokens: warmTok, WeightedInterventions: warmInt},
	}
}

func TestDecideEveryGate(t *testing.T) {
	cases := []struct {
		name    string
		input   DecisionInput
		verdict string
		reason  string
	}{
		{
			name:    "below floor inconclusive",
			input:   in(9, 5, 5, 1000, 800, 0, 0),
			verdict: VerdictInconclusive,
			reason:  "insufficient pairs: 9/10",
		},
		{
			name:    "warm success drop is regression",
			input:   in(10, 8, 7, 1000, 700, 0, 0),
			verdict: VerdictRegression,
			reason:  "warm successes (7) below cold (8)",
		},
		{
			name:    "cost margin not met inconclusive",
			input:   in(10, 8, 8, 1000, 920, 0, 0),
			verdict: VerdictInconclusive,
			reason:  "not cheaper than the 10% margin",
		},
		{
			name:    "needier inconclusive",
			input:   in(10, 8, 8, 1000, 600, 2, 5),
			verdict: VerdictInconclusive,
			reason:  "cheaper but needier",
		},
		{
			name:    "conclusive warm wins",
			input:   in(10, 8, 8, 1000, 600, 0, 0),
			verdict: VerdictConclusive,
			reason:  "warm wins: 40.0% fewer tokens at equal success",
		},
		{
			name:    "tie goes cold",
			input:   in(10, 8, 8, 1000, 1000, 0, 0),
			verdict: VerdictInconclusive,
			reason:  "not cheaper than the 10% margin",
		},
		{
			name:    "exactly 10 percent cheaper is inconclusive",
			input:   in(10, 10, 10, 1000, 900, 0, 0),
			verdict: VerdictInconclusive,
			reason:  "not cheaper than the 10% margin",
		},
		{
			name:    "just over 10 percent cheaper is conclusive",
			input:   in(10, 10, 10, 1000, 899, 0, 0),
			verdict: VerdictConclusive,
			reason:  "warm wins: 10.1% fewer tokens at equal success",
		},
		{
			name:    "zero success cold arm is inconclusive",
			input:   in(10, 0, 5, 1000, 500, 0, 0),
			verdict: VerdictInconclusive,
			reason:  "cold arm had 0 successes; cost comparison undefined (10 pairs)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Decide(tc.input)
			if d.Verdict != tc.verdict {
				t.Fatalf("verdict = %q, want %q", d.Verdict, tc.verdict)
			}
			if d.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", d.Reason, tc.reason)
			}
		})
	}
}

func TestDecideConclusiveReasonCarriesPercentage(t *testing.T) {
	d := Decide(in(10, 10, 10, 1000, 500, 0, 0))
	if d.Verdict != VerdictConclusive {
		t.Fatalf("verdict = %q, want conclusive", d.Verdict)
	}
	if !strings.HasPrefix(d.Reason, "warm wins: ") || !strings.HasSuffix(d.Reason, "% fewer tokens at equal success") {
		t.Fatalf("reason = %q, want percentage form", d.Reason)
	}
	if d.Reason != "warm wins: 50.0% fewer tokens at equal success" {
		t.Fatalf("reason = %q, want 50.0%%", d.Reason)
	}
}

func TestDecideGateTrail(t *testing.T) {
	d := Decide(in(10, 8, 8, 1000, 600, 0, 0))
	if len(d.Gates) != 4 {
		t.Fatalf("gates = %d, want 4", len(d.Gates))
	}
	for _, gate := range d.Gates {
		if !gate.Passed {
			t.Fatalf("gate %s unexpectedly failed: %s", gate.Name, gate.Reason)
		}
	}
}
