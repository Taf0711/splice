package splice

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

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
	// execution must not report suppressed reads. The live stage-input
	// path authorizes no host omission, so the counters stay zero.
	tr := &runTraceAccumulator{stages: map[stageKey]schemas.InputMeta{}}
	tr.recordDiscoveryPlan("code_writer", 1, DiscoveryPlan{
		ResolvedByCognition: []ResolvedQuestion{{Question: "q", NodeID: 1}, {Question: "r", NodeID: 2}},
	})
	meta := tr.stages[stageKey{"code_writer", 1, 0}]
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
	before := tr.stages[stageKey{"code_writer", 1, 0}]
	tr.RecordScopeMetrics("code_writer", 1, schemas.ScopeMetrics{
		ContextQueriesDefault:    6,
		ContextQueriesExecuted:   3,
		ContextQueriesSuppressed: 3,
		GlobalListsSuppressed:    1,
		SearchesSuppressed:       2,
		ScopeExpansions:          1,
	})
	after := tr.stages[stageKey{"code_writer", 1, 0}]
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
	if meta := tr.stages[stageKey{"code_writer", 1, 0}]; meta != (schemas.InputMeta{}) {
		t.Fatalf("invalid metrics must not be recorded, got %+v", meta)
	}
}

func TestRecordScopeMetricsNilAccumulator(t *testing.T) {
	var tr *runTraceAccumulator
	tr.RecordScopeMetrics("code_writer", 1, schemas.ScopeMetrics{GlobalListsSuppressed: 1})
}

func TestScopedToolRunner_EnforcesHostScope(t *testing.T) {
	scope := StageScopePlan{
		CognitionResolved: true,
		KnownFiles:        []string{"internal/session/store.go"},
		KnownSymbols:      []string{"internal/session/store.go#Store.InvalidateUserSessions"},
	}
	inner := &fakeToolRunner{}
	scoped := ScopedToolRunner{Inner: inner, Scope: scope}

	// Granted read passes through.
	res, err := scoped.RunTool(context.Background(), "read_file", map[string]any{"path": "internal/session/store.go"})
	if err != nil || !res.OK {
		t.Fatalf("granted read must pass: %v %+v", err, res)
	}
	// A8: reads outside the granted set stay available (denying reads
	// broke correctness in the large-01 round); the inner runner sees them.
	before := inner.calls
	res, err = scoped.RunTool(context.Background(), "read_file", map[string]any{"path": "cmd/server/main.go"})
	if err != nil || !res.OK {
		t.Fatalf("outside read must pass through (A8): %v", err)
	}
	if inner.calls != before+1 {
		t.Fatalf("outside read must execute via inner runner")
	}
	// Global listing suppressed with the grant directive.
	res, _ = scoped.RunTool(context.Background(), "list_directory", map[string]any{"path": "."})
	if inner.calls != before+1 {
		t.Fatalf("global listing must be suppressed, inner saw %d", inner.calls)
	}
	if !strings.Contains(res.Output, "cognition graph names the relevant files") {
		t.Fatalf("listing suppression must name the cognition grant, got %q", res.Output)
	}
}

type fakeToolRunner struct {
	calls int
}

func (f *fakeToolRunner) RunTool(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	f.calls++
	return ToolResult{OK: true, Output: "ran"}, nil
}

func TestSemanticCandidatesForAuditTask_CannotSuppressAuditDiscovery(t *testing.T) {
	// The billing-distraction case from the review: fresh billing memory
	// on an audit task must not remove audit discovery. scopePlanFor with
	// a SemanticResolved plan grants no narrowing authority.
	plan := DiscoveryPlan{
		ResolvedByCognition: []ResolvedQuestion{{Question: "resolve architecture", NodeID: 1}},
		SemanticResolved:    true,
		SemanticHits:        2,
		Unresolved:          nil, // empty because the semantic path never enumerated needs
	}
	rev, project := "abc123", "/repo"
	nodes := []memd.GraphNode{{
		ID: 1, Kind: "fact", Status: "active",
		ProjectPath: &project, VerifiedRevision: &rev,
		Anchors: []memd.GraphAnchor{{Kind: "file", Value: "internal/billing/dunning.go"}},
	}}
	scope := scopePlanFor(plan, nodes, nil)
	if scope.CognitionResolved {
		t.Fatal("semantic-resolved plan must not claim resolution authority")
	}
	if !scope.AllowGlobalList || !scope.AllowGlobalSearch {
		t.Fatalf("audit discovery must remain available: %+v", scope)
	}
	// But the billing file IS prioritized in KnownFiles for context.
	if len(scope.KnownFiles) != 1 || scope.KnownFiles[0] != "internal/billing/dunning.go" {
		t.Fatalf("KnownFiles = %v, want the billing candidate prioritized", scope.KnownFiles)
	}
}

func TestExactAnchorResolution_AuthorizesNarrowing(t *testing.T) {
	// An exact-anchor resolution (fresh anchor locating the symbol the
	// question asks about) DOES authorize narrowing: the listing drops.
	plan := DiscoveryPlan{
		ResolvedByCognition: []ResolvedQuestion{{Question: "where is EnforceRetention?", NodeID: 1}},
		SemanticResolved:    false,
	}
	rev, project := "abc123", "/repo"
	nodes := []memd.GraphNode{{
		ID: 1, Kind: "fact", Status: "active",
		ProjectPath: &project, VerifiedRevision: &rev,
		Anchors: []memd.GraphAnchor{{Kind: "file", Value: "internal/audit/log.go"}},
	}}
	scope := scopePlanFor(plan, nodes, nil)
	if !scope.CognitionResolved {
		t.Fatal("exact-anchor resolution must claim authority")
	}
	if scope.AllowGlobalList {
		t.Fatal("listing must drop under exact-anchor resolution")
	}
}
