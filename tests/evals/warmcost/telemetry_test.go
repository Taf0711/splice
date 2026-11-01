package warmcost

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }

// telemetryFinal builds a typed PipelineResult that is valid under the harness
// branch schema, plus the Option B telemetry fields the typed parser ignores.
// Three priced requests: two code_writer rounds (one hit, one miss) and one
// test_generator round. prompt_layout_flips is configurable.
func telemetryFinal(t *testing.T, runID string, flips *int) ExecResult {
	t.Helper()
	priced := func(seq, in, cached int, cost float64, source, stage string) map[string]any {
		return map[string]any{
			"sequence":            seq,
			"stage":               stage,
			"iteration":           seq,
			"spend_source":        source,
			"usage_reported":      true,
			"input_tokens":        in,
			"output_tokens":       10,
			"cached_input_tokens": cached,
			"cost_usd":            cost,
			"cost_status":         schemas.CostStatusPriced,
			"cost_provenance":     "reported",
			"pricing_source":      "test",
			"pricing_as_of":       "2026-09-13",
		}
	}
	records := []map[string]any{
		priced(1, 1000, 600, 0.010, schemas.SpendSourceGeneration, "code_writer"),
		priced(2, 1200, 0, 0.020, schemas.SpendSourceRepair, "code_writer"),
		priced(3, 800, 800, 0.005, schemas.SpendSourceGeneration, "test_generator"),
	}
	// Option B telemetry, keyed by sequence. code_writer flips (H1 -> H2);
	// test_generator is stable (H3).
	telemetry := []map[string]any{
		{"sequence": 1, "prompt_layout_hash": "H1", "cache_hit": true, "memory_position": "after_prefix"},
		{"sequence": 2, "prompt_layout_hash": "H2", "cache_hit": false, "memory_position": "after_prefix"},
		{"sequence": 3, "prompt_layout_hash": "H3", "cache_hit": true, "memory_position": "after_prefix"},
	}
	for i := range records {
		records[i]["prompt_layout_hash"] = telemetry[i]["prompt_layout_hash"]
		records[i]["cache_hit"] = telemetry[i]["cache_hit"]
		records[i]["memory_position"] = telemetry[i]["memory_position"]
	}
	payload := map[string]any{
		"run_id": runID, "status": "completed", "tier": "light",
		"stages": []any{}, "cost_coverage": "complete",
		"total_cost_usd": 0.035, "total_tokens_input": 3000,
		"total_tokens_output": 30, "total_tokens_cached": 1400,
		"priced_request_count": 3, "unpriced_request_count": 0, "error_request_count": 0,
		"usage_records": records,
	}
	if flips != nil {
		payload["prompt_layout_flips"] = *flips
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(map[string]any{"type": "final", "text": string(body)})
	return ExecResult{Stdout: append(line, '\n')}
}

func TestParseCacheTelemetryDecodesFields(t *testing.T) {
	res := telemetryFinal(t, "run", intPtr(1))
	tel, err := parseCacheTelemetry(res.Stdout)
	if err != nil {
		t.Fatalf("parseCacheTelemetry: %v", err)
	}
	if !tel.Reported {
		t.Fatal("Reported = false, want true")
	}
	if tel.Flips == nil || *tel.Flips != 1 {
		t.Fatalf("Flips = %v, want 1", tel.Flips)
	}
	if got := tel.BySequence[2].PromptLayoutHash; got != "H2" {
		t.Fatalf("seq2 hash = %q, want H2", got)
	}
	if h := tel.BySequence[2].CacheHit; h == nil || *h != false {
		t.Fatalf("seq2 cache_hit = %v, want false", h)
	}
	if got := tel.BySequence[3].MemoryPosition; got != "after_prefix" {
		t.Fatalf("seq3 memory_position = %q", got)
	}
}

// A telemetry-emitting binary reports a zero flip count with the omitempty
// field absent. Absent must decode to the reported zero, not unknown.
func TestParseCacheTelemetryAbsentFlipsWithTelemetryIsZero(t *testing.T) {
	res := telemetryFinal(t, "run", nil)
	tel, err := parseCacheTelemetry(res.Stdout)
	if err != nil {
		t.Fatalf("parseCacheTelemetry: %v", err)
	}
	if !tel.Reported {
		t.Fatal("Reported = false, want true (usage records carry telemetry)")
	}
	if tel.Flips == nil || *tel.Flips != 0 {
		t.Fatalf("Flips = %v, want reported zero", tel.Flips)
	}
}

// A binary that emits no telemetry must report Reported=false and a nil flip
// count. Absence is never read as a miss or as a zero flip.
func TestParseCacheTelemetryAbsentIsUnknown(t *testing.T) {
	payload := map[string]any{
		"run_id": "run", "status": "completed", "tier": "light",
		"stages": []any{}, "cost_coverage": "not_applicable",
		"total_cost_usd": 0, "priced_request_count": 0,
	}
	body, _ := json.Marshal(payload)
	line, _ := json.Marshal(map[string]any{"type": "final", "text": string(body)})
	tel, err := parseCacheTelemetry(append(line, '\n'))
	if err != nil {
		t.Fatalf("parseCacheTelemetry: %v", err)
	}
	if tel.Reported {
		t.Fatal("Reported = true, want false")
	}
	if tel.Flips != nil {
		t.Fatalf("Flips = %d, want nil (unknown, not zero)", *tel.Flips)
	}
}

func TestMergeCacheTelemetryCopiesOntoRecords(t *testing.T) {
	records := []RequestRecord{
		{Sequence: 1, Stage: "code_writer"},
		{Sequence: 2, Stage: "code_writer"},
	}
	tel := cacheTelemetry{Reported: true, Flips: intPtr(0), BySequence: map[int]requestTelemetry{
		2: {PromptLayoutHash: "H2", CacheHit: boolPtr(true), MemoryPosition: "after_prefix"},
	}}
	merged := mergeCacheTelemetry(records, tel)
	if merged[0].CacheHit != nil || merged[0].PromptLayoutHash != "" {
		t.Fatalf("record with no telemetry entry got %+v, want unknown", merged[0])
	}
	if merged[1].PromptLayoutHash != "H2" || merged[1].CacheHit == nil || !*merged[1].CacheHit {
		t.Fatalf("record 2 did not receive telemetry: %+v", merged[1])
	}
	if records[1].PromptLayoutHash != "" {
		t.Fatal("merge mutated the input slice")
	}
}

func TestComputeRoundsPerStageIteration(t *testing.T) {
	records := []RequestRecord{
		{Stage: "code_writer", Iteration: 1, SpendSource: schemas.SpendSourceGeneration, InputTokens: 1000, CachedTokens: 600},
		{Stage: "code_writer", Iteration: 2, SpendSource: schemas.SpendSourceRepair, InputTokens: 1200, CachedTokens: 0},
		{Stage: "code_writer", Iteration: 2, SpendSource: schemas.SpendSourceGeneration, InputTokens: 800, CachedTokens: 400},
	}
	rounds := computeRounds(records)
	if len(rounds) != 2 {
		t.Fatalf("len(rounds) = %d, want 2", len(rounds))
	}
	r1 := rounds[0]
	if r1.Stage != "code_writer" || r1.Iteration != 1 || r1.Requests != 1 || r1.InputTokens != 1000 || r1.CachedTokens != 600 {
		t.Fatalf("round 1 = %+v", r1)
	}
	if math.Abs(r1.CacheHitRate-0.6) > 1e-9 {
		t.Fatalf("round 1 hit rate = %v, want 0.6", r1.CacheHitRate)
	}
	r2 := rounds[1]
	if r2.Requests != 2 || r2.InputTokens != 2000 || r2.CachedTokens != 400 {
		t.Fatalf("round 2 = %+v", r2)
	}
	if r2.SpendSource != "generation+repair" {
		t.Fatalf("round 2 spend source = %q, want generation+repair", r2.SpendSource)
	}
}

func TestComputeLayoutStabilityDetectsFlip(t *testing.T) {
	records := []RequestRecord{
		{Stage: "code_writer", PromptLayoutHash: "H1"},
		{Stage: "code_writer", PromptLayoutHash: "H2"},
		{Stage: "test_generator", PromptLayoutHash: "H3"},
		{Stage: "test_generator"},
	}
	layouts := computeLayoutStability(records)
	if len(layouts) != 2 {
		t.Fatalf("len(layouts) = %d, want 2", len(layouts))
	}
	if layouts[0].Stable || !layouts[0].Hashed || layouts[0].RequestsWithHash != 2 {
		t.Fatalf("code_writer layout = %+v, want a detected flip", layouts[0])
	}
	if !layouts[1].Stable || !layouts[1].Hashed {
		t.Fatalf("test_generator layout = %+v, want stable", layouts[1])
	}
}

func TestComputeLayoutStabilityNoHashIsUnknown(t *testing.T) {
	layouts := computeLayoutStability([]RequestRecord{{Stage: "code_writer"}})
	if len(layouts) != 1 || layouts[0].Hashed || layouts[0].Stable {
		t.Fatalf("layouts = %+v, want hashed=false and stable=false", layouts)
	}
}

// TestRunnerCapturesCacheTelemetry proves the wiring end to end: the runner
// merges the Option B telemetry onto the attempt, records the flip count and
// the per-round rows, and reports the non-zero flip as a finding.
func TestRunnerCapturesCacheTelemetry(t *testing.T) {
	outDir := t.TempDir()
	cfg := Config{
		Binary:  "/bin/true",
		OutDir:  outDir,
		Repeats: 1,
		Arms:    []Arm{ArmCold},
		Tasks:   []Task{{ID: "t1", Prompt: "do", Check: "true"}},
	}
	seam := func(_ context.Context, _ ExecRequest) (ExecResult, error) {
		return telemetryFinal(t, "run-1", intPtr(1)), nil
	}
	r, err := NewRunner(cfg, seam)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	runDirs, _ := filepath.Glob(filepath.Join(outDir, "*"))
	if len(runDirs) != 1 {
		t.Fatalf("run dirs = %v", runDirs)
	}
	raw, err := os.ReadFile(filepath.Join(runDirs[0], "attempt-t1-cold-r0.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got Attempt
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !got.CacheTelemetryReported {
		t.Fatal("CacheTelemetryReported = false, want true")
	}
	if got.PromptLayoutFlips == nil || *got.PromptLayoutFlips != 1 {
		t.Fatalf("PromptLayoutFlips = %v, want 1", got.PromptLayoutFlips)
	}
	if len(got.Rounds) != 3 {
		t.Fatalf("len(Rounds) = %d, want 3: %+v", len(got.Rounds), got.Rounds)
	}
	if len(got.LayoutStability) != 2 {
		t.Fatalf("len(LayoutStability) = %d, want 2", len(got.LayoutStability))
	}
	if got.LayoutStability[0].Stage != "code_writer" || got.LayoutStability[0].Stable {
		t.Fatalf("code_writer layout = %+v, want a flip", got.LayoutStability[0])
	}
	if got.Requests[1].CacheHit == nil || *got.Requests[1].CacheHit != false {
		t.Fatalf("request 2 cache_hit = %v, want false", got.Requests[1].CacheHit)
	}
	// The aggregate must surface the finding, not hide it.
	aggRaw, err := os.ReadFile(filepath.Join(runDirs[0], "aggregate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var agg Aggregate
	if err := json.Unmarshal(aggRaw, &agg); err != nil {
		t.Fatal(err)
	}
	if agg.CacheLayout.TotalPromptLayoutFlips != 1 {
		t.Fatalf("aggregate flips = %d, want 1", agg.CacheLayout.TotalPromptLayoutFlips)
	}
	report, err := os.ReadFile(filepath.Join(runDirs[0], "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Total prompt layout flips: 1", "code_writer", "H1, H2"} {
		if !strings.Contains(string(report), want) {
			t.Fatalf("report is missing %q:\n%s", want, report)
		}
	}
}
