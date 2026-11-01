package web

import (
	"slices"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// timelineStep is the graph state at one step of the replay. Step 0 is the
// plan before any record; step k is the state after record k. Nodes holds
// every box, so a step needs no earlier step to draw.
type timelineStep struct {
	Caption string      `json:"caption"`
	Nodes   []nodeFrame `json:"nodes"`
	// graph is the view model at this step. The node drawer reads it during a
	// replay, so the drawer shows the facts of the step, not of the run end.
	graph presentrun.Graph
}

// planCaption is the caption of step 0.
const planCaption = "the plan; nothing has run yet"

// timelineData is the payload of the timeline endpoint and event.
type timelineData struct {
	Steps []timelineStep `json:"steps"`
}

// replayTimeline replays the stage records in order through the presentation
// reducer, and builds the graph after each record with presentrun.BuildGraph.
// The steps therefore come from the same view model as the live graph. A
// record the reducer refuses keeps the previous state, and its step still
// shows, so there is one step per record, after step 0.
func replayTimeline(topology *schemas.PipelineTopology, plan presentation.Plan, records []schemas.StageRecord) []timelineStep {
	stages := plan.Stages
	if len(stages) == 0 {
		for _, record := range records {
			if !slices.Contains(stages, record.Name) {
				stages = append(stages, record.Name)
			}
		}
	}
	state, err := presentation.Apply(presentation.State{}, presentation.PlanEvent{
		Title:        plan.Title,
		StageNames:   stages,
		Dependencies: plan.Dependencies,
		Tier:         plan.Tier,
		Topology:     plan.Topology,
		Removed:      plan.Removed,
	})
	if err != nil {
		state = presentation.State{}
	}
	stepFor := func(state presentation.State) timelineStep {
		graph, err := presentrun.BuildGraph(topology, state)
		if err != nil {
			graph, _ = presentrun.BuildGraph(nil, state)
		}
		step := timelineStep{graph: graph, Nodes: make([]nodeFrame, 0, len(graph.Nodes))}
		for _, node := range graph.Nodes {
			step.Nodes = append(step.Nodes, frameFor(graph, node))
		}
		return step
	}
	plan0 := stepFor(state)
	plan0.Caption = planCaption
	steps := append(make([]timelineStep, 0, len(records)+1), plan0)
	for _, record := range records {
		// Records count passes from 1, and the presentation contract from 0.
		iteration := max(record.Iteration-1, 0)
		summary := ""
		if record.OutputSummary != nil {
			summary = *record.OutputSummary
		}
		if next, err := presentation.Apply(state, presentation.StageEvent{
			ID:        record.Name,
			Kind:      presentrun.NodeKindForStage(record.Name),
			Status:    string(record.Status),
			Detail:    summary,
			Iteration: iteration,
		}); err == nil {
			state = next
		}
		if record.TokensInput > 0 || record.TokensOutput > 0 || record.CostUSD > 0 {
			cost := record.CostUSD
			if next, err := presentation.Apply(state, presentation.UsageEvent{
				NodeID:    record.Name,
				Iteration: iteration,
				Usage:     presentation.TokenUsage{InputTokens: int64(record.TokensInput), OutputTokens: int64(record.TokensOutput)},
				CostUSD:   &cost,
			}); err == nil {
				state = next
			}
		}
		step := stepFor(state)
		step.Caption = record.Name
		if node := graphNode(step.graph, record.Name); node.Name != "" {
			step.Caption = record.Name + " · " + presentrun.StatusText(node)
		}
		steps = append(steps, step)
	}
	return steps
}
