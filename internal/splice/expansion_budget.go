package splice

// Work package D2 (warm-cost handoff Section 8): the bounded context
// expansion budget.
//
// One shared execution budget spans initial generation, format retries,
// context expansions, and local repairs for a stage invocation. It is
// threaded through runStageWithContext, runRepairStage, and
// attemptLocalRepair so every provider call spends from the same pot;
// additional context is never a fresh repair budget.
//
// Memory is NOT re-prepared per context round: one immutable admitted
// knowledge set per invocation; repair re-entry may revalidate.
// Bounds are conservative starting values (Section 8 D2) shared by both
// arms; failed formatting consumes slots; a repeated query that returns
// no new bytes terminates.

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Expansion bounds (Section 8 D2 starting values; same for both arms).
const (
	// MaxExpansionRoundsPerStage caps context expansion rounds per stage
	// per iteration.
	MaxExpansionRoundsPerStage = 2
	// MaxProviderRequestsPerStage caps provider calls per stage per
	// iteration across initial generation, format retries, expansions,
	// and local repairs.
	MaxProviderRequestsPerStage = 5
)

const ExpansionRoundsEnvVar = "SPLICE_EXPANSION_ROUNDS"

// ResolveExpansionRounds reads the run-level expansion override. An unset
// value keeps the historical two-round budget. Invalid values fail loudly.
func ResolveExpansionRounds() (int, error) {
	raw, ok := os.LookupEnv(ExpansionRoundsEnvVar)
	if !ok || strings.TrimSpace(raw) == "" {
		return MaxExpansionRoundsPerStage, nil
	}
	rounds, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || rounds < 1 || rounds > 20 {
		return 0, fmt.Errorf("%s must be an integer from 1 through 20, got %q", ExpansionRoundsEnvVar, raw)
	}
	return rounds, nil
}

// StageExecutionBudget is the shared allowance for one stage invocation.
type StageExecutionBudget struct {
	rounds      int // expansion rounds consumed
	requests    int // provider requests consumed (incl. format retries)
	maxRequests int
	maxRounds   int
}

// NewStageExecutionBudget builds the budget with the Section 8 starting
// bounds. maxRequests <= 0 selects the default ceiling.
func NewStageExecutionBudget(maxRequests int) *StageExecutionBudget {
	rounds, err := ResolveExpansionRounds()
	if err != nil {
		rounds = MaxExpansionRoundsPerStage
	}
	if maxRequests <= 0 {
		// Keep the original request ceiling for the default, and widen it with
		// each explicitly requested round so the round override cannot be
		// silently defeated by the request cap.
		maxRequests = MaxProviderRequestsPerStage + maxInt(rounds-MaxExpansionRoundsPerStage, 0)
	}
	return &StageExecutionBudget{maxRequests: maxRequests, maxRounds: rounds}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// SpendRequest records one provider request and reports whether it was
// within the allowance. A false return means the ceiling is exhausted:
// the caller stops expanding and surfaces an explicit incomplete outcome.
// A nil budget (tests, legacy callers) is unbounded: the historical
// behavior is preserved rather than failing closed mid-loop.
func (b *StageExecutionBudget) SpendRequest() bool {
	if b == nil {
		return true
	}
	b.requests++
	return b.requests <= b.maxRequests
}

// SpendRound records one expansion round and reports whether it was
// within the allowance.
func (b *StageExecutionBudget) SpendRound() bool {
	if b == nil {
		return true
	}
	b.rounds++
	return b.rounds <= b.maxRounds
}

// RequestsUsed reports the consumed provider-request slots.
func (b *StageExecutionBudget) RequestsUsed() int {
	if b == nil {
		return 0
	}
	return b.requests
}

// RoundsUsed reports the consumed expansion rounds.
func (b *StageExecutionBudget) RoundsUsed() int {
	if b == nil {
		return 0
	}
	return b.rounds
}

// Exhausted reports whether the request ceiling is spent.
func (b *StageExecutionBudget) Exhausted() bool {
	if b == nil {
		return false
	}
	return b.requests >= b.maxRequests
}

// expansionLedger dedupes expansion queries so a repeated query that
// would return no new bytes terminates predictably. Identity is the
// query signature (type+path+range+pattern+symbol+bounds): the same
// query twice is a repeat even across rounds. Initial-context queries
// are seeded so the first expansion cannot re-ask for what it has.
//
// Digests track what CONTENT was delivered, by file path: a model can vary
// the query signature (different bounds) while receiving the same bytes,
// and the M6 campaigns show the consequence - the model concludes its views
// are "elided" and spends the whole allowance re-requesting complete files
// it already holds. Digests make no-new-bytes detection content-addressed.
type expansionLedger struct {
	seen    map[string]bool
	digests map[string]string
}

func newExpansionLedger() *expansionLedger {
	return &expansionLedger{seen: map[string]bool{}, digests: map[string]string{}}
}

// NewBytes reports whether the fulfilled bundle carried content this
// invocation has not delivered before. A bundle whose every source item
// repeats an already-delivered digest delivered no new bytes.
func (l *expansionLedger) NewBytes(bundle *schemas.ContextBundle) bool {
	if l == nil || bundle == nil {
		return true
	}
	for _, item := range bundle.Items {
		if item.Error != nil {
			continue
		}
		path, _ := item.Payload["path"].(string)
		digest, _ := item.Payload["version"].(string)
		if path == "" || digest == "" {
			return true // unidentifiable content: assume new
		}
		if l.digests[path] != digest {
			return true
		}
	}
	return false
}

// RecordDigests remembers the fulfilled content by file path.
func (l *expansionLedger) RecordDigests(bundle *schemas.ContextBundle) {
	if l == nil || bundle == nil {
		return
	}
	for _, item := range bundle.Items {
		if item.Error != nil {
			continue
		}
		path, _ := item.Payload["path"].(string)
		digest, _ := item.Payload["version"].(string)
		if path != "" && digest != "" {
			l.digests[path] = digest
		}
	}
}

// Check returns an error when the request repeats a query already
// fulfilled this invocation.
func (l *expansionLedger) Check(request schemas.ContextRequest) error {
	if l == nil {
		return nil
	}
	for _, q := range request.Queries {
		key := queryIdentity(q)
		if l.seen[key] {
			return fmt.Errorf("expansion repeats a query already fulfilled this invocation (%s); no new evidence would arrive", key)
		}
	}
	return nil
}

// Record marks a request's queries as fulfilled.
func (l *expansionLedger) Record(request schemas.ContextRequest) {
	if l == nil {
		return
	}
	for _, q := range request.Queries {
		l.seen[queryIdentity(q)] = true
	}
}

func queryIdentity(q schemas.ContextQuery) string {
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	rng := ""
	if q.HasRange() {
		rng = fmt.Sprintf("#%d-%d", *q.StartLine, *q.EndLine)
	}
	return fmt.Sprintf("%s|%s%s|%s|%s|r%d|c%d", q.QueryType, deref(q.Path), rng, deref(q.Pattern), deref(q.Symbol), q.MaxResults, q.MaxChars)
}

// errExpansionBudgetExhausted is the explicit incomplete outcome when the
// shared allowance is spent. All incurred usage rides the error via the
// normal metered-stage-error path.
var errExpansionBudgetExhausted = errors.New("context expansion budget exhausted")
