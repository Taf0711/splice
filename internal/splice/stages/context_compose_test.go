package stages

// B3 regression tests: compose model context once. The test generator's
// provider payload must carry the fulfilled source evidence (post-write),
// source text is serialized once (no nested JSON encoding), duplicate
// ranges dedupe, and prior summaries do not flood the payload.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// b3Provider reuses the existing requestCapturingProvider shape.
type b3Provider = requestCapturingProvider

// b3TestGeneratorPayload runs the test generator with the given input and
// returns the raw user prompt the provider received.
func b3TestGeneratorPayload(t *testing.T, input schemas.HarnessStageInput) string {
	t.Helper()
	output := schemas.TestGeneratorOutput{Files: []schemas.FileChange{}, Language: "go", Intent: "no changes", Confidence: 0.9}
	args, _ := json.Marshal(output)
	provider := &b3Provider{events: toolCallEvent("submit_tests", string(args))}
	if _, err := (TestGenerator{}).Run(context.Background(), input, provider, StageOptions{WorkDir: t.TempDir(), Language: "go"}); err != nil {
		t.Fatalf("stage run: %v", err)
	}
	payload := modelUserPayload(t, provider.request)
	return payload
}

// TestTestGeneratorPayloadCarriesContextSourceEvidence pins the B3 fix:
// source evidence from a fulfilled context bundle reaches the provider
// payload. The historical bug omitted the bundle from RelevantContext, so
// fetched source never reached the model.
func TestTestGeneratorPayloadCarriesContextSourceEvidence(t *testing.T) {
	input := newHarnessInput("write tests for the retention cutoff")
	input.Context = &schemas.ContextBundle{
		Items: []schemas.ContextItem{{
			Query:   schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: b3StrPtr("retention.go"), MaxResults: 5, MaxChars: 2000},
			Summary: "Read retention.go.",
			Payload: map[string]any{
				"text":    "func enforceRetention(cutoff int) {\n\tif olderThan(cutoff) {\n\t\tpurge()\n\t}\n}",
				"path":    "retention.go",
				"version": "abc123",
				"start":   1,
				"end":     5,
			},
		}},
	}
	payload := b3TestGeneratorPayload(t, input)
	if !strings.Contains(payload, "enforceRetention") {
		t.Fatalf("fulfilled source evidence missing from the provider payload: %s", payload)
	}
	if !strings.Contains(payload, "retention.go") {
		t.Fatalf("source path missing from the provider payload")
	}
	// The writer's summary appears at most once; the source bytes are the
	// evidence, not the summary.
	if strings.Count(payload, "context read_file") != 1 {
		t.Fatalf("context block count wrong: %s", payload)
	}
}

// TestFormatContextBundleSerializesSourceOnce pins the single-encoding
// rule: a text payload is delivered as plain text, not as a JSON-escaped
// string embedded in another string. The tell: JSON quotes/escapes must
// not wrap the source text.
func TestFormatContextBundleSerializesSourceOnce(t *testing.T) {
	source := "func one() {\n\treturn \"quoted\"\n}"
	bundle := &schemas.ContextBundle{
		Items: []schemas.ContextItem{{
			Query:   schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: b3StrPtr("a.go"), MaxResults: 5, MaxChars: 2000},
			Summary: "Read a.go.",
			Payload: map[string]any{"text": source, "path": "a.go", "version": "v1", "start": 1, "end": 3},
		}},
	}
	lines := formatContextBundle(bundle)
	if len(lines) != 1 {
		t.Fatalf("formatted blocks = %d, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "\"quoted\"") {
		t.Fatalf("source text lost: %q", lines[0])
	}
	// JSON-escaping would render this as \"quoted\" with backslashes.
	if strings.Contains(lines[0], "\\\"quoted\\\"") {
		t.Fatalf("source text was JSON-escaped a second time: %q", lines[0])
	}
}

// TestFormatContextBundleDedupesByIdentity pins the dedupe rule: two
// fulfilled items with the same (path, version, range) identity deliver
// their bytes once. Dedupe is by identity, never by substring guess.
func TestFormatContextBundleDedupesByIdentity(t *testing.T) {
	makeItem := func() schemas.ContextItem {
		return schemas.ContextItem{
			Query:   schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: b3StrPtr("a.go"), MaxResults: 5, MaxChars: 2000},
			Summary: "Read a.go.",
			Payload: map[string]any{"text": "func one() {}", "path": "a.go", "version": "v1", "start": 1, "end": 1},
		}
	}
	bundle := &schemas.ContextBundle{Items: []schemas.ContextItem{makeItem(), makeItem()}}
	lines := formatContextBundle(bundle)
	if len(lines) != 1 {
		t.Fatalf("duplicate identity delivered twice: %d blocks", len(lines))
	}
	// A DIFFERENT range of the same file is distinct evidence.
	other := makeItem()
	other.Payload["start"], other.Payload["end"] = 10, 20
	bundle.Items = append(bundle.Items, other)
	lines = formatContextBundle(bundle)
	if len(lines) != 2 {
		t.Fatalf("distinct ranges must both deliver: %d blocks", len(lines))
	}
}

// TestTestGeneratorPayloadPriorSummariesBounded pins the discipline rule:
// the code_writer summary rides once as a pointer; the payload is source
// evidence plus the required failure/revision context, not a dump of
// every stage's prose.
func TestTestGeneratorPayloadPriorSummariesBounded(t *testing.T) {
	input := newHarnessInput("write tests")
	input.PriorSummaries = map[string]string{
		"code_writer":      "implemented the fix",
		"static_analyzer":  "no issues found",
		"security_auditor": "nothing to report",
	}
	payload := b3TestGeneratorPayload(t, input)
	if strings.Count(payload, "implemented the fix") != 1 {
		t.Fatalf("code_writer summary count wrong: %s", payload)
	}
	// The test generator contract keeps the code_writer summary (tests
	// target the writer's work); other stages' summaries are not part of
	// the test generator's input contract.
	if strings.Contains(payload, "no issues found") {
		t.Fatal("static_analyzer summary must not flood the test generator payload")
	}
	if strings.Contains(payload, "nothing to report") {
		t.Fatal("security_auditor summary must not flood the test generator payload")
	}
}

// TestTestGeneratorRequestsPostWriteSourceWithPullContext pins the B3
// post-write request: with PullContext and writer-changed files and no
// bundle yet, the stage requests the changed files' current bytes.
func TestTestGeneratorRequestsPostWriteSourceWithPullContext(t *testing.T) {
	input := newHarnessInput("write tests for the implementation")
	input.PriorChangedFiles = map[string][]string{"code_writer": {"storage.go"}}
	var reported []string
	output, err := (TestGenerator{}).Run(context.Background(), input, &b3Provider{}, StageOptions{
		WorkDir: t.TempDir(), Language: "go", PullContext: true,
		ReportActivity: func(msg string) { reported = append(reported, msg) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if output.ContextRequest == nil {
		t.Fatalf("expected a post-write source request, got %+v", output)
	}
	if !strings.Contains(output.ContextRequest.Reason, "post-write") {
		t.Fatalf("request reason = %q", output.ContextRequest.Reason)
	}
	if len(output.ContextRequest.Queries) != 1 || *output.ContextRequest.Queries[0].Path != "storage.go" {
		t.Fatalf("requested queries = %+v, want one read of storage.go", output.ContextRequest.Queries)
	}
}

func b3StrPtr(s string) *string { return &s }
