package splice

import (
	"context"
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// MemoryStore retrieves bounded memory for a stage and persists observations.
// A nil MemoryStore means memory is off: callers skip injection entirely and
// behave byte-identically to a run without memory. *memd.Client satisfies this
// implicitly.
type MemoryStore interface {
	Search(ctx context.Context, q schemas.MemoryQuery) (schemas.MemoryBundle, error)
	Upsert(ctx context.Context, obs schemas.MemoryObservation) (schemas.MemoryObservation, error)
}

// newMemoryQuery builds the bounded search the orchestrator issues for a stage,
// per docs/flug-design/10-structured-memory.md Retrieval Flow: owner_agent is the
// stage name, the query is the first 200 runes of the distilled request intent,
// and project_path is the working dir. Include flags are left nil so the sidecar
// applies its default-true.
func newMemoryQuery(stageName, intent, workDir string) schemas.MemoryQuery {
	q := []rune(intent)
	if len(q) > 200 {
		q = q[:200]
	}
	return schemas.MemoryQuery{
		RequestingAgent: stageName,
		Query:           string(q),
		ProjectPath:     &workDir,
		Scopes:          []string{"project", "global"},
		Limit:           5,
	}
}

// persistObservation writes one observation non-fatally. A nil store means
// memory is off; the helper returns without doing anything. Errors are
// surfaced via the emit callback and never propagated; memory writes are
// never load-bearing.
func persistObservation(ctx context.Context, store MemoryStore, obs schemas.MemoryObservation, emit func(string)) {
	if store == nil {
		return
	}
	if _, err := store.Upsert(ctx, obs); err != nil {
		emit(fmt.Sprintf("memory write skipped: %v\n", err))
	}
}

func buildConfigObservation(runID, workDir string, plan schemas.ExecutionPlan) schemas.MemoryObservation {
	stageNames := make([]string, 0, len(plan.Stages))
	for _, stage := range plan.Stages {
		stageNames = append(stageNames, stage.Name)
	}
	topic := "run_config"
	return schemas.MemoryObservation{
		ProjectPath: &workDir,
		Scope:       "project",
		OwnerAgent:  "orchestrator",
		Visibility:  "shareable",
		MemoryType:  "run_config",
		Title:       "Pipeline run configuration",
		Content:     fmt.Sprintf("tier=%s stages=%s", string(plan.Tier), strings.Join(stageNames, ",")),
		TopicKey:    &topic,
		SourceRunID: &runID,
		Confidence:  Ptr(1.0),
		Pinned:      false,
	}
}

// extractWriteObservations returns the deterministic observations to persist
// after a stage completes.
func extractWriteObservations(stageName, runID, workDir string, output schemas.HarnessStageOutput) []schemas.MemoryObservation {
	var obs []schemas.MemoryObservation
	if stageName == "test_runner" {
		if cmdRaw, ok := output.Data["test_command"]; ok {
			var cmdStr string
			switch v := cmdRaw.(type) {
			case []string:
				cmdStr = strings.Join(v, " ")
			case []any:
				parts := make([]string, 0, len(v))
				for _, p := range v {
					if s, ok := p.(string); ok {
						parts = append(parts, s)
					}
				}
				cmdStr = strings.Join(parts, " ")
			case string:
				cmdStr = v
			}
			if cmdStr != "" {
				topic := "test_command"
				obs = append(obs, schemas.MemoryObservation{
					ProjectPath: &workDir,
					Scope:       "project",
					OwnerAgent:  "orchestrator",
					Visibility:  "shareable",
					MemoryType:  "test_command",
					Title:       "Discovered test command",
					Content:     cmdStr,
					TopicKey:    &topic,
					SourceRunID: &runID,
					SourceStage: &stageName,
					Confidence:  Ptr(1.0),
				})
			}
		}
	}
	return obs
}

func extractDegradationObservations(stageName, runID, workDir string, bundle schemas.ContextBundle) []schemas.MemoryObservation {
	obs := make([]schemas.MemoryObservation, 0)
	for _, item := range bundle.Items {
		if item.Error == nil {
			continue
		}
		topic := fmt.Sprintf("tool_degradation:%s", string(item.Query.QueryType))
		obs = append(obs, schemas.MemoryObservation{
			ProjectPath: &workDir,
			Scope:       "project",
			OwnerAgent:  stageName,
			Visibility:  "private",
			MemoryType:  "tool_degradation",
			TopicKey:    &topic,
			Title:       fmt.Sprintf("%s degraded", string(item.Query.QueryType)),
			Content:     *item.Error,
			SourceRunID: &runID,
			SourceStage: &stageName,
			Confidence:  Ptr(0.5),
			Pinned:      false,
		})
	}
	return obs
}
