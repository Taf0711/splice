package splice

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestDiagramValidate(t *testing.T) {
	valid := schemas.Diagram{
		Kind:  schemas.DiagramKindFlow,
		Title: "graph",
		Nodes: []schemas.DiagramNode{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
		Edges: []schemas.DiagramEdge{{From: "a", To: "b"}},
	}
	tests := []struct {
		name    string
		mutate  func(d *schemas.Diagram)
		wantErr string
	}{
		{name: "valid", mutate: func(d *schemas.Diagram) {}, wantErr: ""},
		{name: "invalid kind", mutate: func(d *schemas.Diagram) { d.Kind = "pie" }, wantErr: `invalid diagram kind "pie"`},
		{name: "empty title", mutate: func(d *schemas.Diagram) { d.Title = "  " }, wantErr: "title is required"},
		{name: "no nodes", mutate: func(d *schemas.Diagram) { d.Nodes = nil }, wantErr: "at least one node is required"},
		{name: "empty node id", mutate: func(d *schemas.Diagram) { d.Nodes[0].ID = "" }, wantErr: "id is required"},
		{name: "empty label", mutate: func(d *schemas.Diagram) { d.Nodes[0].Label = " " }, wantErr: "label is required"},
		{name: "duplicate node id", mutate: func(d *schemas.Diagram) { d.Nodes[1].ID = "a" }, wantErr: `duplicate node id "a"`},
		{name: "unknown from", mutate: func(d *schemas.Diagram) { d.Edges[0].From = "ghost" }, wantErr: `unknown from node id "ghost"`},
		{name: "unknown to", mutate: func(d *schemas.Diagram) { d.Edges[0].To = "ghost" }, wantErr: `unknown to node id "ghost"`},
		{
			name: "sequence duplicate order",
			mutate: func(d *schemas.Diagram) {
				d.Kind = schemas.DiagramKindSequence
				d.Edges = append(d.Edges, schemas.DiagramEdge{From: "b", To: "a", Order: 0})
			},
			wantErr: "duplicate sequence order 0",
		},
		{
			name: "sequence valid",
			mutate: func(d *schemas.Diagram) {
				d.Kind = schemas.DiagramKindSequence
				d.Edges = []schemas.DiagramEdge{{From: "a", To: "b", Order: 1}, {From: "b", To: "a", Order: 2}}
			},
			wantErr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := valid
			d.Nodes = append([]schemas.DiagramNode(nil), valid.Nodes...)
			d.Edges = append([]schemas.DiagramEdge(nil), valid.Edges...)
			tt.mutate(&d)
			err := d.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
				}
			}
		})
	}
}

func planWithTasks(tasks ...schemas.Task) schemas.DesignPlan {
	return schemas.DesignPlan{Tasks: tasks}
}

func TestTaskGraphFromPlan(t *testing.T) {
	t.Run("linear chain", func(t *testing.T) {
		graph, err := TaskGraphFromPlan(planWithTasks(
			schemas.Task{ID: "a", Title: "Design"},
			schemas.Task{ID: "b", Title: "Build", DependsOn: []string{"a"}},
			schemas.Task{ID: "c", Title: "Ship", DependsOn: []string{"b"}},
		))
		if err != nil {
			t.Fatalf("TaskGraphFromPlan() = %v", err)
		}
		if graph.Kind != schemas.DiagramKindFlow || graph.Title != "Task graph" {
			t.Fatalf("graph header = %q %q", graph.Kind, graph.Title)
		}
		if len(graph.Nodes) != 3 || len(graph.Edges) != 2 {
			t.Fatalf("graph = %d nodes %d edges, want 3/2", len(graph.Nodes), len(graph.Edges))
		}
		if graph.Edges[0].From != "a" || graph.Edges[0].To != "b" || graph.Edges[1].From != "b" || graph.Edges[1].To != "c" {
			t.Fatalf("edges = %#v", graph.Edges)
		}
	})
	t.Run("diamond keeps both parents", func(t *testing.T) {
		graph, err := TaskGraphFromPlan(planWithTasks(
			schemas.Task{ID: "a", Title: "Base"},
			schemas.Task{ID: "b", Title: "Left", DependsOn: []string{"a"}},
			schemas.Task{ID: "c", Title: "Right", DependsOn: []string{"a"}},
			schemas.Task{ID: "d", Title: "Join", DependsOn: []string{"b", "c"}},
		))
		if err != nil {
			t.Fatalf("TaskGraphFromPlan() = %v", err)
		}
		var joinParents []string
		for _, e := range graph.Edges {
			if e.To == "d" {
				joinParents = append(joinParents, e.From)
			}
		}
		if len(joinParents) != 2 {
			t.Fatalf("join parents = %#v, want 2", joinParents)
		}
	})
	t.Run("cycle names unresolved ids", func(t *testing.T) {
		_, err := TaskGraphFromPlan(planWithTasks(
			schemas.Task{ID: "a", Title: "A", DependsOn: []string{"b"}},
			schemas.Task{ID: "b", Title: "B", DependsOn: []string{"a"}},
		))
		if err == nil || !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
			t.Fatalf("cycle error = %v, want it naming a and b", err)
		}
	})
	t.Run("unknown dependency is named", func(t *testing.T) {
		_, err := TaskGraphFromPlan(planWithTasks(
			schemas.Task{ID: "x", Title: "X", DependsOn: []string{"ghost"}},
		))
		if err == nil || !strings.Contains(err.Error(), "ghost") {
			t.Fatalf("unknown dep error = %v, want it naming ghost", err)
		}
	})
}

func TestRenderDiagramChainGolden(t *testing.T) {
	graph, err := TaskGraphFromPlan(planWithTasks(
		schemas.Task{ID: "a", Title: "Design"},
		schemas.Task{ID: "b", Title: "Build", DependsOn: []string{"a"}},
		schemas.Task{ID: "c", Title: "Ship", DependsOn: []string{"b"}},
	))
	if err != nil {
		t.Fatalf("TaskGraphFromPlan() = %v", err)
	}
	art, err := RenderDiagram(graph, 60)
	if err != nil {
		t.Fatalf("RenderDiagram() = %v", err)
	}
	want := strings.Join([]string{
		"┌────────┐",
		"│ Design │",
		"└────────┘",
		"     │",
		"     ▼",
		" ┌───────┐",
		" │ Build │",
		" └───────┘",
		"     │",
		"     ▼",
		" ┌──────┐",
		" │ Ship │",
		" └──────┘",
	}, "\n")
	if art != want {
		t.Fatalf("chain render mismatch:\ngot:\n%s\nwant:\n%s", art, want)
	}
}

func TestRenderDiagramFanOut(t *testing.T) {
	graph, err := TaskGraphFromPlan(planWithTasks(
		schemas.Task{ID: "core", Title: "Core"},
		schemas.Task{ID: "web", Title: "Web", DependsOn: []string{"core"}},
		schemas.Task{ID: "api", Title: "API", DependsOn: []string{"core"}},
		schemas.Task{ID: "cli", Title: "CLI", DependsOn: []string{"core"}},
	))
	if err != nil {
		t.Fatalf("TaskGraphFromPlan() = %v", err)
	}
	art, err := RenderDiagram(graph, 60)
	if err != nil {
		t.Fatalf("RenderDiagram() = %v", err)
	}
	for _, want := range []string{"Core", "Web", "API", "CLI", "┬", "▼"} {
		if !strings.Contains(art, want) {
			t.Fatalf("fan-out render missing %q:\n%s", want, art)
		}
	}
	again, err := RenderDiagram(graph, 60)
	if err != nil {
		t.Fatalf("RenderDiagram() second run = %v", err)
	}
	if art != again {
		t.Fatal("RenderDiagram is not deterministic across runs")
	}
}

func TestRenderDiagramNarrowFallsBackToList(t *testing.T) {
	graph, err := TaskGraphFromPlan(planWithTasks(
		schemas.Task{ID: "a", Title: "Design"},
		schemas.Task{ID: "b", Title: "Build", DependsOn: []string{"a"}},
	))
	if err != nil {
		t.Fatalf("TaskGraphFromPlan() = %v", err)
	}
	art, err := RenderDiagram(graph, 40)
	if err != nil {
		t.Fatalf("RenderDiagram() = %v", err)
	}
	if strings.Contains(art, "┌") {
		t.Fatalf("narrow render must not draw boxes:\n%s", art)
	}
	if !strings.Contains(art, "  - Design") || !strings.Contains(art, "  - Build (depends on: Design)") {
		t.Fatalf("narrow render missing list lines:\n%s", art)
	}
}

func TestRenderDiagramSequence(t *testing.T) {
	d := schemas.Diagram{
		Kind:  schemas.DiagramKindSequence,
		Title: "preview flow",
		Nodes: []schemas.DiagramNode{
			{ID: "user", Label: "user"},
			{ID: "tui", Label: "tui"},
			{ID: "api", Label: "api"},
		},
		Edges: []schemas.DiagramEdge{
			{From: "user", To: "tui", Label: "/preview", Order: 1},
			{From: "tui", To: "api", Label: "history", Order: 2},
			{From: "api", To: "tui", Label: "diagram", Order: 3},
		},
	}
	art, err := RenderDiagram(d, 72)
	if err != nil {
		t.Fatalf("RenderDiagram() = %v", err)
	}
	first := strings.Index(art, "/preview")
	second := strings.Index(art, "history")
	third := strings.Index(art, "diagram")
	if first < 0 || second < 0 || third < 0 || !(first < second && second < third) {
		t.Fatalf("messages out of order in render:\n%s", art)
	}
	if !strings.Contains(art, "▶") || !strings.Contains(art, "◀") {
		t.Fatalf("sequence render missing arrows:\n%s", art)
	}
}

func TestRenderDiagramSequenceNarrowFallsBackToList(t *testing.T) {
	d := schemas.Diagram{
		Kind:  schemas.DiagramKindSequence,
		Title: "preview flow",
		Nodes: []schemas.DiagramNode{{ID: "user", Label: "user"}, {ID: "tui", Label: "tui"}},
		Edges: []schemas.DiagramEdge{{From: "user", To: "tui", Label: "/preview", Order: 1}},
	}
	art, err := RenderDiagram(d, 40)
	if err != nil {
		t.Fatalf("RenderDiagram() = %v", err)
	}
	if !strings.Contains(art, "  1. user -> tui: /preview") {
		t.Fatalf("sequence list fallback wrong:\n%s", art)
	}
}

func TestRenderDiagramTruncatesLongLabels(t *testing.T) {
	long := strings.Repeat("x", 40)
	graph, err := TaskGraphFromPlan(planWithTasks(schemas.Task{ID: "a", Title: long}))
	if err != nil {
		t.Fatalf("TaskGraphFromPlan() = %v", err)
	}
	art, err := RenderDiagram(graph, 60)
	if err != nil {
		t.Fatalf("RenderDiagram() = %v", err)
	}
	if strings.Contains(art, long) {
		t.Fatal("long label was not truncated")
	}
	if !strings.Contains(art, "…") {
		t.Fatalf("truncated label missing ellipsis:\n%s", art)
	}
}

func TestRenderDiagramDiamondNote(t *testing.T) {
	graph, err := TaskGraphFromPlan(planWithTasks(
		schemas.Task{ID: "a", Title: "Alpha"},
		schemas.Task{ID: "b", Title: "Beta", DependsOn: []string{"a"}},
		schemas.Task{ID: "c", Title: "Gamma", DependsOn: []string{"a"}},
		schemas.Task{ID: "d", Title: "Delta", DependsOn: []string{"b", "c"}},
	))
	if err != nil {
		t.Fatalf("TaskGraphFromPlan() = %v", err)
	}
	art, err := RenderDiagram(graph, 72)
	if err != nil {
		t.Fatalf("RenderDiagram() = %v", err)
	}
	if !strings.Contains(art, "note: Delta also depends on: Gamma") {
		t.Fatalf("diamond render missing convergence note:\n%s", art)
	}
}

func TestRenderDiagramValidationError(t *testing.T) {
	_, err := RenderDiagram(schemas.Diagram{Kind: "pie", Title: "x"}, 60)
	if err == nil || !strings.Contains(err.Error(), "invalid diagram kind") {
		t.Fatalf("RenderDiagram() = %v, want validation error", err)
	}
}

func TestRenderDiagramFlowCycle(t *testing.T) {
	d := schemas.Diagram{
		Kind:  schemas.DiagramKindFlow,
		Title: "cycle",
		Nodes: []schemas.DiagramNode{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
		Edges: []schemas.DiagramEdge{{From: "a", To: "b"}, {From: "b", To: "a"}},
	}
	_, err := RenderDiagram(d, 60)
	if err == nil || !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Fatalf("cycle error = %v, want it naming a and b", err)
	}
}
