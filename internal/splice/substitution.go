package splice

// Work package E4 (warm-cost handoff Section 9): substitution and delivered
// context.
//
// The improved cold plan comes FIRST, from the same task and the
// current-source tools: explicit paths, symbol lookup, package evidence.
// The arbitrary eight-file fallback is not preserved merely to flatter warm:
// the cold plan reads what the task names plus a bounded symbol-confirmed
// slice of the production sources, and the global listing survives unless a
// justified decision drops it.
//
// For each admissible warm resolution the plan:
//  1. identifies the exact cold-plan operation it replaces,
//  2. fetches the current evidence needed to USE the answer (E3 already
//     re-hashed the bytes; the delivered view is built from those bytes),
//  3. preserves all other unresolved needs and edit prerequisites,
//  4. builds the candidate warm request with compact current source and at
//     most ONE delivered record (more only for distinct unresolved needs
//     with measured value),
//  5. records the before/after operation plan and delivered-view difference.
//
// Location-only records need no prose delivery. A supported behavioral
// constraint may justify a small note. A no-hit or rejected candidate adds
// NO memory narration to the prompt. Open needs survive regardless of the
// old CognitionResolved boolean: excluding a listing needs its own
// justification. The old append-all-hints policy survives only as a
// versioned diagnostic comparator.

import (
	"sort"
	"strings"
)

// ColdPlan is the improved cold request plan: the operations the cold run
// would perform for one stage, derived without any memory.
type ColdPlan struct {
	// Operations is the ordered operation list (reads, symbol lookups,
	// searches, the listing). Each entry is a stable operation name.
	Operations []ColdOperation
	// Justification records WHY the plan has its shape, per decision:
	// why a listing is included, why a fallback file is read, and so on.
	Justification []string
}

// ColdOperation is one discovery operation in the cold plan.
type ColdOperation struct {
	// Name is a stable operation id: "read:<path>", "symbol:<path#Sym>",
	// "search:<pattern>", "list:workspace".
	Name string
	// Kind: read, symbol, search, list.
	Kind string
	// Subject is the path, symbol, or pattern.
	Subject string
}

// buildColdPlan derives the improved cold plan from the task and the
// current-source evidence. It does NOT read memory. Explicit paths from the
// task come first, symbol-confirmed identifiers add symbol lookups, and the
// bounded production-source fallback reads only files that can plausibly be
// edit targets (it shrinks when the task names its targets explicitly).
func buildColdPlan(intent string, workspace string, priorFiles []string, maxFallback int) ColdPlan {
	plan := ColdPlan{}
	named := strictTaskPaths(intent)
	for _, p := range named {
		plan.Operations = append(plan.Operations, ColdOperation{Name: "read:" + p, Kind: "read", Subject: p})
	}
	plan.Justification = append(plan.Justification,
		"explicit task paths read directly: "+strings.Join(named, ", "))
	for _, s := range strictTaskSymbols(intent) {
		plan.Operations = append(plan.Operations, ColdOperation{Name: "symbol:" + s, Kind: "symbol", Subject: s})
	}
	// Symbol-confirmed identifiers from the task text: cold locates them
	// through the shared index (this is exactly why warm cannot claim
	// credit for the same lookup).
	ws := buildSymbolIndex(workspace)
	for _, name := range identifierCandidates(intent) {
		files := ws.lookup(name)
		if len(files) != 1 {
			continue
		}
		op := ColdOperation{Name: "symbol:" + files[0] + "#" + name, Kind: "symbol", Subject: files[0] + "#" + name}
		plan.Operations = append(plan.Operations, op)
	}
	// Prior files the stage must integrate with.
	for _, p := range priorFiles {
		if p == "" || containsOperation(plan.Operations, "read:"+p) {
			continue
		}
		plan.Operations = append(plan.Operations, ColdOperation{Name: "read:" + p, Kind: "read", Subject: p})
	}
	// Fallback production sources ONLY when the task named no targets:
	// the arbitrary 8-file flood is not preserved when the plan already
	// knows what to read.
	if len(named) == 0 && len(priorFiles) == 0 {
		for _, p := range productionGoFiles(workspace) {
			if fallbackOps(plan.Operations) >= maxFallback {
				break
			}
			plan.Operations = append(plan.Operations, ColdOperation{Name: "read:" + p, Kind: "read", Subject: p})
			plan.Justification = append(plan.Justification,
				"fallback production source read (task names no path): "+p)
		}
	} else {
		plan.Justification = append(plan.Justification,
			"fallback sources skipped: the task and prior evidence name their targets")
	}
	// The global listing: included by default; dropping it needs its own
	// recorded justification (E4 rule).
	plan.Operations = append(plan.Operations, ColdOperation{Name: "list:workspace", Kind: "list", Subject: "workspace"})
	plan.Justification = append(plan.Justification,
		"workspace listing retained: open needs survive regardless of exact-key resolution")
	sort.SliceStable(plan.Operations, func(i, j int) bool {
		// Reads of named targets first, then symbols, then reads, then the
		// listing last.
		rank := func(k string) int {
			switch k {
			case "read":
				return 0
			case "symbol":
				return 1
			case "search":
				return 2
			default:
				return 3
			}
		}
		return rank(plan.Operations[i].Kind) < rank(plan.Operations[j].Kind)
	})
	return plan
}

func containsOperation(ops []ColdOperation, name string) bool {
	for _, o := range ops {
		if o.Name == name {
			return true
		}
	}
	return false
}

func fallbackOps(ops []ColdOperation) int {
	n := 0
	for _, o := range ops {
		if o.Kind == "read" {
			n++
		}
	}
	return n
}

// WarmPlan is the candidate warm request: the cold plan with justified
// substitutions applied, plus the delivery decisions.
type WarmPlan struct {
	// Operations is the remaining operation list after substitution.
	Operations []ColdOperation
	// Substituted records the substitution per resolution: the operation
	// replaced and the record that replaced it.
	Substitutions []Substitution
	// DeliveredNotes is the bounded prose delivery: at most one relevant
	// record, and only when the record carries a supported behavioral
	// constraint. Location-only records deliver nothing.
	DeliveredNotes []string
	// DeliveredViews names the source views the warm request delivers to
	// let the model USE the answers (the current evidence for each
	// admitted resolution's subject).
	DeliveredViews []string
	// PolicyDecisions records decisions where the warm candidate has no
	// expected advantage or where exclusion needs justification.
	PolicyDecisions []string
	// UnresolvedNeeds carries the needs no admitted record answered: they
	// survive into the request plan unchanged.
	UnresolvedNeeds []ContextNeed
}

// Substitution is one recorded operation replacement.
type Substitution struct {
	NeedID        string
	ReplacedOp    string
	RecordRef     string
	RecordDigest  string
	RecordVersion string
}

// buildWarmPlan applies the admitted resolutions to the cold plan. For each
// admissible resolution it removes exactly the cold operation the record
// replaces (when present), keeps the current evidence delivery for the
// subject, and records the before/after difference. Delivery of record prose
// is bounded: at most one note, only for a supported behavioral constraint.
func buildWarmPlan(cold ColdPlan, admitted []AdmittedResolution, unresolved []ContextNeed) WarmPlan {
	warm := WarmPlan{
		Operations:      append([]ColdOperation(nil), cold.Operations...),
		UnresolvedNeeds: append([]ContextNeed(nil), unresolved...),
	}
	notesBudget := 1
	for _, res := range admitted {
		for _, opName := range res.Replaced {
			idx := indexOfOperation(warm.Operations, opName)
			if idx < 0 {
				continue
			}
			warm.Operations = append(warm.Operations[:idx], warm.Operations[idx+1:]...)
			warm.Substitutions = append(warm.Substitutions, Substitution{
				NeedID:        res.Need.ID,
				ReplacedOp:    opName,
				RecordRef:     res.Record.Identity,
				RecordDigest:  res.Record.ContentVersion,
				RecordVersion: res.Record.WorktreeIdentity,
			})
			// The subject's current evidence is delivered as a view so the
			// model can USE the located answer without re-reading the file
			// through discovery.
			if subj := viewSubject(res); subj != "" && !containsString(warm.DeliveredViews, subj) {
				warm.DeliveredViews = append(warm.DeliveredViews, subj)
			}
		}
		// Prose delivery: location-only records deliver nothing. A
		// supported behavioral constraint may justify ONE small note.
		if notesBudget > 0 && res.Record.Conclusion != "" && isBehavioralConclusion(res.Record) {
			warm.DeliveredNotes = append(warm.DeliveredNotes, res.Record.Conclusion)
			notesBudget--
		}
	}
	// Policy decisions: rejected and no-hit candidates add NO memory
	// narration. Record the decision when warm has no expected advantage.
	if len(warm.Substitutions) == 0 {
		warm.PolicyDecisions = append(warm.PolicyDecisions,
			"no admissible record: warm request equals the cold plan; no memory narration added")
	}
	for _, n := range unresolved {
		if n.Kind == NeedOpenDiscovery {
			warm.PolicyDecisions = append(warm.PolicyDecisions,
				"open-ended discovery need survives: no substitution may close it")
			break
		}
	}
	return warm
}

// viewSubject returns the file (or file#symbol) whose current view the model
// needs to use the resolution.
func viewSubject(res AdmittedResolution) string {
	if len(res.Record.Supporting) == 0 {
		return ""
	}
	s := res.Record.Supporting[0]
	if s.Symbol != "" {
		return s.Path + "#" + s.Symbol
	}
	return s.Path
}

func indexOfOperation(ops []ColdOperation, name string) int {
	for i, o := range ops {
		// The replaced operation name from replacedOperationsFor embeds the
		// kind prefix and subject; match by containment of the subject in
		// the operation name, or exact name equality.
		if o.Name == name {
			return i
		}
		if strings.HasSuffix(name, " "+o.Subject) || o.Name == subjectToOpName(name) {
			return i
		}
	}
	return -1
}

// subjectToOpName converts a replaced-operation phrase like
// "symbol lookup for a.go#F" back to the operation name "symbol:a.go#F".
func subjectToOpName(phrase string) string {
	for _, prefix := range []string{"symbol lookup for ", "read of ", "declaration lookup for ", "integration read of ", "failure diagnosis for "} {
		if rest, ok := strings.CutPrefix(phrase, prefix); ok {
			return rest
		}
	}
	return phrase
}

// planDifferences records the before/after operation plan and delivered-view
// difference for telemetry and the trace.
type planDifferences struct {
	BeforeOps []string `json:"before_ops"`
	AfterOps  []string `json:"after_ops"`
	// Eliminated lists the cold operations the warm plan structurally
	// omits. This is the measured substitution, not a retrieval hit count.
	Eliminated []string `json:"eliminated_ops"`
	// DeliveredViews lists the views delivered to use the answers.
	DeliveredViews []string `json:"delivered_views,omitempty"`
	// DeliveredNotes counts the bounded prose notes (0 or 1 today).
	DeliveredNotes int `json:"delivered_notes,omitempty"`
}

// diffPlans computes the before/after difference between the cold and warm
// plans. Omissions are structural: an operation the warm plan does not
// issue, never an inference from a retrieval hit.
func diffPlans(cold ColdPlan, warm WarmPlan) planDifferences {
	d := planDifferences{
		BeforeOps:      operationNames(cold.Operations),
		AfterOps:       operationNames(warm.Operations),
		DeliveredViews: warm.DeliveredViews,
		DeliveredNotes: len(warm.DeliveredNotes),
	}
	coldSet := map[string]bool{}
	for _, n := range d.BeforeOps {
		coldSet[n] = true
	}
	warmSet := map[string]bool{}
	for _, n := range d.AfterOps {
		warmSet[n] = true
	}
	for _, n := range d.BeforeOps {
		if !warmSet[n] {
			d.Eliminated = append(d.Eliminated, n)
		}
	}
	sort.Strings(d.Eliminated)
	return d
}

func operationNames(ops []ColdOperation) []string {
	out := make([]string, 0, len(ops))
	for _, o := range ops {
		out = append(out, o.Name)
	}
	return out
}

// DiagnosticAppendAllHints is the versioned diagnostic comparator: the OLD
// policy (append every retrieved hint to every default read) kept runnable
// so comparisons can attribute the difference. It is never the live path.
const DiagnosticAppendAllHints = "append-all-hints/v0"

// WarmRequestFromPlan converts the warm plan into the delivered request
// shape: remaining operations become queries, notes ride as the bounded
// memory narration, and unresolved needs stay attached so the model can
// still discover for them.
func WarmRequestFromPlan(warm WarmPlan) (ops []ColdOperation, notes []string, unresolved []ContextNeed) {
	return warm.Operations, warm.DeliveredNotes, warm.UnresolvedNeeds
}
