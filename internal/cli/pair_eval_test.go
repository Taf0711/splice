package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestParsePairEvalArgs(t *testing.T) {
	options, help, err := parsePairEvalArgs([]string{"--taskset", "/tmp/ts", "--out", "/tmp/out", "--model", "m1"})
	if err != nil {
		t.Fatalf("parsePairEvalArgs: %v", err)
	}
	if help {
		t.Fatal("help = true, want false")
	}
	if options.TasksetDir != "/tmp/ts" || options.OutDir != "/tmp/out" || options.Model != "m1" {
		t.Fatalf("options = %#v", options)
	}

	if _, _, err := parsePairEvalArgs([]string{"--taskset="}); err == nil {
		t.Fatal("empty --taskset must error")
	}
	if _, _, err := parsePairEvalArgs([]string{"--bogus"}); err == nil {
		t.Fatal("unknown flag must error")
	}
}

func TestSumStreamJSONTokens(t *testing.T) {
	transcript := strings.Join([]string{
		`{"totalTokens":5739,"type":"usage","stage":"code_writer"}`,
		`{"totalTokens":1200,"type":"usage","stage":"code_writer"}`,
		`{"type":"final","text":"{}"}`,
		"not json at all",
		`{"totalTokens":9999}`,
	}, "\n")
	if got := sumStreamJSONTokens([]byte(transcript)); got != 6939 {
		t.Fatalf("sum = %d, want only the usage records summed (6939)", got)
	}
	if got := sumStreamJSONTokens(nil); got != 0 {
		t.Fatalf("empty transcript sum = %d, want 0", got)
	}
}

// TestResolveRepoRootCanonicalizesSymlinks pins the trace-join fix: a query
// path that travels through a symlink (/var vs /private/var on macOS) must
// resolve to the same string the trace was stored under, and resolution
// failures fall back to the raw path instead of erroring.
func TestResolveRepoRootCanonicalizesSymlinks(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link-to-repo")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// t.TempDir() itself sits behind /var -> /private/var on macOS, so both
	// the symlink and its target must collapse to ONE canonical string.
	if got := resolveRepoRoot(link); got != resolveRepoRoot(target) {
		t.Fatalf("resolveRepoRoot(%q) = %q, want %q", link, got, resolveRepoRoot(target))
	}
	wantTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got := resolveRepoRoot(target); got != wantTarget {
		t.Fatalf("resolved path = %q, want %q", got, wantTarget)
	}
	if got := resolveRepoRoot(filepath.Join(target, "does-not-exist")); got != filepath.Join(target, "does-not-exist") {
		t.Fatalf("unresolvable path must fall back raw, got %q", got)
	}
}

// TestMatchTraceTokens pins found-vs-absent semantics for the trace join: a
// matching run or session id yields its sums with found=true; no match yields
// zeros with found=false so callers can tell absence from a measured zero.
func TestMatchTraceTokens(t *testing.T) {
	trace := schemas.RunOutcome{
		RunID:     "eval-ts-cold-a",
		SessionID: "sess-1",
		Stages: []schemas.TracedStage{
			{StageRecord: schemas.StageRecord{Name: "code_writer", TokensInput: 3000, TokensOutput: 2000}},
			{StageRecord: schemas.StageRecord{Name: "test_runner", TokensInput: 1000, TokensOutput: 500}},
		},
		Interventions: []schemas.InterventionRecord{{Weight: 2}},
	}
	results := []schemas.TraceQueryResult{{Trace: trace}}

	tokens, interventions, found := matchTraceTokens(results, "eval-ts-cold-a")
	if !found || tokens != 6500 || interventions != 2 {
		t.Fatalf("match by run id = (%d, %d, %v), want (6500, 2, true)", tokens, interventions, found)
	}
	tokens, _, found = matchTraceTokens(results, "sess-1")
	if !found || tokens != 6500 {
		t.Fatalf("match by session id = (%d, %v), want (6500, true)", tokens, found)
	}
	tokens, interventions, found = matchTraceTokens(results, "other")
	if found || tokens != 0 || interventions != 0 {
		t.Fatalf("no match = (%d, %d, %v), want absent-data zeros", tokens, interventions, found)
	}
}

// TestRepoRootQueryCandidatesPinsBothForms pins the trace-join lookup order:
// the raw stored form is tried first, and the symlink-resolved form is added
// only when it differs, so a join works whichever side recorded which form.
func TestRepoRootQueryCandidatesPinsBothForms(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	raw := link
	if got := repoRootQueryCandidates(raw); len(got) != 2 || got[0] != raw || got[1] == raw {
		t.Fatalf("candidates for a symlinked path = %v, want [raw, resolved]", got)
	}
	resolved := target
	got := repoRootQueryCandidates(resolved)
	canonical := resolveRepoRoot(resolved)
	if canonical == resolved {
		if len(got) != 1 || got[0] != resolved {
			t.Fatalf("candidates for a canonical path = %v, want only itself", got)
		}
	} else if len(got) != 2 || got[0] != resolved || got[1] != canonical {
		t.Fatalf("candidates for an aliased path = %v, want raw then canonical %q", got, canonical)
	}
	if got := repoRootQueryCandidates("/no/such/path-zzz"); len(got) != 1 || got[0] != "/no/such/path-zzz" {
		t.Fatalf("unresolvable path must yield exactly itself, got %v", got)
	}
}

// TestParsePipelineResultTokensReadsLedgerTotals pins the symmetric token
// source: the final stream-json event carries the authoritative request
// ledger, and both arms read it. A transcript without a final event reports
// found=false so the caller falls back to stream-json instead of guessing.
func TestParsePipelineResultTokensReadsLedgerTotals(t *testing.T) {
	result := schemas.PipelineResult{
		RunID:             "r",
		Status:            "completed",
		Tier:              schemas.TierLight,
		TotalTokensInput:  100,
		TotalTokensOutput: 40,
	}
	text, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	event, err := json.Marshal(map[string]string{"type": "final", "text": string(text)})
	if err != nil {
		t.Fatal(err)
	}
	out := []byte("{\"type\":\"run_start\"}\n" + string(event) + "\n")
	tokens, found := parsePipelineResultTokens(out)
	if !found || tokens != 140 {
		t.Fatalf("tokens = %d, found = %t; want 140, true", tokens, found)
	}
	if _, found := parsePipelineResultTokens([]byte("{\"type\":\"text\",\"delta\":\"hi\"}\n")); found {
		t.Fatal("a transcript with no final event must report found=false")
	}
}

// TestParsePipelineResultSpendReadsLedgerCost proves the per-row billed dollars
// come from the producer ledger, and that a partial-coverage total is marked as
// an estimate rather than presented as the complete attempt cost.
func TestParsePipelineResultSpendReadsLedgerCost(t *testing.T) {
	finalEvent := func(result schemas.PipelineResult) []byte {
		text, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		event, err := json.Marshal(map[string]string{"type": "final", "text": string(text)})
		if err != nil {
			t.Fatal(err)
		}
		return append(event, '\n')
	}
	record := schemas.PipelineUsageRecord{Sequence: 1, Stage: "code_writer", Iteration: 1}

	complete := finalEvent(schemas.PipelineResult{
		RunID: "r", Status: "completed", Tier: schemas.TierLight,
		TotalCostUSD: 0.0123, CostCoverage: schemas.CostCoverageComplete,
		UsageRecords: []schemas.PipelineUsageRecord{record},
	})
	usd, estimated, source := parsePipelineResultSpend(complete)
	if usd == nil || *usd != 0.0123 {
		t.Fatalf("usd = %v, want 0.0123", usd)
	}
	if estimated == nil || *estimated {
		t.Fatalf("estimated = %v, want false for complete coverage", estimated)
	}
	if source != "ledger" {
		t.Fatalf("source = %q, want ledger", source)
	}

	partial := finalEvent(schemas.PipelineResult{
		RunID: "r", Status: "completed", Tier: schemas.TierLight,
		TotalCostUSD: 0.004, CostCoverage: schemas.CostCoveragePartial,
		UsageRecords: []schemas.PipelineUsageRecord{record},
	})
	usd, estimated, _ = parsePipelineResultSpend(partial)
	if usd == nil || *usd != 0.004 {
		t.Fatalf("partial usd = %v, want 0.004", usd)
	}
	if estimated == nil || !*estimated {
		t.Fatalf("estimated = %v, want true for partial coverage", estimated)
	}

	// No final result and a final with no usage records both mean UNKNOWN,
	// never a fabricated zero.
	usd, estimated, source = parsePipelineResultSpend([]byte("{\"type\":\"run_start\"}\n"))
	if usd != nil || estimated != nil || source != "unavailable" {
		t.Fatalf("no final: usd=%v estimated=%v source=%q, want nil/nil/unavailable", usd, estimated, source)
	}
	empty := finalEvent(schemas.PipelineResult{
		RunID: "r", Status: "completed", Tier: schemas.TierLight,
	})
	usd, _, source = parsePipelineResultSpend(empty)
	if usd != nil || source != "unavailable" {
		t.Fatalf("empty ledger: usd=%v source=%q, want nil/unavailable", usd, source)
	}
}

// TestSpendSumKeepsUnknownApartFromZero proves a row without a producer cost is
// counted as unknown, and an estimated row taints the aggregate total.
func TestSpendSumKeepsUnknownApartFromZero(t *testing.T) {
	v := 0.0025
	est := true
	var s spendSum
	s.add(familyPairRow{})
	s.add(familyPairRow{BilledUSD: &v, BilledUSDEstimated: &est})
	got := s.String()
	for _, want := range []string{"$0.0025", "1 unknown", "estimated"} {
		if !strings.Contains(got, want) {
			t.Fatalf("spendSum %q missing %q", got, want)
		}
	}
}
