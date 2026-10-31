package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// graphDiamondTopology is the diamond fixture: one writer, two parallel
// checks (one of them a command node), and a runner that waits for both.
func graphDiamondTopology() *schemas.PipelineTopology {
	return &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "diamond",
		Nodes: []schemas.PipelineNode{
			{Name: "code_writer", Type: "code_writer"},
			{Name: "lint", Type: schemas.NodeTypeCommand, Command: []string{"go", "vet", "./..."}},
			{Name: "static_analyzer", Type: "static_analyzer"},
			{Name: "test_runner", Type: "test_runner"},
		},
		Edges: []schemas.PipelineEdge{
			{From: "code_writer", To: "lint"},
			{From: "code_writer", To: "static_analyzer"},
			{From: "lint", To: "test_runner"},
			{From: "static_analyzer", To: "test_runner"},
		},
	}
}

// graphRunState drives the real plan, stage, and usage events of one run
// through the reducer: the writer passed and re-entered, one check failed,
// and the rest wait.
func graphRunState(t *testing.T, topology *schemas.PipelineTopology, tier schemas.PipelineTier, check string) presentation.State {
	t.Helper()
	compiled, err := splicerun.CompileTopology(topology, tier)
	if err != nil {
		t.Fatal(err)
	}
	planEvent := agent.PipelinePlanEvent{Dependencies: map[string][]string{}}
	for _, stage := range compiled.Stages {
		planEvent.Stages = append(planEvent.Stages, stage.Name)
		if len(stage.DependsOn) > 0 {
			planEvent.Dependencies[stage.Name] = stage.DependsOn
		}
	}
	cost := 0.0123
	events := []presentation.StreamEventLike{
		presentrun.AdaptPipelinePlan(schemas.ExecutionPlan{RequestIntent: "x", Tier: tier, TopologyName: topology.Name}, planEvent),
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "code_writer", Status: "running"}),
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "code_writer", Status: "completed", Detail: "wrote hello.go", Progress: 100}),
		presentation.UsageEvent{NodeID: "code_writer", Usage: presentation.TokenUsage{InputTokens: 12000, OutputTokens: 1800}, CostUSD: &cost},
		presentrun.AdaptStageEvent(agent.StageEvent{Name: check, Status: "running"}),
		presentrun.AdaptStageEvent(agent.StageEvent{Name: check, Status: "failed", Detail: "check failed: exit status 1"}),
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "code_writer", Iteration: 1, Status: "running"}),
		presentation.RouteEvent{NodeID: "code_writer", Iteration: 1, Route: presentation.NodeRoute{Provider: "cloud", Model: "escalated-model", Effort: "high", InputBudget: 6000, OutputBudget: 900, Escalated: true}},
		presentation.UsageEvent{NodeID: "code_writer", Iteration: 1, Usage: presentation.TokenUsage{InputTokens: 4000, OutputTokens: 600}},
	}
	var state presentation.State
	for _, event := range events {
		if state, err = presentation.Apply(state, event); err != nil {
			t.Fatal(err)
		}
	}
	return state
}

func checkTextGolden(t *testing.T, name, rendered string) {
	t.Helper()
	encoded := []byte(ansiPattern.ReplaceAllString(rendered, "") + "\n")
	path := tuiGoldenPath(name)
	if tuiUpdateGolden() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (create it with SPLICE_UPDATE_GOLDEN=1)", path, err)
	}
	if !bytes.Equal(want, encoded) {
		t.Fatalf("golden %s mismatch (regenerate with SPLICE_UPDATE_GOLDEN=1):\n--- want\n%s--- got\n%s", name, want, encoded)
	}
}

func graphFixtures(t *testing.T) []struct {
	name     string
	topology *schemas.PipelineTopology
	run      presentation.State
} {
	return []struct {
		name     string
		topology *schemas.PipelineTopology
		run      presentation.State
	}{
		{"default", splicerun.DefaultTopology(), graphRunState(t, splicerun.DefaultTopology(), schemas.TierStandard, "static_analyzer")},
		{"diamond", graphDiamondTopology(), graphRunState(t, graphDiamondTopology(), schemas.TierStandard, "lint")},
	}
}

// TestPipelineGraphGoldens pins the idle /pipeline card and the live graph
// at the three terminal widths. The live graph renders at the width of the
// surface that hosts it: the sidebar at 120 columns, the Ctrl+G pane below.
func TestPipelineGraphGoldens(t *testing.T) {
	for _, fixture := range graphFixtures(t) {
		card, err := pipelineGraphCardFor(fixture.topology, splicerun.TopologySourceDefault, nil)
		if err != nil {
			t.Fatal(err)
		}
		live, err := presentrun.BuildGraph(fixture.topology, fixture.run)
		if err != nil {
			t.Fatal(err)
		}
		for _, width := range []int{60, 80, 120} {
			t.Run(fmt.Sprintf("%s_w%d", fixture.name, width), func(t *testing.T) {
				checkTextGolden(t, fmt.Sprintf("pipeline_graph_idle_%s_w%d", fixture.name, width), renderPipelineGraphCard(card, width, presentation.GlyphTierASCII))
				surface := width - 4
				if width >= compactModeMinWidth {
					surface = sidebarWidth(width)
				}
				checkTextGolden(t, fmt.Sprintf("pipeline_graph_live_%s_w%d", fixture.name, width), strings.Join(renderPipelineGraph(live, surface, presentation.GlyphTierASCII, 1), "\n"))
			})
		}
	}
}

// TestPipelineGraphASCIIFallback pins the ASCII frame: the rich-tier graph
// folded for an ASCII terminal has no byte outside ASCII and keeps the
// status words.
func TestPipelineGraphASCIIFallback(t *testing.T) {
	fixture := graphFixtures(t)[1]
	live, err := presentrun.BuildGraph(fixture.topology, fixture.run)
	if err != nil {
		t.Fatal(err)
	}
	folded := foldASCII(strings.Join(renderPipelineGraph(live, 76, presentation.GlyphTierRichUnicode, -1), "\n"), true)
	for _, r := range ansiPattern.ReplaceAllString(folded, "") {
		if r > 0x7f {
			t.Fatalf("ASCII frame has non-ASCII rune %q:\n%s", r, folded)
		}
	}
	checkTextGolden(t, "pipeline_graph_ascii_diamond_w80", folded)
}

// TestPipelineGraphNarrowKeepsDependencies pins the flat fallback: every
// dependency stays readable even in a 26-column sidebar.
func TestPipelineGraphNarrowKeepsDependencies(t *testing.T) {
	live, err := presentrun.BuildGraph(splicerun.DefaultTopology(), graphRunState(t, splicerun.DefaultTopology(), schemas.TierLight, "static_analyzer"))
	if err != nil {
		t.Fatal(err)
	}
	plain := ansiPattern.ReplaceAllString(strings.Join(renderPipelineGraph(live, sidebarMinWidth, presentation.GlyphTierASCII, -1), "\n"), "")
	for _, want := range []string{"<- static_analyzer", "<- test_runner", "skipped at light", "pass 2", "[!] static_analyzer"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("narrow graph lacks %q:\n%s", want, plain)
		}
	}
	for _, line := range strings.Split(plain, "\n") {
		if w := len([]rune(line)); w > sidebarMinWidth {
			t.Fatalf("line %q is %d cells, over %d", line, w, sidebarMinWidth)
		}
	}
	if strings.Contains(plain, "depth ") {
		t.Fatal("the flat fallback must not render depth headers")
	}
}

// TestPipelineGraphCardRoundTrip pins the transcript payload: the card
// stores data and renders it back at the live width.
func TestPipelineGraphCardRoundTrip(t *testing.T) {
	card, err := pipelineGraphCardFor(graphDiamondTopology(), splicerun.TopologySourceLibrary, []string{"ignored untrusted project topology x"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := pipelineGraphCardPayload(card)
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := parsePipelineGraphCard(payload)
	if !ok || parsed.Graph.Topology != "diamond" || len(parsed.Commands) != 2 {
		t.Fatalf("parsed = %+v, ok %v", parsed, ok)
	}
	plain := ansiPattern.ReplaceAllString(renderPipelineGraphCard(parsed, 80, presentation.GlyphTierASCII), "")
	for _, want := range []string{"source: user library", "ignored untrusted project topology x", "command node lint: go vet ./...", "test_runner", "<- lint, static_analyzer"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("card lacks %q:\n%s", want, plain)
		}
	}
}

// graphRunModel is a model mid-run on the given topology at standard tier.
func graphRunModel(t *testing.T, topology *schemas.PipelineTopology, check string) model {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newModel(context.Background(), Options{})
	m.cwd = t.TempDir()
	m.glyphTier = presentation.GlyphTierASCII
	state := graphRunState(t, topology, schemas.TierStandard, check)
	m.pipeline.applyState(state)
	m.observeRunTopology(state)
	if topology.Name != splicerun.DefaultTopologyName {
		// The test workspace resolves the embedded default, so a custom
		// topology is installed as the run's resolved topology.
		m.pipeline.graphTopology, m.pipeline.graphTopologyNote = topology, ""
	}
	m.lastState = state
	return m
}

func renderModulePlain(m model, slot ContextSlot, width int) string {
	for _, module := range contextRegistry() {
		if module.slot == slot && module.has(m) {
			return ansiPattern.ReplaceAllString(strings.Join(module.render(m, width), "\n"), "")
		}
	}
	return ""
}

// TestSidebarPipelineShowsLiveGraph pins the live graph in the sidebar
// PIPELINE section: dependencies, the tier skip, and the run tail.
func TestSidebarPipelineShowsLiveGraph(t *testing.T) {
	m := graphRunModel(t, splicerun.DefaultTopology(), "static_analyzer")
	plain := renderModulePlain(m, ContextSlotPipeline, sidebarWidth(120))
	for _, want := range []string{"PIPELINE", "[-] security_auditor", "skipped at standard", "<- static_analyzer", "[!] static_analyzer", "pass 2"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("sidebar PIPELINE lacks %q:\n%s", want, plain)
		}
	}
}

// TestSidebarPipelineNamesForeignTopology pins the honest fallback: a
// topology that resolves under another name is not used, and the graph says
// it shows the run plan only.
func TestSidebarPipelineNamesForeignTopology(t *testing.T) {
	m := graphRunModel(t, graphDiamondTopology(), "lint")
	m.pipeline.graphTopology = nil
	m.pipeline.graphTopologyName = ""
	m.observeRunTopology(m.lastState)
	plain := renderModulePlain(m, ContextSlotPipeline, 40)
	if !strings.Contains(plain, "not the") || !strings.Contains(plain, "<- lint, static_analyzer") {
		t.Fatalf("sidebar lacks the plan-only note or the plan graph:\n%s", plain)
	}
}

// TestDetailPaneShowsGraph pins the Ctrl+G pane: the live graph with the
// first node selected, and the static graph when no run exists.
func TestDetailPaneShowsGraph(t *testing.T) {
	m := graphRunModel(t, graphDiamondTopology(), "lint").openDetailView()
	plain := ansiPattern.ReplaceAllString(m.renderDetailView(80), "")
	for _, want := range []string{"PIPELINE diamond · standard", "> [>] code_writer", "[!] lint", "<- lint, static_analyzer"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("pane lacks %q:\n%s", want, plain)
		}
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	idle := newModel(context.Background(), Options{})
	idle.cwd = t.TempDir()
	idle = idle.openDetailView()
	plain = ansiPattern.ReplaceAllString(idle.renderDetailView(60), "")
	for _, want := range []string{"PIPELINE default · no run", "acceptance_verifier", "<- test_runner", "No run evidence yet."} {
		if !strings.Contains(plain, want) {
			t.Fatalf("idle pane lacks %q:\n%s", want, plain)
		}
	}
}

func pressAll(t *testing.T, m model, keys ...tea.KeyPressMsg) model {
	t.Helper()
	for _, key := range keys {
		updated, _ := m.Update(key)
		m = updated.(model)
	}
	return m
}

// TestPipelineDrawerOpensAndCloses pins the node drawer: Ctrl+G opens the
// pane, j selects, Enter opens the drawer with the resolved configuration
// and the command result, Esc closes the drawer, and a second Esc closes the
// pane. Typed text is never captured.
func TestPipelineDrawerOpensAndCloses(t *testing.T) {
	m := graphRunModel(t, graphDiamondTopology(), "lint")
	m.userConfigPath = filepath.Join(t.TempDir(), "config.json")
	m.pipeline.graphTopology.Nodes[0].Model = &schemas.StageModelConfig{ProviderProfile: "local", Model: "pinned", ReasoningEffort: "high"}

	m = pressAll(t, m, testKeyCtrl('g'))
	if !m.detailView.active {
		t.Fatal("Ctrl+G did not open the pane")
	}
	m = pressAll(t, m, testKey(tea.KeyEnter))
	plain := ansiPattern.ReplaceAllString(m.renderDetailView(100), "")
	for _, want := range []string{"NODE code_writer", "model     cloud/escalated-model (high) · escalated", "configured local/pinned (high) (node pin in the topology)", "input 4K / 6K · output 600 / 900", "status    running · pass 2", "summary   from pass 1: wrote hello.go"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("writer drawer lacks %q:\n%s", want, plain)
		}
	}
	m = pressAll(t, m, testKey(tea.KeyDown))
	plain = ansiPattern.ReplaceAllString(m.renderDetailView(100), "")
	for _, want := range []string{"NODE lint", "command (model-free)", "command   go vet ./...", "result    failed", "budget    none (model-free)", "check failed: exit status 1"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("lint drawer lacks %q:\n%s", want, plain)
		}
	}
	if nav := ansiPattern.ReplaceAllString(m.detailViewNavBar(100), ""); !strings.Contains(nav, "esc closes details") || strings.Contains(nav, "…") {
		t.Fatalf("nav bar = %q, want the drawer hint", nav)
	}
	m = pressAll(t, m, testKey(tea.KeyEsc))
	if !m.detailView.active || m.detailView.drawerOpen {
		t.Fatal("first Esc must close only the drawer")
	}
	if strings.Contains(ansiPattern.ReplaceAllString(m.renderDetailView(100), ""), "NODE lint") {
		t.Fatal("the drawer still renders after Esc")
	}
	m = pressAll(t, m, testKey(tea.KeyEsc))
	if m.detailView.active {
		t.Fatal("second Esc must close the pane")
	}

	// With text in the composer, j types instead of selecting.
	m = pressAll(t, m, testKeyCtrl('g'))
	m.input.SetValue("x")
	m = pressAll(t, m, testKeyText("j"))
	if m.detailView.selectedName != "" || m.composerValue() != "xj" {
		t.Fatalf("selected = %q composer = %q, want typing to reach the composer", m.detailView.selectedName, m.composerValue())
	}
}

// TestPipelineDrawerKeysAppearOnlyInThePane pins the shortcuts overlay: the
// graph keys are listed only while the pane shows a graph.
func TestPipelineDrawerKeysAppearOnlyInThePane(t *testing.T) {
	m := graphRunModel(t, graphDiamondTopology(), "lint")
	has := func(m model) bool {
		for _, group := range m.buildKeybindingGroups() {
			for _, binding := range group.bindings {
				if strings.Contains(binding.desc, "pipeline node") {
					return true
				}
			}
		}
		return false
	}
	if has(m) {
		t.Fatal("graph keys listed outside the pane")
	}
	if !has(m.openDetailView()) {
		t.Fatal("graph keys missing inside the pane")
	}
}

// TestDetailPaneKeepsSelectionAndDrawerInView pins finding 2 of the Phase 1
// review: with six evidence groups the pane is taller than the screen and
// bottom-pinned. Moving the selection and opening the drawer must scroll
// both into view, at 80x24 and 130x24.
func TestDetailPaneKeepsSelectionAndDrawerInView(t *testing.T) {
	for _, width := range []int{80, 130} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := graphRunModel(t, graphDiamondTopology(), "lint")
			m.altScreen = true
			updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			m = updated.(model)
			for i := 0; i < 6; i++ {
				m.lastState.Evidence = append(m.lastState.Evidence, presentation.EvidenceGroup{
					Label: fmt.Sprintf("suite %d", i), Status: presentation.EvidencePassed, Passed: 3,
					Findings: []string{"finding one", "finding two", "finding three"}, Duration: 1,
				})
			}
			m = pressAll(t, m, testKeyCtrl('g'))
			if view := ansiPattern.ReplaceAllString(viewString(m.View()), ""); !strings.Contains(view, "> [>] code_writer") {
				t.Fatalf("the pane opened without the selected node in view:\n%s", view)
			}
			m = pressAll(t, m, testKey(tea.KeyDown), testKey(tea.KeyDown))
			if view := ansiPattern.ReplaceAllString(viewString(m.View()), ""); !strings.Contains(view, "> [ ] static_analyzer") {
				t.Fatalf("the selection moved out of view:\n%s", view)
			}
			m = pressAll(t, m, testKey(tea.KeyEnter))
			view := ansiPattern.ReplaceAllString(viewString(m.View()), "")
			if !strings.Contains(view, "NODE static_analyzer") || !strings.Contains(view, "summary") {
				t.Fatalf("the open drawer is not in view:\n%s", view)
			}
			// PgUp and PgDn still scroll the pane.
			before := m.chatScrollOffset
			m = pressAll(t, m, testKey(tea.KeyPgDown))
			if m.chatScrollOffset == before && before != 0 {
				t.Fatal("PgDn no longer scrolls the pane")
			}
		})
	}
}

// TestPipelineDrawerFollowsTheGraph pins finding 3 of the Phase 1 review: a
// run that starts under an open drawer re-resolves it at the run tier, and
// the selection follows the node by name, not by row index.
func TestPipelineDrawerFollowsTheGraph(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newModel(context.Background(), Options{})
	m.cwd = t.TempDir()
	m.glyphTier = presentation.GlyphTierASCII
	m.userConfigPath = filepath.Join(t.TempDir(), "config.json")
	m = pressAll(t, m, testKeyCtrl('g'), testKey(tea.KeyDown), testKey(tea.KeyDown), testKey(tea.KeyEnter))
	plain := ansiPattern.ReplaceAllString(m.renderDetailView(100), "")
	if !strings.Contains(plain, "NODE static_analyzer") || !strings.Contains(plain, "standard (no run yet)") {
		t.Fatalf("static drawer:\n%s", plain)
	}

	// A run starts while the drawer is open. Its plan drops test_generator,
	// so the row index of static_analyzer could change under the selection.
	state := graphRunState(t, splicerun.DefaultTopology(), schemas.TierLight, "static_analyzer")
	updated, _ := m.Update(presentationStateMsg{runID: m.activeRunID, state: state})
	m = updated.(model)
	plain = ansiPattern.ReplaceAllString(m.renderDetailView(100), "")
	for _, want := range []string{"NODE static_analyzer", "tier      light", "status    failed"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("drawer after the run started lacks %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "no run yet") || !m.detailView.drawer.Live {
		t.Fatalf("the drawer kept the static resolution:\n%s", plain)
	}
}

// TestDetailPaneLettersAlwaysType pins finding 9 of the Phase 1 review: in
// the pane, with an empty input, j and k type into the input and do not move
// the selection; the nav bar keeps its key hints whole at 80 columns.
func TestDetailPaneLettersAlwaysType(t *testing.T) {
	m := graphRunModel(t, graphDiamondTopology(), "lint")
	m = pressAll(t, m, testKeyCtrl('g'), testKeyText("j"), testKeyText("k"))
	if m.detailView.selectedName != "" || m.composerValue() != "jk" {
		t.Fatalf("selected = %q composer = %q, want the letters typed", m.detailView.selectedName, m.composerValue())
	}
	m.width = 80
	for i := 0; i < 6; i++ {
		m.lastState.Evidence = append(m.lastState.Evidence, presentation.EvidenceGroup{Label: "suite", Status: presentation.EvidencePassed, Findings: []string{"x"}})
	}
	nav := ansiPattern.ReplaceAllString(m.detailViewNavBar(80), "")
	if strings.Contains(nav, "…") || !strings.Contains(nav, "esc") {
		t.Fatalf("nav bar at 80 columns = %q, want whole key hints", nav)
	}
}
