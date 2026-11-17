package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/presentation"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// editorEnv is a running server with the editor, a session, and a user config
// directory of its own.
type editorEnv struct {
	s       *Server
	cookie  *http.Cookie
	config  string // XDG_CONFIG_HOME
	sources splicerun.TopologySources
	used    []string
}

func startEditor(t *testing.T) *editorEnv {
	t.Helper()
	env := &editorEnv{config: t.TempDir()}
	t.Setenv("XDG_CONFIG_HOME", env.config)
	env.sources = pipelineWorkspace(t)
	env.sources.UserConfigDir = env.config
	editor := NewEditor(EditorConfig{
		Sources: env.sources,
		Models:  []ModelChoice{{Provider: "work", Model: "gpt-x"}},
		Use: func(name string) (string, error) {
			env.used = append(env.used, name)
			return "active pipeline: " + name, nil
		},
	})
	s, boot := startServer(t, time.Hour)
	if err := editor.Attach(s); err != nil {
		t.Fatal(err)
	}
	env.s, env.cookie = s, sessionFor(t, s, boot)
	return env
}

// call sends one editor request with the session, the Origin, and the action
// token. It returns the status and the body.
func (env *editorEnv) call(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	return env.raw(t, method, path, reader, map[string]string{"Origin": env.s.origin, actionHeader: actionOf(env.s), "Content-Type": "application/json"})
}

func (env *editorEnv) raw(t *testing.T, method, path string, body io.Reader, hdr map[string]string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, env.s.at("/s/editor/"+path), body)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	req.AddCookie(env.cookie)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func (env *editorEnv) newDraft(t *testing.T, from, name string) string {
	t.Helper()
	status, body := env.call(t, http.MethodPost, "drafts", map[string]string{"from": from, "name": name})
	if status != http.StatusOK {
		t.Fatalf("create draft: %d %s", status, body)
	}
	var reply struct{ ID string }
	if err := json.Unmarshal(body, &reply); err != nil || !draftIDPattern.MatchString(reply.ID) {
		t.Fatalf("create draft reply %s: %v", body, err)
	}
	return reply.ID
}

type saveReply struct {
	editorCheck
	Saved string `json:"saved"`
}

func (env *editorEnv) save(t *testing.T, id string, topology any) saveReply {
	t.Helper()
	status, body := env.call(t, http.MethodPut, "draft?id="+id, map[string]any{"doc": topology, "positions": []position{}})
	if status != http.StatusOK {
		t.Fatalf("save: %d %s", status, body)
	}
	var reply saveReply
	if err := json.Unmarshal(body, &reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

// reviewTopology has one command node and one agent node.
func reviewTopology(name string) *schemas.PipelineTopology {
	return &schemas.PipelineTopology{
		Version:   schemas.TopologySchemaVersion,
		Name:      name,
		MinSplice: schemas.AgentNodeMinSplice,
		Nodes: []schemas.PipelineNode{
			{Name: "code_writer", Type: "code_writer"},
			{Name: "lint", Type: schemas.NodeTypeCommand, Command: []string{"go", "vet", "./... && echo hi"}},
			{Name: "reviewer", Type: schemas.NodeTypeAgent, Prompt: "Review the change. <b>Say</b> what is wrong.", Tools: []string{"read_file", "bash", "web_fetch"}, Budget: &schemas.StageBudget{InputMax: 4000, OutputMax: 1000}},
		},
		Edges: []schemas.PipelineEdge{{From: "code_writer", To: "lint"}, {From: "lint", To: "reviewer", Payload: schemas.EdgePayloadOutput}},
	}
}

func TestEditorSaveReturnsTheLoaderErrorsAndSavesInvalidDrafts(t *testing.T) {
	env := startEditor(t)
	id := env.newDraft(t, fromNew, "")
	broken := reviewTopology("team")
	broken.Nodes[2].Tools = nil         // agent without tools
	broken.Nodes[1].Prompt = "not here" // prompt on a command node
	reply := env.save(t, id, broken)
	if reply.Valid {
		t.Fatal("an invalid document came back valid")
	}
	want := map[string]string{"reviewer": "tools", "lint": "prompt"}
	for _, p := range reply.Problems {
		if p.Index == nil || want[p.Node] == "" {
			continue
		}
		if p.Field != want[p.Node] {
			t.Errorf("problem of %s has field %q, want %q (%s)", p.Node, p.Field, want[p.Node], p.Message)
		}
		if strings.HasPrefix(p.Message, "nodes[") || strings.HasPrefix(p.Message, "node ") {
			t.Errorf("message keeps the location prefix: %q", p.Message)
		}
		delete(want, p.Node)
	}
	if len(want) > 0 {
		t.Fatalf("no problem for %v in %+v: every node problem must show at once", want, reply.Problems)
	}
	// Autosave keeps the invalid work, marked invalid, 0600.
	d, err := loadDraft(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Valid || !strings.Contains(string(d.Doc), "not here") {
		t.Fatalf("draft valid=%v doc=%s, want the invalid document saved", d.Valid, d.Doc)
	}
	path, _ := draftPath(id)
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("draft mode = %v (%v), want 0600", info.Mode().Perm(), err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("drafts dir mode = %v (%v), want 0700", info.Mode().Perm(), err)
	}
	// The library directory that the drafts directory sits in keeps the
	// mode of an import (0755 under the usual umask), never 0700.
	if info, err := os.Stat(filepath.Dir(filepath.Dir(path))); err != nil || info.Mode().Perm()&0o055 == 0 {
		t.Fatalf("library dir mode = %v (%v), want it readable as import makes it", info.Mode().Perm(), err)
	}

	// A fix makes it valid, and the reply carries the exact command line.
	reply = env.save(t, id, reviewTopology("team"))
	if !reply.Valid || len(reply.Problems) != 0 {
		t.Fatalf("valid document: %+v", reply.Problems)
	}
	if len(reply.Commands) != 1 || reply.Commands[0].Line != stages.CommandLine([]string{"go", "vet", "./... && echo hi"}) {
		t.Fatalf("commands = %+v, want the exact command line", reply.Commands)
	}
}

// TestEditorProblemsPlaceEveryLoaderError pairs the loader's error texts with
// the place the page shows them. A reworded schema error fails here.
func TestEditorProblemsPlaceEveryLoaderError(t *testing.T) {
	agent := func(edit func(*schemas.PipelineNode)) *schemas.PipelineTopology {
		top := reviewTopology("team")
		edit(&top.Nodes[2])
		return top
	}
	cases := []struct {
		name          string
		doc           any
		node, field   string
		tier          string
		topologyLevel bool
	}{
		{"no tools", agent(func(n *schemas.PipelineNode) { n.Tools = nil }), "reviewer", "tools", "", false},
		{"bad tool", agent(func(n *schemas.PipelineNode) { n.Tools = []string{"ask_user"} }), "reviewer", "tools", "", false},
		{"no prompt", agent(func(n *schemas.PipelineNode) { n.Prompt = "" }), "reviewer", "prompt", "", false},
		{"no budget", agent(func(n *schemas.PipelineNode) { n.Budget = nil }), "reviewer", "budget", "", false},
		{"turns", agent(func(n *schemas.PipelineNode) { n.MaxTurns = 99 }), "reviewer", "max_turns", "", false},
		{"permission", agent(func(n *schemas.PipelineNode) { n.Permission = "yolo" }), "reviewer", "permission", "", false},
		{"verification", agent(func(n *schemas.PipelineNode) { n.Caps.ProducesVerification = true }), "reviewer", "capabilities", "", false},
		{"model", agent(func(n *schemas.PipelineNode) { n.Model = &schemas.StageModelConfig{Model: "x"} }), "reviewer", "model", "", false},
		{"tiers", agent(func(n *schemas.PipelineNode) { n.Tiers = []schemas.PipelineTier{"huge"} }), "reviewer", "tiers", "", false},
		{"unknown type", agent(func(n *schemas.PipelineNode) { n.Type = "wizard" }), "reviewer", "type", "", false},
		{"command prompt", func() *schemas.PipelineTopology {
			top := reviewTopology("team")
			top.Nodes[1].Command = nil
			return top
		}(), "lint", "command", "", false},
		{"model on model-free", func() *schemas.PipelineTopology {
			top := reviewTopology("team")
			top.Nodes[1].Model = &schemas.StageModelConfig{ProviderProfile: "p", Model: "m"}
			return top
		}(), "lint", "model", "", false},
		{"duplicate name", func() *schemas.PipelineTopology {
			top := reviewTopology("team")
			top.Nodes[1].Name = "code_writer"
			top.Edges = nil
			return top
		}(), "code_writer", "name", "", false},
		{"filtered dependency", func() *schemas.PipelineTopology {
			top := reviewTopology("team")
			top.Nodes[1].Tiers = []schemas.PipelineTier{schemas.TierStandard}
			return top
		}(), "reviewer", "tiers", "trivial", false},
		{"envelope", func() *schemas.PipelineTopology {
			top := reviewTopology("team")
			top.Nodes[0].Budget = &schemas.StageBudget{InputMax: 90_000_000, OutputMax: 10}
			return top
		}(), "", "budget", "trivial", true},
		{"min version", func() *schemas.PipelineTopology {
			top := reviewTopology("team")
			top.MinSplice = ""
			return top
		}(), "", "splice_min_version", "", true},
		{"name", reviewTopology("Team"), "", "name", "", true},
		{"reserved name", reviewTopology("default"), "", "name", "", true},
		{"cycle", func() *schemas.PipelineTopology {
			top := reviewTopology("team")
			top.Edges = append(top.Edges, schemas.PipelineEdge{From: "reviewer", To: "code_writer"})
			return top
		}(), "", "edges", "", true},
		{"wrong type", map[string]any{"version": 1, "name": "team", "nodes": []any{map[string]any{"name": "w", "type": "code_writer"}, map[string]any{"name": "a", "type": "agent", "max_turns": "eight"}}, "edges": []any{}}, "a", "max_turns", "", false},
		{"wrong type on the second", map[string]any{"version": 1, "name": "team", "nodes": []any{map[string]any{"name": "a", "type": "agent", "max_turns": 8}, map[string]any{"name": "b", "type": "agent", "max_turns": "eight"}}, "edges": []any{}}, "b", "max_turns", "", false},
		{"unknown field", map[string]any{"version": 1, "name": "team", "nodes": []any{map[string]any{"name": "w", "type": "code_writer", "modle": "x"}}, "edges": []any{}}, "w", "modle", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := json.Marshal(tc.doc)
			if err != nil {
				t.Fatal(err)
			}
			check := checkDoc(doc)
			if check.Valid || len(check.Problems) == 0 {
				t.Fatalf("document passed: %s", doc)
			}
			p := check.Problems[0]
			if p.Field != tc.field || p.Node != tc.node || p.Tier != tc.tier || (p.Index == nil) != tc.topologyLevel {
				t.Fatalf("problem = %+v (index set %v), want node %q field %q tier %q", p, p.Index != nil, tc.node, tc.field, tc.tier)
			}
		})
	}
	edge := reviewTopology("team")
	edge.Edges[1].Payload = "everything"
	doc, _ := json.Marshal(edge)
	if p := checkDoc(doc).Problems[0]; p.Edge == nil || *p.Edge != 1 {
		t.Fatalf("edge problem = %+v, want edge 1", p)
	}
}

type consentReply struct {
	editorConsent
	Error string `json:"error"`
}

func (env *editorEnv) consent(t *testing.T, id string) (int, consentReply) {
	t.Helper()
	status, body := env.raw(t, http.MethodGet, "consent?id="+id, nil, nil)
	var reply consentReply
	if err := json.Unmarshal(body, &reply); err != nil {
		t.Fatalf("consent %s: %v", body, err)
	}
	return status, reply
}

func TestEditorPublishNeedsAValidDocAndAnExplicitConfirm(t *testing.T) {
	env := startEditor(t)
	id := env.newDraft(t, fromNew, "")
	broken := reviewTopology("team")
	broken.Nodes[2].Budget = nil
	env.save(t, id, broken)
	if status, _ := env.consent(t, id); status != http.StatusUnprocessableEntity {
		t.Fatalf("consent of an invalid draft: %d, want 422", status)
	}
	if status, _ := env.call(t, http.MethodPost, "publish?id="+id, map[string]any{"confirm": true}); status != http.StatusUnprocessableEntity {
		t.Fatalf("publish of an invalid draft: %d, want 422", status)
	}

	env.save(t, id, reviewTopology("team"))
	status, c := env.consent(t, id)
	if status != http.StatusOK || c.Replaces || c.Digest == "" {
		t.Fatalf("consent: %d %+v", status, c)
	}
	target := filepath.Join(env.config, "splice", "pipelines", "team.json")
	for _, bad := range []map[string]any{
		{"confirm": false, "digest": c.Digest},
		{"digest": c.Digest},
	} {
		if status, _ := env.call(t, http.MethodPost, "publish?id="+id, bad); status != http.StatusBadRequest {
			t.Fatalf("publish %v: %d, want 400", bad, status)
		}
	}
	if status, _ := env.call(t, http.MethodPost, "publish?id="+id, map[string]any{"confirm": true, "digest": "0" + c.Digest[1:]}); status != http.StatusConflict {
		t.Fatalf("publish with another digest: %d, want 409", status)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("a refused publish wrote the library entry: %v", err)
	}

	// The draft changes after the review: the old confirm is refused.
	changed := reviewTopology("team")
	changed.Nodes[1].Command = []string{"rm", "-rf", "/"}
	env.save(t, id, changed)
	if status, _ := env.call(t, http.MethodPost, "publish?id="+id, map[string]any{"confirm": true, "digest": c.Digest}); status != http.StatusConflict {
		t.Fatalf("publish of a changed draft with the old digest: %d, want 409", status)
	}
	env.save(t, id, reviewTopology("team"))

	status, body := env.call(t, http.MethodPost, "publish?id="+id, map[string]any{"confirm": true, "digest": c.Digest})
	if status != http.StatusOK {
		t.Fatalf("publish: %d %s", status, body)
	}
	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := splicerun.DecodeTopology(written)
	if err != nil || loaded.Name != "team" || len(loaded.Nodes) != 3 {
		t.Fatalf("library entry does not load: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(target))
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("publish left a temporary file: %s", entry.Name())
		}
	}
	if d, _ := loadDraft(id); d.Base != "team" {
		t.Fatalf("draft base = %q, want the published entry", d.Base)
	}
	// Use it now goes through the host.
	if status, body := env.call(t, http.MethodPost, "use", map[string]string{"name": "team"}); status != http.StatusOK || len(env.used) != 1 || !strings.Contains(string(body), "active pipeline: team") {
		t.Fatalf("use: %d %s %v", status, body, env.used)
	}
}

func TestEditorConsentDisclosesCommandAndAgentNodes(t *testing.T) {
	env := startEditor(t)
	id := env.newDraft(t, fromNew, "")
	top := reviewTopology("team")
	top.Nodes[2].Prompt = "Line one.\n\nLine two <b>bold</b>,\tand " + strings.Repeat("long ", 80)
	env.save(t, id, top)
	status, c := env.consent(t, id)
	if status != http.StatusOK {
		t.Fatalf("consent: %d %s", status, c.Error)
	}
	if len(c.Commands) != 1 || c.Commands[0].Name != "lint" || c.Commands[0].Line != splicerun.CommandNodes(top)[0].Line {
		t.Fatalf("commands = %+v", c.Commands)
	}
	if c.CommandCount != splicerun.CommandNodeCountText(1) {
		t.Fatalf("command count = %q", c.CommandCount)
	}
	want := agentViewOf(splicerun.AgentNodes(top)[0])
	if len(c.Agents) != 1 {
		t.Fatalf("agents = %+v", c.Agents)
	}
	a := c.Agents[0]
	if a.Name != "reviewer" || a.Tools != want.Tools || a.Permission != want.Permission || a.Budget != want.Budget || a.Prompt != want.Prompt || a.Turns != schemas.AgentDefaultMaxTurns {
		t.Fatalf("agent = %+v, want the trust card's disclosure %+v", a, want)
	}
	if got := visibleText("a\u202eb\u200bc\x1b[2Jd\n\te"); got != "abc[2Jd\n\te" {
		t.Fatalf("visibleText kept a control or format character: %q", got)
	}
	if c.Agents[0].PromptFull != top.Nodes[2].Prompt || !strings.HasSuffix(a.Prompt, "more") {
		t.Fatalf("full prompt = %q, preview = %q; want the whole prompt next to the bounded preview", c.Agents[0].PromptFull, a.Prompt)
	}
	for _, mark := range []string{"can run commands", "network"} {
		if !strings.Contains(a.Tools, mark) {
			t.Errorf("agent tools %q lack the mark %q", a.Tools, mark)
		}
	}
	if c.AgentCount != splicerun.AgentNodeCountText(1) || c.AgentBudget != splicerun.AgentBudgetSumText(top) {
		t.Fatalf("agent count %q, budget %q", c.AgentCount, c.AgentBudget)
	}
}

func TestEditorReplaceShowsTheStructuralDiff(t *testing.T) {
	env := startEditor(t)
	old := reviewTopology("team")
	data, _ := json.Marshal(old)
	target := filepath.Join(env.config, "splice", "pipelines", "team.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		t.Fatal(err)
	}
	id := env.newDraft(t, fromLibrary, "team")
	if again := env.newDraft(t, fromLibrary, "team"); again != id {
		t.Fatalf("a second edit of the entry made draft %s, want the resume of %s", again, id)
	}
	cur := reviewTopology("team")
	cur.Description = "now with tests"
	cur.Nodes[1].Command = []string{"go", "test", "./..."}
	cur.Nodes = append(cur.Nodes[:2], schemas.PipelineNode{Name: "test_runner", Type: "test_runner"})
	cur.Edges = []schemas.PipelineEdge{{From: "code_writer", To: "lint", Payload: schemas.EdgePayloadNone}, {From: "lint", To: "test_runner"}}
	cur.MinSplice = ""
	env.save(t, id, cur)
	status, c := env.consent(t, id)
	if status != http.StatusOK || !c.Replaces || c.Diff == nil || c.OldDigest == "" {
		t.Fatalf("consent: %d %+v", status, c)
	}
	d := c.Diff
	checks := map[string][2][]string{
		"nodes added":   {d.NodesAdded, {"test_runner"}},
		"nodes removed": {d.NodesRemoved, {"reviewer"}},
		"nodes changed": {d.NodesChanged, {"lint (command)"}},
		"edges added":   {d.EdgesAdded, {"lint -> test_runner"}},
		"edges removed": {d.EdgesRemoved, {"lint -> reviewer"}},
		"edges changed": {d.EdgesChanged, {"code_writer -> lint (summary to none)"}},
		"settings":      {d.Settings, {"description", "splice_min_version"}},
	}
	for name, pair := range checks {
		if strings.Join(pair[0], "|") != strings.Join(pair[1], "|") {
			t.Errorf("%s = %q, want %q", name, pair[0], pair[1])
		}
	}
	// An unchanged entry has empty lists, never null ones: the page reads each length.
	same := diffTopology(cur, cur)
	if same.Settings == nil || same.NodesChanged == nil {
		t.Fatalf("diff of an unchanged topology = %+v, want empty lists", same)
	}
	// A confirm that did not see the replace is refused.
	if status, _ := env.call(t, http.MethodPost, "publish?id="+id, map[string]any{"confirm": true, "digest": c.Digest, "old_digest": c.OldDigest}); status != http.StatusConflict {
		t.Fatalf("publish over an entry without replace: %d, want 409", status)
	}
	// The entry changes after the review: the diff is stale, so the confirm is refused.
	changed := append(append([]byte{}, data...), ' ')
	if err := os.WriteFile(target, changed, 0o644); err != nil {
		t.Fatal(err)
	}
	if status, _ := env.call(t, http.MethodPost, "publish?id="+id, map[string]any{"confirm": true, "replace": true, "digest": c.Digest, "old_digest": c.OldDigest}); status != http.StatusConflict {
		t.Fatalf("publish over an entry that changed after the review: %d, want 409", status)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if status, body := env.call(t, http.MethodPost, "publish?id="+id, map[string]any{"confirm": true, "replace": true, "digest": c.Digest, "old_digest": c.OldDigest}); status != http.StatusOK {
		t.Fatalf("replace: %d %s", status, body)
	}
	written, _ := os.ReadFile(target)
	if !strings.Contains(string(written), "now with tests") {
		t.Fatal("replace did not write the draft")
	}
}

// TestEditorNeverWritesTheProjectFile pins decision D5: a copy of a trusted
// project topology publishes to the user library, and the project file and
// the workspace stay as they were.
func TestEditorNeverWritesTheProjectFile(t *testing.T) {
	env := startEditor(t)
	projectFile := filepath.Join(env.sources.WorkspaceRoot, ".splice", "pipeline.json")
	before, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatal(err)
	}
	id := env.newDraft(t, fromActive, "")
	d, _ := loadDraft(id)
	if d.Base != "" || draftName(d.Doc) != "diamond" || d.Source != "copy of the project file "+projectFile {
		t.Fatalf("draft of the project topology = base %q name %q source %q", d.Base, draftName(d.Doc), d.Source)
	}
	if len(d.Positions) != 4 || d.Positions[0] == d.Positions[1] {
		t.Fatalf("positions = %+v, want the viewer layout", d.Positions)
	}
	changed := pipelineDiamond()
	changed.Description = "edited"
	env.save(t, id, changed)
	_, c := env.consent(t, id)
	if status, body := env.call(t, http.MethodPost, "publish?id="+id, map[string]any{"confirm": true, "digest": c.Digest}); status != http.StatusOK {
		t.Fatalf("publish: %d %s", status, body)
	}
	after, _ := os.ReadFile(projectFile)
	if !bytes.Equal(before, after) {
		t.Fatal("the editor changed the project file")
	}
	_ = filepath.WalkDir(env.sources.WorkspaceRoot, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && path != projectFile {
			t.Errorf("the editor wrote %s in the workspace", path)
		}
		return nil
	})
	if _, err := os.Stat(filepath.Join(env.config, "splice", "pipelines", "diamond.json")); err != nil {
		t.Fatalf("publish did not write the library entry: %v", err)
	}
}

func TestEditorDraftIDsCannotReachTheFilesystem(t *testing.T) {
	env := startEditor(t)
	outside := filepath.Join(env.config, "splice", "pipelines", "victim.json")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"doc": reviewTopology("team"), "positions": []position{}}
	for _, id := range []string{"../victim", "..%2Fvictim", "%2e%2e%2fvictim", strings.Repeat("a", 31), strings.Repeat("A", 32), strings.Repeat("a", 32) + "/../../victim", strings.Repeat("0", 32)} {
		for _, req := range []struct{ method, path string }{
			{http.MethodPut, "draft?id=" + id},
			{http.MethodDelete, "draft?id=" + id},
			{http.MethodPost, "publish?id=" + id},
		} {
			var body any
			if req.method == http.MethodPut {
				body = doc
			} else if req.method == http.MethodPost {
				body = map[string]any{"confirm": true}
			}
			if status, out := env.call(t, req.method, req.path, body); status != http.StatusNotFound {
				t.Errorf("%s %s: %d %s, want 404", req.method, req.path, status, out)
			}
		}
		if status, _ := env.raw(t, http.MethodGet, "consent?id="+id, nil, nil); status != http.StatusNotFound {
			t.Errorf("consent %s: %d, want 404", id, status)
		}
		if status, _ := env.raw(t, http.MethodGet, "?draft="+id, nil, nil); status != http.StatusNotFound {
			t.Errorf("page %s: %d, want 404", id, status)
		}
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "{}" {
		t.Fatalf("a request reached a file outside the drafts: %q %v", data, err)
	}
	// A client cannot choose the id of a new draft.
	if status, _ := env.call(t, http.MethodPost, "drafts", map[string]string{"from": "new", "id": "../victim"}); status != http.StatusBadRequest {
		t.Fatalf("create with a client id: %d, want 400", status)
	}
	if status, _ := env.call(t, http.MethodPost, "drafts", map[string]string{"from": "library", "name": "../victim"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("create from a traversal name: %d, want 422", status)
	}
}

func TestEditorMutationsNeedTheActionToken(t *testing.T) {
	env := startEditor(t)
	id := env.newDraft(t, fromNew, "")
	before, _ := loadDraft(id)
	requests := []struct{ method, path, body string }{
		{http.MethodPost, "drafts", `{"from":"new"}`},
		{http.MethodPut, "draft?id=" + id, `{"doc":{"version":1,"name":"changed","nodes":[],"edges":[]},"positions":[]}`},
		{http.MethodPost, "publish?id=" + id, `{"confirm":true}`},
		{http.MethodPost, "use", `{"name":"team"}`},
		{http.MethodPost, "layout", `{"doc":{"version":1,"name":"x","nodes":[],"edges":[]}}`},
		{http.MethodDelete, "draft?id=" + id, ``},
	}
	for _, req := range requests {
		for name, hdr := range map[string]map[string]string{
			"no token":     {"Origin": env.s.origin, "Content-Type": "application/json"},
			"wrong token":  {"Origin": env.s.origin, actionHeader: "nope", "Content-Type": "application/json"},
			"other origin": {"Origin": "http://evil.example", actionHeader: actionOf(env.s), "Content-Type": "application/json"},
		} {
			if status, _ := env.raw(t, req.method, req.path, strings.NewReader(req.body), hdr); status != http.StatusForbidden {
				t.Errorf("%s %s with %s: %d, want 403", req.method, req.path, name, status)
			}
		}
	}
	after, err := loadDraft(id)
	if err != nil || string(after.Doc) != string(before.Doc) || len(listDrafts()) != 1 || len(env.used) != 0 {
		t.Fatalf("a refused request changed state: %v drafts=%d used=%v", err, len(listDrafts()), env.used)
	}
}

func TestEditorRequestBodiesAreCappedStrictJSON(t *testing.T) {
	env := startEditor(t)
	id := env.newDraft(t, fromNew, "")
	ok := map[string]string{"Origin": env.s.origin, actionHeader: actionOf(env.s), "Content-Type": "application/json"}
	huge := `{"doc":{"version":1,"name":"x","description":"` + strings.Repeat("a", maxEditorBody) + `","nodes":[],"edges":[]},"positions":[]}`
	cases := []struct {
		name, body string
		hdr        map[string]string
		want       int
	}{
		{"over the cap", huge, ok, http.StatusRequestEntityTooLarge},
		{"not json", `{"doc":{}}`, map[string]string{"Origin": env.s.origin, actionHeader: actionOf(env.s), "Content-Type": "text/plain"}, http.StatusUnsupportedMediaType},
		{"unknown field", `{"doc":{},"positions":[],"path":"/etc/passwd"}`, ok, http.StatusBadRequest},
		{"trailing data", `{"doc":{},"positions":[]} {}`, ok, http.StatusBadRequest},
		{"doc not an object", `{"doc":[1],"positions":[]}`, ok, http.StatusBadRequest},
	}
	for _, tc := range cases {
		if status, body := env.raw(t, http.MethodPut, "draft?id="+id, strings.NewReader(tc.body), tc.hdr); status != tc.want {
			t.Errorf("%s: %d %s, want %d", tc.name, status, body, tc.want)
		}
	}
	// The draft is still the empty one.
	if d, _ := loadDraft(id); draftName(d.Doc) != newDraftName {
		t.Fatalf("a refused body changed the draft: %s", d.Doc)
	}
}

func TestEditorPagesAreCSPCompatibleAndEscapeText(t *testing.T) {
	env := startEditor(t)
	id := env.newDraft(t, fromNew, "")
	env.save(t, id, reviewTopology("team"))
	for _, sub := range []string{"", "?draft=" + id, "?from=active"} {
		status, body := env.raw(t, http.MethodGet, sub, nil, nil)
		page := string(body)
		if status != http.StatusOK {
			t.Fatalf("page %q: %d", sub, status)
		}
		scripts := regexp.MustCompile(`(?i)<script\b[^>]*>`).FindAllString(page, -1)
		if len(scripts) != 1 || !strings.Contains(scripts[0], `src="editor.js"`) {
			t.Fatalf("page %q script tags = %q, want only editor.js", sub, scripts)
		}
		if regexp.MustCompile(`(?i)\son[a-z]+\s*=|\sstyle\s*=|<style\b`).MatchString(page) {
			t.Fatalf("page %q has an inline handler or style", sub)
		}
		if !strings.Contains(page, `<meta name="splice-action" content="`+actionOf(env.s)+`">`) {
			t.Fatalf("page %q lacks the action token", sub)
		}
	}
	// Prompt text and argv are data: the page escapes them.
	hostile := reviewTopology("team")
	hostile.Name = "x"
	hostile.Nodes[2].Prompt = `</textarea><script>alert(1)</script>`
	hostile.Nodes[1].Command = []string{`"><img src=x onerror=alert(2)>`}
	env.save(t, id, hostile)
	for _, sub := range []string{"", "?draft=" + id} {
		_, body := env.raw(t, http.MethodGet, sub, nil, nil)
		if page := string(body); strings.Contains(page, "<script>alert") || strings.Contains(page, "<img src=x") || strings.Contains(page, `"><img`) {
			t.Fatalf("page %q carries raw markup from the draft", sub)
		}
	}
	// The boot data round-trips the hostile text as data.
	_, body := env.raw(t, http.MethodGet, "?draft="+id, nil, nil)
	m := regexp.MustCompile(`data-boot="([^"]*)"`).FindSubmatch(body)
	if m == nil {
		t.Fatal("editor page has no boot data")
	}
	var boot editorBoot
	if err := json.Unmarshal([]byte(htmlUnescape(string(m[1]))), &boot); err != nil {
		t.Fatalf("boot data: %v", err)
	}
	if !strings.Contains(string(boot.Doc), `alert(1)`) || boot.ID != id || len(boot.Models) != 1 {
		t.Fatalf("boot = %+v", boot)
	}
	for sub, want := range map[string]string{"editor.js": "text/javascript", "editor.css": "text/css", "pipeline.css": "text/css", "favicon.svg": "image/svg+xml"} {
		req, _ := http.NewRequest(http.MethodGet, env.s.at("/s/editor/"+sub), nil)
		req.AddCookie(env.cookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), want) {
			t.Errorf("%s: %d %q", sub, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}

func htmlUnescape(s string) string {
	return strings.NewReplacer("&#34;", `"`, "&quot;", `"`, "&lt;", "<", "&gt;", ">", "&#39;", "'", "&amp;", "&", "&#43;", "+").Replace(s)
}

func TestEditorPageNeedsTheSession(t *testing.T) {
	env := startEditor(t)
	for _, sub := range []string{"", "editor.js", "consent?id=" + strings.Repeat("a", 32)} {
		if resp := send(t, http.MethodGet, env.s.at("/s/editor/"+sub), nil, nil); resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET %q without a cookie: %d, want 403", sub, resp.StatusCode)
		}
	}
}

func TestEditorUseWithoutAHostSaysHow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, boot := startServer(t, time.Hour)
	if err := NewEditor(EditorConfig{}).Attach(s); err != nil {
		t.Fatal(err)
	}
	env := &editorEnv{s: s, cookie: sessionFor(t, s, boot)}
	status, body := env.call(t, http.MethodPost, "use", map[string]string{"name": "team"})
	if status != http.StatusNotImplemented || !strings.Contains(string(body), "splice pipeline use team") {
		t.Fatalf("use without a host: %d %s", status, body)
	}
}

func TestUseMakesTheEntryActiveForTheEditor(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	// No project file, so the embedded default is the active topology.
	sources := splicerun.TopologySources{UserConfigDir: config}
	team := pipelineDiamond()
	team.Name = "team"
	data, err := json.Marshal(team)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(config, "splice", "pipelines")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "team.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	editor := NewEditor(EditorConfig{Sources: sources, Use: func(name string) (string, error) {
		topology, _, err := splicerun.ActivateLibraryPipeline(name)
		if err != nil {
			return "", err
		}
		return "active pipeline: " + topology.Name, nil
	}})
	s, boot := startServer(t, time.Hour)
	if err := editor.Attach(s); err != nil {
		t.Fatal(err)
	}
	env := &editorEnv{s: s, cookie: sessionFor(t, s, boot), config: config, sources: sources}
	if status, body := env.call(t, http.MethodPost, "use", map[string]string{"name": "missing"}); status != http.StatusConflict {
		t.Fatalf("use of a missing entry: %d %s", status, body)
	}
	if _, page := env.raw(t, http.MethodGet, "", nil, nil); strings.Contains(string(page), "missing, ") {
		t.Fatal("a failed use changed the active topology")
	}
	if status, body := env.call(t, http.MethodPost, "use", map[string]string{"name": "team"}); status != http.StatusOK {
		t.Fatalf("use: %d %s", status, body)
	}
	if _, page := env.raw(t, http.MethodGet, "", nil, nil); !strings.Contains(string(page), "team, ") {
		t.Error("the home page does not name team as the active topology")
	}
	id := env.newDraft(t, fromActive, "")
	d, err := loadDraft(id)
	if err != nil {
		t.Fatal(err)
	}
	if name := draftName(d.Doc); name != "team" {
		t.Errorf("a draft from the active topology is a copy of %q, want team", name)
	}
}

// libraryUseEnv starts an editor over sources with a library entry "team".
// Its Use host accepts "team" and refuses every other name.
func libraryUseEnv(t *testing.T, sources splicerun.TopologySources) (*editorEnv, *Editor) {
	t.Helper()
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	sources.UserConfigDir = config
	team := pipelineDiamond()
	team.Name = "team"
	data, err := json.Marshal(team)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(config, "splice", "pipelines")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "team.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	// The host is the CLI's Use: it writes config.json, as the real host does.
	editor := NewEditor(EditorConfig{Sources: sources, Use: func(name string) (string, error) {
		topology, _, err := splicerun.ActivateLibraryPipeline(name)
		if err != nil {
			return "", err
		}
		return "active pipeline: " + topology.Name, nil
	}})
	s, boot := startServer(t, time.Hour)
	if err := editor.Attach(s); err != nil {
		t.Fatal(err)
	}
	return &editorEnv{s: s, cookie: sessionFor(t, s, boot), config: config, sources: sources}, editor
}

// TestFailedUseKeepsTheActiveTopology pins that a refused Use leaves the
// active topology as it was: the home page still names the embedded default.
func TestFailedUseKeepsTheActiveTopology(t *testing.T) {
	env, _ := libraryUseEnv(t, splicerun.TopologySources{})
	if status, body := env.call(t, http.MethodPost, "use", map[string]string{"name": "ghost"}); status != http.StatusConflict {
		t.Fatalf("use of a missing entry: %d %s", status, body)
	}
	_, page := env.raw(t, http.MethodGet, "", nil, nil)
	if !strings.Contains(string(page), "default, "+splicerun.TopologySourceDefault) || strings.Contains(string(page), "did not resolve") {
		t.Fatalf("after a failed use the home page does not name the embedded default:\n%s", page)
	}
}

// TestUseKeepsATrustedProjectFileFirst pins that after Use the editor still
// resolves a trusted project file first, as a run does.
func TestUseKeepsATrustedProjectFileFirst(t *testing.T) {
	env, _ := libraryUseEnv(t, pipelineWorkspace(t))
	if status, body := env.call(t, http.MethodPost, "use", map[string]string{"name": "team"}); status != http.StatusOK {
		t.Fatalf("use: %d %s", status, body)
	}
	if _, page := env.raw(t, http.MethodGet, "", nil, nil); !strings.Contains(string(page), "diamond, "+splicerun.TopologySourceProject) {
		t.Fatalf("the home page does not name the trusted project file as active:\n%s", page)
	}
	d, err := loadDraft(env.newDraft(t, fromActive, ""))
	if err != nil {
		t.Fatal(err)
	}
	if name := draftName(d.Doc); name != "diamond" || !strings.HasPrefix(d.Source, "copy of the project file") {
		t.Errorf("a draft from the active topology is %q (%s), want a copy of the project file", name, d.Source)
	}
}

// TestUseRacesWithTheActiveReaders runs Use beside the readers of the
// active topology (the home page, a draft from the active topology, and
// OpenDraft) and beside Configure, the one writer of the configuration. The
// race detector fails it on an unguarded access.
func TestUseRacesWithTheActiveReaders(t *testing.T) {
	env, editor := libraryUseEnv(t, splicerun.TopologySources{})
	do := func(method, path, body string) error {
		req, err := http.NewRequest(method, env.s.at("/s/editor/"+path), strings.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Origin", env.s.origin)
		req.Header.Set(actionHeader, actionOf(env.s))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(env.cookie)
		resp, err := noRedirect().Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			return errors.New(method + " " + path + ": " + resp.Status)
		}
		return nil
	}
	const rounds = 20
	errs := make(chan error, 5*rounds)
	done := make(chan struct{})
	for _, work := range []func() error{
		func() error { return do(http.MethodPost, "use", `{"name":"team"}`) },
		func() error { return do(http.MethodGet, "", "") },
		func() error { return do(http.MethodPost, "drafts", `{"from":"active","name":"","new":true}`) },
		func() error { _, err := editor.OpenDraft("", false); return err },
		func() error { editor.Configure(editor.config()); return nil },
	} {
		go func() {
			for range rounds {
				errs <- work()
			}
			done <- struct{}{}
		}()
	}
	for range 5 {
		<-done
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if _, page := env.raw(t, http.MethodGet, "", nil, nil); !strings.Contains(string(page), "team, "+splicerun.TopologySourceLibrary) {
		t.Error("after the uses the home page does not name team as the active topology")
	}
}

// TestActivationOutsideTheEditorShowsOnTheHomePage pins that a change of the
// active pipeline made by the CLI or the TUI, not by this editor's Use, is
// shown on the next page load.
func TestActivationOutsideTheEditorShowsOnTheHomePage(t *testing.T) {
	env, _ := libraryUseEnv(t, splicerun.TopologySources{})
	if _, _, err := splicerun.ActivateLibraryPipeline("team"); err != nil {
		t.Fatal(err)
	}
	if _, page := env.raw(t, http.MethodGet, "", nil, nil); !strings.Contains(string(page), "team, "+splicerun.TopologySourceLibrary) {
		t.Fatalf("the home page does not name team after an outside activation:\n%s", page)
	}
}

// TestHomeShowsABrokenConfigAndStaysUsable pins that a config.json that does
// not parse or cannot be read shows its error on the home page, and that the
// drafts that do not need the active pipeline still open.
func TestHomeShowsABrokenConfigAndStaysUsable(t *testing.T) {
	for name, write := range map[string]func(path string) error{
		"invalid JSON": func(path string) error { return os.WriteFile(path, []byte("{"), 0o600) },
		"unreadable":   func(path string) error { return os.WriteFile(path, []byte("{}"), 0o000) },
	} {
		t.Run(name, func(t *testing.T) {
			env, _ := libraryUseEnv(t, splicerun.TopologySources{})
			path := filepath.Join(env.config, "splice", "config.json")
			if err := write(path); err != nil {
				t.Fatal(err)
			}
			if name == "unreadable" {
				if _, err := os.ReadFile(path); err == nil {
					t.Skip("the file is readable for this user")
				}
			}
			status, page := env.raw(t, http.MethodGet, "", nil, nil)
			if status != http.StatusOK || !strings.Contains(string(page), "The active topology did not resolve") || !strings.Contains(string(page), "config.json") {
				t.Fatalf("home page: %d\n%s", status, page)
			}
			if status, body := env.call(t, http.MethodPost, "drafts", map[string]string{"from": "active"}); status != http.StatusUnprocessableEntity {
				t.Errorf("a draft from the unknown active pipeline: %d %s", status, body)
			}
			env.newDraft(t, fromDefault, "")
			env.newDraft(t, fromLibrary, "team")
		})
	}
}

// TestLayoutSkipsRoutingOverTheSizeLimit pins that the layout endpoint does not
// route a topology over the size limit: it returns a position for every node
// and no routed paths.
func TestLayoutSkipsRoutingOverTheSizeLimit(t *testing.T) {
	env, _ := libraryUseEnv(t, splicerun.TopologySources{})
	node := func(i int) schemas.PipelineNode {
		return schemas.PipelineNode{Name: "n" + strconv.Itoa(i), Type: schemas.NodeTypeCommand, Command: []string{"true"}}
	}
	// Too many nodes, no edges.
	var wide schemas.PipelineTopology
	for i := 0; i <= schemas.MaxTopologyNodes; i++ {
		wide.Nodes = append(wide.Nodes, node(i))
	}
	// Too many edges: forward edges over 65 nodes, one more than the cap.
	var dense schemas.PipelineTopology
	for i := 0; i < 65; i++ {
		dense.Nodes = append(dense.Nodes, node(i))
	}
	for i := 0; i < 65 && len(dense.Edges) <= schemas.MaxTopologyEdges; i++ {
		for j := i + 1; j < 65 && len(dense.Edges) <= schemas.MaxTopologyEdges; j++ {
			dense.Edges = append(dense.Edges, schemas.PipelineEdge{From: "n" + strconv.Itoa(i), To: "n" + strconv.Itoa(j)})
		}
	}
	for name, topology := range map[string]schemas.PipelineTopology{"wide": wide, "dense": dense} {
		status, body := env.call(t, http.MethodPost, "layout", map[string]any{"doc": topology})
		if status != http.StatusOK {
			t.Fatalf("%s: layout status %d: %s", name, status, body)
		}
		var reply struct {
			Positions []position    `json:"positions"`
			Paths     []layoutRoute `json:"paths"`
		}
		if err := json.Unmarshal(body, &reply); err != nil {
			t.Fatal(err)
		}
		if len(reply.Positions) != len(topology.Nodes) {
			t.Errorf("%s: %d positions for %d nodes", name, len(reply.Positions), len(topology.Nodes))
		}
		if len(reply.Paths) != 0 {
			t.Errorf("%s: %d routed paths, want none over the size limit", name, len(reply.Paths))
		}
	}
}

// TestEditorShowsTheTopologySizeLimitAsAPipelineProblem pins that a topology
// over the size limit is one pipeline-level problem that names the count.
func TestEditorShowsTheTopologySizeLimitAsAPipelineProblem(t *testing.T) {
	topology := pipelineDiamond()
	topology.Nodes = nil
	for i := 0; i <= schemas.MaxTopologyNodes; i++ {
		topology.Nodes = append(topology.Nodes, schemas.PipelineNode{Name: "n" + strconv.Itoa(i), Type: schemas.NodeTypeCommand, Command: []string{"true"}})
	}
	topology.Edges = nil
	data, err := json.Marshal(topology)
	if err != nil {
		t.Fatal(err)
	}
	check := checkDoc(data)
	if check.Valid || len(check.Problems) != 1 {
		t.Fatalf("problems = %+v, want one pipeline problem", check.Problems)
	}
	p := check.Problems[0]
	if p.Index != nil || p.Edge != nil || p.Node != "" || !strings.Contains(p.Message, "exceed the limit") {
		t.Fatalf("problem = %+v, want a pipeline-level problem that names the limit", p)
	}
}

func TestBootstrapURLForOpensThePageOnce(t *testing.T) {
	s, _ := startServer(t, time.Hour)
	link, err := s.BootstrapURLFor(EditorSurface, url.Values{"draft": {strings.Repeat("a", 32)}})
	if err != nil {
		t.Fatal(err)
	}
	resp := send(t, http.MethodGet, link, nil, nil)
	if got, want := resp.Header.Get("Location"), s.prefixPath()+"s/editor/?draft="+strings.Repeat("a", 32); resp.StatusCode != http.StatusSeeOther || got != want {
		t.Fatalf("redirect = %d %q, want %q", resp.StatusCode, got, want)
	}
	// The next plain link opens the index again: the target went with its token.
	plain, _ := s.BootstrapURL()
	if got := send(t, http.MethodGet, plain, nil, nil).Header.Get("Location"); got != s.prefixPath() {
		t.Fatalf("plain link redirect = %q, want the index", got)
	}
	if _, err := s.BootstrapURLFor("../x", nil); err == nil {
		t.Fatal("BootstrapURLFor accepted a bad surface name")
	}
}

func TestOpenDraftResolvesTheEntryPoints(t *testing.T) {
	env := startEditor(t)
	editor := NewEditor(EditorConfig{Sources: env.sources})
	for ref, want := range map[string]string{"": "diamond", "default": "default-copy"} {
		id, err := editor.OpenDraft(ref, false)
		if err != nil {
			t.Fatalf("OpenDraft(%q): %v", ref, err)
		}
		d, _ := loadDraft(id)
		if name := draftName(d.Doc); name != want {
			t.Errorf("OpenDraft(%q) name = %q, want %q", ref, name, want)
		}
		if !checkDoc(d.Doc).Valid {
			t.Errorf("OpenDraft(%q) is not valid: %+v", ref, checkDoc(d.Doc).Problems)
		}
	}
	if _, err := editor.OpenDraft("ghost", false); err == nil {
		t.Fatal("OpenDraft of a missing entry succeeded")
	}
}

func TestViewerLinksToTheEditorOnlyWhenServed(t *testing.T) {
	view := NewPipelineView(pipelineWorkspace(t), "")
	s, cookie := startPipeline(t, view)
	link := `href="../editor/?from=active"`
	if page := readBody(t, send(t, http.MethodGet, s.at("/s/pipeline/"), nil, cookie)); strings.Contains(page, link) {
		t.Fatal("the viewer links to an editor that this server does not serve")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := NewEditor(EditorConfig{}).Attach(s); err != nil {
		t.Fatal(err)
	}
	if page := readBody(t, send(t, http.MethodGet, s.at("/s/pipeline/"), nil, cookie)); !strings.Contains(page, link+`>Edit a copy</a>`) {
		t.Fatal("the viewer lacks the Edit a copy link")
	}
}

// TestEditorBootSaysWhetherTheViewerIsServed pins the boot flag that keeps
// the standalone editor from asking for the viewer's graph: false on a
// server with the editor only, true when the server also has the viewer.
func TestEditorBootSaysWhetherTheViewerIsServed(t *testing.T) {
	env := startEditor(t)
	id := env.newDraft(t, fromNew, "")
	viewerOf := func() bool {
		t.Helper()
		_, body := env.raw(t, http.MethodGet, "?draft="+id, nil, nil)
		m := regexp.MustCompile(`data-boot="([^"]*)"`).FindSubmatch(body)
		if m == nil {
			t.Fatal("editor page has no boot data")
		}
		var boot struct {
			Viewer *bool `json:"viewer"`
		}
		if err := json.Unmarshal([]byte(htmlUnescape(string(m[1]))), &boot); err != nil || boot.Viewer == nil {
			t.Fatalf("boot data: %v, viewer %v", err, boot.Viewer)
		}
		return *boot.Viewer
	}
	if viewerOf() {
		t.Fatal("a server with the editor only says that it has the viewer")
	}
	if err := NewPipelineView(env.sources, "").Attach(env.s); err != nil {
		t.Fatal(err)
	}
	if !viewerOf() {
		t.Fatal("a server with the viewer says that it has none")
	}
}

// TestEditorJSONIsThePublishedFile pins the JSON tab: the document in the
// check is byte for byte the file that publish writes.
func TestEditorJSONIsThePublishedFile(t *testing.T) {
	env := startEditor(t)
	id := env.newDraft(t, fromNew, "")
	reply := env.save(t, id, reviewTopology("team"))
	if reply.Document == "" {
		t.Fatal("a valid draft has no publish document")
	}
	_, c := env.consent(t, id)
	if status, body := env.call(t, http.MethodPost, "publish?id="+id, map[string]any{"confirm": true, "digest": c.Digest}); status != http.StatusOK {
		t.Fatalf("publish: %d %s", status, body)
	}
	written, _ := os.ReadFile(filepath.Join(env.config, "splice", "pipelines", "team.json"))
	if string(written) != reply.Document {
		t.Fatalf("publish wrote another document than the JSON tab showed:\n%s\n---\n%s", written, reply.Document)
	}
	broken := reviewTopology("team")
	broken.Nodes[2].Tools = nil
	if reply := env.save(t, id, broken); reply.Document != "" {
		t.Fatal("an invalid draft shows a publish document")
	}
}

func TestEditorStartsFromAShippedExample(t *testing.T) {
	env := startEditor(t)
	examples := exampleTopologies()
	if len(examples) < 2 {
		t.Fatalf("examples = %d, want the docs/examples pipelines", len(examples))
	}
	id := env.newDraft(t, fromExample, examples[0].Name)
	d, _ := loadDraft(id)
	if draftName(d.Doc) != examples[0].Name || !checkDoc(d.Doc).Valid || !strings.HasPrefix(d.Source, "example ") {
		t.Fatalf("example draft = %s (%s)", draftName(d.Doc), d.Source)
	}
	if status, _ := env.call(t, http.MethodPost, "drafts", map[string]string{"from": fromExample, "name": "ghost"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("a missing example: %d, want 422", status)
	}
	_, body := env.raw(t, http.MethodGet, "", nil, nil)
	if !strings.Contains(string(body), `data-from="example" data-name="`+examples[0].Name+`"`) {
		t.Fatal("the drafts page does not offer the examples")
	}
}

func TestEditorAutoLayoutIsTheViewerLayout(t *testing.T) {
	env := startEditor(t)
	status, body := env.call(t, http.MethodPost, "layout", map[string]any{"doc": pipelineDiamond()})
	if status != http.StatusOK {
		t.Fatalf("layout: %d %s", status, body)
	}
	var reply struct {
		Positions []position
		Paths     []layoutRoute
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		t.Fatal(err)
	}
	want, routes := layoutTopology(pipelineDiamond())
	if len(reply.Positions) != len(want) || reply.Positions[3] != want[3] || reply.Positions[0] == reply.Positions[3] {
		t.Fatalf("positions = %+v, want the viewer layout %+v", reply.Positions, want)
	}
	// The routed paths are the viewer's edges, one per edge, keyed by its ends.
	graph, err := presentrun.BuildGraph(pipelineDiamond(), presentation.State{})
	if err != nil {
		t.Fatal(err)
	}
	viewer := layoutGraph(graph)
	if len(reply.Paths) != len(viewer.Edges) || fmt.Sprint(reply.Paths) != fmt.Sprint(routes) {
		t.Fatalf("paths = %+v, want %+v", reply.Paths, routes)
	}
	for i, e := range viewer.Edges {
		if p := reply.Paths[i]; p.From != e.From || p.To != e.To || p.D != e.Path || p.X != e.MidX || p.Y != e.MidY {
			t.Fatalf("path %d = %+v, want the viewer edge %s -> %s %q at (%d, %d)", i, p, e.From, e.To, e.Path, e.MidX, e.MidY)
		}
	}
	if status, _ := env.call(t, http.MethodPost, "layout", map[string]any{"doc": "x"}); status != http.StatusBadRequest {
		t.Fatalf("layout of a bad document: %d, want 400", status)
	}
}

// TestEditorLaysOutAnOldDraftOnceWhenItOpens pins the upgrade of a draft from
// the top-to-bottom layout: the first open lays it out left to right and
// stores the marker, a save keeps the marker, and a later open keeps the
// positions that the user set.
func TestEditorLaysOutAnOldDraftOnceWhenItOpens(t *testing.T) {
	env := startEditor(t)
	id := env.newDraft(t, fromActive, "")
	d, err := loadDraft(id)
	if err != nil || !d.LeftToRight {
		t.Fatalf("a new draft: marker %v, err %v; want the marker", d.LeftToRight, err)
	}
	old := []position{{X: 12, Y: 12}, {X: 12, Y: 140}, {X: 232, Y: 140}, {X: 12, Y: 268}}
	d.Positions, d.LeftToRight = old, false
	if err := saveDraft(d); err != nil {
		t.Fatal(err)
	}
	open := func() draft {
		t.Helper()
		if status, body := env.raw(t, http.MethodGet, "?draft="+id, nil, nil); status != http.StatusOK {
			t.Fatalf("open: %d %s", status, body)
		}
		d, err := loadDraft(id)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	want := initialPositions(pipelineDiamond())
	if got := open(); !got.LeftToRight || fmt.Sprint(got.Positions) != fmt.Sprint(want) || !got.Updated.Equal(d.Updated) {
		t.Fatalf("after the first open: marker %v, positions %v, updated %v; want the marker, %v, and no new edit time", got.LeftToRight, got.Positions, got.Updated, want)
	}
	moved := []position{{X: 500, Y: 40}, {X: 12, Y: 12}, {X: 12, Y: 300}, {X: 900, Y: 40}}
	if status, body := env.call(t, http.MethodPut, "draft?id="+id, map[string]any{"doc": pipelineDiamond(), "positions": moved}); status != http.StatusOK {
		t.Fatalf("save: %d %s", status, body)
	}
	if got := open(); !got.LeftToRight || fmt.Sprint(got.Positions) != fmt.Sprint(moved) {
		t.Fatalf("after a save and a second open: marker %v, positions %v; want the marker and %v", got.LeftToRight, got.Positions, moved)
	}
}

// TestEditorUpgradeIsSafeWithTwoTabsAndAFailedWrite pins the one-time
// layout upgrade under concurrent page loads and saves (run with -race), and
// that a write error keeps the old draft on disk and still serves the page.
func TestEditorUpgradeIsSafeWithTwoTabsAndAFailedWrite(t *testing.T) {
	env := startEditor(t)
	old := []position{{X: 12, Y: 12}, {X: 12, Y: 140}, {X: 232, Y: 140}, {X: 12, Y: 268}}
	makeOld := func() (string, draft) {
		t.Helper()
		d, err := loadDraft(env.newDraft(t, fromDefault, ""))
		if err != nil {
			t.Fatal(err)
		}
		d.Doc, _ = json.Marshal(pipelineDiamond())
		d.Positions, d.LeftToRight = old, false
		if err := saveDraft(d); err != nil {
			t.Fatal(err)
		}
		return d.ID, d
	}
	id, before := makeOld()
	page := func() error {
		req, err := http.NewRequest(http.MethodGet, env.s.at("/s/editor/?draft="+id), nil)
		if err != nil {
			return err
		}
		req.AddCookie(env.cookie)
		resp, err := noRedirect().Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return errors.New("open: " + resp.Status)
		}
		return nil
	}
	errs := make(chan error, 20)
	for range 10 {
		go func() { errs <- page() }()
	}
	for range 10 {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	after, err := loadDraft(id)
	if err != nil {
		t.Fatal(err)
	}
	if !after.LeftToRight || fmt.Sprint(after.Positions) != fmt.Sprint(initialPositions(pipelineDiamond())) || !after.Updated.Equal(before.Updated) {
		t.Fatalf("after concurrent opens: marker %v, positions %v, updated changed %v", after.LeftToRight, after.Positions, !after.Updated.Equal(before.Updated))
	}

	// A write error: the drafts directory refuses the new file.
	id, _ = makeOld()
	dir, err := draftsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if probe := filepath.Join(dir, "probe"); os.WriteFile(probe, nil, 0o600) == nil {
		_ = os.Remove(probe)
		t.Skip("the drafts directory is writable for this user")
	}
	if err := page(); err != nil {
		t.Fatal(err)
	}
	kept, err := loadDraft(id)
	if err != nil {
		t.Fatalf("the draft is lost after a failed upgrade write: %v", err)
	}
	if kept.LeftToRight || fmt.Sprint(kept.Positions) != fmt.Sprint(old) {
		t.Fatalf("after a failed write: marker %v, positions %v; want the old draft unchanged", kept.LeftToRight, kept.Positions)
	}
}

// TestEditorResumesTheLatestDraftOfEachSource pins resume: each source keeps
// one working draft, a fresh start makes another, and the drafts page names
// the source of each.
func TestEditorResumesTheLatestDraftOfEachSource(t *testing.T) {
	env := startEditor(t)
	editor := NewEditor(EditorConfig{Sources: env.sources})
	open := func(ref string, fresh bool) string {
		t.Helper()
		id, err := editor.OpenDraft(ref, fresh)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	project, def := open("", false), open("default", false)
	if project == def {
		t.Fatal("the project copy and the default copy share a draft")
	}
	if open("", false) != project || open("default", false) != def {
		t.Fatal("a second edit of the same source did not resume its draft")
	}
	fresh := open("default", true)
	if fresh == def {
		t.Fatal("--new resumed a draft")
	}
	if open("default", false) != fresh {
		t.Fatal("resume did not take the latest draft of the source")
	}
	// The drafts page offers the source of each draft, and its start buttons
	// ask for a fresh draft.
	_, body := env.raw(t, http.MethodGet, "", nil, nil)
	for _, want := range []string{"copy of the project file ", "copy of the embedded default"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("drafts page lacks the source %q", want)
		}
	}
	status, reply := env.call(t, http.MethodPost, "drafts", map[string]any{"from": "default", "new": true})
	var made struct{ ID string }
	if status != http.StatusOK || json.Unmarshal(reply, &made) != nil || made.ID == fresh {
		t.Fatalf("a fresh create from the page: %d %s", status, reply)
	}
	if id := env.newDraft(t, fromDefault, ""); id != made.ID {
		t.Fatalf("a create without new = %s, want the resume of the latest draft %s", id, made.ID)
	}
}

func TestParseEditArgs(t *testing.T) {
	cases := []struct {
		args  []string
		ref   string
		fresh bool
		bad   bool
	}{
		{nil, "", false, false},
		{[]string{"team"}, "team", false, false},
		{[]string{"--new"}, "", true, false},
		{[]string{"--new", "default"}, "default", true, false},
		{[]string{"default", "--new"}, "default", true, false},
		{[]string{"a", "b"}, "", false, true},
		{[]string{"--yes"}, "", false, true},
	}
	for _, tc := range cases {
		ref, fresh, err := ParseEditArgs(tc.args)
		if (err != nil) != tc.bad || ref != tc.ref || fresh != tc.fresh {
			t.Errorf("ParseEditArgs(%q) = %q, %v, %v", tc.args, ref, fresh, err)
		}
	}
}

// TestEditorSavesAnOversizeDraft pins that a draft over the topology size
// limit still saves, as an invalid draft whose problem names the limit, so
// no work is lost.
func TestEditorSavesAnOversizeDraft(t *testing.T) {
	env := startEditor(t)
	id := env.newDraft(t, fromNew, "")
	big := pipelineDiamond()
	big.Nodes, big.Edges = nil, nil
	for i := 0; i <= schemas.MaxTopologyNodes; i++ {
		big.Nodes = append(big.Nodes, schemas.PipelineNode{Name: "n" + strconv.Itoa(i), Type: schemas.NodeTypeCommand, Command: []string{"true"}})
	}
	reply := env.save(t, id, big)
	if reply.Valid || len(reply.Problems) == 0 || !strings.Contains(reply.Problems[0].Message, "exceed the limit") {
		t.Fatalf("save of an oversize draft: valid %v, problems %+v", reply.Valid, reply.Problems)
	}
	d, err := loadDraft(id)
	if err != nil {
		t.Fatal(err)
	}
	var saved schemas.PipelineTopology
	if err := json.Unmarshal(d.Doc, &saved); err != nil || len(saved.Nodes) != schemas.MaxTopologyNodes+1 {
		t.Fatalf("the oversize draft was not saved whole: %d nodes, err %v", len(saved.Nodes), err)
	}
}
