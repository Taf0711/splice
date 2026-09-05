package splice

import (
	"testing"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func scopeTestRequest() schemas.ContextRequest {
	p1 := "cmd/server/main.go"
	p2 := "internal/session/store.go"
	return schemas.ContextRequest{
		Reason: "default",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextListFiles, MaxResults: 100, MaxChars: 10000},
			{QueryType: schemas.ContextReadFile, Path: &p1, MaxResults: 10, MaxChars: 5000},
			{QueryType: schemas.ContextReadFile, Path: &p2, MaxResults: 10, MaxChars: 5000},
		},
	}
}

func TestScopedContextRequest_ColdFallbackIsDefault(t *testing.T) {
	// Zero-value plan: no cognition privilege. The scoped request must be
	// byte-identical to the default, with zero suppression.
	scope := StageScopePlan{AllowGlobalList: true, AllowGlobalSearch: true}
	req, sup := ScopedContextRequest(scopeTestRequest(), scope, "cold")
	if len(req.Queries) != 3 {
		t.Fatalf("cold scoped queries = %d, want 3", len(req.Queries))
	}
	if sup.ContextQueriesSuppressed != 0 || sup.FileReadsSuppressed != 0 || sup.GlobalListsSuppressed != 0 {
		t.Fatalf("cold suppression must be zero: %+v", sup)
	}
	if req.Queries[0].QueryType != schemas.ContextListFiles {
		t.Fatal("cold request must keep the global listing")
	}
}

func TestScopedContextRequest_FreshCognitionSuppressesListingAndDefaults(t *testing.T) {
	// Fresh cognition resolved the location: known files cover the
	// counterfactual default reads; global listing is suppressed.
	scope := StageScopePlan{
		CognitionResolved: true,
		KnownFiles:        []string{"internal/session/store.go"},
		KnownSymbols:      []string{"internal/session/store.go#Store.InvalidateUserSessions"},
		ExpansionBudget:   2,
	}
	req, sup := ScopedContextRequest(scopeTestRequest(), scope, "warm")
	// Queries: 1 read (known file) + 1 get_symbol = 2. The default had 3
	// (listing + 2 reads). The main.go read is dropped structurally.
	if len(req.Queries) != 2 {
		t.Fatalf("scoped queries = %d, want 2 (read + symbol)", len(req.Queries))
	}
	if req.Queries[0].QueryType != schemas.ContextReadFile || *req.Queries[0].Path != "internal/session/store.go" {
		t.Fatalf("first scoped query must read the known file, got %+v", req.Queries[0])
	}
	if req.Queries[1].QueryType != schemas.ContextGetSymbol {
		t.Fatalf("second scoped query must be the symbol, got %+v", req.Queries[1])
	}
	if sup.GlobalListsSuppressed != 1 {
		t.Fatalf("global listing must be suppressed, got %+v", sup)
	}
	if sup.FileReadsSuppressed != 2 {
		// main.go dropped (not covered, cognition-resolved) + store.go
		// folded into the scoped read (covered).
		t.Fatalf("file reads suppressed = %d, want 2", sup.FileReadsSuppressed)
	}
	if sup.ContextQueriesDefault != 3 || sup.ContextQueriesExecuted != 2 || sup.ContextQueriesSuppressed != 1 {
		t.Fatalf("context query accounting wrong: %+v", sup)
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("scoped request invalid: %v", err)
	}
}

func TestScopedContextRequest_UnresolvedKeepsTargetedDiscovery(t *testing.T) {
	// Cognition resolves the invalidation location but the admin wiring is
	// unknown: unresolved questions keep targeted search alive.
	scope := StageScopePlan{
		CognitionResolved:   true,
		KnownFiles:          []string{"internal/session/store.go"},
		UnresolvedQuestions: []string{"where is admin HTTP behavior implemented?"},
		ExpansionBudget:     2,
	}
	def := scopeTestRequest()
	// Give the default a search query so the unresolved path has something
	// targeted to carry over.
	pat := "admin"
	def.Queries = append(def.Queries, schemas.ContextQuery{
		QueryType: schemas.ContextSearch, Pattern: &pat, MaxResults: 10, MaxChars: 4000,
	})
	req, sup := ScopedContextRequest(def, scope, "partial")
	found := 0
	for _, q := range req.Queries {
		if q.QueryType == schemas.ContextSearch {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("targeted search must survive for unresolved questions, got %d", found)
	}
	if sup.GlobalListsSuppressed != 1 {
		t.Fatal("global listing stays suppressed even with unresolved questions")
	}
}

func TestScopePlanFor_StaleNodesGrantNothing(t *testing.T) {
	// scopePlanFor consumes only admitted fresh nodes; a plan with zero
	// resolutions must not grant any privilege.
	plan := DiscoveryPlan{Unresolved: []string{"where is X?"}}
	scope := scopePlanFor(plan, nil, nil)
	if scope.CognitionResolved {
		t.Fatal("no resolved questions must not claim cognition resolution")
	}
	if len(scope.KnownFiles) != 0 || len(scope.KnownSymbols) != 0 {
		t.Fatalf("no nodes must grant no files/symbols: %+v", scope)
	}
	if !scope.AllowGlobalList || !scope.AllowGlobalSearch {
		t.Fatal("unresolved-only plan keeps ordinary discovery available")
	}
}

func TestScopePlanFor_AnchorExtraction(t *testing.T) {
	rev := "abc123"
	project := "/repo"
	nodes := []memd.GraphNode{
		{
			ID: 1, Kind: "fact", Status: "active",
			ProjectPath: &project, VerifiedRevision: &rev,
			Anchors: []memd.GraphAnchor{
				{Kind: "file", Value: "internal/session/store.go"},
				{Kind: "symbol", Value: "internal/session/store.go#Store.InvalidateUserSessions"},
				{Kind: "file", Value: "internal/session/store.go"},
			},
		},
	}
	plan := DiscoveryPlan{ResolvedByCognition: []ResolvedQuestion{{Question: "q", NodeID: 1}}}
	scope := scopePlanFor(plan, nodes, nil)
	if !scope.CognitionResolved {
		t.Fatal("resolved plan must mark CognitionResolved")
	}
	if len(scope.KnownFiles) != 1 || scope.KnownFiles[0] != "internal/session/store.go" {
		t.Fatalf("KnownFiles = %v, want deduplicated single entry", scope.KnownFiles)
	}
	if len(scope.KnownSymbols) != 1 || scope.KnownSymbols[0] != "internal/session/store.go#Store.InvalidateUserSessions" {
		t.Fatalf("KnownSymbols = %v", scope.KnownSymbols)
	}
	if scope.AllowGlobalList {
		t.Fatal("resolved plan must suppress global listing")
	}
}

func TestScopePlanFor_RepairReEntryCarriesGrants(t *testing.T) {
	prior := &StageScopePlan{
		KnownFiles:      []string{"internal/session/store.go"},
		ExpansionBudget: 1,
	}
	plan := DiscoveryPlan{Unresolved: []string{"new question"}}
	scope := scopePlanFor(plan, nil, prior)
	found := false
	for _, f := range scope.KnownFiles {
		if f == "internal/session/store.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("repair re-entry must carry prior grants, got %v", scope.KnownFiles)
	}
	if scope.ExpansionBudget != 1 {
		t.Fatalf("expansion budget must carry over, got %d", scope.ExpansionBudget)
	}
}

func TestTelemetry_ResolvedQuestionsAreNotSuppressedReads(t *testing.T) {
	// A9 pin: a plan with resolved questions but NO scoped-request
	// execution must not report suppressed reads. The suppression counters
	// only move through ScopedContextRequest host decisions.
	tr := &runTraceAccumulator{stages: map[stageKey]schemas.InputMeta{}}
	tr.recordDiscoveryPlan("code_writer", 1, DiscoveryPlan{
		ResolvedByCognition: []ResolvedQuestion{{Question: "q", NodeID: 1}, {Question: "r", NodeID: 2}},
	})
	meta := tr.stages[stageKey{"code_writer", 1}]
	if meta.DiscoveryReadsAvoided != 0 {
		t.Fatalf("DiscoveryReadsAvoided must stay zero (decoupled from resolved questions), got %d", meta.DiscoveryReadsAvoided)
	}
	if meta.FileReadsSuppressed != 0 || meta.ContextQueriesSuppressed != 0 {
		t.Fatalf("no host suppression decision was made yet, got %+v", meta)
	}
}

func TestRecordScopeMetricsWritesOnlyScopeFields(t *testing.T) {
	// RecordScopeMetrics must write ONLY the scope fields. The discovery
	// counters, and the legacy DiscoveryReadsAvoided counter in particular,
	// stay untouched so observed suppression never blends with resolved
	// question tallies.
	tr := &runTraceAccumulator{stages: map[stageKey]schemas.InputMeta{}}
	tr.recordDiscoveryPlan("code_writer", 1, DiscoveryPlan{
		ResolvedByCognition: []ResolvedQuestion{{Question: "q", NodeID: 1}},
		AnchorsValidated:    1,
	})
	before := tr.stages[stageKey{"code_writer", 1}]
	tr.RecordScopeMetrics("code_writer", 1, schemas.ScopeMetrics{
		ContextQueriesDefault:    6,
		ContextQueriesExecuted:   3,
		ContextQueriesSuppressed: 3,
		GlobalListsSuppressed:    1,
		SearchesSuppressed:       2,
		ScopeExpansions:          1,
	})
	after := tr.stages[stageKey{"code_writer", 1}]
	if after.ContextQueriesDefault != 6 || after.ContextQueriesExecuted != 3 ||
		after.ContextQueriesSuppressed != 3 || after.GlobalListsSuppressed != 1 ||
		after.SearchesSuppressed != 2 || after.ScopeExpansions != 1 {
		t.Fatalf("scope fields not written: %+v", after)
	}
	if after.DiscoveryQuestions != before.DiscoveryQuestions ||
		after.DiscoveryResolvedCog != before.DiscoveryResolvedCog ||
		after.DiscoveryReadsAvoided != before.DiscoveryReadsAvoided ||
		after.AnchorsValidated != before.AnchorsValidated {
		t.Fatalf("RecordScopeMetrics mutated discovery counters: before %+v after %+v", before, after)
	}
	if after.DiscoveryReadsAvoided != 0 {
		t.Fatalf("DiscoveryReadsAvoided must stay zero, got %d", after.DiscoveryReadsAvoided)
	}
}

func TestRecordScopeMetricsRejectsNegative(t *testing.T) {
	tr := &runTraceAccumulator{stages: map[stageKey]schemas.InputMeta{}}
	tr.RecordScopeMetrics("code_writer", 1, schemas.ScopeMetrics{SearchesSuppressed: -1})
	if meta := tr.stages[stageKey{"code_writer", 1}]; meta != (schemas.InputMeta{}) {
		t.Fatalf("invalid metrics must not be recorded, got %+v", meta)
	}
}

func TestRecordScopeMetricsNilAccumulator(t *testing.T) {
	var tr *runTraceAccumulator
	tr.RecordScopeMetrics("code_writer", 1, schemas.ScopeMetrics{GlobalListsSuppressed: 1})
}
