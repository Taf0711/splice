package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/web"
)

// execSurface serves the browser surface for one `splice exec --serve` run.
type execSurface struct {
	server   *web.Server
	pipeline *web.PipelineView
	secrets  []string
	begun    sync.Once
}

// startExecSurface starts the local server and prints its one-time URL to
// stderr. stdout stays free for machine-read output.
func startExecSurface(ctx context.Context, stderr io.Writer) (*execSurface, error) {
	server := web.New(0)
	pipeline := web.NewPipelineView(splicerun.TopologySources{}, "")
	if err := pipeline.Attach(server); err != nil {
		return nil, fmt.Errorf("start surface server: %w", err)
	}
	bootstrap, err := server.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("start surface server: %w", err)
	}
	fmt.Fprintf(stderr, "[splice] surface: %s\n", bootstrap)
	return &execSurface{server: server, pipeline: pipeline}, nil
}

// stop shuts the server down. It is nil-safe, so a run without --serve can defer it.
func (s *execSurface) stop() {
	if s != nil {
		s.server.Stop()
	}
}

// sink returns the run's EventSink, or nil when --serve is off.
func (s *execSurface) sink() func(agent.StageEvent) {
	if s == nil {
		return nil
	}
	return s.server.Sink()
}

// configurePipeline points the pipeline viewer at the topology this run
// resolves, the stage-models.json path, and the secrets to redact.
func (s *execSurface) configurePipeline(sources splicerun.TopologySources, stageConfigPath string, secrets []string) {
	if s == nil {
		return
	}
	s.pipeline.Configure(sources, stageConfigPath)
	s.secrets = append([]string(nil), secrets...)
}

// pipelineViewSources returns the topology sources of the pipeline viewer: the
// sources that the run resolves (as resolveRunTopology in internal/splice). A
// design plan (--plan) runs the default topology, so the viewer shows it.
func pipelineViewSources(workspaceRoot string, trusted bool, pipeline, planPath string) splicerun.TopologySources {
	if planPath != "" {
		return splicerun.TopologySources{}
	}
	sources := splicerun.TopologySourcesFor(workspaceRoot, trusted)
	sources.FlagName = pipeline
	return sources
}

// presentation returns the run's OnPresentationState: next, and the pipeline
// viewer when --serve is on.
func (s *execSurface) presentation(next func(presentation.State)) func(presentation.State) {
	if s == nil {
		return next
	}
	return func(state presentation.State) {
		next(state)
		s.begun.Do(func() {
			s.pipeline.BeginRun(func() ([]string, error) { return s.secrets, nil })
		})
		s.pipeline.Observe(state)
	}
}

// finish gives the pipeline viewer the end of the run: the stage records of
// the result, or, when the run returned an error, a CANCELLED or FAILED end.
// A cancelled run skips the pipeline's own end event.
func (s *execSurface) finish(finalAnswer string, runErr error) {
	if s == nil {
		return
	}
	switch {
	case runErr == nil:
		s.pipeline.RecordResult(finalAnswer)
	case errors.Is(runErr, context.Canceled):
		s.pipeline.EndRun("cancelled", "")
	default:
		s.pipeline.EndRun("failed", runErr.Error())
	}
}
