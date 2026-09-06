package splice

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/cognition"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Track C integration: cognition-driven discovery planning. The planner
// resolves task questions from the cognition graph (exact anchor retrieval
// first, semantic fallback second) so the stage executes only the UNRESOLVED
// part of its context needs. It never runs a model call; everything here is
// deterministic host work over the sidecar graph. A sidecar failure degrades
// the stage to its ordinary search path, never fails the run.

// DiscoveryPlan records which parts of a stage's context need are already
// answered and which require discovery. It is the unit the telemetry
// counters measure: resolved_by_cognition items are work the run skipped.
type DiscoveryPlan struct {
	// ResolvedByTask lists questions the task prompt itself answers.
	ResolvedByTask []string `json:"resolved_by_task,omitempty"`
	// ResolvedByCognition lists questions a cognition node answers, paired
	// with the node id that answered them for explainability.
	ResolvedByCognition []ResolvedQuestion `json:"resolved_by_cognition,omitempty"`
	// Unresolved lists questions requiring discovery (reads/searches).
	Unresolved []string `json:"unresolved,omitempty"`
	// AnchorsValidated counts file/symbol anchors whose freshness check
	// passed for admitted nodes. AnchorsFailed counts rejected ones.
	AnchorsValidated int `json:"anchors_validated,omitempty"`
	AnchorsFailed    int `json:"anchors_failed,omitempty"`
	// SemanticHits counts entry nodes found through the semantic index when
	// no exact anchor was derivable. Zero unless the semantic path ran.
	SemanticHits int `json:"semantic_hits,omitempty"`
	// SemanticResolved marks that ResolvedByCognition came from the
	// semantic fallback rather than exact anchor matches. Semantic
	// candidates prioritize context but do NOT authorize scope narrowing:
	// the planner never enumerated the full set of needs, so a fresh fact
	// about one package does not answer how another package exposes what
	// the target requires.
	SemanticResolved bool `json:"semantic_resolved,omitempty"`
}

// ResolvedQuestion pairs a resolved question with its cognition source.
type ResolvedQuestion struct {
	Question string `json:"question"`
	NodeID   int64  `json:"node_id"`
	NodeKind string `json:"node_kind"`
	Claim    string `json:"claim"`
}

// discoveryQuestion describes one thing a stage would otherwise discover by
// reading or searching the repository.
type discoveryQuestion struct {
	question    string
	anchorKind  string
	anchorValue string
}

// planDiscovery builds a DiscoveryPlan for one stage invocation.
//
// Question sources (deterministic, no model involvement):
//
//  1. the stage's derived cognition keys, as discovery questions: a file key
//     asks "what lives in <path>", a symbol key asks "where is <symbol>".
//     Keys the task intent itself names are resolved_by_task; the rest go to
//     exact anchor retrieval against the graph;
//  2. when NO keys derive (the common cross-task case: the new task does not
//     name the old work's files), one architecture question falls back to the
//     semantic index over the intent, and the returned entry nodes expand one
//     bounded BFS hop for related facts.
//
// Every candidate node passes structural freshness validation (its file
// anchors diffed against its verified revision) before it may resolve a
// question: fresh nodes resolve, stale nodes count in AnchorsFailed and the
// question stays unresolved. client may be nil (cognition off): everything
// stays unresolved and the plan is still returned so callers emit consistent
// telemetry.
//
// The returned nodes slice is the admitted, fresh cognition for the stage,
// ready for cognitionBundleFromNodes.
func planDiscovery(ctx context.Context, client *memd.Client, projectPath, intent string, keys []string) (DiscoveryPlan, []memd.GraphNode) {
	plan := DiscoveryPlan{}
	if client == nil {
		return plan, nil
	}
	fresh := map[int64]memd.GraphNode{}

	for _, key := range keys {
		anchorKind, anchorValue, ok := anchorForKey(key)
		if !ok {
			continue
		}
		question := discoveryQuestionText(key)
		nodes, err := client.GetExactNodes(ctx, map[string][]string{anchorKind: {anchorValue}}, projectPath, 4)
		if err != nil || len(nodes) == 0 {
			plan.Unresolved = append(plan.Unresolved, question)
			continue
		}
		resolved, failed, okNodes := admitFreshNodes(ctx, projectPath, nodes)
		plan.AnchorsValidated += resolved
		plan.AnchorsFailed += failed
		if resolved == 0 {
			plan.Unresolved = append(plan.Unresolved, question)
			continue
		}
		// Cite the first FRESH node: a stale node must never be the
		// explainability source for a resolved question.
		cited := okNodes[0]
		for _, n := range okNodes {
			if _, ok := fresh[n.ID]; !ok {
				fresh[n.ID] = n
			}
		}
		plan.ResolvedByCognition = append(plan.ResolvedByCognition, ResolvedQuestion{
			Question: question,
			NodeID:   cited.ID,
			NodeKind: cited.Kind,
			Claim:    cited.Claim,
		})
	}

	// Semantic fallback: only when no exact question produced cognition. The
	// intent text ranks against node claims plus anchor values in the
	// sidecar's local hashed n-gram index; entry nodes expand one bounded
	// hop so related facts ride along.
	if len(plan.ResolvedByCognition) == 0 && len(plan.Unresolved) == 0 && intent != "" {
		hits, err := client.SearchGraphSemanticallyScoped(ctx, intent, 4, projectPath)
		if err != nil || len(hits) == 0 {
			return plan, bundleNodes(fresh)
		}
		var entry []memd.GraphNode
		for _, h := range hits {
			if h.Node != nil {
				entry = append(entry, *h.Node)
			}
		}
		if len(entry) == 0 {
			return plan, bundleNodes(fresh)
		}
		plan.SemanticHits = len(entry)
		plan.SemanticResolved = true
		resolved, failed, okNodes := admitFreshNodes(ctx, projectPath, entry)
		plan.AnchorsValidated += resolved
		plan.AnchorsFailed += failed
		kept := 0
		for _, n := range okNodes {
			if _, ok := fresh[n.ID]; !ok {
				fresh[n.ID] = n
			}
			kept++
			plan.ResolvedByCognition = append(plan.ResolvedByCognition, ResolvedQuestion{
				Question: "resolve architecture for the request intent",
				NodeID:   n.ID,
				NodeKind: n.Kind,
				Claim:    n.Claim,
			})
			neighbors, _, nerr := client.GetNeighbors(ctx, n.ID, nil, 1, 4)
			if nerr == nil {
				nResolved, nFailed, okNeighbors := admitFreshNodes(ctx, projectPath, neighbors)
				plan.AnchorsValidated += nResolved
				plan.AnchorsFailed += nFailed
				for _, nb := range okNeighbors {
					if _, ok := fresh[nb.ID]; !ok {
						fresh[nb.ID] = nb
					}
				}
			}
		}
		if kept == 0 {
			plan.SemanticHits = 0
			plan.ResolvedByCognition = nil
		}
	}

	out := bundleNodes(fresh)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return plan, out
}

// planStageDiscovery derives the discovery questions for one stage from its
// deterministic context and runs planDiscovery against the graph client the
// MemoryStore carries (nil client, cognition off, or a non-graph store: the
// plan stays empty and everything falls through to the ordinary paths).
func planStageDiscovery(ctx context.Context, p stageInputPreparation, input schemas.HarnessStageInput, root string) (DiscoveryPlan, []memd.GraphNode) {
	type graphProvider interface{ GraphClient() *memd.Client }
	provider, ok := p.Memory.(graphProvider)
	if !ok || provider == nil {
		return DiscoveryPlan{}, nil
	}
	client := provider.GraphClient()
	if client == nil {
		return DiscoveryPlan{}, nil
	}
	keys := cognition.DeriveKeys(cognition.DeriveInput{
		RequestIntent:        input.RequestIntent,
		PriorChangedFiles:    input.PriorChangedFiles,
		VerificationCommands: acceptanceFactCommands(input.AcceptanceFacts),
	})
	return planDiscovery(ctx, client, root, input.RequestIntent, keys)
}

// bundleNodes materializes the fresh-node set as a deterministic slice.
func bundleNodes(fresh map[int64]memd.GraphNode) []memd.GraphNode {
	if len(fresh) == 0 {
		return nil
	}
	out := make([]memd.GraphNode, 0, len(fresh))
	for _, n := range fresh {
		out = append(out, n)
	}
	return out
}

// admitFreshNodes classifies nodes' file anchors against their verified
// revisions and returns (validated, failed) counts. Stale or unknown-freshness
// nodes fail closed: they are excluded by the caller through the fresh set.
// Freshness is structural: one porcelain diff per unique verified revision
// through the shared C1b batch machinery, never "repo changed, reject all".
func admitFreshNodes(ctx context.Context, projectPath string, nodes []memd.GraphNode) (int, int, []memd.GraphNode) {
	validated, failed := 0, 0
	var okNodes []memd.GraphNode
	cache := map[string]map[string]bool{}
	for _, n := range nodes {
		if n.Status != "active" {
			continue
		}
		rev := ""
		if n.VerifiedRevision != nil {
			rev = *n.VerifiedRevision
		}
		if rev == "" {
			// No provenance: freshness cannot be proved, fail closed.
			failed++
			continue
		}
		paths := nodeFileAnchors(n)
		if len(paths) == 0 {
			// No file anchor to diff: the node cannot prove freshness.
			failed++
			continue
		}
		changed, ok := cache[rev]
		if !ok {
			revPaths, err := cognition.ChangedPaths(ctx, projectPath, rev, nil)
			if err != nil {
				failed++
				cache[rev] = nil
				continue
			}
			changed = revPaths
			cache[rev] = changed
		}
		if changed == nil {
			failed++
			continue
		}
		anchorFresh := true
		for _, p := range paths {
			if cognition.ClassifyBatch(p, changed) != cognition.FreshnessFresh {
				anchorFresh = false
				break
			}
		}
		if anchorFresh {
			validated++
			okNodes = append(okNodes, n)
		} else {
			failed++
		}
	}
	return validated, failed, okNodes
}

// nodeFileAnchors returns the file anchor values on one node.
func nodeFileAnchors(n memd.GraphNode) []string {
	var paths []string
	for _, a := range n.Anchors {
		if a.Kind == "file" && a.Value != "" {
			paths = append(paths, a.Value)
		}
	}
	return paths
}

// anchorForKey converts one derived cognition key into an exact-anchor query.
// Keys already carry their anchor inline: file:<path>, symbol:<path>#Sym,
// package:<pkg>.
func anchorForKey(key string) (kind, value string, ok bool) {
	switch {
	case strings.HasPrefix(key, cognition.KeyFile):
		return "file", strings.TrimPrefix(key, cognition.KeyFile), true
	case strings.HasPrefix(key, cognition.KeySymbol):
		return "symbol", strings.TrimPrefix(key, cognition.KeySymbol), true
	case strings.HasPrefix(key, cognition.KeyPackage):
		return "package", strings.TrimPrefix(key, cognition.KeyPackage), true
	}
	return "", "", false
}

// discoveryQuestionText renders the human-readable discovery question one
// derived key stands for. These strings land in telemetry and progress lines.
func discoveryQuestionText(key string) string {
	switch {
	case strings.HasPrefix(key, cognition.KeyFile):
		return "locate the implementation file " + strings.TrimPrefix(key, cognition.KeyFile)
	case strings.HasPrefix(key, cognition.KeySymbol):
		return "locate the definition of " + strings.TrimPrefix(key, cognition.KeySymbol)
	case strings.HasPrefix(key, cognition.KeyPackage):
		return "locate the package " + strings.TrimPrefix(key, cognition.KeyPackage)
	}
	return "resolve " + key
}

// cognitionBundleFromNodes converts graph nodes into memory observations
// delivered to a stage through the existing MemoryBundle channel. Only
// ACTIVE nodes are converted; freshness validation happens upstream. Facts
// stay one line each: typed title plus the node claim.
func cognitionBundleFromNodes(nodes []memd.GraphNode) []schemas.MemoryObservation {
	out := make([]schemas.MemoryObservation, 0, len(nodes))
	for _, n := range nodes {
		if n.Status != "active" {
			continue
		}
		project := ""
		if n.ProjectPath != nil {
			project = *n.ProjectPath
		}
		id := n.ID
		obs := schemas.MemoryObservation{
			ID:          id,
			ProjectPath: &project,
			Scope:       schemas.MemoryScopeProject,
			OwnerAgent:  "cognition_graph",
			Visibility:  "shareable",
			MemoryType:  nodeKindToMemoryType(n.Kind),
			Title:       nodeTitle(n),
			Content:     n.Claim,
		}
		if n.VerifiedRevision != nil && *n.VerifiedRevision != "" {
			rev := *n.VerifiedRevision
			obs.SourceCommit = &rev
		}
		if n.VerifiedAt != nil {
			obs.UpdatedAt = *n.VerifiedAt
		}
		out = append(out, obs)
	}
	return out
}

func nodeKindToMemoryType(kind string) string {
	switch kind {
	case "failure":
		return "failure"
	case "procedure":
		return "procedure"
	case "decision":
		return "decision"
	case "evidence":
		return "evidence"
	default:
		return "pattern"
	}
}

func nodeTitle(n memd.GraphNode) string {
	kind := strings.ToUpper(strings.TrimSpace(n.Kind))
	if kind == "" {
		kind = "COGNITION"
	}
	return fmt.Sprintf("[%s] %s", kind, firstLine(n.Claim))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return strings.TrimSpace(s)
}

// GraphCapture is one verified cognition node derived from a completed run,
// ready to persist through the sidecar. Capture is evidence-gated: the run
// must have completed verification before a FACT node is proposed.
type GraphCapture struct {
	Kind     string
	Claim    string
	Project  string
	RunID    string
	Revision string
	Anchors  []memd.GraphAnchor
	Evidence []memd.GraphEvidence
}

// captureFromVerifiedRun derives candidate cognition from a completed
// pipeline run's deterministic artifacts:
//
//  1. a verified procedure (the test command that passed), anchored on the
//     allowed "test" anchor kind so exact retrieval can find it;
//  2. one fact per changed file: where the file lives and which symbols it
//     declared at the verified revision. Symbols come from go/parser over
//     the changed Go files, so the claim is deterministic host evidence,
//     never model prose;
//  3. each fact carries a "file" anchor (exact retrieval) plus a "symbol"
//     anchor per declared top-level function or method.
//
// The revision argument is the worktree snapshot revision the verified tree
// state was captured at (git stash create), so a later freshness diff
// compares against the exact bytes the run verified. It produces nothing
// when verification did not complete.
func captureFromVerifiedRun(projectPath, outcomeStatus string, changedFiles []string, testCommand, revision, runID string) []GraphCapture {
	if outcomeStatus != "completed" {
		return nil
	}
	var captures []GraphCapture
	if testCommand != "" {
		captures = append(captures, GraphCapture{
			Kind:     "procedure",
			Claim:    fmt.Sprintf("Verification passes with: %s", testCommand),
			Project:  projectPath,
			RunID:    runID,
			Revision: revision,
			Anchors:  []memd.GraphAnchor{{Kind: "test", Value: testCommand}},
			Evidence: []memd.GraphEvidence{{Kind: "test_run", Ref: runID, Detail: "test command exited 0"}},
		})
	}
	for _, file := range changedFiles {
		if file == "" {
			continue
		}
		anchors := []memd.GraphAnchor{{Kind: "file", Value: file}}
		claim := fmt.Sprintf("%s was modified by a verified run at revision %s", file, shortRev(revision))
		if symbols := goFileSymbols(projectPath, file); len(symbols) > 0 {
			claim = fmt.Sprintf("%s defines %s; verified at revision %s", file, strings.Join(symbols, ", "), shortRev(revision))
			// Cap symbol anchors per file so one huge file cannot flood the
			// exact index; the file anchor stays authoritative for retrieval.
			if len(symbols) > maxCaptureSymbolsPerFile {
				symbols = symbols[:maxCaptureSymbolsPerFile]
			}
			for _, sym := range symbols {
				anchors = append(anchors, memd.GraphAnchor{Kind: "symbol", Value: file + "#" + sym})
			}
		}
		captures = append(captures, GraphCapture{
			Kind:     "fact",
			Claim:    claim,
			Project:  projectPath,
			RunID:    runID,
			Revision: revision,
			Anchors:  anchors,
			Evidence: []memd.GraphEvidence{{Kind: "git", Ref: revision, Detail: "verified run changed this file"}},
		})
	}
	return captures
}

// maxCaptureSymbolsPerFile bounds the symbol anchors one captured file emits.
const maxCaptureSymbolsPerFile = 12

// goFileSymbols parses one Go file under workspace and returns its top-level
// function and method names in source order. A parse failure returns nil:
// capture enrichment is best-effort and never blocks the run.
func goFileSymbols(workspace, relPath string) []string {
	if workspace == "" || relPath == "" || !strings.HasSuffix(relPath, ".go") {
		return nil
	}
	path := filepath.Join(workspace, relPath)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil
	}
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			if recv := receiverTypeNameString(fn.Recv.List[0].Type); recv != "" {
				name = recv + "." + name
			}
		}
		names = append(names, name)
	}
	return names
}

// receiverTypeNameString renders a receiver type expression as its identifier
// (including a pointer receiver's identifier), or "" when unresolved.
func receiverTypeNameString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeNameString(t.X)
	default:
		return ""
	}
}

// persistGraphCapture upserts one capture with its anchors and evidence via
// the sidecar client. Best-effort at the call site: a sidecar failure must
// degrade the run to cold, never fail it.
func persistGraphCapture(ctx context.Context, client *memd.Client, c GraphCapture) (int64, error) {
	if client == nil {
		return 0, nil
	}
	node, err := client.UpsertGraphNode(ctx, memd.GraphUpsertInput{
		Kind:             c.Kind,
		Claim:            c.Claim,
		Scope:            "project",
		ProjectPath:      c.Project,
		Status:           "active",
		SourceRunID:      c.RunID,
		VerifiedRevision: c.Revision,
		Anchors:          c.Anchors,
		Evidence:         c.Evidence,
	})
	if err != nil {
		return 0, err
	}
	return node.ID, nil
}

func shortRev(rev string) string {
	if len(rev) > 10 {
		return rev[:10]
	}
	return rev
}

// scopedReadMaxChars bounds one cognition-scoped file read. Same budget the
// default fallback reads use, so scoped reads are never smaller than what
// the cold path would have granted.
const scopedReadMaxChars = 5000

// StageScopePlan is the host-side bridge from admitted cognition to
// repository context acquisition. It is deterministic, derived only from
// freshness-validated cognition and the stage's deterministic context, and
// it governs TWO channels: what the model knows (MemoryBundle, existing)
// and what repository discovery the host performs (ContextRequest scoping,
// new). A zero-value plan means "no cognition privilege": callers must fall
// back to the ordinary default context request byte-identically.
type StageScopePlan struct {
	// CognitionResolved is true only when at least one question was
	// resolved by a fresh, validated cognition node.
	CognitionResolved bool
	// KnownFiles are repo-relative files named by fresh file anchors,
	// deduplicated and sorted.
	KnownFiles []string
	// KnownSymbols are path#Symbol names from fresh symbol anchors,
	// deduplicated and sorted.
	KnownSymbols []string
	// UnresolvedQuestions carries the questions the plan could not answer;
	// targeted discovery for these stays allowed.
	UnresolvedQuestions []string
	// AllowGlobalList permits a workspace-wide file listing. True only when
	// nothing was resolved by cognition.
	AllowGlobalList bool
	// AllowGlobalSearch permits workspace-wide pattern search. True only
	// when unresolved questions exist that need it; false after a full
	// cognition resolution so redundant searches are suppressed.
	AllowGlobalSearch bool
	// ExpansionBudget is the remaining number of bounded scope expansions
	// (new deterministic evidence grants one targeted lookup each).
	ExpansionBudget int
}

// scopePlanFor derives the scope plan from a DiscoveryPlan and the fresh
// nodes behind it. planNodes must be exactly the admitted fresh nodes
// planDiscovery returned; anchors come from those nodes only.
func scopePlanFor(plan DiscoveryPlan, planNodes []memd.GraphNode, priorScope *StageScopePlan) StageScopePlan {
	scope := StageScopePlan{
		ExpansionBudget: 2,
	}
	for _, q := range plan.Unresolved {
		scope.UnresolvedQuestions = append(scope.UnresolvedQuestions, q)
	}
	// Repair re-entry inherits the prior scope's granted files so a
	// re-planning stage does not lose privileges it already earned.
	if priorScope != nil {
		scope.KnownFiles = append(scope.KnownFiles, priorScope.KnownFiles...)
		scope.KnownSymbols = append(scope.KnownSymbols, priorScope.KnownSymbols...)
		if priorScope.ExpansionBudget < scope.ExpansionBudget {
			scope.ExpansionBudget = priorScope.ExpansionBudget
		}
	}
	for _, n := range planNodes {
		if n.Status != "active" {
			continue
		}
		for _, a := range n.Anchors {
			switch a.Kind {
			case "file":
				if a.Value != "" {
					scope.KnownFiles = append(scope.KnownFiles, a.Value)
				}
			case "symbol":
				if a.Value != "" {
					scope.KnownSymbols = append(scope.KnownSymbols, a.Value)
				}
			}
		}
	}
	sort.Strings(scope.KnownFiles)
	scope.KnownFiles = dedupeStrings(scope.KnownFiles)
	sort.Strings(scope.KnownSymbols)
	scope.KnownSymbols = dedupeStrings(scope.KnownSymbols)

	// Resolution authority: ONLY exact-anchor resolutions (a concrete
	// question matched to concrete evidence) authorize narrowing. Semantic
	// candidates deliver as knowledge and prioritize context, but the
	// planner never enumerated the full set of needs, so an empty
	// unresolved list from the semantic path must NOT mean complete
	// coverage. A fresh fact about one package does not answer how another
	// package stores or exposes what the target needs.
	scope.CognitionResolved = len(plan.ResolvedByCognition) > 0 && !plan.SemanticResolved
	// Privileges: global listing survives whenever the resolutions came
	// from the semantic path (authority not established); global search
	// survives while unresolved questions remain.
	scope.AllowGlobalList = !scope.CognitionResolved
	scope.AllowGlobalSearch = len(scope.UnresolvedQuestions) > 0 || !scope.CognitionResolved
	return scope
}

// dedupeStrings preserves order while dropping adjacent duplicates; the
// caller sorts first so the result is fully deduplicated and stable.
func dedupeStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// ScopedContextRequest builds the cognition-aware ContextRequest. The
// counterfactual default request is passed in so suppression accounting can
// compare structurally. Returns the scoped request and the counts of
// default queries that were structurally suppressed.
//
// Hierarchy (directive A5):
//  1. reads of known files (cognition contract),
//  2. symbol outlines for known symbols,
//  3. targeted searches ONLY for unresolved questions,
//  4. global listing only when nothing was resolved.
func ScopedContextRequest(defaultReq schemas.ContextRequest, scope StageScopePlan, reason string) (schemas.ContextRequest, ScopeSuppression) {
	sup := ScopeSuppression{}
	if !scope.CognitionResolved {
		if len(scope.KnownFiles) == 0 {
			// Cold fallback: no cognition at all. Default byte-identical.
			return defaultReq, sup
		}
		// Semantic-only candidates: PRIORITIZE the known files by
		// prepending their reads to the FULL default request. No
		// suppression is claimed - the planner did not establish what can
		// be omitted. The global listing stays (discovery remains open);
		// the model simply sees the prioritized files first.
		prepended := make([]schemas.ContextQuery, 0, len(scope.KnownFiles)+len(defaultReq.Queries))
		for _, f := range scope.KnownFiles {
			path := f
			prepended = append(prepended, schemas.ContextQuery{
				QueryType:  schemas.ContextReadFile,
				Path:       &path,
				MaxResults: 10,
				MaxChars:   scopedReadMaxChars,
			})
		}
		prepended = append(prepended, defaultReq.Queries...)
		return schemas.ContextRequest{Reason: reason, Queries: prepended}, sup
	}
	if len(scope.UnresolvedQuestions) > 0 {
		// Exact-anchor resolutions with remaining unresolved questions:
		// A4 partial scope. Default reads stay (targeted discovery remains
		// allowed); only the global listing is dropped - the one operation
		// the resolved cognition provably makes redundant.
		partial := defaultReq
		kept := make([]schemas.ContextQuery, 0, len(defaultReq.Queries))
		for _, q := range defaultReq.Queries {
			if q.QueryType == schemas.ContextListFiles {
				sup.GlobalListsSuppressed = 1
				continue
			}
			kept = append(kept, q)
		}
		partial.Queries = kept
		return partial, sup
	}
	queries := make([]schemas.ContextQuery, 0, len(scope.KnownFiles)+len(scope.KnownSymbols)+len(scope.UnresolvedQuestions))
	for _, f := range scope.KnownFiles {
		path := f
		queries = append(queries, schemas.ContextQuery{
			QueryType:  schemas.ContextReadFile,
			Path:       &path,
			MaxResults: 10,
			MaxChars:   scopedReadMaxChars,
		})
	}
	for _, sym := range scope.KnownSymbols {
		symbol := sym
		queries = append(queries, schemas.ContextQuery{
			QueryType:  schemas.ContextGetSymbol,
			Symbol:     &symbol,
			MaxResults: 10,
			MaxChars:   4000,
		})
	}
	for range scope.UnresolvedQuestions {
		// One targeted search per unresolved question keeps discovery
		// possible without reopening the workspace; the question text is
		// prose, so the search pattern uses the intent's candidate paths
		// already extracted by the default planner.
		for _, dq := range defaultReq.Queries {
			if dq.QueryType == schemas.ContextSearch {
				clone := dq
				queries = append(queries, clone)
				break
			}
		}
	}
	if scope.AllowGlobalList {
		for _, dq := range defaultReq.Queries {
			if dq.QueryType == schemas.ContextListFiles {
				queries = append(queries, dq)
				break
			}
		}
	} else {
		sup.GlobalListsSuppressed = 1
	}
	// Suppression accounting: the default request's read queries that name
	// files already covered by KnownFiles are structurally suppressed, and
	// the default listing when suppressed above.
	covered := map[string]bool{}
	for _, f := range scope.KnownFiles {
		covered[f] = true
	}
	for _, dq := range defaultReq.Queries {
		switch dq.QueryType {
		case schemas.ContextReadFile:
			if dq.Path != nil && covered[*dq.Path] {
				sup.FileReadsSuppressed++
			} else if scope.CognitionResolved {
				// The scoped request deliberately narrows to known files;
				// default fallback reads for files cognition did not name
				// are suppressed only when the plan resolved the location
				// question. This is a structural omission, not an
				// inferred one: the scoped request never issues them.
				sup.FileReadsSuppressed++
			}
		case schemas.ContextListFiles:
			if !scope.AllowGlobalList {
				sup.GlobalListsSuppressed = 1
			}
		}
	}
	sup.ContextQueriesDefault = len(defaultReq.Queries)
	sup.ContextQueriesExecuted = len(queries)
	sup.ContextQueriesSuppressed = sup.ContextQueriesDefault - sup.ContextQueriesExecuted
	if sup.ContextQueriesSuppressed < 0 {
		sup.ContextQueriesSuppressed = 0
	}
	return schemas.ContextRequest{
		Reason:  reason,
		Queries: queries,
	}, sup
}

// ScopeSuppression records the host decisions that actually omitted
// operations versus the deterministic counterfactual default request.
type ScopeSuppression struct {
	ContextQueriesDefault    int
	ContextQueriesExecuted   int
	ContextQueriesSuppressed int
	GlobalListsSuppressed    int
	FileReadsSuppressed      int
	SearchesSuppressed       int
}

// ScopedToolRunner wraps the model-facing tool runner with the cognition
// scope: when cognition resolved the location question, workspace-wide
// listing and reads/searches outside the granted files return a directive
// pointing at the granted context instead of executing. This is host-side
// enforcement (A7): prompting the model to trust cognition is not
// enforcement. The context FULFILLMENT path bypasses this wrapper - it
// uses the raw runner with explicit granted paths - so the scoped request
// itself is never blocked by its own scope.
type ScopedToolRunner struct {
	Inner ToolRunner
	Scope StageScopePlan
}

// ToolRunner implements the same interface via RunTool.
func (s ScopedToolRunner) RunTool(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	switch name {
	case "list_directory":
		// The workspace-wide listing is the one operation cognition
		// PROVABLY makes redundant: the scope already knows where the
		// verified work lives. Reads and searches stay available: the
		// model may need files outside the granted set (A8 escape
		// hatch), and denying reads broke correctness in the large-01
		// eval round (cold 3/3, warm 0/3 when audit-package reads were
		// denied). Suppression is scoped to what is provably redundant.
		if !s.Scope.AllowGlobalList {
			return ToolResult{OK: true, Output: "workspace listing suppressed by cognition scope. The verified cognition graph names the relevant files: " +
				strings.Join(s.Scope.KnownFiles, ", ")}, nil
		}
	}
	return s.Inner.RunTool(ctx, name, args)
}

// grantsPath reports whether the path is a granted known file or the
// containing file of a granted symbol.
func (s ScopedToolRunner) grantsPath(path string) bool {
	for _, f := range s.Scope.KnownFiles {
		if f == path {
			return true
		}
	}
	for _, sym := range s.Scope.KnownSymbols {
		if i := strings.Index(sym, "#"); i > 0 && sym[:i] == path {
			return true
		}
	}
	return false
}

// CaptureFromVerifiedRun is the exported capture entry for eval harnesses
// and tooling outside the splice package. It reconstructs the deterministic
// captures of a verified run from its committed tree without a model call.
func CaptureFromVerifiedRun(projectPath, outcomeStatus string, changedFiles []string, testCommand, revision, runID string) []GraphCapture {
	return captureFromVerifiedRun(projectPath, outcomeStatus, changedFiles, testCommand, revision, runID)
}

// PersistGraphCapture is the exported persistence entry for eval harnesses.
func PersistGraphCapture(ctx context.Context, client *memd.Client, projectPath string, c GraphCapture) (int64, error) {
	c.Project = projectPath
	return persistGraphCapture(ctx, client, c)
}

// WorktreeChangedFiles is the exported changed-file listing for eval
// harnesses reconstructing a verified run's capture set.
func WorktreeChangedFiles(ctx context.Context, workDir string) []string {
	return worktreeChangedFiles(ctx, workDir)
}
