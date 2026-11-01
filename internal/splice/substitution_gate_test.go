package splice

import "testing"

// TestExpectedValueGateTable pins the strict p*W > H rule. Exact equality
// must not select: a zero-margin substitution trades a certain cost for an
// expected benefit of the same size.
func TestExpectedValueGateTable(t *testing.T) {
	cases := []struct {
		name string
		in   SubstitutionEVInputs
		want bool
	}{
		{
			name: "expected benefit exceeds overhead",
			in:   SubstitutionEVInputs{NeedKind: NeedLocateNamedOperation, HitRate: 0.8, AvoidedTokens: 100, OverheadTokens: 50},
			want: true,
		},
		{
			name: "exact equality does not select",
			in:   SubstitutionEVInputs{NeedKind: NeedLocateNamedOperation, HitRate: 0.5, AvoidedTokens: 100, OverheadTokens: 50},
			want: false,
		},
		{
			name: "unmeasured hit rate selects nothing",
			in:   SubstitutionEVInputs{NeedKind: NeedLocateNamedOperation, HitRate: 0, AvoidedTokens: 100, OverheadTokens: 1},
			want: false,
		},
		{
			name: "overhead larger than the avoided work",
			in:   SubstitutionEVInputs{NeedKind: NeedLocateNamedOperation, HitRate: 0.9, AvoidedTokens: 10, OverheadTokens: 100},
			want: false,
		},
		{
			name: "no avoided work selects nothing",
			in:   SubstitutionEVInputs{NeedKind: NeedLocateNamedOperation, HitRate: 1, AvoidedTokens: 0, OverheadTokens: 0},
			want: false,
		},
		{
			name: "zero or unmeasured overhead selects nothing",
			in:   SubstitutionEVInputs{NeedKind: NeedLocateNamedOperation, HitRate: 1, AvoidedTokens: 100, OverheadTokens: 0},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := tc.in.ExpectedValueAdmits()
			if got != tc.want {
				t.Fatalf("admitted = %v, want %v (%s)", got, tc.want, reason)
			}
			if reason == "" {
				t.Fatal("the gate must return a reason that names p, W, and H")
			}
		})
	}
}

// TestSubstitutionEVInputsValidateFailsLoud pins the malformed-input
// contract: a bad hit rate or a negative cost is an error, never a silent
// default.
func TestSubstitutionEVInputsValidateFailsLoud(t *testing.T) {
	bad := []SubstitutionEVInputs{
		{NeedKind: "", HitRate: 0.5, AvoidedTokens: 1, OverheadTokens: 1},
		{NeedKind: NeedLocateNamedOperation, HitRate: 1.5, AvoidedTokens: 1, OverheadTokens: 1},
		{NeedKind: NeedLocateNamedOperation, HitRate: -0.1, AvoidedTokens: 1, OverheadTokens: 1},
		{NeedKind: NeedLocateNamedOperation, HitRate: 0.5, AvoidedTokens: -1, OverheadTokens: 1},
		{NeedKind: NeedLocateNamedOperation, HitRate: 0.5, AvoidedTokens: 1, OverheadTokens: -1},
	}
	for _, in := range bad {
		if err := in.Validate(); err == nil {
			t.Fatalf("expected a validation error for %+v", in)
		}
	}
}

// TestAdmissionExpectedValueGate pins the W1 wiring through admitRecord: with
// no measured inputs the gate is inactive and admission is unchanged; with
// measured inputs the gate admits or rejects on p*W > H.
func TestAdmissionExpectedValueGate(t *testing.T) {
	clearGate := func() {
		if err := SetSubstitutionEVInputs(nil); err != nil {
			t.Fatalf("clear gate: %v", err)
		}
	}
	clearGate()
	defer clearGate()

	ws, digests := e3Workspace(t)
	rec := e3Record(digests)

	// Inactive gate: no measurement installed, so the pre-gate decision
	// stands.
	if d, why := admitRecord(t.Context(), rec, e3Need(), admissionContext{Workspace: ws}); d != AdmissionAccepted {
		t.Fatalf("inactive gate: %s (%s), want accepted", d, why)
	}

	// p*W <= H: the gate downgrades an otherwise accepted record to a hint.
	if err := SetSubstitutionEVInputs([]SubstitutionEVInputs{{
		NeedKind: NeedLocateNamedOperation, HitRate: 0.2, AvoidedTokens: 10, OverheadTokens: 5,
	}}); err != nil {
		t.Fatalf("install gate: %v", err)
	}
	if d, why := admitRecord(t.Context(), rec, e3Need(), admissionContext{Workspace: ws}); d != AdmissionHintOnly {
		t.Fatalf("gate rejected: %s (%s), want hint-only", d, why)
	}

	// p*W > H: the gate admits and admission stays accepted.
	if err := SetSubstitutionEVInputs([]SubstitutionEVInputs{{
		NeedKind: NeedLocateNamedOperation, HitRate: 0.9, AvoidedTokens: 100, OverheadTokens: 5,
	}}); err != nil {
		t.Fatalf("install gate: %v", err)
	}
	if d, why := admitRecord(t.Context(), rec, e3Need(), admissionContext{Workspace: ws}); d != AdmissionAccepted {
		t.Fatalf("gate admitted: %s (%s), want accepted", d, why)
	}
}
