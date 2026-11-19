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
	"math"
	"os"
	"path/filepath"
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

// RoundsLeft reports the expansion rounds still grantable. A nil budget is
// unbounded.
func (b *StageExecutionBudget) RoundsLeft() int {
	if b == nil {
		return math.MaxInt
	}
	return clampLeft(b.maxRounds - b.rounds)
}

// RequestsLeft reports the provider-request slots still grantable. A nil
// budget is unbounded.
func (b *StageExecutionBudget) RequestsLeft() int {
	if b == nil {
		return math.MaxInt
	}
	return clampLeft(b.maxRequests - b.requests)
}

func clampLeft(n int) int {
	if n < 0 {
		return 0
	}
	return n
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

// expansionLedger tracks what content the invocation has delivered. A
// repeated query that returns no new bytes is caught by the no-new-bytes
// check, not by the query signature.
//
// Digests track what CONTENT was delivered, by file path: a model can vary
// the query signature (different bounds) while receiving the same bytes,
// and the M6 campaigns show the consequence - the model concludes its views
// are "elided" and spends the whole allowance re-requesting complete files
// it already holds. Digests make no-new-bytes detection content-addressed.
type expansionLedger struct {
	digests map[string]string
}

func newExpansionLedger() *expansionLedger {
	return &expansionLedger{digests: map[string]string{}}
}

// deliveryKey identifies one delivered view: its path and line range. Two
// ranges of one file are two deliveries (E04). A range may arrive as an int
// or, after a JSON decode, as a float64.
func deliveryKey(item schemas.ContextItem, path string) string {
	return fmt.Sprintf("%s#%d-%d", path, payloadLineNumber(item.Payload["start"]), payloadLineNumber(item.Payload["end"]))
}

// viewIsPartialView reports whether a delivered view leaves part of its file
// unseen: it was truncated, or its line range misses the first or last line.
func viewIsPartialView(item schemas.ContextItem) bool {
	if item.Truncated {
		return true
	}
	raw, _ := item.Payload["raw"].(string)
	total := 0
	if raw != "" {
		total = strings.Count(raw, "\n") + 1
	}
	start, end := payloadLineNumber(item.Payload["start"]), payloadLineNumber(item.Payload["end"])
	return start > 1 || end < total
}

// noNewBytesNote is the host note for an expansion that repeats content the
// stage already holds (R2). When every view is complete, the note says so. A
// partial view is named as partial, and it is never called complete.
func noNewBytesNote(bundle *schemas.ContextBundle) string {
	var views []string
	allComplete := true
	for _, item := range bundle.Items {
		path, _ := item.Payload["path"].(string)
		if item.Error != nil || path == "" {
			continue
		}
		if !viewIsPartialView(item) {
			views = append(views, path+" in complete current form, byte for byte")
			continue
		}
		allComplete = false
		if item.Truncated {
			views = append(views, path+" is a partial view (the read was truncated)")
		} else {
			start, end := payloadLineNumber(item.Payload["start"]), payloadLineNumber(item.Payload["end"])
			views = append(views, fmt.Sprintf("%s lines %d-%d are a partial view of the file", path, start, end))
		}
	}
	if allComplete {
		return "This expansion delivered no new content: every requested file was already delivered above in its complete current form, byte for byte. The delivered views are not truncated and contain no elided ranges. Do not request these files again; proceed with the task using the delivered evidence."
	}
	return "This expansion delivered no new content: every requested view was already delivered above. " + strings.Join(views, "; ") + ". Do not request these views again; proceed with the task using the delivered evidence."
}

// payloadLineNumber reads a line number that is an int in process or a
// float64 after a JSON decode.
func payloadLineNumber(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
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
		if l.digests[deliveryKey(item, path)] != digest {
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
			l.digests[deliveryKey(item, path)] = digest
		}
	}
}

// errExpansionBudgetExhausted is the explicit incomplete outcome when the
// shared allowance is spent. All incurred usage rides the error via the
// normal metered-stage-error path.
var errExpansionBudgetExhausted = errors.New("context expansion budget exhausted")

// maxCarriedContextPaths caps the reads one stage carries to its next
// invocation in the same run. maxCarriedReadChars is the per-read bound a
// carried read keeps: the schema's largest MaxChars.
const (
	maxCarriedContextPaths = 8
	// maxCarriedReadChars is the character bound a carried read keeps. It is
	// below the schema maximum, so one carried read cannot take the budget.
	maxCarriedReadChars = 8000
)

// carriedContextPaths keeps, per stage, the reads that recent invocations
// were granted in their expansion rounds without submitting (E01b). A run
// creates one store. The next invocation of the stage starts from those
// reads, with the granted line range and character bound. A nil store carries
// nothing. A successful invocation clears its stage's carry.
type carriedContextPaths struct {
	byStage map[string][]schemas.ContextQuery
}

func newCarriedContextPaths() *carriedContextPaths {
	return &carriedContextPaths{byStage: map[string][]schemas.ContextQuery{}}
}

// put records the reads one invocation was granted, oldest first. The
// newest reads lead, then older carried reads. The list keeps one read per
// path, at most maxCarriedContextPaths, and only paths that exist in workDir.
func (c *carriedContextPaths) put(stage, workDir string, fetched []schemas.ContextQuery) {
	if c == nil || len(fetched) == 0 {
		return
	}
	next := []schemas.ContextQuery{}
	seen := map[string]bool{}
	add := func(q schemas.ContextQuery) {
		if q.Path == nil || len(next) == maxCarriedContextPaths {
			return
		}
		path := *q.Path
		if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") || seen[path] || !fileExists(workDir, path) {
			return
		}
		seen[path] = true
		if q.MaxChars > maxCarriedReadChars {
			q.MaxChars = maxCarriedReadChars
		}
		next = append(next, q)
	}
	for i := len(fetched) - 1; i >= 0; i-- {
		add(fetched[i])
	}
	for _, q := range c.byStage[stage] {
		add(q)
	}
	c.byStage[stage] = next
}

// clear drops the carry for stage. A successful invocation calls it.
func (c *carriedContextPaths) clear(stage string) {
	if c != nil {
		delete(c.byStage, stage)
	}
}

// get returns the carried reads for stage whose paths still exist in
// workDir, most recent first. Together they stay within half the aggregate
// context bound, so the ranked reads that follow still fit.
func (c *carriedContextPaths) get(stage, workDir string) []schemas.ContextQuery {
	if c == nil {
		return nil
	}
	budget := schemas.MaxAggregateContextBytes / 2
	used := 0
	var out []schemas.ContextQuery
	for _, q := range c.byStage[stage] {
		if q.Path == nil || !fileExists(workDir, *q.Path) || used+q.MaxChars > budget {
			continue
		}
		used += q.MaxChars
		out = append(out, q)
	}
	return out
}

// requestedQueries lists the granted reads of a context request, in order.
// Listing queries and queries without a path name no file to carry.
func requestedQueries(request *schemas.ContextRequest) []schemas.ContextQuery {
	if request == nil {
		return nil
	}
	var out []schemas.ContextQuery
	for _, q := range request.Queries {
		if q.QueryType == schemas.ContextListFiles || q.Path == nil {
			continue
		}
		out = append(out, q)
	}
	return out
}

// finalContextCall reports whether no later context request can be granted
// after the next call: no expansion round or no provider request is left.
// One request left is still a grant.
func finalContextCall(budget *StageExecutionBudget) bool {
	return budget.RoundsLeft() == 0 || budget.RequestsLeft() == 0
}

// fileExists reports whether path names a regular file under workDir.
func fileExists(workDir, path string) bool {
	info, err := os.Stat(filepath.Join(workDir, path))
	return err == nil && info.Mode().IsRegular()
}
