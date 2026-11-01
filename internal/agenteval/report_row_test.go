package agenteval

import (
	"encoding/json"
	"strings"
	"testing"
)

func intPtr(v int) *int { return &v }

func passingReport() Report {
	return Report{
		Contract: ReportContractVersion,
		SuiteID:  "suite-1",
		TaskID:   "task-1",
		Status:   StatusPass,
		OK:       true,
		Results: []Result{
			{ID: "v1", Kind: ResultCommand, Status: StatusPass},
		},
	}
}

// A passing report derives no taxonomy and marks verified success.
func TestRowFromPassingReport(t *testing.T) {
	row := RowFromReport(passingReport(), RowMeta{Attempt: 1})
	if !row.VerifiedSuccess {
		t.Fatal("passing report must mark verified success")
	}
	if row.Taxonomy != "" {
		t.Fatalf("passing report must have no taxonomy, got %q", row.Taxonomy)
	}
	if row.Contract != ReportContractRowVersion {
		t.Fatalf("contract = %q", row.Contract)
	}
}

// A failing verifier command maps to verification_failure (a task failure),
// NOT infrastructure (section 33 separation).
func TestTaxonomyVerificationFailure(t *testing.T) {
	report := Report{SuiteID: "s", TaskID: "t", Status: StatusFail, OK: false, Results: []Result{
		{ID: "v1", Kind: ResultCommand, Status: StatusFail, ExitCode: intPtr(1)},
	}}
	if got := DeriveTaxonomy(report); got != TaxonomyVerification {
		t.Fatalf("taxonomy = %q, want %q", got, TaxonomyVerification)
	}
}

// A blocked run is infrastructure: the agent never ran.
func TestTaxonomyBlockedIsInfrastructure(t *testing.T) {
	report := Report{SuiteID: "s", TaskID: "t", Status: StatusBlocked, OK: false}
	if got := DeriveTaxonomy(report); got != TaxonomyInfrastructure {
		t.Fatalf("taxonomy = %q, want %q", got, TaxonomyInfrastructure)
	}
}

// A command that ERRORED (not failed) is infrastructure: the harness could
// not execute the verifier, so the agent's work was never judged.
func TestTaxonomyCommandErrorIsInfrastructure(t *testing.T) {
	report := Report{SuiteID: "s", TaskID: "t", Status: StatusFail, OK: false, Results: []Result{
		{ID: "v1", Kind: ResultCommand, Status: StatusError, Message: "verifier binary missing"},
	}}
	if got := DeriveTaxonomy(report); got != TaxonomyInfrastructure {
		t.Fatalf("taxonomy = %q, want %q", got, TaxonomyInfrastructure)
	}
}

// A suite-level error with no command evidence is an agent error.
func TestTaxonomySuiteErrorIsAgentError(t *testing.T) {
	report := Report{SuiteID: "s", TaskID: "t", Status: StatusError, OK: false}
	if got := DeriveTaxonomy(report); got != TaxonomyAgentError {
		t.Fatalf("taxonomy = %q, want %q", got, TaxonomyAgentError)
	}
}

// Usage folding takes the LAST sample with token totals (cumulative
// stream-json semantics) and keeps absent usage absent.
func TestRowFoldsLastUsageSample(t *testing.T) {
	cost := 0.0123
	meta := RowMeta{
		Attempt: 2,
		UsageSamples: []UsageSample{
			{InputTokens: 100, OutputTokens: 10},
			{InputTokens: 250, OutputTokens: 40, ReasoningTokens: 5, CachedInputTokens: 20, CostUSD: &cost, CostProvenance: "litllm:openrouter"},
		},
	}
	row := RowFromReport(passingReport(), meta)
	if row.TokensInput != 250 || row.TokensOutput != 40 {
		t.Fatalf("usage fold wrong: in=%d out=%d", row.TokensInput, row.TokensOutput)
	}
	if row.TokensReasoning != 5 || row.TokensCached != 20 {
		t.Fatalf("reasoning/cached fold wrong: %d/%d", row.TokensReasoning, row.TokensCached)
	}
	if row.EstimatedCostUSD == nil || *row.EstimatedCostUSD != cost {
		t.Fatalf("cost fold wrong: %v", row.EstimatedCostUSD)
	}
	if row.CostCoverage != "litllm:openrouter" {
		t.Fatalf("cost provenance = %q", row.CostCoverage)
	}
	if row.ModelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", row.ModelCalls)
	}
	if row.Attempt != 2 {
		t.Fatalf("attempt = %d", row.Attempt)
	}
}

// No usage samples: usage fields stay absent (omitempty), never zero-faked.
func TestRowAbsentUsageStaysAbsent(t *testing.T) {
	row := RowFromReport(passingReport(), RowMeta{Attempt: 1})
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"tokens_input", "tokens_output", "estimated_cost_usd", "model_calls"} {
		if _, present := probe[key]; present {
			t.Fatalf("absent metric %q must not serialize", key)
		}
	}
}

// Forbidden-file violations count as forbidden modifications.
func TestRowCountsForbiddenModifications(t *testing.T) {
	report := passingReport()
	report.OK = false
	report.Status = StatusFail
	report.Results = append(report.Results, Result{
		ID:              "forbidden_files",
		Kind:            ResultChangedFiles,
		Status:          StatusFail,
		UnexpectedFiles: []string{"README.md", "go.mod"},
	})
	row := RowFromReport(report, RowMeta{Attempt: 1})
	if row.ForbiddenModifications != 2 {
		t.Fatalf("forbidden modifications = %d, want 2", row.ForbiddenModifications)
	}
	if row.VerifiedSuccess {
		t.Fatal("failing report must not claim verified success")
	}
}

// Round-trip: rows serialize under the row contract and validate.
func TestRowContractRoundTrip(t *testing.T) {
	cost := 0.5
	meta := RowMeta{
		Attempt: 3, SpliceCommit: "abc123", FixtureCommit: "def456",
		ModelID: "glm", ProviderRoute: "openrouter", OSArch: "darwin/arm64",
		TimeoutSecs: 600, Autonomy: "auto", StartedUnix: 1000,
		UsageSamples: []UsageSample{{InputTokens: 10, OutputTokens: 2, CostUSD: &cost}},
	}
	row := RowFromReport(passingReport(), meta)
	data, err := MarshalRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if !RowContractMatches(data) {
		t.Fatalf("row contract mismatch: %s", data)
	}
	var back ReportRow
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.SpliceCommit != "abc123" || back.FixtureCommit != "def456" || back.TaskID != "task-1" {
		t.Fatalf("round-trip lost provenance: %+v", back)
	}
	if back.EstimatedCostUSD == nil || *back.EstimatedCostUSD != 0.5 {
		t.Fatalf("round-trip lost cost: %v", back.EstimatedCostUSD)
	}
}

// Section-30 attempts log: every task attempt folds to exactly one JSONL
// row, one per line, each declaring the row contract.
func TestWriteAttemptsJSONLOneRowPerTask(t *testing.T) {
	report := BenchmarkReport{
		Contract: BenchmarkContractVersion,
		SuiteID:  "suite-1",
		Tasks: []BenchmarkTaskReport{
			{TaskID: "t1", Model: "m1", Report: passingReport()},
			{TaskID: "t2", Model: "m1", Report: passingReport()},
		},
	}
	var buf strings.Builder
	if err := WriteAttemptsJSONL(&buf, report); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL rows, got %d", len(lines))
	}
	for i, line := range lines {
		if !RowContractMatches([]byte(line)) {
			t.Fatalf("line %d does not declare the row contract: %s", i+1, line)
		}
		var row ReportRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i+1, err)
		}
		if row.TaskID != report.Tasks[i].TaskID {
			t.Fatalf("line %d task = %q, want %q", i+1, row.TaskID, report.Tasks[i].TaskID)
		}
		if row.SuiteID != "suite-1" {
			t.Fatalf("line %d suite = %q, want suite-1", i+1, row.SuiteID)
		}
		if row.Attempt != 1 {
			t.Fatalf("line %d attempt = %d, want 1", i+1, row.Attempt)
		}
	}
}

// Unknown cost stays unknown: a task with no usage samples must produce a
// row whose cost field is ABSENT (not zero) and whose usage fields stay
// absent too (section 30, fake-zero prohibition).
func TestWriteAttemptsJSONLUnknownCostStaysUnknown(t *testing.T) {
	report := BenchmarkReport{
		Contract: BenchmarkContractVersion,
		SuiteID:  "s",
		Tasks: []BenchmarkTaskReport{
			{TaskID: "blocked-task", Model: "m", Report: Report{
				Contract: ReportContractVersion, SuiteID: "s", TaskID: "blocked-task",
				Status: StatusBlocked, OK: false,
			}},
		},
	}
	var buf strings.Builder
	if err := WriteAttemptsJSONL(&buf, report); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(buf.String())
	if strings.Contains(line, "estimated_cost_usd") {
		t.Fatalf("unknown cost leaked as a field: %s", line)
	}
	if strings.Contains(line, "tokens_input") {
		t.Fatalf("unknown usage leaked as a field: %s", line)
	}
	// The blocked run still classifies as infrastructure (section 33):
	// a benchmark bug must never count as a coding failure.
	var row ReportRow
	if err := json.Unmarshal([]byte(line), &row); err != nil {
		t.Fatal(err)
	}
	if row.Taxonomy != TaxonomyInfrastructure {
		t.Fatalf("blocked run taxonomy = %q, want %q", row.Taxonomy, TaxonomyInfrastructure)
	}
	if row.VerifiedSuccess {
		t.Fatal("blocked run must not claim verified success")
	}
}
