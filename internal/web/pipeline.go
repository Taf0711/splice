package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/redaction"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// PipelineSurface is the surface name of the pipeline viewer.
const PipelineSurface = "pipeline"

// Event names on the surface stream. Each one keys by what it replaces, so
// the hub keeps only the latest of each within one flush.
const (
	pipelineGraphEvent    = "pipeline-graph"
	pipelineNodeEvent     = "pipeline-node"
	pipelineTimelineEvent = "pipeline-timeline"
)

// PipelineView is the read-only pipeline viewer. It keeps the latest
// presentation snapshot of a run, the same snapshot the terminal renders, and
// builds the graph from it with presentrun.BuildGraph. With no run it shows
// the active topology as a static graph. A view outlives a server: attach it
// to each new server with Attach.
//
// Every text that leaves the view passes redaction with the run's secret
// values first.
type PipelineView struct {
	mu              sync.Mutex
	server          *Server
	sources         splicerun.TopologySources
	stageConfigPath string
	// source loads the run's exact secret values on first use. A load reads
	// credentials, so it runs only when the view sends run text to a browser.
	source *secretSource

	state presentation.State
	// The run topology resolves once per topology name, like the terminal view.
	runTopologyName string
	runTopology     *schemas.PipelineTopology
	runTopologyNote string

	// What the stream last sent, so an update sends only what changed.
	sentLayout string
	sentHeader pipelineHeader
	sentFrames map[string]nodeFrame

	timeline []timelineStep
	// run counts the runs this view has shown. A page that sees a new run
	// loads the whole graph again, so nothing of the previous run stays.
	run int
}

// maxNodeEvents is the most node events one snapshot sends. A snapshot that
// changes more nodes sends one resync event instead, so a large topology
// cannot overflow a client's event buffer (clientBuffer).
const maxNodeEvents = 16

// NewPipelineView returns a view that shows the topology from sources until a
// run starts. stageConfigPath is the stage-models.json path the drawer reads.
func NewPipelineView(sources splicerun.TopologySources, stageConfigPath string) *PipelineView {
	return &PipelineView{sources: sources, stageConfigPath: stageConfigPath, sentFrames: map[string]nodeFrame{}}
}

// Configure replaces the topology sources and the stage-models.json path. The
// TUI calls it when the workspace or its trust changes.
func (v *PipelineView) Configure(sources splicerun.TopologySources, stageConfigPath string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.sources, v.stageConfigPath = sources, stageConfigPath
	v.runTopologyName = "" // resolve the run topology again from the new sources
}

// secretSource is the run's list of exact secret values, loaded once.
type secretSource struct {
	load   func() ([]string, error)
	loaded bool
	values []string
	err    error
}

// SetSecrets sets the exact secret values to redact, such as the run's
// provider keys. Pattern-matched secrets are redacted anyway.
func (v *PipelineView) SetSecrets(secrets []string) {
	values := append([]string(nil), secrets...)
	v.SetSecretSource(func() ([]string, error) { return values, nil })
}

// SetSecretSource sets the loader of the run's exact secret values. The view
// calls it the first time run text goes to a browser, and never before. If the
// load fails, the view hides the run and shows the static graph with a note.
func (v *PipelineView) SetSecretSource(load func() ([]string, error)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.source = &secretSource{load: load}
}

// BeginRun starts a new run in the view: it counts the run, sets the run's
// secret loader (see SetSecretSource), and drops the previous run's
// timeline. The next snapshot then tells every page to load the graph again.
func (v *PipelineView) BeginRun(secrets func() ([]string, error)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.run++
	v.source = nil // nil secrets: pattern redaction only
	if secrets != nil {
		v.source = &secretSource{load: secrets}
	}
	v.timeline = nil
	v.sentLayout, v.sentHeader, v.sentFrames = "", pipelineHeader{}, map[string]nodeFrame{}
}

// Reset forgets the run: its snapshot, its timeline, and its secret source.
// The TUI calls it when the session changes, so a new session never sees the
// run of the old one.
func (v *PipelineView) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.state = presentation.State{}
	v.timeline = nil
	v.source = nil
	v.runTopologyName, v.runTopology, v.runTopologyNote = "", nil, ""
	v.sentLayout, v.sentHeader, v.sentFrames = "", pipelineHeader{}, map[string]nodeFrame{}
}

// runSecretsLocked returns the run's exact secret values, and loads them on
// the first call. The caller holds mu.
func (v *PipelineView) runSecretsLocked() ([]string, error) {
	if v.source == nil {
		return nil, nil
	}
	if !v.source.loaded {
		v.source.values, v.source.err = v.source.load()
		v.source.loaded = true
	}
	return v.source.values, v.source.err
}

// Attach registers the view as the pipeline surface of server, and makes the
// bootstrap URL open it. Later updates stream to that server.
func (v *PipelineView) Attach(server *Server) error {
	if err := server.Register(PipelineSurface, Surface{Page: v.servePage, Sub: v.serveSub}); err != nil {
		return err
	}
	server.SetLanding(PipelineSurface)
	v.mu.Lock()
	defer v.mu.Unlock()
	v.server = server
	// A new server has new pages: the next snapshot streams every node.
	v.sentLayout, v.sentHeader, v.sentFrames = "", pipelineHeader{}, map[string]nodeFrame{}
	return nil
}

// streaming reports whether a server runs to stream to. The caller holds mu.
func (v *PipelineView) streaming() bool {
	if v.server == nil {
		return false
	}
	select {
	case <-v.server.Done():
		return false
	default:
		return true
	}
}

// Observe records a presentation snapshot of the run and streams the change:
// a header event when the run line or the layout changes, else one node event
// per node whose box changed. A snapshot of a new run clears the timeline.
func (v *PipelineView) Observe(state presentation.State) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.observeLocked(state)
}

// EndRun ends a run that stopped without a completion snapshot, for example a
// run that the user cancelled, or one that failed before the pipeline could
// report its end. status is "cancelled" or "failed", the same receipt the
// terminal shows. A run that already has its completion does not change.
func (v *PipelineView) EndRun(status, detail string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.liveStateLocked() || v.state.Completion != nil {
		return
	}
	ended, err := presentation.Apply(v.state, presentation.RunEvent{Status: status, Detail: detail})
	if err != nil {
		// The reducer refuses a run end outside execution. The run ended
		// anyway, so record the receipt as the terminal does.
		ended = v.state
		ended.Completion = &presentation.CompletionReceipt{Status: status, Detail: detail}
	}
	v.observeLocked(ended)
}

// liveStateLocked reports whether the stored state is a run. The caller holds mu.
func (v *PipelineView) liveStateLocked() bool {
	return len(v.state.Plan.Stages) > 0 || len(v.state.Nodes) > 0
}

// observeLocked is Observe. The caller holds mu.
//
// ponytail: each snapshot rebuilds the whole graph and layout (BuildGraph and
// layoutGraph are linear in the nodes, and a snapshot comes per stage event
// and per usage report). Diff the presentation state instead if a topology
// grows to hundreds of nodes.
func (v *PipelineView) observeLocked(state presentation.State) {
	v.state = state
	if !v.streaming() {
		return // a page that loads later renders the stored state
	}
	view := v.currentLocked()
	layout := layoutGraph(view.graph)
	sent := make(map[string]nodeFrame, len(layout.Nodes))
	var changed []nodeFrame
	for _, box := range layout.Nodes {
		sent[box.Frame.Name] = box.Frame
		if v.sentFrames[box.Frame.Name] != box.Frame {
			changed = append(changed, box.Frame)
		}
	}
	// A new layout, or more changes than a client can buffer, is one resync:
	// the page loads the whole graph.
	resync := layout.Key != v.sentLayout || len(changed) > maxNodeEvents
	if resync || view.header != v.sentHeader {
		v.sentLayout, v.sentHeader = layout.Key, view.header
		v.publish(pipelineGraphEvent, pipelineGraphEvent, v.graphDataLocked(view, layout.Key, resync))
	}
	if !resync {
		for _, frame := range changed {
			v.publish(pipelineNodeEvent+"/"+frame.Name, pipelineNodeEvent, nodeEvent{nodeFrame: frame, Run: v.run})
		}
	}
	v.sentFrames = sent
}

// graphDataLocked is the graph event and the graph endpoint payload. The
// caller holds mu.
func (v *PipelineView) graphDataLocked(view pipelineCurrent, layout string, resync bool) graphData {
	return graphData{Run: v.run, Layout: layout, Resync: resync, pipelineHeader: view.header, Notes: view.notes, HasTimeline: len(v.timeline) > 0}
}

// nodeEvent is one node frame on the stream, with the run it belongs to.
type nodeEvent struct {
	nodeFrame
	Run int `json:"run_seq"`
}

// RecordResult builds the post-run timeline from the stage records of a
// pipeline result (agent.Result.FinalAnswer), and streams it. A text that is
// not a pipeline result with stage records leaves the timeline unchanged.
func (v *PipelineView) RecordResult(finalAnswer string) {
	var result schemas.PipelineResult
	if err := json.Unmarshal([]byte(finalAnswer), &result); err != nil || result.Status == "" || len(result.Stages) == 0 {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.resolveRunTopologyLocked() // the replay needs the topology of this run
	v.timeline = replayTimeline(v.runTopology, v.state.Plan, result.Stages)
	if v.streaming() {
		if steps, ok := v.timelineLocked(); ok {
			v.publish(pipelineTimelineEvent, pipelineTimelineEvent, timelineData{Steps: steps})
		}
	}
}

// timelineLocked returns the timeline, redacted. ok is false when the run's
// secret values did not load: the timeline then stays hidden. The caller
// holds mu.
func (v *PipelineView) timelineLocked() ([]timelineStep, bool) {
	secrets, err := v.runSecretsLocked()
	if err != nil {
		return []timelineStep{}, false
	}
	steps := make([]timelineStep, len(v.timeline))
	for i, step := range v.timeline {
		nodes := make([]nodeFrame, len(step.Nodes))
		for j, frame := range step.Nodes {
			frame.Name = redactText(frame.Name, secrets)
			frame.Label = redactText(frame.Label, secrets)
			nodes[j] = frame
		}
		steps[i] = timelineStep{Caption: redactText(step.Caption, secrets), Nodes: nodes, graph: step.graph}
	}
	return steps, true
}

// publish sends one event when a server runs. The caller holds mu.
func (v *PipelineView) publish(key, name string, payload any) {
	if !v.streaming() {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	v.server.Publish(Event{Key: key, Name: name, Data: string(data)})
}

// redactText removes secrets from one text. It runs on each source string,
// never on encoded JSON or HTML: some patterns match to the end of a line.
func redactText(text string, secrets []string) string {
	if text == "" {
		return ""
	}
	return redaction.RedactString(text, redaction.Options{ExtraSecretValues: secrets})
}

// redactCurrent redacts every text of the view: names, types, run
// summaries, reasons, commands, notes, and error text. Redaction is
// deterministic, so a redacted node name is still the same key in the page,
// the stream, and the drawer. The caller holds mu.
func redactCurrent(cur pipelineCurrent, secrets []string) pipelineCurrent {
	cur.secrets = secrets
	redact := func(text string) string { return redactText(text, secrets) }
	nodes := make([]presentrun.GraphNode, len(cur.graph.Nodes))
	for i, node := range cur.graph.Nodes {
		node.Name = redact(node.Name)
		node.Type = redact(node.Type)
		node.Dependencies = redactAll(node.Dependencies, secrets)
		node.SkippedAt = redactAll(node.SkippedAt, secrets)
		node.Summary = redact(node.Summary)
		node.Removed = redact(node.Removed)
		node.Command = redact(node.Command)
		if node.Route != nil {
			route := *node.Route
			route.Provider, route.Model = redact(route.Provider), redact(route.Model)
			node.Route = &route
		}
		nodes[i] = node
	}
	cur.graph.Nodes = nodes
	cur.graph.Topology = redact(cur.graph.Topology)
	cur.graph.Notes = redactAll(cur.graph.Notes, secrets)
	cur.notes = redactAll(cur.notes, secrets)
	commands := make([]splicerun.CommandNode, len(cur.commands))
	for i, command := range cur.commands {
		commands[i] = splicerun.CommandNode{Name: redact(command.Name), Line: redact(command.Line)}
	}
	cur.commands = commands
	agents := make([]agentView, len(cur.agents))
	for i, a := range cur.agents {
		agents[i] = agentView{Name: redact(a.Name), Tools: redact(a.Tools), Permission: redact(a.Permission), Turns: a.Turns, Budget: redact(a.Budget), Prompt: redact(a.Prompt)}
	}
	cur.agents = agents
	cur.problem = redact(cur.problem)
	cur.header.Topology = redact(cur.header.Topology)
	cur.header.Facts = redact(cur.header.Facts)
	cur.header.Run = redact(cur.header.Run)
	return cur
}

func redactAll(texts []string, secrets []string) []string {
	out := make([]string, len(texts))
	for i, text := range texts {
		out[i] = redactText(text, secrets)
	}
	return out
}

// pipelineHeader is the run line above the graph.
type pipelineHeader struct {
	Topology string `json:"topology"`
	Facts    string `json:"facts"`
	Run      string `json:"run"`
	RunState string `json:"run_state"`
}

// graphData is the payload of the pipeline-graph event, and of the graph
// endpoint, which adds the SVG.
//
//   - run_seq: the run sequence. A page that shows another run loads the graph.
//   - layout: the layout key. A page with another layout loads the graph.
//   - resync: the page must load the graph, because node events were left out.
type graphData struct {
	Run    int    `json:"run_seq"`
	Layout string `json:"layout"`
	Resync bool   `json:"resync,omitempty"`
	pipelineHeader
	Notes       []string `json:"notes"`
	HasTimeline bool     `json:"has_timeline"`
	SVG         string   `json:"svg,omitempty"`
}

// pipelineCurrent is the graph the view shows now, with its context.
type pipelineCurrent struct {
	graph    presentrun.Graph
	topology *schemas.PipelineTopology
	header   pipelineHeader
	notes    []string
	commands []splicerun.CommandNode
	agents   []agentView
	// agentInput and agentOutput are the token caps of all agent nodes.
	agentInput, agentOutput int
	problem                 string
	secrets                 []string // the exact values this view was redacted with
}

// currentLocked builds the graph to show: the live graph when a run exists,
// else the static graph of the active topology. Both come from
// presentrun.BuildGraph, like the terminal view. The caller holds mu.
func (v *PipelineView) currentLocked() pipelineCurrent {
	if v.liveStateLocked() {
		secrets, err := v.runSecretsLocked()
		if err == nil {
			return redactCurrent(v.liveLocked(), secrets)
		}
		// Never show run text that the exact values could not redact.
		cur := v.staticLocked()
		cur.notes = append([]string{"The run is hidden: the credentials for redaction did not load (" + err.Error() + ")."}, cur.notes...)
		return redactCurrent(cur, nil)
	}
	// The static view holds no run text, so it loads no credential.
	return redactCurrent(v.staticLocked(), nil)
}

// resolveRunTopologyLocked resolves the topology that the run names, once per
// name, like the terminal view. The caller holds mu.
func (v *PipelineView) resolveRunTopologyLocked() {
	if name := v.state.Plan.Topology; name != "" && name != v.runTopologyName {
		v.runTopologyName = name
		v.runTopology, v.runTopologyNote = splicerun.ResolveRunGraphTopology(v.sources, name)
	}
}

func (v *PipelineView) liveLocked() pipelineCurrent {
	v.resolveRunTopologyLocked()
	cur := pipelineCurrent{topology: v.runTopology}
	graph, err := presentrun.BuildGraph(v.runTopology, v.state)
	if err != nil {
		cur.problem = "The pipeline graph is not available: " + err.Error()
	}
	if v.runTopologyNote != "" {
		graph.Notes = append([]string{v.runTopologyNote}, graph.Notes...)
	}
	cur.graph, cur.notes = graph, graph.Notes
	cur.header = pipelineHeader{Topology: graph.Topology, RunState: "running", Run: "Running."}
	if graph.Tier != "" {
		cur.header.Facts = graph.Tier + " tier"
	}
	if c := v.state.Completion; c != nil {
		cur.header.RunState = c.Status
		switch {
		case c.Status == "completed" && c.Unverified:
			cur.header.Run = "Completed, not verified: " + c.Detail
		case c.Status == "completed":
			cur.header.Run = "Completed."
		case c.Status == "cancelled":
			cur.header.Run = "Cancelled."
		default:
			cur.header.Run = "Failed: " + c.Detail
		}
		cur.header.Run = strings.TrimSuffix(cur.header.Run, ": ")
	}
	return cur
}

// staticLocked resolves the active topology the way /pipeline does. It reads
// files, so it runs in request handlers, and in Observe only before a run.
func (v *PipelineView) staticLocked() pipelineCurrent {
	cur := pipelineCurrent{header: pipelineHeader{RunState: "static", Run: "No run yet. The graph shows the active topology."}}
	topology, source, warnings, err := splicerun.ResolveTopology(v.sources)
	if err != nil {
		cur.problem = "The active topology did not resolve: " + err.Error()
		return cur
	}
	graph, err := presentrun.BuildGraph(topology, presentation.State{})
	if err != nil {
		cur.problem = "The pipeline graph is not available: " + err.Error()
		return cur
	}
	compileWarnings, err := splicerun.TopologyWarnings(topology)
	if err != nil {
		cur.problem = "The pipeline graph is not available: " + err.Error()
		return cur
	}
	cur.graph, cur.topology = graph, topology
	cur.header.Topology = graph.Topology
	cur.header.Facts = "source: " + source
	cur.notes = append(append(append([]string(nil), graph.Notes...), warnings...), compileWarnings...)
	cur.commands = splicerun.CommandNodes(topology)
	for _, agent := range splicerun.AgentNodes(topology) {
		cur.agents = append(cur.agents, agentViewOf(agent))
	}
	cur.agentInput, cur.agentOutput = splicerun.AgentBudgetSums(topology)
	if len(graph.Nodes) == 0 {
		cur.problem = "The topology has no nodes."
	}
	return cur
}

// agentView is the page text of one agent node disclosure.
type agentView struct {
	Name       string
	Tools      string // the tools, then their reach in parentheses
	Permission string // the permission and its rule
	Turns      int
	Budget     string
	Prompt     string // the bounded, quoted prompt preview
}

// agentViewOf words a disclosure for the page, as the trust card does.
func agentViewOf(d splicerun.AgentNodeDisclosure) agentView {
	tools := strings.Join(d.Tools, ", ")
	if len(d.Marks) > 0 {
		tools += " (" + strings.Join(d.Marks, ", ") + ")"
	}
	return agentView{
		Name:       d.Name,
		Tools:      tools,
		Permission: d.Permission + ": " + d.PermissionRule,
		Turns:      d.MaxTurns,
		Budget:     d.Budget,
		Prompt:     d.PromptPreview,
	}
}

// pipelinePage is the data of the page template.
type pipelinePage struct {
	pipelineHeader
	Notes    []string
	Commands []splicerun.CommandNode
	// CommandCount says how many command nodes run shell commands.
	CommandCount string
	// Agents disclose the agent nodes as the terminal trust card does.
	Agents      []agentView
	AgentCount  string
	AgentBudget string
	Problem     string
	Layout      pipelineLayout
	RunSeq      int
	// CanEdit shows the "Edit a copy" link when the server has the editor.
	CanEdit bool
}

func (v *PipelineView) servePage(w http.ResponseWriter, _ *http.Request, _ string) {
	v.mu.Lock()
	cur := v.currentLocked()
	page := pipelinePage{
		pipelineHeader: cur.header,
		Notes:          cur.notes,
		Commands:       cur.commands,
		CommandCount:   splicerun.CommandNodeCountText(len(cur.commands)),
		Agents:         cur.agents,
		AgentCount:     splicerun.AgentNodeCountText(len(cur.agents)),
		AgentBudget:    fmt.Sprintf("Together they may use up to %d input and %d output tokens, on top of the tier envelope", cur.agentInput, cur.agentOutput),
		Problem:        cur.problem,
		Layout:         layoutGraph(cur.graph),
		RunSeq:         v.run,
		CanEdit:        v.server != nil && v.server.hasSurface(EditorSurface),
	}
	v.mu.Unlock()
	v.render(w, "page", page)
}

// render executes a template into memory first, so a failure is a clean 500.
// html/template escapes every text.
func (v *PipelineView) render(w http.ResponseWriter, name string, data any) {
	var out strings.Builder
	if err := pipelineTemplates.ExecuteTemplate(&out, name, data); err != nil {
		http.Error(w, "web: render pipeline page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out.String()))
}

// serveSub serves the parts below the page: the assets, the graph fragment
// for a new layout, the node drawer, and the timeline.
func (v *PipelineView) serveSub(w http.ResponseWriter, r *http.Request, sub string) {
	switch sub {
	case "pipeline.css":
		serveAsset(w, "assets/pipeline.css", "text/css; charset=utf-8")
	case "pipeline.js":
		serveAsset(w, "assets/pipeline.js", "text/javascript; charset=utf-8")
	case "favicon.svg":
		serveAsset(w, "assets/favicon.svg", "image/svg+xml")
	case "graph":
		// The whole graph state: the page loads it on every stream connect,
		// for a new run, and for a new layout.
		v.mu.Lock()
		cur := v.currentLocked()
		layout := layoutGraph(cur.graph)
		data := v.graphDataLocked(cur, layout.Key, false)
		v.mu.Unlock()
		var svg strings.Builder
		if err := pipelineTemplates.ExecuteTemplate(&svg, "graph", layout); err != nil {
			http.Error(w, "web: render pipeline graph", http.StatusInternalServerError)
			return
		}
		data.SVG = svg.String()
		if cur.problem != "" {
			data.SVG = ""
		}
		writeJSON(w, data)
	case "node":
		v.serveNode(w, r.URL.Query().Get("name"), r.URL.Query().Get("step"))
	case "timeline":
		v.mu.Lock()
		steps, _ := v.timelineLocked()
		v.mu.Unlock()
		writeJSON(w, timelineData{Steps: steps})
	default:
		http.NotFound(w, r)
	}
}

func serveAsset(w http.ResponseWriter, path, contentType string) {
	data, err := assetFS.ReadFile(path)
	if err != nil {
		http.Error(w, "web: read asset", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}

// nodeData is the drawer payload of one node.
type nodeData struct {
	Name      string                  `json:"name"`
	Iteration int                     `json:"iteration"`
	Fields    []splicerun.DrawerField `json:"fields"`
	// Budget feeds the budget bars. It is nil when the node has no budget.
	Budget *splicerun.BudgetUse `json:"budget,omitempty"`
}

// serveNode returns the drawer facts of one node, resolved and worded by the
// same splicerun functions as the terminal drawer. With step, it describes
// the node at that timeline step, so a replay never shows end-of-run facts.
// An unknown node or step is 404.
func (v *PipelineView) serveNode(w http.ResponseWriter, name, step string) {
	v.mu.Lock()
	cur := v.currentLocked()
	stageConfigPath := v.stageConfigPath
	if step != "" {
		k, err := strconv.Atoi(step)
		steps, ok := v.timelineLocked()
		if err != nil || !ok || k < 0 || k >= len(steps) {
			v.mu.Unlock()
			http.Error(w, fmt.Sprintf("web: no timeline step %q", step), http.StatusNotFound)
			return
		}
		cur = redactCurrent(pipelineCurrent{graph: steps[k].graph, topology: v.runTopology}, cur.secrets)
	}
	v.mu.Unlock()
	for _, node := range cur.graph.Nodes {
		if node.Name != name {
			continue
		}
		drawer := splicerun.ResolveNodeDrawer(cur.graph, node, cur.topology, stageConfigPath)
		fields := splicerun.NodeDrawerFields(cur.graph, node, drawer)
		for i := range fields {
			fields[i].Value = redactText(fields[i].Value, cur.secrets)
		}
		writeJSON(w, nodeData{Name: node.Name, Iteration: node.Iteration, Fields: fields, Budget: splicerun.NodeBudgetUse(cur.graph, node, drawer)})
		return
	}
	http.Error(w, fmt.Sprintf("web: no node %q in the pipeline graph", name), http.StatusNotFound)
}

func writeJSON(w http.ResponseWriter, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "web: encode pipeline data", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
