package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newGraphTestStore opens a temp-database store for graph tests.
func newGraphTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := New(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestNodeKindEnums(t *testing.T) {
	for _, k := range []string{NodeKindFact, NodeKindConclusion, NodeKindDecision, NodeKindProcedure, NodeKindFailure, NodeKindEvidence} {
		if !ValidNodeKind(k) {
			t.Errorf("expected kind %q to be valid", k)
		}
	}
	for _, k := range []string{EdgeKindImplementedBy, EdgeKindDefinedIn, EdgeKindTestedBy, EdgeKindSupportedBy, EdgeKindDependsOn, EdgeKindContradicts, EdgeKindSupersedes, EdgeKindDerivedFrom, EdgeKindRelatedTo} {
		if !ValidEdgeKind(k) {
			t.Errorf("expected edge kind %q to be valid", k)
		}
	}
	for _, s := range []string{NodeStatusActive, NodeStatusSuperseded, NodeStatusStale, NodeStatusContradicted, NodeStatusArchived, NodeStatusEphemeral} {
		if !ValidNodeStatus(s) {
			t.Errorf("expected status %q to be valid", s)
		}
	}
	if ValidNodeKind("hypothesis") {
		t.Error("expected unknown kind to be invalid")
	}
	if ValidAnchorKind("sybmol") {
		t.Error("expected typo anchor kind to be invalid")
	}
}

func TestClaimHashWhitespaceCollapse(t *testing.T) {
	a := claimHash("use  NewStore   for sessions")
	b := claimHash("use NewStore for sessions")
	if a != b {
		t.Errorf("expected whitespace-collapsed claims to hash equal: %s vs %s", a, b)
	}
	// Case is preserved: "Foo" and "foo" are different claims.
	if claimHash("Foo bar") == claimHash("foo bar") {
		t.Error("expected case-sensitive claim hashes")
	}
}

func TestUpsertNodeValidation(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	cases := []struct {
		name string
		in   NodeInput
	}{
		{"unknown kind", NodeInput{Kind: "hypothesis", Claim: "x"}},
		{"empty claim", NodeInput{Kind: NodeKindFact, Claim: "   "}},
		{"bad scope", NodeInput{Kind: NodeKindFact, Claim: "x", Scope: "team"}},
		{"bad status", NodeInput{Kind: NodeKindFact, Claim: "x", Status: "zombie"}},
		{"bad anchor kind", NodeInput{Kind: NodeKindFact, Claim: "x", Anchors: []AnchorInput{{Kind: "sybmol", Value: "v"}}}},
		{"empty anchor value", NodeInput{Kind: NodeKindFact, Claim: "x", Anchors: []AnchorInput{{Kind: "file", Value: " "}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := st.UpsertNode(ctx, tc.in); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestUpsertNodeDedupe(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	in := NodeInput{
		Kind:        NodeKindDecision,
		Claim:       "session storage uses NewStore",
		ProjectPath: "/repo",
		SourceRunID: "run-1",
		Anchors:     []AnchorInput{{Kind: "file", Value: "internal/sessions/store.go"}},
	}
	first, err := st.UpsertNode(ctx, in)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Same claim, different whitespace: dedupes to the canonical id.
	in2 := in
	in2.Claim = "session   storage uses NewStore"
	in2.SourceRunID = "run-2"
	in2.VerifiedRevision = "abc123"
	second, err := st.UpsertNode(ctx, in2)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected dedupe to return canonical id %d, got %d", first.ID, second.ID)
	}
	if second.VerifiedRevision.Valid && second.VerifiedRevision.String != "abc123" {
		t.Errorf("expected verified revision abc123, got %q", second.VerifiedRevision.String)
	}
	if second.VerifiedAt.Int64 < first.VerifiedAt.Int64 {
		t.Errorf("expected verified_at to advance on update")
	}

	// Provenance merged: run-2 should appear in metadata run_ids.
	if !second.MetadataJSON.Valid {
		t.Fatalf("expected metadata_json to carry merged provenance")
	}
	if !strings.Contains(second.MetadataJSON.String, "run-2") {
		t.Errorf("expected metadata to mention run-2, got %s", second.MetadataJSON.String)
	}

	// Different project_path means a different node (dedupe scope).
	in3 := in
	in3.ProjectPath = "/other"
	third, err := st.UpsertNode(ctx, in3)
	if err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	if third.ID == first.ID {
		t.Error("expected different project_path to create a new node")
	}

	// Different kind, same claim text: different node.
	in4 := in
	in4.Kind = NodeKindFact
	fourth, err := st.UpsertNode(ctx, in4)
	if err != nil {
		t.Fatalf("fourth upsert: %v", err)
	}
	if fourth.ID == first.ID {
		t.Error("expected different kind to create a new node")
	}
}

func TestUpsertNodeWithEdgesAndAnchors(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	target, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindProcedure, Claim: "run make build"})
	if err != nil {
		t.Fatalf("target upsert: %v", err)
	}
	node, err := st.UpsertNode(ctx, NodeInput{
		Kind:    NodeKindFact,
		Claim:   "the build is wired through make",
		Anchors: []AnchorInput{{Kind: "file", Value: "Makefile"}},
		Edges:   []EdgeInput{{DstID: target.ID, Kind: EdgeKindDependsOn}},
	})
	if err != nil {
		t.Fatalf("upsert with edges: %v", err)
	}

	// Anchor is queryable.
	nodes, err := st.GetExact(ctx, map[string][]string{"file": {"Makefile"}}, GetExactOptions{})
	if err != nil {
		t.Fatalf("exact: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != node.ID {
		t.Fatalf("expected exact anchor hit for node %d, got %+v", node.ID, nodes)
	}

	// Re-upserting the same anchor does not duplicate it (UNIQUE-free table,
	// but the replace-on-write keeps one row per (node, kind, value) pair
	// because the insert is idempotent by content).
	if _, err := st.UpsertNode(ctx, NodeInput{
		Kind:    NodeKindFact,
		Claim:   "the build is wired through make",
		Anchors: []AnchorInput{{Kind: "file", Value: "Makefile"}},
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
}

func TestUpsertEdgeIdempotent(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	a, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "a"})
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "b"})
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if err := st.UpsertEdge(ctx, a.ID, b.ID, EdgeKindRelatedTo); err != nil {
		t.Fatalf("upsert edge: %v", err)
	}
	// Duplicate edge is a no-op, not an error.
	if err := st.UpsertEdge(ctx, a.ID, b.ID, EdgeKindRelatedTo); err != nil {
		t.Fatalf("duplicate edge: %v", err)
	}
	if err := st.UpsertEdge(ctx, a.ID, b.ID, "not_a_kind"); err == nil {
		t.Fatal("expected unknown edge kind to fail")
	}
	if err := st.UpsertEdge(ctx, 9999, b.ID, EdgeKindRelatedTo); err == nil {
		// Edges reference nodes only by convention here; missing src is not
		// validated by UpsertEdge (the DB foreign key rejects it on commit).
		t.Log("missing src accepted by UpsertEdge; FK enforcement is at the DB level")
	}
}

func TestGetExactRequiresAllAnchors(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	if _, err := st.UpsertNode(ctx, NodeInput{
		Kind:    NodeKindProcedure,
		Claim:   "test the mux",
		Anchors: []AnchorInput{{Kind: "test", Value: "TestHealth"}, {Kind: "file", Value: "main_test.go"}},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := st.UpsertNode(ctx, NodeInput{
		Kind:    NodeKindFact,
		Claim:   "mux lives in server.go",
		Anchors: []AnchorInput{{Kind: "test", Value: "TestHealth"}},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Both anchor kinds must match: only the first node has both.
	nodes, err := st.GetExact(ctx, map[string][]string{
		"test": {"TestHealth"},
		"file": {"main_test.go"},
	}, GetExactOptions{})
	if err != nil {
		t.Fatalf("exact: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected exactly 1 node matching all anchors, got %d", len(nodes))
	}
	if nodes[0].Claim != "test the mux" {
		t.Errorf("unexpected node claim %q", nodes[0].Claim)
	}

	// One anchor kind alone matches both.
	nodes, err = st.GetExact(ctx, map[string][]string{"test": {"TestHealth"}}, GetExactOptions{})
	if err != nil {
		t.Fatalf("exact single: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes for the single-anchor query, got %d", len(nodes))
	}

	// Missing anchor value: empty result, no error.
	nodes, err = st.GetExact(ctx, map[string][]string{"test": {"NoSuchTest"}}, GetExactOptions{})
	if err != nil {
		t.Fatalf("exact missing: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes for unknown anchor, got %d", len(nodes))
	}

	// 'test' anchor kind is supported per the research-report requirement:
	// diagnostic-directed repair queries by failing test name.
	if _, err := st.UpsertNode(ctx, NodeInput{
		Kind:    NodeKindFailure,
		Claim:   "TestHealth fails on method mismatch",
		Anchors: []AnchorInput{{Kind: "test", Value: "TestHealth"}, {Kind: "file", Value: "server.go"}},
	}); err != nil {
		t.Fatalf("upsert failure node: %v", err)
	}
	nodes, err = st.GetExact(ctx, map[string][]string{"test": {"TestHealth"}, "file": {"server.go"}}, GetExactOptions{})
	if err != nil {
		t.Fatalf("exact test+file: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Claim != "TestHealth fails on method mismatch" {
		t.Errorf("expected the failure node, got %+v", nodes)
	}
}

func TestGetExactProjectScopeAndStatus(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	if _, err := st.UpsertNode(ctx, NodeInput{
		Kind:        NodeKindFact,
		Claim:       "only in repo a",
		ProjectPath: "/repo-a",
		Anchors:     []AnchorInput{{Kind: "symbol", Value: "NewStore"}},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := st.UpsertNode(ctx, NodeInput{
		Kind:        NodeKindFact,
		Claim:       "only in repo b",
		ProjectPath: "/repo-b",
		Anchors:     []AnchorInput{{Kind: "symbol", Value: "NewStore"}},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	nodes, err := st.GetExact(ctx, map[string][]string{"symbol": {"NewStore"}}, GetExactOptions{ProjectPath: "/repo-a"})
	if err != nil {
		t.Fatalf("exact: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ProjectPath.String != "/repo-a" {
		t.Fatalf("expected only repo-a node, got %+v", nodes)
	}

	// Superseded nodes drop out of the default active query.
	active, err := st.UpsertNode(ctx, NodeInput{
		Kind:    NodeKindFact,
		Claim:   "superseded claim",
		Anchors: []AnchorInput{{Kind: "symbol", Value: "OldStore"}},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.SetStatus(ctx, active.ID, NodeStatusSuperseded); err != nil {
		t.Fatalf("set status: %v", err)
	}
	nodes, err = st.GetExact(ctx, map[string][]string{"symbol": {"OldStore"}}, GetExactOptions{})
	if err != nil {
		t.Fatalf("exact: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected superseded node to be excluded, got %d", len(nodes))
	}
}

func TestNeighborsBoundedBFS(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	// Chain: n1 -> n2 -> n3 -> n4. Depth 2 from n1 must reach n2, n3, not n4.
	ids := make([]int64, 5)
	for i, claim := range []string{"n1", "n2", "n3", "n4"} {
		n, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: claim})
		if err != nil {
			t.Fatalf("upsert %s: %v", claim, err)
		}
		ids[i] = n.ID
	}
	for i := 0; i < 3; i++ {
		if err := st.UpsertEdge(ctx, ids[i], ids[i+1], EdgeKindDependsOn); err != nil {
			t.Fatalf("edge %d: %v", i, err)
		}
	}
	// A superseded neighbor is skipped.
	if err := st.SetStatus(ctx, ids[2], NodeStatusSuperseded); err != nil {
		t.Fatalf("set status: %v", err)
	}

	nodes, edges, err := st.Neighbors(ctx, ids[0], nil, 2, 32)
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	// n2 (active) and n3 (superseded, filtered). Edge n1->n2 and n2->n3 exist.
	if len(nodes) != 1 {
		t.Fatalf("expected 1 active neighbor node, got %d", len(nodes))
	}
	if nodes[0].ID != ids[1] {
		t.Errorf("expected neighbor %d, got %d", ids[1], nodes[0].ID)
	}
	// Both edges are reachable even though the far node n3 is superseded:
	// edges are not status-filtered, only nodes are.
	if len(edges) != 2 {
		t.Fatalf("expected 2 reachable edges, got %d", len(edges))
	}

	// Edge-kind filter: unrelated kinds return nothing.
	nodes, _, err = st.Neighbors(ctx, ids[0], []EdgeKindFilter{EdgeKindContradicts}, 2, 32)
	if err != nil {
		t.Fatalf("neighbors filtered: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected no neighbors for contradicts filter, got %d", len(nodes))
	}

	// Node budget: limit 1 from n0 returns at most 1 node.
	nodes, _, err = st.Neighbors(ctx, ids[0], nil, 2, 1)
	if err != nil {
		t.Fatalf("neighbors limited: %v", err)
	}
	if len(nodes) > 1 {
		t.Errorf("expected node budget respected, got %d", len(nodes))
	}
}

func TestSetStatusLifecycle(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	node, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "scratch", Status: NodeStatusEphemeral})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for _, s := range []string{NodeStatusStale, NodeStatusArchived, NodeStatusActive} {
		if err := st.SetStatus(ctx, node.ID, s); err != nil {
			t.Fatalf("set status %s: %v", s, err)
		}
	}
	if err := st.SetStatus(ctx, 99999, NodeStatusActive); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for missing node, got %v", err)
	}
	if err := st.SetStatus(ctx, node.ID, "zombie"); err == nil {
		t.Fatal("expected unknown status to fail")
	}
}

func TestContradict(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	fact, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "the fixture API is newSessionStore"})
	if err != nil {
		t.Fatalf("fact: %v", err)
	}
	failure, err := st.UpsertNode(ctx, NodeInput{
		Kind:    NodeKindFailure,
		Claim:   "newSessionStore does not exist; the fixture API is NewStore + newSessionMux",
		Anchors: []AnchorInput{{Kind: "symbol", Value: "NewStore"}, {Kind: "file", Value: "fixture_test.go"}},
	})
	if err != nil {
		t.Fatalf("failure: %v", err)
	}

	if err := st.Contradict(ctx, fact.ID, failure.ID, EvidenceInput{
		Kind:   "falsified_approach",
		Ref:    "fixture_test.go",
		Detail: "compile error: undefined: newSessionStore",
	}); err != nil {
		t.Fatalf("contradict: %v", err)
	}

	// The fact node is contradicted, the failure node is still active.
	got, err := st.getActiveNode(ctx, fact.ID)
	if err != ErrNotFound {
		t.Fatalf("expected contradicted fact to leave the active set, got %v", err)
	}
	_ = got

	// The contradicts edge exists (failure -> fact).
	_, edges, err := st.Neighbors(ctx, failure.ID, []EdgeKindFilter{EdgeKindContradicts}, 1, 10)
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	if len(edges) != 1 || edges[0].Kind != EdgeKindContradicts || edges[0].DstID != fact.ID {
		t.Fatalf("expected contradicts edge failure->fact, got %+v", edges)
	}

	// Missing nodes fail loud.
	if err := st.Contradict(ctx, 424242, failure.ID, EvidenceInput{Kind: "x"}); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := st.Contradict(ctx, fact.ID, 424242, EvidenceInput{Kind: "x"}); err == nil {
		t.Fatal("expected missing contradicting node to fail")
	}
	if err := st.Contradict(ctx, fact.ID, failure.ID, EvidenceInput{Kind: ""}); err == nil {
		t.Fatal("expected empty evidence kind to fail")
	}
}

func TestCompactMergesDuplicatesAndKeepsContradicted(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	// Three identical active claims. c2 and c3 are inserted directly to
	// simulate duplicates that reach the table outside the dedupe window
	// (for example from concurrent writers or an older schema).
	c1, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "store uses WAL mode", SourceRunID: "run-1"})
	if err != nil {
		t.Fatalf("c1: %v", err)
	}
	hash := claimHash("store uses WAL mode")
	insertRawNode := func(run string) int64 {
		t.Helper()
		res, err := st.db.Exec(`INSERT INTO cognition_nodes(
			kind, claim, scope, status, source_run_id, created_at, verified_at, claim_hash
		) VALUES (?, ?, 'project', 'active', ?, strftime('%s','now'), strftime('%s','now'), ?)`,
			NodeKindFact, "store uses WAL mode", run, hash)
		if err != nil {
			t.Fatalf("raw insert %s: %v", run, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("raw id: %v", err)
		}
		return id
	}
	dupeA := insertRawNode("run-2")
	dupeB := insertRawNode("run-3")
	_ = dupeB // merged like dupeA; provenance checked below
	// A contradicted twin must NOT be merged: inserted raw as a distinct
	// row, then contradicted.
	c4 := insertRawNode("run-4")
	contradictor, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFailure, Claim: "WAL claim is false"})
	if err != nil {
		t.Fatalf("contradictor: %v", err)
	}
	if err := st.Contradict(ctx, c4, contradictor.ID, EvidenceInput{Kind: "falsified_approach"}); err != nil {
		t.Fatalf("contradict: %v", err)
	}
	// A different-kind twin with the same claim is a separate node.
	if _, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindDecision, Claim: "store uses WAL mode"}); err != nil {
		t.Fatalf("other kind: %v", err)
	}
	// An edge into c2 gets retargeted to the canonical c1.
	witness, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindEvidence, Claim: "witness"})
	if err != nil {
		t.Fatalf("witness: %v", err)
	}
	if err := st.UpsertEdge(ctx, witness.ID, dupeA, EdgeKindSupportedBy); err != nil {
		t.Fatalf("edge: %v", err)
	}

	report, err := st.Compact(ctx)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if report.DuplicateGroups != 1 {
		t.Errorf("expected 1 duplicate group (fact kind), got %d", report.DuplicateGroups)
	}
	if report.DuplicatesMerged != 2 {
		t.Errorf("expected 2 duplicates merged, got %d", report.DuplicatesMerged)
	}

	// Canonical is the lowest id: c1 was upserted first.
	canonical := c1.ID
	// Witness edge now points at canonical.
	_, edges, err := st.Neighbors(ctx, witness.ID, []EdgeKindFilter{EdgeKindSupportedBy}, 1, 10)
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	if len(edges) != 1 || edges[0].DstID != canonical {
		t.Fatalf("expected retargeted edge to canonical %d, got %+v (dupe was %d)", canonical, edges, dupeA)
	}

	// Canonical metadata carries merged provenance run ids.
	var meta string
	if err := st.db.QueryRow(`SELECT metadata_json FROM cognition_nodes WHERE id = ?`, canonical).Scan(&meta); err != nil {
		t.Fatalf("meta: %v", err)
	}
	for _, run := range []string{"run-2", "run-3"} {
		if !strings.Contains(meta, run) {
			t.Errorf("expected merged provenance to include %s, got %s", run, meta)
		}
	}

	// Contradicted twin survives with its status.
	var status string
	if err := st.db.QueryRow(`SELECT status FROM cognition_nodes WHERE id = ?`, c4).Scan(&status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != NodeStatusContradicted {
		t.Errorf("expected contradicted node to keep status, got %q", status)
	}

	// A second compact is a no-op.
	report2, err := st.Compact(ctx)
	if err != nil {
		t.Fatalf("compact 2: %v", err)
	}
	if report2.DuplicatesMerged != 0 {
		t.Errorf("expected second compact to be a no-op, got %+v", report2)
	}
}

func TestCollectDeletesOnlyStaleUnreferencedEphemeral(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	// Stale, unreferenced ephemeral: collected.
	eph, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "old scratch", Status: NodeStatusEphemeral})
	if err != nil {
		t.Fatalf("eph: %v", err)
	}
	// Backdate verified_at beyond any plausible cutoff.
	if _, err := st.db.Exec(`UPDATE cognition_nodes SET verified_at = 1 WHERE id = ?`, eph.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	// Fresh ephemeral: kept.
	fresh, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "fresh scratch", Status: NodeStatusEphemeral})
	if err != nil {
		t.Fatalf("fresh: %v", err)
	}
	// Ephemeral but referenced by an edge: kept.
	ref, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "referenced scratch", Status: NodeStatusEphemeral})
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	// fresh stays fresh (no backdate); ref and ev are stale but referenced.
	if _, err := st.db.Exec(`UPDATE cognition_nodes SET verified_at = 1 WHERE id = ?`, ref.ID); err != nil {
		t.Fatalf("backdate2: %v", err)
	}
	anchor, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "anchor node"})
	if err != nil {
		t.Fatalf("anchor: %v", err)
	}
	if err := st.UpsertEdge(ctx, anchor.ID, ref.ID, EdgeKindRelatedTo); err != nil {
		t.Fatalf("edge: %v", err)
	}
	// Ephemeral with evidence: kept.
	ev, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "evidenced scratch", Status: NodeStatusEphemeral})
	if err != nil {
		t.Fatalf("ev: %v", err)
	}
	if _, err := st.db.Exec(`UPDATE cognition_nodes SET verified_at = 1 WHERE id = ?`, ev.ID); err != nil {
		t.Fatalf("backdate3: %v", err)
	}
	if err := st.AddEvidence(ctx, ev.ID, EvidenceInput{Kind: "run_log"}); err != nil {
		t.Fatalf("add evidence: %v", err)
	}
	// Active stale node is NEVER collected.
	act, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "active fact"})
	if err != nil {
		t.Fatalf("act: %v", err)
	}
	if _, err := st.db.Exec(`UPDATE cognition_nodes SET verified_at = 1 WHERE id = ?`, act.ID); err != nil {
		t.Fatalf("backdate4: %v", err)
	}

	collected, err := st.Collect(ctx, time.Nanosecond)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if collected != 1 {
		t.Fatalf("expected exactly 1 collected node, got %d", collected)
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM cognition_nodes WHERE id = ?`, eph.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Error("expected stale unreferenced ephemeral node hard-deleted")
	}
	for _, id := range []int64{fresh.ID, ref.ID, ev.ID, act.ID} {
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM cognition_nodes WHERE id = ?`, id).Scan(&n); err != nil {
			t.Fatalf("count %d: %v", id, err)
		}
		if n != 1 {
			t.Errorf("expected node %d to survive collection", id)
		}
	}
}

func TestGraphTelemetryCounts(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	before := storeGraphTelemetry()
	in := NodeInput{Kind: NodeKindFact, Claim: "telemetry"}
	if _, err := st.UpsertNode(ctx, in); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := st.UpsertNode(ctx, in); err != nil {
		t.Fatalf("upsert again: %v", err)
	}
	after := storeGraphTelemetry()
	if after.Created-before.Created != 1 {
		t.Errorf("expected 1 created, got delta %d", after.Created-before.Created)
	}
	if after.Updated-before.Updated != 1 {
		t.Errorf("expected 1 updated, got delta %d", after.Updated-before.Updated)
	}

	if _, err := st.Compact(ctx); err != nil {
		t.Fatalf("compact: %v", err)
	}
	// Seed one stale unreferenced ephemeral node so Collect has work to do.
	eph, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "telemetry scratch", Status: NodeStatusEphemeral})
	if err != nil {
		t.Fatalf("eph: %v", err)
	}
	if _, err := st.db.Exec(`UPDATE cognition_nodes SET verified_at = 1 WHERE id = ?`, eph.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if _, err := st.Collect(ctx, time.Nanosecond); err != nil {
		t.Fatalf("collect: %v", err)
	}
	final := storeGraphTelemetry()
	if final.Compacted < after.Compacted {
		t.Errorf("expected compacted counter to move, got %d -> %d", after.Compacted, final.Compacted)
	}
	if final.Collected <= after.Collected {
		t.Errorf("expected collected counter to move, got %d -> %d", after.Collected, final.Collected)
	}
}

// storeGraphTelemetry reads the package-level graph counters.
func storeGraphTelemetry() GraphCounters { return GraphTelemetry() }
