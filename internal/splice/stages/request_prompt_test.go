package stages

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// promptCapture records the first request a stage sends and then stops.
type promptCapture struct {
	request *zeroruntime.CompletionRequest
}

var errStopAfterCapture = errors.New("stop after capture")

func (p *promptCapture) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	if p.request == nil {
		p.request = &request
	}
	return nil, errStopAfterCapture
}

// TestCodeWriterPromptCarriesTheFullRequest pins audit item E03: the writer's
// prompt holds the full request, code fences and all, not the 320-character
// distilled intent.
func TestCodeWriterPromptCarriesTheFullRequest(t *testing.T) {
	full := "Bug report: the parser crashes.\n\n```go\n" + strings.Repeat("\tvalue := compute(item)  // step\n", 60) + "}\n```\n\nFix it."
	if len(full) < 2000 {
		t.Fatalf("fixture is %d bytes, want at least 2000", len(full))
	}
	input := schemas.HarnessStageInput{
		StageName:     "code_writer",
		RequestIntent: full[:300] + "...",
		Request:       full,
		Context:       &schemas.ContextBundle{},
	}
	capture := &promptCapture{}
	_, _ = CodeWriter{}.Run(context.Background(), input, capture, StageOptions{WorkDir: t.TempDir(), Language: "go"})
	if capture.request == nil {
		t.Fatal("the code writer sent no model request")
	}
	var prompt map[string]any
	if err := json.Unmarshal([]byte(capture.request.Messages[len(capture.request.Messages)-1].Content), &prompt); err != nil {
		t.Fatalf("writer prompt is not the payload JSON: %v", err)
	}
	if got, _ := prompt["intent"].(string); got != full {
		t.Fatalf("writer prompt intent is %d bytes, want the full %d-byte request intact", len(got), len(full))
	}
}
