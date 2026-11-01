package splice

// The bounded dependency selector (Jev M4, plan Section 8). One unresolved
// need at stage preparation, at most four eligible candidates from the
// existing discovery result, one Choice question per candidate over the
// fixed dependency_relation_v1 labels, at most one dependency group and two
// source files materialized through the existing guarded tools, delivered
// once through the existing context-request seam. Success still comes from
// normal verification; the selector only decides which source is already
// present when the model first asks.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// maxSelectorCandidates bounds the candidate set (plan 8.1).
const maxSelectorCandidates = 4

// maxSelectorFiles bounds the materialized delivery (plan 8.3: one
// dependency group, two source files).
const maxSelectorFiles = 2

// SelectorCandidate is one eligible candidate from the discovery result.
// Only the bounded fields cross the decision boundary: candidate ID, short
// claim, origin, and source anchors. The transcript and the cognition graph
// never enter a decision request.
type SelectorCandidate struct {
	Ref     string   // candidate ID: path or path#symbol anchor
	Claim   string   // short claim line, bounded
	Origin  string   // provenance: capture, graph node, static scan
	Anchors []string // file anchors this candidate vouches for
}

// selectorCriteria are the fixed dependency_relation_v1 labels (plan 8.2).
func selectorCriteria() []zeroruntime.DecisionCriterion {
	return []zeroruntime.DecisionCriterion{
		{Label: "direct_dependency", Description: "The supplied candidate identifies a concrete dependency required for this named need."},
		{Label: "background", Description: "The candidate concerns the area but does not resolve this need."},
		{Label: "irrelevant", Description: "The candidate does not concern this need."},
		{Label: "insufficient_evidence", Description: "The supplied description cannot establish the relation."},
	}
}

// eligibleCandidates applies the plan-8.1 filters: bounded count, non-empty
// ref and claim, and at least one file anchor a materialization could
// resolve. An exact symbol/path mapping bypasses Jev upstream (the caller
// never builds a snapshot for an exact hit), so every surviving candidate
// is genuinely ambiguous.
func eligibleCandidates(candidates []SelectorCandidate) []SelectorCandidate {
	out := make([]SelectorCandidate, 0, len(candidates))
	for _, c := range candidates {
		if strings.TrimSpace(c.Ref) == "" || strings.TrimSpace(c.Claim) == "" {
			continue
		}
		if len(c.Anchors) == 0 {
			continue
		}
		out = append(out, c)
		if len(out) >= maxSelectorCandidates {
			break
		}
	}
	return out
}

// questionIDFor mints a stable per-candidate question ID from the anchor.
func questionIDFor(ref string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(ref) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 60 {
		out = out[len(out)-60:]
	}
	return "dep_" + out
}

// BuildDependencyPackV1 builds the versioned question pack: one Choice
// question per candidate over the fixed four labels. The instructions name
// the exact candidate ref and the need, per plan 8.2: a question ID alone
// is not a semantic reference, and no question asks whether code is
// correct or whether a source may be read.
func BuildDependencyPackV1(need string, candidates []SelectorCandidate) []zeroruntime.DecisionQuestion {
	questions := make([]zeroruntime.DecisionQuestion, 0, len(candidates))
	for _, c := range candidates {
		questions = append(questions, zeroruntime.DecisionQuestion{
			ID:   questionIDFor(c.Ref),
			Kind: zeroruntime.DecisionKindChoice,
			Instructions: fmt.Sprintf(
				"Need: %s. Candidate under judgment: %s (claim: %s). Does this candidate identify a concrete dependency required for the need?",
				need, c.Ref, c.Claim),
			Criteria: selectorCriteria(),
		})
	}
	return questions
}

// SelectorOutcome is the selector's typed result for one snapshot.
type SelectorOutcome struct {
	// SelectedRef is the chosen candidate, empty when the answer keeps the
	// baseline path.
	SelectedRef string
	// Probability is the winning direct_dependency probability.
	Probability float64
	// Fetches are the materialization queries for the selected group (at
	// most maxSelectorFiles).
	Fetches []SelectorFetch
	// Disposition pins why the selector stopped where it did.
	Disposition schemas.DecisionDisposition
	Reason      string
	// RequestID, Model, and ChargeUSD identify the decision that
	// authorized the delivery, when one was bought.
	RequestID string
	Model     string
	ChargeUSD *float64
}

// SelectorFetch is one materialization: an anchor resolved into a guarded
// context query. SelectedRef, RequestID, Model, and ChargeUSD pin which
// provider decision authorized the fetch, so the delivery record and the
// disposition trace reconcile.
type SelectorFetch struct {
	Path        string
	Symbol      string // bare symbol when the anchor names a declaration
	IsSymbol    bool
	StartLine   int // non-zero when a bounded range is known
	EndLine     int
	SelectedRef string
	RequestID   string
	Model       string
	ChargeUSD   *float64
}

// selectorMinDirectProbability is the bounded_prefetch_v1 policy floor: a
// direct_dependency answer below this probability keeps the baseline
// discovery path (plan 8.3: insufficient evidence retains baseline). The
// value is part of the host policy version, so changing it bumps
// HostPolicyBoundedPrefetchV1.
const selectorMinDirectProbability = 0.5

// selectFromAnswers applies the host policy to the validated answers:
// highest direct_dependency probability wins, ties break lexicographically
// by ref (stable tie-breaking belongs in code, plan 8.3), and a best answer
// below the policy floor retains the baseline discovery path.
func selectFromAnswers(answers []schemas.DecisionAnswerRecord, candidates []SelectorCandidate) SelectorOutcome {
	byID := map[string]SelectorCandidate{}
	for _, c := range candidates {
		byID[questionIDFor(c.Ref)] = c
	}
	best := SelectorOutcome{Disposition: schemas.DispositionInsufficientEvidence, Reason: "no candidate established the relation"}
	bestP := -1.0
	// maxDirect is the highest direct_dependency probability over every
	// answer, chosen or not. The refusal reason reports it, so a trace
	// shows how close the answers came to the floor.
	maxDirect := 0.0
	for _, answer := range answers {
		c, ok := byID[answer.QuestionID]
		if !ok {
			continue
		}
		if p := answer.Probabilities["direct_dependency"]; p > maxDirect {
			maxDirect = p
		}
		if answer.Choice != "direct_dependency" {
			continue
		}
		p := answer.Probabilities["direct_dependency"]
		if p > bestP || (p == bestP && best.SelectedRef != "" && c.Ref < best.SelectedRef) {
			bestP = p
			best = SelectorOutcome{SelectedRef: c.Ref, Probability: p, Disposition: schemas.DispositionApplied, Reason: "selected by policy"}
		}
	}
	if best.SelectedRef == "" || best.Probability < selectorMinDirectProbability {
		// The policy floor refused: the disposition must say so, never
		// carry the stale "selected" label from the reassignment above.
		best.Disposition = schemas.DispositionInsufficientEvidence
		if best.SelectedRef == "" {
			best.Reason = fmt.Sprintf("no answer chose direct_dependency (highest direct_dependency probability %.2f)", maxDirect)
		} else {
			best.Reason = fmt.Sprintf("best direct_dependency probability %.2f is below the %.2f policy floor", best.Probability, selectorMinDirectProbability)
		}
		best.SelectedRef = ""
		best.Fetches = nil
		return best
	}
	best.Fetches = materializationFetches(best.SelectedRef, byID[questionIDFor(best.SelectedRef)])
	return best
}

// materializationFetches turns the selected candidate's anchors into at
// most maxSelectorFiles bounded fetches, preferring exact symbol anchors.
func materializationFetches(selectedRef string, c SelectorCandidate) []SelectorFetch {
	fetches := make([]SelectorFetch, 0, maxSelectorFiles)
	seen := map[string]bool{}
	add := func(anchor string) {
		if len(fetches) >= maxSelectorFiles || seen[anchor] {
			return
		}
		seen[anchor] = true
		f := SelectorFetch{}
		if idx := strings.LastIndex(anchor, "#"); idx >= 0 {
			f.Path, f.Symbol, f.IsSymbol = anchor[:idx], anchor[idx+1:], true
		} else {
			f.Path = anchor
		}
		fetches = append(fetches, f)
	}
	// The selected ref itself is an anchor when qualified.
	if strings.Contains(selectedRef, "#") {
		add(selectedRef)
	}
	for _, a := range c.Anchors {
		add(a)
	}
	return fetches
}

// ContextQueriesFor converts fetches into guarded context queries. Symbol
// anchors resolve through get_symbol (the exact-declaration path the
// delivery proofs repaired); bare file anchors read a bounded view. The
// fulfillment path is the materialization: the same guarded tools, the
// same version stamps, the same truncation honesty.
func ContextQueriesFor(fetches []SelectorFetch) []schemas.ContextQuery {
	queries := make([]schemas.ContextQuery, 0, len(fetches))
	for _, f := range fetches {
		if f.IsSymbol && f.Symbol != "" {
			qualified := f.Symbol
			path := f.Path
			queries = append(queries, schemas.ContextQuery{
				QueryType:  schemas.ContextGetSymbol,
				Path:       &path,
				Symbol:     &qualified,
				MaxResults: 1,
				MaxChars:   6000,
			})
			continue
		}
		path := f.Path
		queries = append(queries, schemas.ContextQuery{
			QueryType:  schemas.ContextReadFile,
			Path:       &path,
			MaxResults: 10,
			MaxChars:   5000,
		})
	}
	return queries
}

// CalibrationStore is the per-class calibration source both the Jev gate
// and W1's deterministic gate read (spec 6.4). Jev confidence is never
// p_useful.
type CalibrationStore interface {
	// PUseful reports the calibrated probability that a delivered source
	// for this decision class is actually used by the model, and whether a
	// calibration exists at all.
	PUseful(class string) (p float64, ok bool)
}

// gate2 allows acting on an answer. The full active mode requires the
// calibrated positive net saving with run-level economics (plan 6.4): a
// missing quantity refuses rather than defaults to permission.
// active_experimental's permission is the preregistered manifest exception:
// the calibrated p_useful and the fixed cap authorize a bounded delivery so
// the campaign can MEASURE the avoided-discovery term that a full gate
// would need. With no calibration the refusal is named either way; it
// never silently becomes permission.
func gate2(mode AdvisorMode, class string, calibration CalibrationStore, economics *schemas.EconomicAssessment) (bool, string) {
	if calibration == nil {
		return false, "uncalibrated: no calibration store wired; baseline retained"
	}
	p, ok := calibration.PUseful(class)
	if !ok {
		return false, "uncalibrated: no calibrated p_useful for the class; baseline retained"
	}
	if mode == AdvisorModeActiveExperimental && economics == nil {
		return true, "gate2: preregistered experiment exception (calibrated p_useful, bounded delivery); economics measured by the campaign"
	}
	if economics == nil || economics.ActionCostUSD == nil {
		return false, "action cost absent; refusing to default to permission"
	}
	// expected net saving = p_useful * avoided discovery - action cost.
	// The avoided-discovery term uses the economic assessment's baseline
	// continuation when present, else zero (conservative: only a
	// demonstrated baseline cost can justify the action).
	avoided := 0.0
	if economics.BaselineContinuationHighUSD != nil {
		avoided = *economics.BaselineContinuationHighUSD
	}
	net := p*avoided - *economics.ActionCostUSD
	if net <= 0 {
		return false, fmt.Sprintf("gate2: expected net saving %.8f is not positive", net)
	}
	return true, "gate2: calibrated net saving positive"
}

// runSelector is the M4 orchestration over one prepared snapshot: the pack
// asks one Choice question per candidate, gate 1 buys the decision (or
// names why not), the policy selects with stable tie-breaks, and gate 2
// decides whether the delivery proceeds. Everything is serial and
// synchronous; the advisor records the full disposition trace. Shadow mode
// records the bought decision and never delivers.
func (a *DecisionAdvisor) runSelector(ctx context.Context, snap schemas.SemanticSnapshot, candidates []SelectorCandidate, calibration CalibrationStore, economics *schemas.EconomicAssessment) (SelectorOutcome, error) {
	out := SelectorOutcome{Disposition: schemas.DispositionInsufficientEvidence, Reason: "selector not run"}
	if a == nil {
		return out, nil
	}
	eligible := eligibleCandidates(candidates)
	if len(eligible) == 0 {
		out.Disposition = schemas.DispositionInsufficientEvidence
		out.Reason = "no eligible candidates after the plan-8.1 filters"
		return out, nil
	}
	questions := BuildDependencyPackV1(snap.UnresolvedRelation, eligible)
	record, bought := a.EvaluateGate1WithQuestions(ctx, snap, economics, questions)
	if !bought {
		out.Disposition = record.Disposition
		out.Reason = record.Reason
		return out, nil
	}
	if record.Assessment == nil || len(record.Assessment.Answers) == 0 {
		out.Disposition = schemas.DispositionProviderFailure
		out.Reason = "gate1 bought the decision but no answers survived"
		return out, nil
	}
	selection := selectFromAnswers(record.Assessment.Answers, eligible)
	if selection.SelectedRef == "" {
		// The purchase is sunk and the policy kept the baseline: record the
		// SELECTION outcome as its own disposition, so the trace
		// distinguishes a bought-and-delivered decision from a bought-and-
		// refused one. The powered campaign's telemetry conflated the two
		// (gate-1 "applied" lines with zero deliveries) and obscured the
		// engagement bottleneck.
		out.Disposition = selection.Disposition
		out.Reason = "gate1 bought; " + selection.Reason
		a.records = append(a.records, schemas.DecisionDispositionRecord{
			Disposition:   out.Disposition,
			Reason:        out.Reason,
			Snapshot:      snap,
			Assessment:    record.Assessment,
			RequestID:     record.Assessment.RequestID,
			RecordedAtUTC: a.now().Format(time.RFC3339),
		})
		return out, nil
	}
	// The winning decision's identity rides the outcome.
	selection.RequestID = record.Assessment.RequestID
	selection.Model = record.Assessment.ModelVersion
	for i := range selection.Fetches {
		selection.Fetches[i].RequestID = record.Assessment.RequestID
		selection.Fetches[i].Model = record.Assessment.ModelVersion
	}
	// Gate 2: act only under the mode's permission rule. A refusal
	// delivers nothing: the fetches were never approved.
	allow, reason := gate2(a.config.Mode, "dependency_selection", calibration, economics)
	if !allow {
		selection.Fetches = nil
		selection.Disposition = schemas.DispositionUncalibrated
		selection.Reason = reason
		return selection, nil
	}
	selection.Disposition = schemas.DispositionApplied
	selection.Reason = reason
	return selection, nil
}

// selectorCandidatesFromNodes turns the fresh discovery nodes into selector
// candidates: one candidate per anchored declaration, never one per node. A
// capture node for a file anchors every declaration in that file, so a
// per-node candidate hid all but one of them behind an arbitrary ref (the
// sidecar returns anchors sorted, so the M6 campaigns asked about
// main.go#healthHandler for almost every need).
//
// Declarations the intent names are the deterministic path's job (plan 8.1:
// an exact mapping bypasses Jev); they are returned in named and never asked
// about. The rest rank by how many of their identifier words appear in the
// intent, then by ref, and the top maxSelectorCandidates survive. A node with
// no symbol anchor contributes one file-level candidate.
func selectorCandidatesFromNodes(intent string, nodes []memd.GraphNode) (candidates []SelectorCandidate, named []string) {
	words := intentWords(intent)
	type ranked struct {
		c     SelectorCandidate
		score int
	}
	var pool []ranked
	seen := map[string]bool{}
	for _, n := range nodes {
		claim := strings.TrimSpace(n.Claim)
		hasSymbol := false
		for _, a := range n.Anchors {
			if a.Kind != "symbol" {
				continue
			}
			idx := strings.LastIndex(a.Value, "#")
			if idx <= 0 || idx == len(a.Value)-1 {
				continue
			}
			hasSymbol = true
			if seen[a.Value] {
				continue
			}
			seen[a.Value] = true
			file, decl := a.Value[:idx], a.Value[idx+1:]
			// "Store.Get" is named by its method word, "Store" by itself.
			bare := decl[strings.LastIndex(decl, ".")+1:]
			if words.exact[bare] {
				named = append(named, a.Value)
				continue
			}
			pool = append(pool, ranked{
				c: SelectorCandidate{
					Ref:     a.Value,
					Claim:   truncateClaim(fmt.Sprintf("%s is declared in %s. File fact: %s", decl, file, claim), 200),
					Origin:  "graph:" + n.Kind,
					Anchors: []string{a.Value},
				},
				score: words.overlap(decl),
			})
		}
		if hasSymbol {
			continue
		}
		for _, a := range n.Anchors {
			if a.Kind == "file" && a.Value != "" && !seen[a.Value] {
				seen[a.Value] = true
				pool = append(pool, ranked{
					c:     SelectorCandidate{Ref: a.Value, Claim: truncateClaim(claim, 200), Origin: "graph:" + n.Kind, Anchors: []string{a.Value}},
					score: words.overlap(a.Value),
				})
				break
			}
		}
	}
	// ponytail: word-overlap ranking only picks which four declarations Jev
	// sees; Jev makes the choice. A file with many unnamed declarations and
	// no word overlap falls back to ref order. Rank by a repo-map relevance
	// score if that truncation starts to drop the real dependency.
	sort.SliceStable(pool, func(i, j int) bool {
		if pool[i].score != pool[j].score {
			return pool[i].score > pool[j].score
		}
		return pool[i].c.Ref < pool[j].c.Ref
	})
	for _, r := range pool {
		if len(candidates) == maxSelectorCandidates {
			break
		}
		candidates = append(candidates, r.c)
	}
	sort.Strings(named)
	return candidates, named
}

// intentWordSet holds the intent's identifiers (case-sensitive, for exact
// naming) and its lower-case word stems (for ranking).
type intentWordSet struct {
	exact map[string]bool
	stems map[string]bool
}

func intentWords(intent string) intentWordSet {
	set := intentWordSet{exact: map[string]bool{}, stems: map[string]bool{}}
	for _, w := range strings.FieldsFunc(intent, func(r rune) bool {
		return !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) {
		set.exact[w] = true
		for _, part := range identWords(w) {
			set.stems[wordStem(part)] = true
		}
	}
	return set
}

// overlap counts the identifier words of ref that the intent mentions.
func (s intentWordSet) overlap(ref string) int {
	n := 0
	for _, w := range identWords(ref) {
		if s.stems[wordStem(w)] {
			n++
		}
	}
	return n
}

// identWords splits an identifier or path into lower-case words at
// non-letters and camelCase boundaries: "adminSessionsHandler" gives
// admin, sessions, handler. Words shorter than three letters carry no
// signal and are dropped.
func identWords(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) >= 3 {
			out = append(out, strings.ToLower(string(cur)))
		}
		cur = cur[:0]
	}
	runes := []rune(s)
	for i, r := range runes {
		isUpper := r >= 'A' && r <= 'Z'
		isLetter := isUpper || r >= 'a' && r <= 'z'
		if !isLetter {
			flush()
			continue
		}
		// A new word starts at an upper-case letter that follows a
		// lower-case one, or that starts the last word of an acronym run
		// ("JSONWriter" gives json, writer).
		if isUpper && len(cur) > 0 {
			prevLower := runes[i-1] >= 'a' && runes[i-1] <= 'z'
			nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
			if prevLower || nextLower {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

// wordStem folds a plural "s" so "sessions" matches "session".
func wordStem(w string) string {
	if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
		return w[:len(w)-1]
	}
	return w
}

// recordExactBypass records that every candidate declaration was named by
// the intent, so no decision was bought. Without the record, the trace
// could not tell this case from "no fresh cognition".
func (a *DecisionAdvisor) recordExactBypass(snap schemas.SemanticSnapshot, named []string) {
	if a == nil {
		return
	}
	a.records = append(a.records, schemas.DecisionDispositionRecord{
		Disposition:   schemas.DispositionExactBypass,
		Reason:        "every anchored declaration is named by the intent: " + strings.Join(named, ", "),
		Snapshot:      snap,
		RecordedAtUTC: a.now().Format(time.RFC3339),
	})
}
