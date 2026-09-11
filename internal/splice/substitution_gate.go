package splice

// Work package W1 (warm-cost review fold): the expected-value gate for
// evidence substitution.
//
// The pre-fold automatic condition was a boolean: any eliminated operation
// counted as a benefit. That ignores the delivered overhead and the
// prediction hit rate. The gate below replaces it with the expected-value
// test p*W > H:
//
//	p: the measured hit rate of the selection on a labelled corpus, per need
//	   class. It is a Splice measurement.
//	W: the cost (in input tokens) of the discovery work the substitution
//	   avoids.
//	H: the delivered overhead (in input tokens): the added prompt tokens plus
//	   the validation read.
//
// The inputs are MEASURED, never assumed. A literature review reports a
// next-tool-call hit rate of 61 to 66 percent for a different model, task,
// and harness. That figure MUST NOT be used here; Splice measures its own p.
// With no measured inputs the gate is INACTIVE and admission is
// byte-identical to the pre-gate path, so an unmeasured need class can never
// select a substitution.

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
)

// SubstitutionEVInputs are the measured inputs to the expected-value gate for
// one need class.
type SubstitutionEVInputs struct {
	// NeedKind is the ContextNeed.Kind the inputs apply to.
	NeedKind string `json:"need_kind"`
	// HitRate is the measured share of selections that proved useful, in
	// [0,1]. Zero means unmeasured and selects nothing.
	HitRate float64 `json:"hit_rate"`
	// AvoidedTokens is W: the input-token cost of the discovery work the
	// substitution removes.
	AvoidedTokens float64 `json:"avoided_tokens"`
	// OverheadTokens is H: the delivered overhead, the added prompt tokens
	// plus the validation read.
	OverheadTokens float64 `json:"overhead_tokens"`
}

// ErrSubstitutionEVInputs names a malformed gate input.
var ErrSubstitutionEVInputs = errors.New("substitution ev inputs")

// Validate rejects malformed inputs. It fails loud: a NaN, a negative cost,
// or a hit rate outside [0,1] is a configuration error, never a silent
// default.
func (in SubstitutionEVInputs) Validate() error {
	if strings.TrimSpace(in.NeedKind) == "" {
		return fmt.Errorf("%w: need kind is required", ErrSubstitutionEVInputs)
	}
	if math.IsNaN(in.HitRate) || math.IsInf(in.HitRate, 0) || in.HitRate < 0 || in.HitRate > 1 {
		return fmt.Errorf("%w: %s hit rate %v must be within [0,1]", ErrSubstitutionEVInputs, in.NeedKind, in.HitRate)
	}
	if math.IsNaN(in.AvoidedTokens) || math.IsInf(in.AvoidedTokens, 0) || in.AvoidedTokens < 0 {
		return fmt.Errorf("%w: %s avoided tokens %v must be non-negative", ErrSubstitutionEVInputs, in.NeedKind, in.AvoidedTokens)
	}
	if math.IsNaN(in.OverheadTokens) || math.IsInf(in.OverheadTokens, 0) || in.OverheadTokens < 0 {
		return fmt.Errorf("%w: %s overhead tokens %v must be non-negative", ErrSubstitutionEVInputs, in.NeedKind, in.OverheadTokens)
	}
	return nil
}

// ExpectedValueAdmits applies the STRICT expected-value test: select only when
// p*W > H. Equality does not select, because a zero-margin substitution trades
// a certain cost for an expected benefit of the same size. The returned reason
// names p, W, and H so the trace records the measured inputs, not a summary.
func (in SubstitutionEVInputs) ExpectedValueAdmits() (bool, string) {
	benefit := in.HitRate * in.AvoidedTokens
	evidence := fmt.Sprintf("need=%s p=%g W=%g H=%g pW=%g", in.NeedKind, in.HitRate, in.AvoidedTokens, in.OverheadTokens, benefit)
	switch {
	case in.HitRate <= 0:
		return false, "expected-value gate: hit rate is zero or unmeasured (" + evidence + ")"
	case in.AvoidedTokens <= 0:
		return false, "expected-value gate: no avoided work (" + evidence + ")"
	case in.OverheadTokens <= 0:
		return false, "expected-value gate: overhead is zero or unmeasured (" + evidence + ")"
	case benefit <= in.OverheadTokens:
		return false, "expected-value gate: expected benefit does not exceed overhead (" + evidence + ")"
	}
	return true, "expected-value gate admitted (" + evidence + ")"
}

// run-scoped measured gate inputs. The seam mirrors the digest memo: offline
// slices and tests run with no inputs (nil), and their admission behavior is
// byte-identical to the pre-gate path.
var (
	substitutionEVMu sync.Mutex
	substitutionEVV  map[string]SubstitutionEVInputs
)

// SetSubstitutionEVInputs installs the measured gate inputs for this run,
// keyed by need kind. Pass nil to clear. Every entry is validated first: a
// malformed input fails loud instead of silently disabling the gate.
func SetSubstitutionEVInputs(inputs []SubstitutionEVInputs) error {
	next := make(map[string]SubstitutionEVInputs, len(inputs))
	for _, in := range inputs {
		if err := in.Validate(); err != nil {
			return err
		}
		next[in.NeedKind] = in
	}
	substitutionEVMu.Lock()
	defer substitutionEVMu.Unlock()
	if len(next) == 0 {
		substitutionEVV = nil
		return nil
	}
	substitutionEVV = next
	return nil
}

// substitutionEVFor returns the installed inputs for one need kind. ok=false
// means no measurement exists, so the gate is inactive for that need.
func substitutionEVFor(needKind string) (SubstitutionEVInputs, bool) {
	substitutionEVMu.Lock()
	defer substitutionEVMu.Unlock()
	if substitutionEVV == nil {
		return SubstitutionEVInputs{}, false
	}
	in, ok := substitutionEVV[needKind]
	return in, ok
}

// activeSubstitutionEVInputs returns the installed inputs in a stable order.
// An empty result means the gate is inactive everywhere.
func activeSubstitutionEVInputs() []SubstitutionEVInputs {
	substitutionEVMu.Lock()
	defer substitutionEVMu.Unlock()
	if len(substitutionEVV) == 0 {
		return nil
	}
	kinds := make([]string, 0, len(substitutionEVV))
	for k := range substitutionEVV {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	out := make([]SubstitutionEVInputs, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, substitutionEVV[k])
	}
	return out
}

// SliceEVGate records the expected-value gate inputs and decision for one
// offline slice condition. Nil means no measured inputs were installed, so the
// gate was inactive and the structural elimination is the recorded benefit.
type SliceEVGate struct {
	Inputs   []SubstitutionEVInputs `json:"inputs"`
	Admitted bool                   `json:"admitted"`
	Reasons  []string               `json:"reasons,omitempty"`
}

// evaluateSubstitutionEVGate evaluates the installed gate inputs. It returns
// nil when the gate is inactive, so the caller falls back to the structural
// elimination rule.
func evaluateSubstitutionEVGate() *SliceEVGate {
	inputs := activeSubstitutionEVInputs()
	if len(inputs) == 0 {
		return nil
	}
	gate := &SliceEVGate{Inputs: inputs}
	for _, in := range inputs {
		admitted, reason := in.ExpectedValueAdmits()
		gate.Reasons = append(gate.Reasons, reason)
		if admitted {
			gate.Admitted = true
		}
	}
	return gate
}
