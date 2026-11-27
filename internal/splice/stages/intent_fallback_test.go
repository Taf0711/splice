package stages

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// fullRequestWithPath is a request whose only file reference sits well past
// the 320-character distilled intent, so a fallback to RequestIntent loses it.
func fullRequestWithPath() string {
	return strings.Repeat("Background text describing the symptoms in detail. ", 10) + "\nThe fix belongs in deep/zz_target.go."
}

// TestCodeWriterSeedReadsTheFullRequestPath pins audit item F6 for the seed: the
// handshake ranks and names files from the full request, not the intent.
func TestCodeWriterSeedReadsTheFullRequestPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "deep", "zz_target.go"), []byte("package deep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// More than eight sources, so the small-workspace fallback cannot read
	// the file for us: only a named path can seed it.
	for i := 0; i < 12; i++ {
		seedFile(t, root, filepath.Join("pkg", "other"+string(rune('a'+i))+".go"), "package pkg\n")
	}
	input := schemas.HarnessStageInput{
		StageName:     "code_writer",
		RequestIntent: "Fix the symptoms described above.",
		Request:       fullRequestWithPath(),
	}
	output, err := CodeWriter{}.Run(context.Background(), input, nil, StageOptions{WorkDir: root, Language: "go", PullContext: true})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	found := false
	for _, q := range output.ContextRequest.Queries {
		if q.Path != nil && *q.Path == "deep/zz_target.go" {
			found = true
		}
	}
	if !found {
		t.Fatal("the seed must read deep/zz_target.go, named only in the full request")
	}
}

// TestTestGeneratorPromptCarriesTheFullRequest pins F6 for the test generator's
// prompt: the intent field holds the full request.
func TestTestGeneratorPromptCarriesTheFullRequest(t *testing.T) {
	full := fullRequestWithPath()
	input := schemas.HarnessStageInput{
		StageName:     "test_generator",
		RequestIntent: "Fix the symptoms described above.",
		Request:       full,
		Context:       &schemas.ContextBundle{},
	}
	capture := &promptCapture{}
	_, _ = TestGenerator{}.Run(context.Background(), input, capture, StageOptions{WorkDir: t.TempDir(), Language: "go"})
	if capture.request == nil {
		t.Fatal("the test generator sent no model request")
	}
	var prompt map[string]any
	if err := json.Unmarshal([]byte(capture.request.Messages[len(capture.request.Messages)-1].Content), &prompt); err != nil {
		t.Fatalf("test generator prompt is not the payload JSON: %v", err)
	}
	if got, _ := prompt["intent"].(string); got != full {
		t.Fatalf("test generator intent is %d bytes, want the full %d-byte request", len(got), len(full))
	}
}

// TestPromptStageRendersTheFullRequest pins F6 for the prompt stage: its
// {{intent}} placeholder renders the full request.
func TestPromptStageRendersTheFullRequest(t *testing.T) {
	full := fullRequestWithPath()
	input := schemas.HarnessStageInput{RequestIntent: "Fix the symptoms described above.", Request: full}
	got := renderPromptTemplate("Task: {{intent}}", input)
	if !strings.Contains(got, "deep/zz_target.go") {
		t.Fatalf("prompt stage rendered the distilled intent, not the full request: %q", got)
	}
}
