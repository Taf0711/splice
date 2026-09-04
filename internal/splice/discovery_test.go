package splice

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/cognition"
	"github.com/Taf0711/splice/internal/splice/schemas"
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
		f.nodes[id] = memd.GraphNode{
			ID: id, Kind: req.Kind, Claim: req.Claim,
			Status: req.Status, ProjectPath: &project,
			VerifiedRevision: &rev, SourceRunID: &req.SourceRunID,
			Anchors: req.Anchors,
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
	fake.add(t, "internal/session/store.go defines Store.InvalidateUserSessions", "active", rev, dir,
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
	fake.add(t, "internal/session/store.go defines Store.InvalidateUserSessions; verified at revision abc", "active", rev, dir,
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
	// The avoided-operations counter derives ONLY from resolved questions:
	// recordDiscoveryPlan counts len(ResolvedByCognition), never mere
	// retrieval. Pin the arithmetic.
	plan := DiscoveryPlan{
		ResolvedByTask:      []string{"a"},
		ResolvedByCognition: []ResolvedQuestion{{Question: "q", NodeID: 1}, {Question: "r", NodeID: 2}},
		Unresolved:          []string{"u"},
		AnchorsValidated:    2,
		AnchorsFailed:       1,
	}
	tr := &runTraceAccumulator{stages: map[stageKey]schemas.InputMeta{}}
	tr.recordDiscoveryPlan("code_writer", 1, plan)
	meta := tr.stages[stageKey{"code_writer", 1}]
	if meta.DiscoveryQuestions != 4 || meta.DiscoveryResolvedCog != 2 ||
		meta.DiscoveryReadsAvoided != 2 || meta.DiscoveryUnresolved != 1 ||
		meta.AnchorsValidated != 2 || meta.AnchorsFailed != 1 {
		t.Fatalf("discovery telemetry arithmetic wrong: %+v", meta)
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
