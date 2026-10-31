package web

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// startPipeline starts a server with the view attached and returns the
// session cookie.
func startPipeline(t *testing.T, view *PipelineView) (*Server, *http.Cookie) {
	t.Helper()
	s, boot := startServer(t, time.Hour)
	if err := view.Attach(s); err != nil {
		t.Fatal(err)
	}
	return s, sessionFor(t, s, boot)
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestPipelineSurfaceNeedsTheSessionCookie(t *testing.T) {
	s, cookie := startPipeline(t, NewPipelineView(pipelineWorkspace(t), ""))
	for _, sub := range []string{"", "pipeline.js", "pipeline.css", "graph", "node?name=lint", "timeline"} {
		if resp := send(t, http.MethodGet, s.at("/s/pipeline/"+sub), nil, nil); resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET /s/pipeline/%s without a cookie: status %d, want 403", sub, resp.StatusCode)
		}
	}
	resp := send(t, http.MethodGet, s.at("/s/pipeline/"), nil, cookie)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("page: status %d, type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	page := readBody(t, resp)
	// No run yet: the static graph of the active topology, with no fetch.
	if strings.Contains(page, "node(s)") {
		t.Error("the command count says node(s)")
	}
	for _, want := range []string{`<svg class="graph"`, `data-name="test_runner"`, "No run yet", "source: project",
		// Only the command line is code; the count is prose.
		`<span class="command-name">lint</span> <code>go vet ./...</code>`, "Active topology at page load. 1 command node runs shell commands when this topology is active."} {
		if !strings.Contains(page, want) {
			t.Errorf("static page lacks %q", want)
		}
	}
	for sub, want := range map[string]string{"pipeline.js": "text/javascript", "pipeline.css": "text/css"} {
		resp := send(t, http.MethodGet, s.at("/s/pipeline/"+sub), nil, cookie)
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), want) {
			t.Errorf("%s: status %d, type %q", sub, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
	if resp := send(t, http.MethodGet, s.at("/s/pipeline/nope"), nil, cookie); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown part: status %d, want 404", resp.StatusCode)
	}
	if resp := send(t, http.MethodPost, s.at("/s/pipeline/node"), map[string]string{"Origin": s.origin, "X-Splice-Action": actionOf(s)}, cookie); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST to the read-only surface: status %d, want 405", resp.StatusCode)
	}
}

func drawerOf(t *testing.T, s *Server, cookie *http.Cookie, name string) (int, nodeData, string) {
	t.Helper()
	resp := send(t, http.MethodGet, s.at("/s/pipeline/node?name="+name), nil, cookie)
	raw := readBody(t, resp)
	var data nodeData
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			t.Fatalf("drawer JSON: %v\n%s", err, raw)
		}
	}
	return resp.StatusCode, data, raw
}

func fieldValue(data nodeData, key string) string {
	for _, field := range data.Fields {
		if field.Key == key {
			return field.Value
		}
	}
	return ""
}

func TestPipelineDrawerReturnsTheViewModel(t *testing.T) {
	view := NewPipelineView(pipelineWorkspace(t), "")
	s, cookie := startPipeline(t, view)
	view.Observe(pipelineRun(t))

	status, data, _ := drawerOf(t, s, cookie, "lint")
	if status != http.StatusOK {
		t.Fatalf("drawer for lint: status %d", status)
	}
	// The fields are the terminal drawer's fields, from the same functions.
	graph, err := presentrun.BuildGraph(pipelineDiamond(), pipelineRun(t))
	if err != nil {
		t.Fatal(err)
	}
	node := graphNode(graph, "lint")
	want := splicerun.NodeDrawerFields(graph, node, splicerun.ResolveNodeDrawer(graph, node, pipelineDiamond(), ""))
	if len(data.Fields) != len(want) {
		t.Fatalf("drawer fields = %v, want %v", data.Fields, want)
	}
	for i := range want {
		if data.Fields[i] != want[i] {
			t.Errorf("field %d = %v, want %v", i, data.Fields[i], want[i])
		}
	}
	for key, value := range map[string]string{"status": "failed", "command": "go vet ./...", "result": "failed; the summary names the cause", "summary": "check failed: exit status 1"} {
		if got := fieldValue(data, key); got != value {
			t.Errorf("drawer %s = %q, want %q", key, got, value)
		}
	}
	if status, _, _ := drawerOf(t, s, cookie, "no_such_node"); status != http.StatusNotFound {
		t.Fatalf("unknown node: status %d, want 404", status)
	}
}

// TestPipelineObserveUpdatesDrawerAndStream pins the live path: a snapshot
// changes the state the drawer reads, and only the changed node streams.
func TestPipelineObserveUpdatesDrawerAndStream(t *testing.T) {
	view := NewPipelineView(pipelineWorkspace(t), "")
	s, cookie := startPipeline(t, view)
	events, unsubscribe := s.hub.subscribe()
	defer unsubscribe()

	view.Observe(pipelineRun(t))
	s.hub.flush()
	// The first snapshot is a new layout: one resync, and the page loads the
	// whole graph.
	first := byName(events)
	if data := graphEventOf(t, first); !data.Resync || len(first[pipelineNodeEvent]) != 0 {
		t.Fatalf("first snapshot events = %v (%+v), want one resync", names(first), data)
	}

	view.Observe(pipelineRun(t,
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "static_analyzer", Status: "running"}),
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "static_analyzer", Status: "completed", Detail: "no findings"}),
	))
	s.hub.flush()
	second := byName(events)
	if len(second[pipelineGraphEvent]) != 0 || len(second[pipelineNodeEvent]) != 1 {
		t.Fatalf("second snapshot events = %v, want one node event", names(second))
	}
	var frame nodeFrame
	if err := json.Unmarshal([]byte(second[pipelineNodeEvent][0]), &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Name != "static_analyzer" || frame.Status != "complete" || frame.Iteration != 0 {
		t.Fatalf("node frame = %+v", frame)
	}
	if _, data, _ := drawerOf(t, s, cookie, "static_analyzer"); fieldValue(data, "summary") != "no findings" || fieldValue(data, "status") != "passed" {
		t.Fatalf("drawer after the frame = %v", data.Fields)
	}
}

// byName groups the waiting events by event name.
func byName(events <-chan Event) map[string][]string {
	out := map[string][]string{}
	for _, ev := range drain(events) {
		out[ev.Name] = append(out[ev.Name], ev.Data)
	}
	return out
}

func names(events map[string][]string) map[string]int {
	out := map[string]int{}
	for name, list := range events {
		out[name] = len(list)
	}
	return out
}

func TestPipelineTimelineFollowsRecordOrder(t *testing.T) {
	view := NewPipelineView(pipelineWorkspace(t), "")
	s, cookie := startPipeline(t, view)
	view.Observe(pipelineRun(t))
	summary := "done"
	result := schemas.PipelineResult{Status: "completed", Stages: []schemas.StageRecord{
		{Name: "code_writer", Status: schemas.StageCompleted, Iteration: 1, TokensInput: 1000, TokensOutput: 200, CostUSD: 0.01, OutputSummary: &summary},
		{Name: "lint", Status: schemas.StageFailed, Iteration: 1},
		{Name: "code_writer", Status: schemas.StageCompleted, Iteration: 2},
		{Name: "lint", Status: schemas.StageCompleted, Iteration: 2},
	}}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	view.RecordResult(string(encoded))
	view.RecordResult("not a pipeline result") // ignored

	resp := send(t, http.MethodGet, s.at("/s/pipeline/timeline"), nil, cookie)
	var data timelineData
	if err := json.Unmarshal([]byte(readBody(t, resp)), &data); err != nil {
		t.Fatal(err)
	}
	// Step 0 is the plan; then one step per record, the last one included.
	wantCaptions := []string{planCaption, "code_writer · passed", "lint · failed", "code_writer · passed · pass 2", "lint · passed · pass 2"}
	if len(data.Steps) != len(wantCaptions) {
		t.Fatalf("steps = %d, want %d", len(data.Steps), len(wantCaptions))
	}
	for i, want := range wantCaptions {
		if data.Steps[i].Caption != want {
			t.Errorf("step %d caption = %q, want %q", i, data.Steps[i].Caption, want)
		}
	}
	frameAt := func(step int, name string) nodeFrame {
		for _, frame := range data.Steps[step].Nodes {
			if frame.Name == name {
				return frame
			}
		}
		t.Fatalf("step %d has no %s", step, name)
		return nodeFrame{}
	}
	if f := frameAt(0, "code_writer"); f.Status != "pending" {
		t.Errorf("step 0 code_writer = %+v, want pending: nothing has run", f)
	}
	if f := frameAt(1, "lint"); f.Status != "pending" {
		t.Errorf("step 1 lint = %+v, want pending", f)
	}
	if f := frameAt(1, "code_writer"); f.Status != "complete" || !strings.Contains(f.Meta, "1.2K tok") {
		t.Errorf("step 1 code_writer = %+v, want complete with its tokens", f)
	}
	if f := frameAt(2, "lint"); f.Status != "failed" {
		t.Errorf("step 2 lint = %+v, want failed", f)
	}
	if f := frameAt(4, "lint"); f.Status != "complete" || f.Iteration != 1 {
		t.Errorf("step 4 lint = %+v, want the second pass complete", f)
	}

	// The drawer describes a node at a step, not at the end of the run.
	stepDrawer := func(step string) (int, nodeData) {
		resp := send(t, http.MethodGet, s.at("/s/pipeline/node?name=lint&step="+step), nil, cookie)
		var data nodeData
		if resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal([]byte(readBody(t, resp)), &data); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode, data
	}
	if _, at1 := stepDrawer("1"); fieldValue(at1, "status") != "not started" {
		t.Errorf("lint drawer at step 1: status %q, want not started", fieldValue(at1, "status"))
	}
	if _, at2 := stepDrawer("2"); fieldValue(at2, "status") != "failed" {
		t.Errorf("lint drawer at step 2: status %q, want failed", fieldValue(at2, "status"))
	}
	if status, _ := stepDrawer("9"); status != http.StatusNotFound {
		t.Errorf("drawer at an unknown step: status %d, want 404", status)
	}

	// A new run clears the timeline.
	view.BeginRun(nil)
	view.Observe(presentation.State{Plan: presentation.Plan{Stages: []string{"code_writer"}}})
	resp = send(t, http.MethodGet, s.at("/s/pipeline/timeline"), nil, cookie)
	if body := readBody(t, resp); body != `{"steps":[]}` {
		t.Fatalf("timeline after a new run = %s", body)
	}
}

// TestPipelinePageIsCSPCompatible pins section 7: no inline script, no event
// handler attributes, no inline styles, and no external URL in any asset.
func TestPipelinePageIsCSPCompatible(t *testing.T) {
	s, cookie := startPipeline(t, NewPipelineView(pipelineWorkspace(t), ""))
	page := readBody(t, send(t, http.MethodGet, s.at("/s/pipeline/"), nil, cookie))
	scripts := regexp.MustCompile(`(?i)<script\b[^>]*>`).FindAllString(page, -1)
	if len(scripts) != 1 || !strings.Contains(scripts[0], `src="pipeline.js"`) {
		t.Fatalf("script tags = %q, want only the same-origin pipeline.js", scripts)
	}
	if regexp.MustCompile(`(?i)<script\b[^>]*>[^<]+</script>`).MatchString(page) {
		t.Fatal("page has an inline script body")
	}
	if regexp.MustCompile(`(?i)\son[a-z]+\s*=|\sstyle\s*=|<style\b`).MatchString(page) {
		t.Fatal("page has an inline handler or inline style; CSP default-src 'self' blocks both")
	}
	assets, err := fs.Glob(assetFS, "assets/*")
	if err != nil || len(assets) < 8 {
		t.Fatalf("assets = %v (%v), want every page, script, and style", assets, err)
	}
	for _, path := range assets {
		data, err := assetFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// The SVG namespace is a name, not a request.
		lower := strings.ReplaceAll(strings.ToLower(string(data)), `xmlns="http://www.w3.org/2000/svg"`, "")
		for _, banned := range []string{"http://", "https://", "//cdn", "@import"} {
			if strings.Contains(lower, banned) {
				t.Errorf("%s contains %q; the surface makes no external request", path, banned)
			}
		}
		// Absolute paths break under a path prefix; every URL is relative.
		if regexp.MustCompile(`(?:href|src)="/|(?:fetch|EventSource)\("/`).MatchString(string(data)) {
			t.Errorf("%s uses an absolute URL; use a URL relative to the page", path)
		}
	}
}

func TestPipelineEscapesAndRedactsRunText(t *testing.T) {
	view := NewPipelineView(pipelineWorkspace(t), "")
	view.SetSecrets([]string{"sk-PIPELINE-SECRET-VALUE"})
	s, cookie := startPipeline(t, view)
	state := pipelineRun(t,
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "static_analyzer", Status: "running"}),
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "static_analyzer", Status: "failed", Detail: `<script>alert(1)</script> key sk-PIPELINE-SECRET-VALUE`}),
	)
	state.Plan.Removed = map[string]string{"test_runner": "<script>bad()</script>"}
	state.Plan.Stages = state.Plan.Stages[:3] // the run removed test_runner
	view.Observe(state)

	page := readBody(t, send(t, http.MethodGet, s.at("/s/pipeline/"), nil, cookie))
	if strings.Contains(page, "<script>bad()") || !strings.Contains(page, "&lt;script&gt;bad()") {
		t.Fatal("the page did not escape a removal reason")
	}
	_, data, raw := drawerOf(t, s, cookie, "static_analyzer")
	if strings.Contains(raw, "<script>") || strings.Contains(raw, "sk-PIPELINE-SECRET-VALUE") {
		t.Fatalf("drawer JSON carries raw markup or the secret: %s", raw)
	}
	if got := fieldValue(data, "summary"); !strings.HasPrefix(got, "<script>alert(1)</script>") || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("drawer summary = %q, want the text with the secret redacted", got)
	}
}

// TestStopDeliversPendingEvents pins that a run's last events, such as the
// timeline, reach open streams when the server stops at the end of the run.
func TestStopDeliversPendingEvents(t *testing.T) {
	s, _ := startServer(t, time.Hour)
	events, unsubscribe := s.hub.subscribe()
	defer unsubscribe()
	s.Publish(Event{Key: "last", Name: "pipeline-timeline", Data: "{}"})
	s.Stop()
	select {
	case ev, ok := <-events:
		if !ok || ev.Key != "last" {
			t.Fatalf("first event after Stop = %+v (open %v), want the pending event", ev, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event after Stop")
	}
}

// TestBootstrapLandsOnThePipeline pins that /web and exec --serve open the
// pipeline page under the server's prefix, and that the index stays.
func TestBootstrapLandsOnThePipeline(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	if err := NewPipelineView(pipelineWorkspace(t), "").Attach(s); err != nil {
		t.Fatal(err)
	}
	resp := send(t, http.MethodGet, boot, nil, nil)
	want := s.prefixPath() + "s/pipeline/"
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != want {
		t.Fatalf("bootstrap: status %d, Location %q; want 303 to %s", resp.StatusCode, resp.Header.Get("Location"), want)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == s.cookieName {
			cookie = c
		}
	}
	if resp := send(t, http.MethodGet, s.at("/"), nil, cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("index after landing: status %d, want 200", resp.StatusCode)
	}
}

// TestPipelineEndRunShowsTheReceipt pins that a run that stops without a
// completion snapshot (a cancel skips the pipeline's end event) ends on the
// page as the terminal receipt says, and that a node still running then
// shows that it stopped.
func TestPipelineEndRunShowsTheReceipt(t *testing.T) {
	view := NewPipelineView(pipelineWorkspace(t), "")
	s, cookie := startPipeline(t, view)
	view.Observe(pipelineRun(t, presentrun.AdaptStageEvent(agent.StageEvent{Name: "static_analyzer", Status: "running"})))
	view.EndRun("cancelled", "")

	page := readBody(t, send(t, http.MethodGet, s.at("/s/pipeline/"), nil, cookie))
	for _, want := range []string{`data-state="cancelled"`, "Cancelled.", `class="node s-stopped" data-name="static_analyzer"`, "stopped when the run ended"} {
		if !strings.Contains(page, want) {
			t.Fatalf("page after a cancel lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "Running.") {
		t.Fatal("a cancelled run still says Running.")
	}
	// A run with its completion keeps it.
	view.EndRun("failed", "second-end-detail")
	if page := readBody(t, send(t, http.MethodGet, s.at("/s/pipeline/"), nil, cookie)); strings.Contains(page, "second-end-detail") {
		t.Fatal("EndRun replaced an existing completion")
	}
}

// graphEventOf decodes the one pipeline-graph event of a flush.
func graphEventOf(t *testing.T, events map[string][]string) graphData {
	t.Helper()
	if len(events[pipelineGraphEvent]) != 1 {
		t.Fatalf("graph events = %d, want 1 (%v)", len(events[pipelineGraphEvent]), names(events))
	}
	var data graphData
	if err := json.Unmarshal([]byte(events[pipelineGraphEvent][0]), &data); err != nil {
		t.Fatal(err)
	}
	return data
}

// TestPipelineSecondRunReloadsTheGraph pins item 1 of the review: a second run
// in the same session tells the page to load the graph again, so a node that
// reached pass 2 in the first run does not keep that pass, and every node
// event names its run.
func TestPipelineSecondRunReloadsTheGraph(t *testing.T) {
	view := NewPipelineView(pipelineWorkspace(t), "")
	s, cookie := startPipeline(t, view)
	events, unsubscribe := s.hub.subscribe()
	defer unsubscribe()

	view.BeginRun(nil)
	view.Observe(pipelineRun(t, presentrun.AdaptStageEvent(agent.StageEvent{Name: "code_writer", Iteration: 1, Status: "running"})))
	s.hub.flush()
	if first := graphEventOf(t, byName(events)); first.Run != 1 {
		t.Fatalf("first run = %d, want 1", first.Run)
	}

	view.BeginRun(nil)
	view.Observe(pipelineRun(t))
	s.hub.flush()
	second := byName(events)
	data := graphEventOf(t, second)
	if data.Run != 2 || !data.Resync {
		t.Fatalf("second run graph event = %+v, want run 2 with a resync", data)
	}
	var graph graphData
	if err := json.Unmarshal([]byte(readBody(t, send(t, http.MethodGet, s.at("/s/pipeline/graph"), nil, cookie))), &graph); err != nil {
		t.Fatal(err)
	}
	if graph.Run != 2 || !strings.Contains(graph.SVG, `data-name="code_writer" data-iteration="0"`) {
		t.Fatalf("graph endpoint after the second run: run %d, svg %s", graph.Run, graph.SVG)
	}

	view.Observe(pipelineRun(t, presentrun.AdaptStageEvent(agent.StageEvent{Name: "static_analyzer", Status: "running"})))
	s.hub.flush()
	nodes := byName(events)[pipelineNodeEvent]
	if len(nodes) != 1 || !strings.Contains(nodes[0], `"run_seq":2`) {
		t.Fatalf("node events = %v, want one event of run 2", nodes)
	}
}

// wideTopology is one root and fanout parallel children.
func wideTopology(fanout int) *schemas.PipelineTopology {
	topology := &schemas.PipelineTopology{Version: schemas.TopologySchemaVersion, Name: "wide", Nodes: []schemas.PipelineNode{{Name: "code_writer", Type: "code_writer"}}}
	for i := 0; i < fanout; i++ {
		name := fmt.Sprintf("check_%02d", i)
		topology.Nodes = append(topology.Nodes, schemas.PipelineNode{Name: name, Type: schemas.NodeTypeCommand, Command: []string{"true"}})
		topology.Edges = append(topology.Edges, schemas.PipelineEdge{From: "code_writer", To: name})
	}
	return topology
}

// TestPipelineLargeChangeIsOneResync pins item 3 of the review: a snapshot
// that changes more nodes than a client can buffer sends one resync event,
// never a burst that drops the client.
func TestPipelineLargeChangeIsOneResync(t *testing.T) {
	view := NewPipelineView(splicerun.TopologySources{UserConfigDir: t.TempDir()}, "")
	s, _ := startPipeline(t, view)
	events, unsubscribe := s.hub.subscribe()
	defer unsubscribe()
	topology := wideTopology(39) // 40 nodes
	var roster []string
	deps := map[string][]string{}
	for _, node := range topology.Nodes {
		roster = append(roster, node.Name)
	}
	for _, edge := range topology.Edges {
		deps[edge.To] = append(deps[edge.To], edge.From)
	}
	plan, err := presentation.Apply(presentation.State{}, presentation.PlanEvent{StageNames: roster, Dependencies: deps})
	if err != nil {
		t.Fatal(err)
	}
	view.BeginRun(nil)
	view.Observe(plan)
	s.hub.flush()
	_ = byName(events)

	all := plan
	for _, name := range roster {
		if all, err = presentation.Apply(all, presentrun.AdaptStageEvent(agent.StageEvent{Name: name, Status: "running"})); err != nil {
			t.Fatal(err)
		}
	}
	view.Observe(all)
	s.hub.flush()
	burst := byName(events)
	if data := graphEventOf(t, burst); !data.Resync {
		t.Fatalf("40 changed nodes: graph event %+v, want a resync", data)
	}
	if n := len(burst[pipelineNodeEvent]); n != 0 {
		t.Fatalf("40 changed nodes sent %d node events, want none next to the resync", n)
	}

	few, err := presentation.Apply(all, presentrun.AdaptStageEvent(agent.StageEvent{Name: "check_00", Status: "completed"}))
	if err != nil {
		t.Fatal(err)
	}
	view.Observe(few)
	s.hub.flush()
	if got := byName(events); len(got[pipelineNodeEvent]) != 1 || len(got[pipelineGraphEvent]) != 0 {
		t.Fatalf("one changed node: events %v, want one node event", names(got))
	}
}

// TestPipelineDrawerCarriesTheBudgetUse pins item 15 of the review: the
// drawer payload carries the latest pass's use of the applied budget, from
// the same function as the budget text.
func TestPipelineDrawerCarriesTheBudgetUse(t *testing.T) {
	view := NewPipelineView(pipelineWorkspace(t), "")
	s, cookie := startPipeline(t, view)
	view.Observe(pipelineRun(t,
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "code_writer", Iteration: 1, Status: "running"}),
		presentation.RouteEvent{NodeID: "code_writer", Iteration: 1, Route: presentation.NodeRoute{Model: "m", InputBudget: 6000, OutputBudget: 900}},
		presentation.UsageEvent{NodeID: "code_writer", Iteration: 1, Usage: presentation.TokenUsage{InputTokens: 4000, OutputTokens: 1000}},
	))
	_, data, _ := drawerOf(t, s, cookie, "code_writer")
	want := splicerun.BudgetUse{InputUsed: 4000, InputMax: 6000, OutputUsed: 1000, OutputMax: 900}
	if data.Budget == nil || *data.Budget != want {
		t.Fatalf("budget = %+v, want %+v", data.Budget, want)
	}
	if got := fieldValue(data, "budget"); got != "input 4K / 6K · output 1K / 900" {
		t.Fatalf("budget text = %q", got)
	}
	if _, lint, _ := drawerOf(t, s, cookie, "lint"); lint.Budget != nil {
		t.Fatalf("a model-free node has a budget: %+v", lint.Budget)
	}
}

// TestPipelineRedactsNamesAndTypes pins minor item d of the review: the
// topology name, node names, and type subtitles pass the same redaction as
// run text, and a redacted node is still one key for the drawer.
func TestPipelineRedactsNamesAndTypes(t *testing.T) {
	view := NewPipelineView(pipelineWorkspace(t), "")
	view.SetSecrets([]string{"diamond", "lint"})
	s, cookie := startPipeline(t, view)
	view.Observe(pipelineRun(t))

	page := readBody(t, send(t, http.MethodGet, s.at("/s/pipeline/"), nil, cookie))
	for _, leak := range []string{`data-name="lint"`, `>diamond<`, "<- lint"} {
		if strings.Contains(page, leak) {
			t.Errorf("page leaks %q", leak)
		}
	}
	if !strings.Contains(page, `data-name="[REDACTED]"`) {
		t.Fatal("page lacks the redacted node")
	}
	if status, data, _ := drawerOf(t, s, cookie, url.QueryEscape("[REDACTED]")); status != http.StatusOK || data.Name != "[REDACTED]" {
		t.Fatalf("drawer of the redacted node: status %d, name %q", status, data.Name)
	}
}

// TestPipelineServesItsFavicon pins item 12 of the review: the page links a
// favicon under the surface, so the browser makes no request that 404s.
func TestPipelineServesItsFavicon(t *testing.T) {
	s, cookie := startPipeline(t, NewPipelineView(pipelineWorkspace(t), ""))
	resp := send(t, http.MethodGet, s.at("/s/pipeline/favicon.svg"), nil, cookie)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("favicon: status %d, type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if page := readBody(t, send(t, http.MethodGet, s.at("/s/pipeline/"), nil, cookie)); !strings.Contains(page, `<link rel="icon" href="favicon.svg"`) {
		t.Fatal("the page does not link its favicon")
	}
	index, err := assetFS.ReadFile(indexPath)
	if err != nil || !strings.Contains(string(index), `href="s/pipeline/favicon.svg"`) {
		t.Fatal("the index page does not link the favicon")
	}
}

// TestPipelineGraphDataKeepsTheRunLine pins that the run sequence and the run
// line are two JSON keys: one hid the other, and the page said "1".
func TestPipelineGraphDataKeepsTheRunLine(t *testing.T) {
	data, err := json.Marshal(graphData{Run: 3, pipelineHeader: pipelineHeader{Run: "Running."}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"run_seq":3`) || !strings.Contains(string(data), `"run":"Running."`) {
		t.Fatalf("graph data = %s, want both the run sequence and the run line", data)
	}
}
