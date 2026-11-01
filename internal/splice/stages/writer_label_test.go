package stages

import (
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// The writer label names the writer's plan name when a topology renames it.
func TestWriterSummaryLineNamesTheWriterPlanStage(t *testing.T) {
	summary := map[string]string{"code_writer": "wrote add.go"}
	if got := writerSummaryLine(schemas.HarnessStageInput{PriorSummaries: summary}); got != "code_writer: wrote add.go" {
		t.Fatalf("builtin writer label = %q", got)
	}
	if got := writerSummaryLine(schemas.HarnessStageInput{PriorSummaries: summary, WriterStage: "writer"}); got != "writer: wrote add.go" {
		t.Fatalf("renamed writer label = %q", got)
	}
	if got := writerSummaryLine(schemas.HarnessStageInput{WriterStage: "writer"}); got != "" {
		t.Fatalf("no summary must give no label, got %q", got)
	}
}
