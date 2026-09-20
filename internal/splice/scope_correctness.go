package splice

// Work package W2 (warm-cost review fold): pair every host-side tool
// suppression with a correctness signal.
//
// The existing counters (SearchesSuppressed, GlobalListsSuppressed) count
// removed calls. Nothing counts a removed call that later proved necessary,
// and nothing checks that the scoped runner did not reduce correctness. The
// scope predicate is a model self-report, so a miscalibrated self-report could
// remove a needed call and still look like a win.

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// SuppressionRecorder counts the host-side tool suppressions the scoped runner
// actually made for one invocation. It is safe for concurrent use.
type SuppressionRecorder struct {
	mu     sync.Mutex
	counts map[string]int
}

// NewSuppressionRecorder returns an empty recorder.
func NewSuppressionRecorder() *SuppressionRecorder {
	return &SuppressionRecorder{counts: map[string]int{}}
}

// record counts one suppression of the named tool. A nil recorder is a no-op,
// so a runner without a recorder behaves exactly as before W2.
func (r *SuppressionRecorder) record(tool string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counts[tool]++
}

// Suppressed is the total number of suppressed calls.
func (r *SuppressionRecorder) Suppressed() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	total := 0
	for _, n := range r.counts {
		total += n
	}
	return total
}

// Counts returns a stable copy of the per-tool counts.
func (r *SuppressionRecorder) Counts() map[string]int {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.counts))
	for k, v := range r.counts {
		out[k] = v
	}
	return out
}

// ErrScopeNonInferiority names a correctness decline that coincided with host
// suppression.
var ErrScopeNonInferiority = errors.New("scope suppression non-inferiority violated")

// ScopeCorrectnessPairing pairs the suppression count with the correctness
// signal observed before and after the scoped runner. A suppressed call is
// "necessary" only when the correctness signal declined: the host omitted work
// and the run got worse. The pairing attributes the omission to the decline
// conservatively; it never claims the omission caused the decline.
type ScopeCorrectnessPairing struct {
	SuppressedCalls          int                    `json:"suppressed_calls"`
	NecessaryCallsSuppressed int                    `json:"necessary_calls_suppressed"`
	Baseline                 schemas.IterationState `json:"baseline"`
	Observed                 schemas.IterationState `json:"observed"`
	Decline                  string                 `json:"decline,omitempty"`
}

// Validate rejects a pairing whose counts are impossible.
func (p ScopeCorrectnessPairing) Validate() error {
	if p.SuppressedCalls < 0 {
		return fmt.Errorf("scope correctness pairing: suppressed calls %d must be non-negative", p.SuppressedCalls)
	}
	if p.NecessaryCallsSuppressed < 0 || p.NecessaryCallsSuppressed > p.SuppressedCalls {
		return fmt.Errorf("scope correctness pairing: necessary calls %d must be within [0,%d]", p.NecessaryCallsSuppressed, p.SuppressedCalls)
	}
	return nil
}

// CorrectnessDecline returns the first correctness signal that got worse, or
// "" when the observed state is non-inferior. It reads the correctness fields
// of the iteration state: acceptance facts passing, tests passing, and the
// three failure counters.
func (p ScopeCorrectnessPairing) CorrectnessDecline() string {
	switch {
	case p.Observed.AcceptanceFactsPassing < p.Baseline.AcceptanceFactsPassing:
		return fmt.Sprintf("acceptance facts passing fell %d -> %d", p.Baseline.AcceptanceFactsPassing, p.Observed.AcceptanceFactsPassing)
	case p.Observed.TestsPassing < p.Baseline.TestsPassing:
		return fmt.Sprintf("tests passing fell %d -> %d", p.Baseline.TestsPassing, p.Observed.TestsPassing)
	case p.Observed.AcceptanceFactsFailing > p.Baseline.AcceptanceFactsFailing:
		return fmt.Sprintf("acceptance facts failing rose %d -> %d", p.Baseline.AcceptanceFactsFailing, p.Observed.AcceptanceFactsFailing)
	case p.Observed.TestsFailing > p.Baseline.TestsFailing:
		return fmt.Sprintf("tests failing rose %d -> %d", p.Baseline.TestsFailing, p.Observed.TestsFailing)
	case p.Observed.TestsErrored > p.Baseline.TestsErrored:
		return fmt.Sprintf("tests errored rose %d -> %d", p.Baseline.TestsErrored, p.Observed.TestsErrored)
	}
	return ""
}

// CheckScopeNonInferiority pairs the recorder's suppression count with the
// correctness signal. It fails loud when a suppressed call coincided with a
// correctness decline: the run lost correctness while the host omitted work,
// so every omission is recorded as necessary. When nothing was suppressed the
// check passes and records no necessary-suppression, even if correctness
// declined, because the decline is then not a scope fault.
func CheckScopeNonInferiority(rec *SuppressionRecorder, baseline, observed schemas.IterationState) (ScopeCorrectnessPairing, error) {
	pair := ScopeCorrectnessPairing{
		SuppressedCalls: rec.Suppressed(),
		Baseline:        baseline,
		Observed:        observed,
	}
	pair.Decline = pair.CorrectnessDecline()
	if pair.Decline == "" || pair.SuppressedCalls == 0 {
		if err := pair.Validate(); err != nil {
			return ScopeCorrectnessPairing{}, err
		}
		return pair, nil
	}
	pair.NecessaryCallsSuppressed = pair.SuppressedCalls
	if err := pair.Validate(); err != nil {
		return ScopeCorrectnessPairing{}, err
	}
	return pair, fmt.Errorf("%w: %s with %d suppressed call(s)", ErrScopeNonInferiority, pair.Decline, pair.SuppressedCalls)
}
