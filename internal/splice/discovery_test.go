package splice

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/cognition"
	"github.com/Taf0711/splice/internal/splice/memoryreason"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/worktrees"
)

// Tests in this file pin the MVP causal loop at the unit/integration seam:
// capture produces sidecar-valid anchors, exact + semantic retrieval find the
// captured nodes, freshness validation is structural (file-anchored, not
// repo-wide), and the DiscoveryPlan only counts an avoided operation when a
// fresh node actually resolved the question.

func gitInit(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", "-A")
	run("commit", "-qm", "base")
}

func gitHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return string(out[:len(out)-1])
}

func TestCaptureFromVerifiedRunAnchorsAreSidecarValid(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/session/store.go", "package session\n\ntype Store struct{}\n\nfunc (s *Store) InvalidateUserSessions(userID string) int { return 0 }\n\nfunc NewStore() *Store { return &Store{} }\n")

	captures := captureFromVerifiedRun(dir, "completed", []string{"internal/session/store.go"}, "go test ./...", "abc123", "run-1")
	if len(captures) != 2 {
		t.Fatalf("captures = %d, want procedure + file fact", len(captures))
	}
	proc, fact := captures[0], captures[1]
	if proc.Kind != "procedure" {
		t.Fatalf("first capture kind = %s, want procedure", proc.Kind)
	}
	for _, a := range proc.Anchors {
		if a.Kind != "test" {
			t.Fatalf("procedure anchor kind = %q, want test (the sidecar rejects unknown kinds)", a.Kind)
		}
	}
	if fact.Kind != "fact" {
		t.Fatalf("second capture kind = %s, want fact", fact.Kind)
	}
	symbols := goFileSymbols(dir, "internal/session/store.go")
	found := map[string]bool{}
	for _, s := range symbols {
		found[s] = true
	}
	for _, want := range []string{"Store.InvalidateUserSessions", "NewStore"} {
		if !found[want] {
			t.Errorf("goFileSymbols missing %s, got %v", want, symbols)
		}
	}
	hasFile, hasSymbol := false, false
	for _, a := range fact.Anchors {
		if a.Kind == "file" && a.Value == "internal/session/store.go" {
			hasFile = true
		}
		if a.Kind == "symbol" {
			hasSymbol = true
		}
	}
	if !hasFile || !hasSymbol {
		t.Errorf("fact anchors missing file or symbol anchors: %+v", fact.Anchors)
	}

	// A failed run captures nothing: only VERIFIED work becomes cognition.
	if failed := captureFromVerifiedRun(dir, "failed", []string{"a.go"}, "go test ./...", "abc123", "run-1"); failed != nil {
		t.Errorf("failed run produced %d captures, want 0", len(failed))
	}
}

// fakeSidecar implements the /graph/* endpoints planDiscovery uses over the
// real wire types, backed by an in-memory node map. Contract fidelity comes
// from the wire structs, not from reimplemented logic.
type fakeSidecar struct {
	nodes map[int64]memd.GraphNode
	next  int64
}

func newFakeSidecar(t *testing.T) (*fakeSidecar, *memd.Client) {
	t.Helper()
	f := &fakeSidecar{nodes: map[int64]memd.GraphNode{}, next: 1}
	mux := http.NewServeMux()
	mux.HandleFunc("/graph/exact", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Anchors     map[string][]string `json:"anchors"`
			ProjectPath string              `json:"project_path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		out := []memd.GraphNode{}
		for _, n := range f.nodes {
			if n.Status != "active" || n.ProjectPath == nil || *n.ProjectPath != req.ProjectPath {
				continue
			}
			match := true
			for kind, values := range req.Anchors {
				want := map[string]bool{}
				for _, v := range values {
					want[v] = true
				}
				hit := false
				for _, a := range n.Anchors {
					if a.Kind == kind && want[a.Value] {
						hit = true
						break
					}
				}
				if !hit {
					match = false
					break
				}
			}
			if match {
				out = append(out, n)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "nodes": out})
	})
	mux.HandleFunc("/graph/upsert", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Kind             string             `json:"kind"`
			Claim            string             `json:"claim"`
			ProjectPath      string             `json:"project_path"`
			Status           string             `json:"status"`
			SourceRunID      string             `json:"source_run_id"`
			VerifiedRevision string             `json:"verified_revision"`
			Anchors          []memd.GraphAnchor `json:"anchors"`
			Metadata         map[string]any     `json:"metadata"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Kind == "" || req.Claim == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "kind and claim required"})
			return
		}
		id := f.next
		f.next++
		project := req.ProjectPath
		rev := req.VerifiedRevision
		var metaJSON *string
		if req.Metadata != nil {
			raw, _ := json.Marshal(req.Metadata)
			s := string(raw)
			metaJSON = &s
		}
		f.nodes[id] = memd.GraphNode{
			ID: id, Kind: req.Kind, Claim: req.Claim,
			Status: req.Status, ProjectPath: &project,
			VerifiedRevision: &rev, SourceRunID: &req.SourceRunID,
			Anchors: req.Anchors, MetadataJSON: metaJSON,
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "node": f.nodes[id]})
	})
	mux.HandleFunc("/graph/neighbors", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "nodes": []memd.GraphNode{}, "edges": []any{}})
	})
	mux.HandleFunc("/graph/search_semantic", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Text string `json:"text"`
			K    int    `json:"k"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		hits := []memd.GraphSearchHit{}
		for _, n := range f.nodes {
			if n.Status != "active" {
				continue
			}
			// Trivial containment rank mirrors the sidecar's purpose: the
			// semantic endpoint returns full nodes with anchors.
			node := n
			hits = append(hits, memd.GraphSearchHit{NodeID: n.ID, Score: 1, Node: &node})
		}
		// Equal scores, oldest first, cut at k: the worst case for stale
		// captures crowding out a fresh one.
		sort.Slice(hits, func(i, j int) bool { return hits[i].NodeID < hits[j].NodeID })
		if req.K > 0 && len(hits) > req.K {
			hits = hits[:req.K]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "hits": hits})
	})
	f0, err := os.CreateTemp("", "memd-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	sock := f0.Name()
	f0.Close()
	os.Remove(sock)
	t.Cleanup(func() { os.Remove(sock) })
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(mux)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return f, memd.NewClient(sock)
}

func (f *fakeSidecar) add(t *testing.T, claim string, status string, rev string, project string, anchors []memd.GraphAnchor) int64 {
	t.Helper()
	id := f.next
	f.next++
	projectCopy := project
	revCopy := rev
	f.nodes[id] = memd.GraphNode{
		ID: id, Kind: "fact", Claim: claim, Status: status,
		ProjectPath: &projectCopy, VerifiedRevision: &revCopy, Anchors: anchors,
	}
	return id
}

func setupFreshnessRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	// The anchored file must be TRACKED from the base commit so later
	// modifications show up in the changed-path diff (untracked files are
	// invisible to git diff, which would fake freshness).
	if err := os.MkdirAll(filepath.Join(dir, "internal", "session"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal/session/store.go"), []byte("package session\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "admin.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	return dir, gitHead(t, dir)
}

func TestPlanDiscoveryExactRetrievalAndFreshness(t *testing.T) {
	dir, rev := setupFreshnessRepo(t)
	fake, client := newFakeSidecar(t)

	// Task A's captured node: session.go defines the invalidation helper.
	fake.add(t, "internal/session/store.go defines Store.InvalidateUserSessions", "active", rev, memd.CanonicalProjectPath(dir),
		[]memd.GraphAnchor{
			{Kind: "file", Value: "internal/session/store.go"},
			{Kind: "symbol", Value: "internal/session/store.go#Store.InvalidateUserSessions"},
		})

	// Test A (directive 10): same anchor, unchanged content -> valid.
	plan, nodes := planDiscovery(context.Background(), client, dir, "intent mentioning internal/session/store.go", []string{"file:internal/session/store.go"})
	if len(plan.ResolvedByCognition) != 1 || plan.AnchorsValidated != 1 || len(nodes) != 1 {
		t.Fatalf("fresh anchor not admitted: %+v nodes=%d", plan, len(nodes))
	}

	// Test B: unrelated file changes -> cognition remains valid.
	if err := os.WriteFile(filepath.Join(dir, "admin.go"), []byte("package main\n// unrelated change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, nodes = planDiscovery(context.Background(), client, dir, "intent", []string{"file:internal/session/store.go"})
	if len(plan.ResolvedByCognition) != 1 || len(nodes) != 1 {
		t.Fatalf("unrelated change wrongly invalidated cognition: %+v", plan)
	}

	// Test C: the anchored file changes incompatibly -> rejected.
	if err := os.WriteFile(filepath.Join(dir, "internal/session/store.go"), []byte("package session\n// rewritten\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, nodes = planDiscovery(context.Background(), client, dir, "intent", []string{"file:internal/session/store.go"})
	if len(plan.ResolvedByCognition) != 0 || len(nodes) != 0 || plan.AnchorsFailed != 1 {
		t.Fatalf("changed anchor not rejected: %+v nodes=%d", plan, len(nodes))
	}
	if len(plan.Unresolved) != 1 {
		t.Fatalf("rejected question should be unresolved, got %+v", plan)
	}
}

func TestPlanDiscoverySemanticFallbackAndSuppression(t *testing.T) {
	dir, rev := setupFreshnessRepo(t)
	fake, client := newFakeSidecar(t)

	// Task A's captured node is in the graph; the intent names no paths, so
	// only the semantic path can find it.
	fake.add(t, "internal/session/store.go defines Store.InvalidateUserSessions; verified at revision abc", "active", rev, memd.CanonicalProjectPath(dir),
		[]memd.GraphAnchor{{Kind: "file", Value: "internal/session/store.go"}})

	// No structural keys: the plan must fall back to semantic entry nodes.
	plan, nodes := planDiscovery(context.Background(), client, dir,
		"administrator force sign-out should invalidate every active session",
		nil)
	if len(plan.ResolvedByCognition) != 1 || len(nodes) == 0 {
		t.Fatalf("semantic fallback did not resolve: %+v nodes=%d", plan, len(nodes))
	}

	// Cognition-off shape: nil client resolves nothing and never panics.
	emptyPlan, emptyNodes := planDiscovery(context.Background(), nil, dir, "intent", nil)
	if len(emptyPlan.ResolvedByCognition) != 0 || len(emptyNodes) != 0 {
		t.Fatalf("nil client plan should be empty: %+v", emptyPlan)
	}
}

func TestDiscoveryPlanAccountingIsHonest(t *testing.T) {
	// The avoided-operations counter is retired: recordDiscoveryPlan never
	// sets DiscoveryReadsAvoided because a resolved question is an observed
	// resolution, not a suppressed read. Real suppression moves through
	// RecordScopeMetrics only. Pin the decoupling.
	plan := DiscoveryPlan{
		ResolvedByTask:      []string{"a"},
		ResolvedByCognition: []ResolvedQuestion{{Question: "q", NodeID: 1}, {Question: "r", NodeID: 2}},
		Unresolved:          []string{"u"},
		AnchorsValidated:    2,
		AnchorsFailed:       1,
	}
	tr := &runTraceAccumulator{stages: map[stageKey]schemas.InputMeta{}}
	tr.recordDiscoveryPlan("code_writer", 1, plan)
	meta := tr.stages[stageKey{"code_writer", 1, 0}]
	if meta.DiscoveryQuestions != 4 || meta.DiscoveryResolvedCog != 2 ||
		meta.DiscoveryUnresolved != 1 ||
		meta.AnchorsValidated != 2 || meta.AnchorsFailed != 1 {
		t.Fatalf("discovery telemetry arithmetic wrong: %+v", meta)
	}
	if meta.DiscoveryReadsAvoided != 0 {
		t.Fatalf("DiscoveryReadsAvoided must stay zero from recordDiscoveryPlan, got %d", meta.DiscoveryReadsAvoided)
	}
	if err := meta.Validate(); err != nil {
		t.Fatalf("meta validate: %v", err)
	}
}

func TestAnchorForKeyMapping(t *testing.T) {
	cases := map[string][2]string{
		"file:internal/session/store.go":                {"file", "internal/session/store.go"},
		"symbol:internal/session/store.go#Store.Delete": {"symbol", "internal/session/store.go#Store.Delete"},
		"package:internal/session":                      {"package", "internal/session"},
	}
	for key, want := range cases {
		kind, value, ok := anchorForKey(key)
		if !ok || kind != want[0] || value != want[1] {
			t.Errorf("anchorForKey(%q) = %q,%q,%v want %v", key, kind, value, ok, want)
		}
	}
	if _, _, ok := anchorForKey("bogus:x"); ok {
		t.Error("unknown key class must not map to an anchor")
	}
}

func TestAnchorPathForKeyMatchesDiscoveryMapping(t *testing.T) {
	// Pairing test: the freshness anchor resolution in the C0 fast path and
	// the exact-anchor mapping in planDiscovery must agree on file keys, or
	// a hit would be validated against a different path than it was stored
	// with.
	key := "file:internal/session/store.go"
	anchor := cognition.AnchorPathForKey(key)
	_, value, ok := anchorForKey(key)
	if !ok || anchor != value {
		t.Fatalf("anchor disagreement: C0=%q discovery=%q", anchor, value)
	}
}

// TestEndToEndCaptureRetrieveApply pins the MVP causal chain at the
// integration seam: a verified Task A capture persists through the wire,
// and a Task B stage input with NO structural keys retrieves the captured
// node through the semantic path, passes freshness, and resolves a
// discovery question the cold run would have to answer by searching.
func TestEndToEndCaptureRetrieveApply(t *testing.T) {
	dir, rev := setupFreshnessRepo(t)
	fake, client := newFakeSidecar(t)

	// Task A completes and captures: persist through the real client path.
	captures := captureFromVerifiedRun(dir, "completed", []string{"internal/session/store.go"}, "go test ./...", rev, "run-a-1")
	if len(captures) == 0 {
		t.Fatal("verified run produced no captures")
	}
	for _, c := range captures {
		id, err := persistGraphCapture(context.Background(), client, c)
		if err != nil {
			t.Fatalf("persist capture: %v", err)
		}
		if id < 1 {
			t.Fatal("persisted node id must be positive")
		}
	}
	if len(fake.nodes) != len(captures) {
		t.Fatalf("sidecar holds %d nodes, want %d", len(fake.nodes), len(captures))
	}

	// Task B: intent names no paths. The semantic entry path must surface
	// the captured file fact and validate its anchor.
	intent := "administrator force sign-out should invalidate every active session"
	plan, nodes := planDiscovery(context.Background(), client, dir, intent, nil)
	if len(plan.ResolvedByCognition) == 0 {
		t.Fatalf("warm retrieval resolved nothing: %+v", plan)
	}
	if plan.AnchorsValidated < 1 {
		t.Fatalf("no anchor validated: %+v", plan)
	}
	obs := cognitionBundleFromNodes(nodes)
	if len(obs) == 0 {
		t.Fatal("cognition bundle empty after successful retrieval")
	}
	for _, o := range obs {
		if o.MemoryType != "pattern" && o.MemoryType != "procedure" {
			t.Errorf("unexpected memory type %q", o.MemoryType)
		}
		if o.SourceCommit == nil || *o.SourceCommit != rev {
			t.Errorf("observation source commit missing or wrong: %v", o.SourceCommit)
		}
	}
}

func TestSemanticPathRejectsStaleNodes(t *testing.T) {
	dir, rev := setupFreshnessRepo(t)
	fake, client := newFakeSidecar(t)

	// A's captured node: fresh at capture time.
	fake.add(t, "internal/session/store.go defines Store.InvalidateUserSessions", "active", rev, memd.CanonicalProjectPath(dir),
		[]memd.GraphAnchor{{Kind: "file", Value: "internal/session/store.go"}})

	// B edits the anchored file BEFORE retrieval: the node is now stale and
	// must not resolve the question, must not deliver, and the anchor must
	// count as failed.
	if err := os.WriteFile(filepath.Join(dir, "internal/session/store.go"), []byte("package session\n// B rewrote this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, nodes := planDiscovery(context.Background(), client, dir,
		"administrator force sign-out should invalidate every active session", nil)
	if len(plan.ResolvedByCognition) != 0 || len(nodes) != 0 {
		t.Fatalf("stale node resolved a question: %+v nodes=%d", plan, len(nodes))
	}
	if plan.AnchorsFailed != 1 || plan.AnchorsValidated != 0 {
		t.Fatalf("freshness accounting wrong: %+v", plan)
	}
}

// TestEvalContract_CommittedVerifiedTreeFresh pins the MVP eval contract:
// after the harness commits the verified Task A tree, captured cognition
// anchored at HEAD classifies FRESH at Task B start (diff HEAD..worktree
// is empty), which is what makes the warm arm's retrieval usable.
func TestEvalContract_CommittedVerifiedTree(t *testing.T) {
	dir, _ := setupFreshnessRepo(t)
	fake, client := newFakeSidecar(t)

	// Task A's capture happens with the tree still dirty: stash create is
	// sandbox-refused, so the anchor degrades to HEAD (pre-Task-A edits).
	// Simulate: edit the anchored file (Task A's change), anchor at the OLD
	// HEAD. At that point the anchored file diffs stale.
	oldHead := gitHead(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "internal/session/store.go"),
		[]byte("package session\n// Task A added ActiveSessionsFor here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake.add(t, "internal/session/store.go defines Store.ActiveSessionsFor", "active",
		oldHead,
		memd.CanonicalProjectPath(dir), []memd.GraphAnchor{{Kind: "file", Value: "internal/session/store.go"}})
	plan, nodes := planDiscovery(context.Background(), client, dir, "intent", []string{"file:internal/session/store.go"})
	if len(nodes) != 0 || plan.AnchorsFailed != 1 {
		t.Fatalf("dirty-tree anchor must fail closed: %+v nodes=%d", plan, len(nodes))
	}

	// The eval commits the verified tree (the harness contract).
	if out, err := exec.Command("git", "-C", dir, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	cmd := exec.Command("git", "-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "verified")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	// Simulate capture re-anchoring at the new HEAD (the commit made the
	// worktree clean, so verifiedRevision returns HEAD with no reason).
	rev, reason := verifiedRevision(context.Background(), dir)
	if rev != gitHead(t, dir) || reason != "" {
		t.Fatalf("post-commit anchor = %s reason=%q, want HEAD with no reason", rev[:10], reason)
	}
	// Re-capture at the committed revision: now FRESH at Task B start.
	fake.add(t, "internal/session/store.go defines Store.ActiveSessionsFor", "active",
		rev, memd.CanonicalProjectPath(dir), []memd.GraphAnchor{{Kind: "file", Value: "internal/session/store.go"}})
	plan, nodes = planDiscovery(context.Background(), client, dir, "intent", []string{"file:internal/session/store.go"})
	if len(plan.ResolvedByCognition) != 1 || len(nodes) != 1 || plan.AnchorsValidated != 1 {
		t.Fatalf("committed verified tree must classify FRESH: %+v nodes=%d", plan, len(nodes))
	}
}

// TestDiscoveryResolvesBareFilenameIntentAndDelivers is the fam-05 pairing
// test: the task text names a bare root-level file ("in main.go"), A's
// natural capture is anchored on exactly that file, and the whole delivery
// chain must light up as one unit: key derivation, exact-anchor retrieval,
// structural freshness, the node-to-observation bridge, and admission. The
// live run broke at the first link (zero keys derived) and delivered zero
// cognition despite a matched matrix.
func TestDiscoveryResolvesBareFilenameIntentAndDelivers(t *testing.T) {
	dir, rev := setupFreshnessRepo(t)
	fake, client := newFakeSidecar(t)
	// Store under the identity a canonicalizing client writes, not the
	// caller's spelling: the client canonicalizes on write and on query.
	fake.add(t,
		"main.go defines mapStoreError, main, sessionHandler; verified at revision "+rev,
		"active", rev, memd.CanonicalProjectPath(dir),
		[]memd.GraphAnchor{
			{Kind: "file", Value: "main.go"},
			{Kind: "symbol", Value: "main.go#mapStoreError"},
		})

	// Link 1: the fam-05 task intent derives the bare-filename key.
	intent := "New admin endpoints must map their store errors using the SAME table as the /session handler. Refactor mapStoreError in main.go if needed and use it from any new admin handlers you add. The mapping table must stay the single source of truth for store-error-to-status translation."
	keys := cognition.DeriveKeys(cognition.DeriveInput{RequestIntent: intent})
	if !slices.Contains(keys, "file:main.go") {
		t.Fatalf("bare filename not keyed: %v", keys)
	}

	// Link 2: the exact-anchor discovery resolves the question.
	plan, nodes := planDiscovery(context.Background(), client, dir, intent, keys)
	if len(plan.ResolvedByCognition) != 1 || len(nodes) != 1 {
		t.Fatalf("discovery did not resolve the bare-filename question: %+v nodes=%d", plan, len(nodes))
	}
	if plan.ResolvedByCognition[0].NodeKind != "fact" {
		t.Fatalf("resolved node kind = %q, want fact", plan.ResolvedByCognition[0].NodeKind)
	}

	// Link 3: the resolved node becomes an admissible observation - the
	// delivery path the stage input actually runs.
	bundle := schemas.MemoryBundle{Observations: cognitionBundleFromNodes(nodes)}
	admitted := memoryreason.Admit(&bundle, dir, time.Now().Unix())
	if admitted.Bundle == nil || len(admitted.Bundle.Observations) != 1 {
		t.Fatalf("admission rejected the delivered capture: %+v", admitted.Rejected)
	}

	// Adversarial: a sibling anchor that the intent never names must not
	// ride along through the bare-filename key.
	other := fake.add(t, "session.go defines Store; verified at revision "+rev,
		"active", rev, memd.CanonicalProjectPath(dir),
		[]memd.GraphAnchor{{Kind: "file", Value: "session.go"}})
	plan2, nodes2 := planDiscovery(context.Background(), client, dir, intent, keys)
	gotIDs := map[int64]bool{}
	for _, n := range nodes2 {
		gotIDs[n.ID] = true
	}
	if gotIDs[other] {
		t.Fatalf("unrelated sibling anchor resolved from the main.go key")
	}
	if len(plan2.ResolvedByCognition) != 1 || len(nodes2) != 1 {
		t.Fatalf("second pass should still resolve exactly the main.go node: %+v", plan2)
	}
}

// TestDiscoverySurvivesProjectPathSpellings is the live geometry of the
// fam-05 deepseek failure, portable: the capture is replayed through one
// spelling of the arm directory (the /var form os.MkdirTemp returns) while
// the pipeline resolves the repo root to the other (/private/var). The
// sidecar identity must survive both spellings, so the exact-anchor
// discovery still resolves and still delivers.
func TestDiscoverySurvivesProjectPathSpellings(t *testing.T) {
	dir, rev := setupFreshnessRepo(t)
	link := filepath.Join(t.TempDir(), "arm-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	fake, client := newFakeSidecar(t)
	// Replay import through the LINK spelling (what a MkdirTemp caller
	// would pass as the warm project).
	// The import side canonicalizes, so the row lands under the canonical
	// identity even though the replay caller held the link spelling.
	fake.add(t,
		"main.go defines mapStoreError, main, sessionHandler; verified at revision "+rev,
		"active", rev, memd.CanonicalProjectPath(link),
		[]memd.GraphAnchor{{Kind: "file", Value: "main.go"}})

	intent := "Refactor mapStoreError in main.go if needed and use it from any new admin handlers you add."
	keys := cognition.DeriveKeys(cognition.DeriveInput{RequestIntent: intent})
	if !slices.Contains(keys, "file:main.go") {
		t.Fatalf("bare filename not keyed: %v", keys)
	}

	// Retrieval through the REAL spelling (what the pipeline's repo root
	// holds). The client canonicalizes both sides, so the question
	// resolves regardless of which spelling the caller held.
	plan, nodes := planDiscovery(context.Background(), client, dir, intent, keys)
	if len(plan.ResolvedByCognition) != 1 || len(nodes) != 1 {
		t.Fatalf("cross-spelling discovery did not resolve: %+v nodes=%d", plan, len(nodes))
	}

	// Admission must accept the record under either root spelling: the
	// same class of comparison the stage input runs.
	bundle := schemas.MemoryBundle{Observations: cognitionBundleFromNodes(nodes)}
	for _, rootSpelling := range []string{dir, link} {
		admitted := memoryreason.Admit(&bundle, rootSpelling, time.Now().Unix())
		if admitted.Bundle == nil || len(admitted.Bundle.Observations) != 1 {
			t.Fatalf("admission rejected under root spelling %s: %+v", rootSpelling, admitted.Rejected)
		}
	}
}

// TestCaptureAnchorKeepsARunsOwnChangesFresh is the cross-run contract the
// host snapshot restores. A run edits a tracked file and creates a new one,
// then captures while the tree is dirty (no harness commit). Before the host
// snapshot, capture anchored at the pre-run HEAD: the edited file read stale
// forever and the new file read fresh even after it was deleted. Anchored at
// the snapshot, both read fresh while the bytes stay, and stale once the
// change is reverted.
func TestCaptureAnchorKeepsARunsOwnChangesFresh(t *testing.T) {
	dir, _ := setupFreshnessRepo(t)
	fake, client := newFakeSidecar(t)
	head := gitHead(t, dir)
	edited := "internal/session/store.go"
	created := "internal/session/admin.go"
	if err := os.WriteFile(filepath.Join(dir, edited), []byte("package session\n// the run's edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, created), []byte("package session\n// the run's new file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rev, reason := verifiedRevision(context.Background(), dir)
	if rev == head || reason != "" {
		t.Fatalf("dirty-tree anchor = %s (HEAD %s) reason=%q, want a snapshot with no fallback", rev[:10], head[:10], reason)
	}
	project := memd.CanonicalProjectPath(dir)
	fake.add(t, "store.go changes the rule", "active", rev, project, []memd.GraphAnchor{{Kind: "file", Value: edited}})
	fake.add(t, "admin.go defines AdminForceSignOut", "active", rev, project, []memd.GraphAnchor{{Kind: "file", Value: created}})
	keys := []string{"file:" + edited, "file:" + created}

	plan, nodes := planDiscovery(context.Background(), client, dir, "intent", keys)
	if len(nodes) != 2 || plan.AnchorsFailed != 0 {
		t.Fatalf("the next run over the same bytes must see both captures fresh: %+v nodes=%d", plan, len(nodes))
	}

	// Revert the run's change: both captures now describe code that is gone.
	if out, err := exec.Command("git", "-C", dir, "checkout", "--", edited).CombinedOutput(); err != nil {
		t.Fatalf("git checkout: %v: %s", err, out)
	}
	if err := os.Remove(filepath.Join(dir, created)); err != nil {
		t.Fatal(err)
	}
	plan, nodes = planDiscovery(context.Background(), client, dir, "intent", keys)
	if len(nodes) != 0 || plan.AnchorsFailed != 2 {
		t.Fatalf("reverted change must fail both captures closed: %+v nodes=%d", plan, len(nodes))
	}
}

// TestWorkRecordBecomesTheNextRunsFirstRoundFiles is the memory effect end
// to end, without a provider. Run 1 asked for internal/audit/log.go in an
// expansion round and changed internal/audit/retention.go. Its work record
// reaches a related run 2 through the semantic path, and run 2's first
// context round reads those two files instead of the arbitrary
// production-source slice a large workspace gets without memory.
func TestWorkRecordBecomesTheNextRunsFirstRoundFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module demo\n")
	write("internal/audit/log.go", "package audit\n\ntype Trail struct{}\n")
	write("internal/audit/retention.go", "package audit\n")
	for _, pkg := range []string{"admin", "auth", "billing", "config", "notifications", "session", "storage", "webhooks", "worker"} {
		write("internal/"+pkg+"/"+pkg+".go", "package "+pkg+"\n")
	}
	gitInit(t, dir)

	// Run 1 changes retention.go, then captures while the tree is dirty.
	write("internal/audit/retention.go", "package audit\n\nfunc (t *Trail) EnforceRetention() int { return 0 }\n")
	rev, reason := verifiedRevision(context.Background(), dir)
	if reason != "" {
		t.Fatalf("capture must anchor at a snapshot, got reason %q", reason)
	}
	project := memd.CanonicalProjectPath(dir)
	changed := []string{"internal/audit/retention.go"}
	captures := captureFromVerifiedRunVerified(project, "completed", changed, "go test ./...", rev, "run-1", captureVerification{}, CaptureOriginRuntime)
	record, ok := workRecordCapture("Add EnforceRetention on the audit Trail", []string{"internal/audit/log.go"}, changed, captures)
	if !ok {
		t.Fatal("run 1 requested a file, so it must form a work record")
	}
	if !strings.HasPrefix(record.Claim, workRecordClaimPrefix) || !strings.Contains(record.Claim, "read internal/audit/log.go and changed internal/audit/retention.go") {
		t.Fatalf("claim = %q", record.Claim)
	}
	if rec, ok := workRecordCapture("x", nil, changed, captures); !ok || !strings.Contains(rec.Claim, "a verified run changed internal/audit/retention.go") {
		t.Fatalf("a run that changed files forms a work record even with no requests: %+v", rec)
	}
	if _, ok := workRecordCapture("x", nil, nil, captures); ok {
		t.Fatal("a run that neither requested nor changed files adds no work record")
	}
	fake, client := newFakeSidecar(t)
	fake.add(t, record.Claim, "active", rev, project, record.Anchors)

	// Run 2, a related task, on the same uncommitted tree.
	intent := "Add RetentionDeficit in the audit package that reuses the EnforceRetention rules"
	_, nodes := planDiscovery(context.Background(), client, dir, intent, nil)
	fetches := MemoryPrefetchFromNodes(intent, nodes)
	var paths []string
	for _, f := range fetches {
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, ",") != "internal/audit/retention.go,internal/audit/log.go" {
		t.Fatalf("prefetch = %v, want the files run 1 needed", paths)
	}
	reads := func(req schemas.ContextRequest) []string {
		var out []string
		for _, q := range req.Queries {
			if q.QueryType == schemas.ContextReadFile && q.Path != nil {
				out = append(out, *q.Path)
			}
		}
		return out
	}
	with := reads(stages.DefaultContextRequestPreferring(intent, "", dir, "go", paths))
	without := reads(stages.DefaultContextRequestFor(intent, "", dir, "go"))
	if strings.Join(with, ",") != "internal/audit/retention.go,internal/audit/log.go,go.mod" {
		t.Fatalf("with memory the first round reads %v", with)
	}
	// The intent names the audit package, so the ranked fallback reaches the
	// same files without memory. Memory still sets the order: the file the
	// related run changed comes first.
	if strings.Join(without, ",") != "internal/audit/retention.go,internal/audit/log.go,go.mod" {
		t.Fatalf("without memory the ranked fallback reads %v", without)
	}
	t.Logf("first-round reads: with memory %v; without memory %d files %v", with, len(without), without)
}

func TestNoteRequestedPathsKeepsDeliveredSourcesOnly(t *testing.T) {
	tr := &runTraceAccumulator{}
	path := func(p string) *string { return &p }
	failed := "not found"
	tr.noteRequestedPaths([]schemas.ContextItem{
		{Query: schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: path("./internal/audit/log.go")}},
		{Query: schemas.ContextQuery{QueryType: schemas.ContextGetSymbol, Path: path("internal/audit/retention.go")}},
		{Query: schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: path("missing.go")}, Error: &failed},
		{Query: schemas.ContextQuery{QueryType: schemas.ContextListFiles}},
	})
	if got := strings.Join(tr.requestedPathList(), ","); got != "internal/audit/log.go,internal/audit/retention.go" {
		t.Fatalf("requested = %s", got)
	}
	var nilTrace *runTraceAccumulator
	nilTrace.noteRequestedPaths(nil)
	if nilTrace.requestedPathList() != nil {
		t.Fatal("a nil trace records nothing")
	}
}

// TestExpandContextRoundRecordsOnlyExpansionPaths goes through the real
// fulfill path. The first live work-record campaign captured no work record:
// the round test read budget.RoundsUsed, which nothing increments. The
// initial round must record nothing; an expansion must record its files.
func TestExpandContextRoundRecordsOnlyExpansionPaths(t *testing.T) {
	runner := newStaticRunner(map[string]ToolResult{
		readToolName: {OK: true, Output: "File: x.go (1 lines)\n  1 package x"},
	})
	read := func(path string) *schemas.ContextRequest {
		return &schemas.ContextRequest{Reason: "read", Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextReadFile, Path: ptrCtx(path), MaxChars: 100, MaxResults: 10},
		}}
	}
	tr := newRunTraceAccumulator(nil, "run", "session", t.TempDir(), schemas.ExecutionPlan{Tier: schemas.TierLight}, "active", nil)
	budget := NewStageExecutionBudget(0)
	input := schemas.HarnessStageInput{StageName: "code_writer"}
	input, err := expandContextRound(context.Background(), input, read("initial.go"), budget, nil, runner, nil, tr, PipelineRunConfig{}, t.TempDir(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.requestedPathList(); len(got) != 0 {
		t.Fatalf("the initial round is the host's choice, recorded %v", got)
	}
	if _, err := expandContextRound(context.Background(), input, read("internal/billing/dunning.go"), budget, nil, runner, nil, tr, PipelineRunConfig{}, t.TempDir(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(tr.requestedPathList(), ","); got != "internal/billing/dunning.go" {
		t.Fatalf("expansion paths = %q, want internal/billing/dunning.go", got)
	}
}

// TestComposedRequestKeepsMemoryFilesWithinTheBound is the warm-arm failure
// of the first large effect campaign: an evidence-plan request plus memory
// prefetch summed 73000 max-chars, over the 64000 bound, and the stage
// failed on every iteration. Memory's files go first and the fit drops the
// plan's fallback reads instead.
func TestComposedRequestKeepsMemoryFilesWithinTheBound(t *testing.T) {
	read := func(path string) schemas.ContextQuery {
		return schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: ptrCtx(path), MaxResults: 10, MaxChars: 5000}
	}
	plan := []schemas.ContextQuery{{QueryType: schemas.ContextListFiles, MaxResults: 100, MaxChars: 10000}}
	for i := 0; i < 11; i++ {
		plan = append(plan, read(fmt.Sprintf("fallback%02d.go", i)))
	}
	memory := []schemas.ContextQuery{read("internal/billing/usage.go"), read("internal/billing/overage.go")}
	req := schemas.ContextRequest{Reason: "r", Queries: insertAfterListing(plan, memory)}
	if req.AggregateValidate() == nil {
		t.Fatal("the fixture must start over the bound")
	}
	dropped := req.FitAggregate()
	if err := req.AggregateValidate(); err != nil || dropped == 0 {
		t.Fatalf("after fit: dropped=%d err=%v", dropped, err)
	}
	if *req.Queries[1].Path != "internal/billing/usage.go" || *req.Queries[2].Path != "internal/billing/overage.go" {
		t.Fatalf("memory files must survive the fit, first reads = %v, %v", *req.Queries[1].Path, *req.Queries[2].Path)
	}
	if req.Queries[0].QueryType != schemas.ContextListFiles {
		t.Fatal("the listing stays first")
	}
}

func TestMemoryHealthCountsHitsAndMisses(t *testing.T) {
	tr := &runTraceAccumulator{}
	tr.noteMemoryDelivered([]string{"internal/billing/usage.go", "internal/billing/overage.go"})
	tr.noteRequestedPaths([]schemas.ContextItem{
		{Query: schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: ptrCtx("internal/billing/usage.go")}},
		{Query: schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: ptrCtx("internal/billing/plans.go")}},
	})
	delivered, hits, misses := tr.memoryHealth([]string{"internal/billing/overage.go", "internal/billing/overage_cents.go"})
	if delivered != 2 || hits != 1 || misses != 1 {
		t.Fatalf("delivered=%d hits=%d misses=%d, want 2 1 1", delivered, hits, misses)
	}
}

// TestStaleWorkRecordsDoNotHideTheFreshOne: the same task ran several times,
// and each verified run left a work record. After the file changed, the old
// records are stale. The lookup must still reach the newest, fresh record.
func TestStaleWorkRecordsDoNotHideTheFreshOne(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module demo\n")
	write("internal/audit/log.go", "package audit\n")
	write("internal/audit/retention.go", "package audit\n")
	gitInit(t, dir)
	project := memd.CanonicalProjectPath(dir)
	intent := "Add EnforceRetention on the audit Trail"
	fake, client := newFakeSidecar(t)
	record := func(body string) {
		t.Helper()
		write("internal/audit/retention.go", body)
		rev, reason := verifiedRevision(context.Background(), dir)
		if reason != "" {
			t.Fatalf("snapshot: %s", reason)
		}
		changed := []string{"internal/audit/retention.go"}
		captures := captureFromVerifiedRunVerified(project, "completed", changed, "go test ./...", rev, "run", captureVerification{}, CaptureOriginRuntime)
		rec, ok := workRecordCapture(intent, []string{"internal/audit/log.go"}, changed, captures)
		if !ok {
			t.Fatal("no work record")
		}
		fake.add(t, rec.Claim, "active", rev, project, rec.Anchors)
	}
	for i := 0; i < 5; i++ {
		record(fmt.Sprintf("package audit\n\n// attempt %d\n", i))
	}
	record("package audit\n\nfunc EnforceRetention() {}\n")

	plan, nodes := planDiscovery(context.Background(), client, dir, intent, nil)
	if len(MemoryPrefetchFromNodes(intent, nodes)) == 0 {
		t.Fatalf("five stale records hid the fresh one: failed=%d resolved=%d", plan.AnchorsFailed, len(plan.ResolvedByCognition))
	}
}

// TestRunChangedFilesExcludesEarlierUncommittedWork: an earlier run left
// edits and a new file uncommitted. This run's changed files are only what
// this run touched, so memory health and the work record do not credit the
// run with the earlier task's files.
func TestRunChangedFilesExcludesEarlierUncommittedWork(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module demo\n")
	write("a.go", "package demo\n")
	write("c.go", "package demo\n")
	gitInit(t, dir)

	// Earlier run: modifies a.go and creates b.go and e.go, all uncommitted.
	write("a.go", "package demo\n\nfunc A() {}\n")
	write("b.go", "package demo\n\nfunc B() {}\n")
	write("e.go", "package demo\n\nfunc E() {}\n")
	ctx := context.Background()
	tree, err := worktrees.SnapshotTree(ctx, dir)
	if err != nil {
		t.Fatalf("snapshot tree: %v", err)
	}

	// This run: modifies c.go, creates d.go, and edits the earlier new e.go.
	write("c.go", "package demo\n\nfunc C() {}\n")
	write("d.go", "package demo\n\nfunc D() {}\n")
	write("e.go", "package demo\n\nfunc E() int { return 1 }\n")

	got := strings.Join(runChangedFiles(ctx, dir, tree), ",")
	if got != "c.go,d.go,e.go" {
		t.Fatalf("run changed files = %s, want c.go,d.go,e.go", got)
	}
	if head := strings.Join(runChangedFiles(ctx, dir, ""), ","); !strings.Contains(head, "a.go") {
		t.Fatalf("without a pre-run tree the HEAD fallback lists the earlier edits too, got %s", head)
	}
}

// TestPrefetchRanksTheRecordOfTheMatchingTaskFirst pins the P3 ranking:
// three work records share a workspace, and each read ranks the record of
// its own kind of task first, not the oldest record. The prompts are the
// competing corpus's admin trio.
func TestPrefetchRanksTheRecordOfTheMatchingTaskFirst(t *testing.T) {
	record := func(id int64, task string, files ...string) memd.GraphNode {
		n := memd.GraphNode{ID: id, Kind: "fact", Claim: fmt.Sprintf("%s%q: a verified run changed %s.", workRecordClaimPrefix, task, strings.Join(files, ", "))}
		for _, f := range files {
			n.Anchors = append(n.Anchors, memd.GraphAnchor{Kind: "file", Value: f})
		}
		return n
	}
	nodes := []memd.GraphNode{
		record(1, `In the auth package, add an "auditor" role (RoleAuditor) that users can hold. Auditors hold the permissions "read" and "export".`, "internal/auth/roles.go", "internal/admin/permissions.go"),
		record(2, `In the billing package, add a "startup" subscription plan: 900 cents a month, 3 seats. Customers see it between free and team.`, "internal/billing/plans.go", "internal/admin/pricing.go"),
		record(3, `In the audit package, add the audit action "session.revoked" (ActionSessionRevoked) to the recorded actions. Its label is "Session revoked".`, "internal/audit/actions.go", "internal/admin/labels.go"),
	}
	for intent, want := range map[string]string{
		`In the auth package, add a "billing_admin" role (RoleBillingAdmin) that users can hold. Billing admins hold the permissions "read" and "billing".`: "internal/admin/permissions.go",
		`In the billing package, add an "enterprise" subscription plan: 49900 cents a month, 1000 seats. Customers see it after scale.`:                     "internal/admin/pricing.go",
		`In the audit package, add the audit action "invoice.voided" (ActionInvoiceVoided) to the recorded actions. Its label is "Invoice voided".`:         "internal/admin/labels.go",
	} {
		fetches := MemoryPrefetchFromNodes(intent, nodes)
		if len(fetches) < 2 || fetches[1].Path != want {
			t.Errorf("%.40s: prefetch %v, want %s from the matching record first", intent, fetches, want)
		}
	}
}

// TestWorkRecordStaysWhileItsFilesExist pins the P2e rule: a work record
// names files, and the prefetch reads their current content, so it stays
// admissible after its files change and goes when one is gone. A fact
// about file content keeps the strict rule (here it cannot prove freshness
// at all: the revision is not in a repository).
func TestWorkRecordStaysWhileItsFilesExist(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "internal/config/catalog.go", "package config\n\n// edited after the record\n")
	rev := "0123456789abcdef0123456789abcdef01234567"
	node := func(id int64, claim string, files ...string) memd.GraphNode {
		n := memd.GraphNode{ID: id, Kind: "fact", Status: "active", Claim: claim, VerifiedRevision: &rev}
		for _, f := range files {
			n.Anchors = append(n.Anchors, memd.GraphAnchor{Kind: "file", Value: f})
		}
		return n
	}
	nodes := []memd.GraphNode{
		node(1, workRecordClaimPrefix+`"add a template": a verified run changed internal/config/catalog.go.`, "internal/config/catalog.go"),
		node(2, workRecordClaimPrefix+`"add a template": a verified run changed internal/gone.go.`, "internal/gone.go"),
		node(3, "catalog.go lists the notification kinds.", "internal/config/catalog.go"),
	}
	validated, failed, ok := admitFreshNodes(context.Background(), dir, nodes)
	if validated != 1 || failed != 2 || len(ok) != 1 || ok[0].ID != 1 {
		t.Fatalf("validated=%d failed=%d admitted=%v, want only the work record whose file exists", validated, failed, ok)
	}
}

// TestStageRequestOverTheBoundIsFitted: chain pilot 2. The test generator's
// post-write reads of nine changed files asked for 72000 characters, over
// the 64000 bound, and the stage failed. The request is fitted where every
// request is fulfilled.
func TestStageRequestOverTheBoundIsFitted(t *testing.T) {
	runner := newStaticRunner(map[string]ToolResult{
		readToolName: {OK: true, Output: "File: x.go (1 lines)\n  1 package x"},
	})
	req := &schemas.ContextRequest{Reason: "post-write source"}
	for i := 0; i < 9; i++ {
		req.Queries = append(req.Queries, schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: ptrCtx(fmt.Sprintf("f%d.go", i)), MaxChars: 8000, MaxResults: 10})
	}
	tr := newRunTraceAccumulator(nil, "run", "session", t.TempDir(), schemas.ExecutionPlan{Tier: schemas.TierLight}, "active", nil)
	input := schemas.HarnessStageInput{StageName: "test_generator"}
	if _, err := expandContextRound(context.Background(), input, req, NewStageExecutionBudget(0), nil, runner, nil, tr, PipelineRunConfig{}, t.TempDir(), 1, 0); err != nil {
		t.Fatalf("a request over the aggregate bound must be fitted, not fail: %v", err)
	}
	if len(req.Queries) != 8 {
		t.Fatalf("queries after the fit = %d, want 8 (64000 / 8000)", len(req.Queries))
	}
}
