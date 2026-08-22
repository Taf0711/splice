package stages

// Tests for the bounded failing-evidence excerpt that test_runner embeds in
// its summary so revision context carries real assertion text to the
// re-entered writer instead of "exit code 1".

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestFailingEvidenceExcerptExtractsMarkersAndAssertions(t *testing.T) {
	output := strings.Join([]string{
		"=== RUN   TestAuditTombstone",
		"    audit_test.go:12: setup",
		"--- FAIL: TestAuditTombstone (0.00s)",
		"    audit_test.go:20: last audit action = \"delete\", want tombstone",
		"FAIL",
		"ok  example.com/demo 0.1s",
	}, "\n")

	excerpt := failingEvidenceExcerpt(output)
	if !strings.Contains(excerpt, "--- FAIL: TestAuditTombstone") {
		t.Fatalf("excerpt missing the fail marker:\n%s", excerpt)
	}
	if !strings.Contains(excerpt, `last audit action = "delete", want tombstone`) {
		t.Fatalf("excerpt missing the assertion text:\n%s", excerpt)
	}
	if strings.Contains(excerpt, "=== RUN") || strings.Contains(excerpt, "ok  example.com/demo") {
		t.Fatalf("excerpt must keep only marker blocks, got:\n%s", excerpt)
	}
}

func TestFailingEvidenceExcerptIsBoundedAndTailBiased(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines,
			fmt.Sprintf("--- FAIL: TestEarly%d (0.00s)", i),
			fmt.Sprintf("    early_test.go:%d: early failure padding padding padding", i))
	}
	lines = append(lines,
		"--- FAIL: TestTheRealOne (0.00s)",
		"    audit_test.go:99: last audit action = \"delete\", want tombstone")
	huge := strings.Join(lines, "\n")

	excerpt := failingEvidenceExcerpt(huge)
	if len(excerpt) > maxFailureEvidenceChars+len(failureEvidenceTruncatedNotice)+2 {
		t.Fatalf("excerpt length = %d, exceeds the budget", len(excerpt))
	}
	if !strings.Contains(excerpt, "TestTheRealOne") {
		t.Fatal("tail bias must keep the LAST failure")
	}
	if !strings.Contains(excerpt, failureEvidenceTruncatedNotice) {
		t.Fatal("dropped earlier failures must be flagged as omitted")
	}
}

func TestFailingEvidenceExcerptHandlesNonMarkerOutput(t *testing.T) {
	if got := failingEvidenceExcerpt("panic: boom\n\ngoroutine 1 [running]:"); got != "" {
		t.Fatalf("output without markers must yield an empty excerpt, got %q", got)
	}
	if got := failingEvidenceExcerpt(""); got != "" {
		t.Fatalf("empty output must yield an empty excerpt, got %q", got)
	}
}

// TestTestRunnerSummaryCarriesFailureEvidence is the pairing pin: a seeded
// failing run must land the marker and the assertion text in OutputSummary —
// the exact field buildRevisionContext and repair priorSummaries transport to
// the re-entered code_writer.
func TestTestRunnerSummaryCarriesFailureEvidence(t *testing.T) {
	failingOutput := strings.Join([]string{
		"--- FAIL: TestAuditTombstone (0.00s)",
		"    audit_test.go:20: last audit action = \"delete\", want tombstone",
		"FAIL",
	}, "\n")
	options := StageOptions{
		Command:        []string{"true"},
		TimeoutSeconds: 5,
		RecordCommand: func(_ context.Context, name string, args map[string]any, run func(context.Context) (ToolResult, error)) (ToolResult, error) {
			// The runner executes the real command inside the closure and then
			// decodes recorded.Output as JSON test results, so the stub returns
			// seeded failing results in exactly that wire shape.
			_ = name
			_ = args
			_ = run
			payload, _ := json.Marshal(schemas.TestRunResults{
				Command:  []string{"true"},
				ExitCode: 1,
				Stdout:   failingOutput,
			})
			return ToolResult{OK: false, Output: string(payload)}, nil
		},
	}
	output, err := TestRunner{}.Run(context.Background(), newHarnessInput("run tests"), nil, options)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(output.Summary, "--- FAIL: TestAuditTombstone") ||
		!strings.Contains(output.Summary, `want tombstone`) {
		t.Fatalf("summary lacks failure evidence:\n%s", output.Summary)
	}
}
