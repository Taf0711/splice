package cli

// ADDENDUM 5 regressions:
//   - frozen Task A reuse rematerializes the exact recorded commit/tree from
//     the pristine fixture plus the supplied A tree (no producer re-run);
//   - a result with tokens but zero priced cost is estimated, never a
//     confirmed $0 (the manual-arm attribution defect).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFrozenSnapshotReuseReproducesRecordedIdentity(t *testing.T) {
	fixture := t.TempDir()
	writeFile(t, fixture, "main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, fixture, "session.go", "package main\n\nvar ErrNotFound = 1\n")

	// The frozen A tree: fixture plus the producer's edits.
	reuse := t.TempDir()
	writeFile(t, reuse, "main.go", "package main\n\nfunc main() {}\n\nfunc mapStoreError() int { return 404 }\n")
	writeFile(t, reuse, "session.go", "package main\n\nvar ErrNotFound = 1\nvar ErrConflict = 2\n")

	rebuild := func() (commit, tree string) {
		snap := t.TempDir()
		if err := copyFixtureTree(fixture, snap); err != nil {
			t.Fatalf("copy fixture: %v", err)
		}
		if err := overlayTree(reuse, snap); err != nil {
			t.Fatalf("overlay reuse tree: %v", err)
		}
		if _, err := gitCommitAll(snap); err != nil {
			t.Fatalf("commit rebuilt tree: %v", err)
		}
		return gitHeadCommit(snap), gitTreeHash(snap)
	}
	commit1, tree1 := rebuild()
	commit2, tree2 := rebuild()
	if commit1 == "" || tree1 == "" {
		t.Fatal("rebuilt identity is empty")
	}
	if commit1 != commit2 || tree1 != tree2 {
		t.Fatalf("reuse rebuild is not deterministic: %s/%s vs %s/%s", commit1, tree1, commit2, tree2)
	}
	// The rebuilt tree must differ from the pristine fixture base: A's edits
	// are real, so a no-op reuse cannot masquerade as a match.
	base := t.TempDir()
	if err := copyFixtureTree(fixture, base); err != nil {
		t.Fatal(err)
	}
	if gitTreeHash(base) == tree1 {
		t.Fatal("rebuilt tree equals the pristine fixture; the A edits were not applied")
	}

	// The bundle names that identity and its capture digest; a reuse record
	// pins the match.
	outDir := t.TempDir()
	bundle := snapshotBundle{ProducerRunID: "run-reuse", Commit: commit1, Tree: tree1, CaptureOrigin: "natural", CaptureDigest: bundleDigest(nil)}
	if err := writeReuseRecord(outDir, "fam-05-handler-error-mapping", "snap-1", "bundle.json", bundle, commit1, tree1, []string{"main.go", "session.go"}); err != nil {
		t.Fatalf("write reuse record: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "snapshots", "snap-1", "reuse-record.json"))
	if err != nil {
		t.Fatalf("read reuse record: %v", err)
	}
	var rec reuseRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("decode reuse record: %v", err)
	}
	if !rec.Reused || rec.ProducerRerun || rec.Commit != commit1 || rec.Tree != tree1 || rec.CaptureDigest != bundle.CaptureDigest {
		t.Fatalf("reuse record = %+v, want the reused identity and no producer rerun", rec)
	}
}

func TestParsePipelineResultSpendEstimatesZeroPricedTokens(t *testing.T) {
	zero := 0.0
	result := schemas.PipelineResult{
		Status: "completed", TotalCostUSD: 0,
		TotalTokensInput: 16419, TotalTokensOutput: 1657,
		CostCoverage: schemas.CostCoverageComplete, PricedRequestCount: 3,
		UsageRecords: []schemas.PipelineUsageRecord{
			{Stage: "code_writer", InputTokens: 5473, OutputTokens: 552, CostUSD: &zero, CostStatus: schemas.CostStatusPriced},
			{Stage: "code_writer", InputTokens: 5473, OutputTokens: 552, CostUSD: &zero, CostStatus: schemas.CostStatusPriced},
			{Stage: "code_writer", InputTokens: 5473, OutputTokens: 553, CostUSD: &zero, CostStatus: schemas.CostStatusPriced},
		},
	}
	text, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(map[string]string{"type": "final", "text": string(text)})
	if err != nil {
		t.Fatal(err)
	}
	usd, estimated, source := parsePipelineResultSpend([]byte(string(line) + "\n"))
	if usd == nil {
		t.Fatal("zero-priced token spend returned nil; want a token-based estimate")
	}
	if *usd <= 0 {
		t.Fatalf("estimated cost = %v, want > 0 (never a confirmed $0)", *usd)
	}
	if estimated == nil || !*estimated || source != "estimated" {
		t.Fatalf("source=%q estimated=%v, want an estimated token-based cost", source, estimated)
	}

	// A genuinely priced non-zero ledger total is untouched.
	one := 1.0
	priced := schemas.PipelineResult{
		Status: "completed", TotalCostUSD: 0.5, TotalTokensInput: 100, TotalTokensOutput: 10,
		CostCoverage: schemas.CostCoverageComplete, PricedRequestCount: 1,
		UsageRecords: []schemas.PipelineUsageRecord{{Stage: "code_writer", InputTokens: 100, OutputTokens: 10, CostUSD: &one, CostStatus: schemas.CostStatusPriced}},
	}
	text2, _ := json.Marshal(priced)
	line2, _ := json.Marshal(map[string]string{"type": "final", "text": string(text2)})
	usd2, est2, src2 := parsePipelineResultSpend([]byte(string(line2) + "\n"))
	if usd2 == nil || *usd2 != 0.5 || src2 != "ledger" || (est2 != nil && *est2) {
		t.Fatalf("priced ledger total changed: usd=%v estimated=%v source=%q", usd2, est2, src2)
	}
	if !strings.Contains(src2, "ledger") {
		t.Fatalf("source = %q, want ledger", src2)
	}
}
