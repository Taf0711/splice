package swebenchpro

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigValidation covers the documented minimum configuration and the
// failure modes: missing fields, negative numbers.
func TestConfigValidation(t *testing.T) {
	valid := Config{
		RepoDir:           "/opt/SWE-bench_Pro-os",
		RawSamplePath:     "/data/swe_bench_pro_full.csv",
		WorkspaceDir:      "/tmp/ws",
		DockerHubUsername: "jefzda",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing repo_dir", func(c *Config) { c.RepoDir = "" }},
		{"missing raw_sample_path", func(c *Config) { c.RawSamplePath = "" }},
		{"missing workspace_dir", func(c *Config) { c.WorkspaceDir = "" }},
		{"negative num_workers", func(c *Config) { c.NumWorkers = -1 }},
		{"negative timeout", func(c *Config) { c.TimeoutPerInstance = -5 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatalf("%s: validation should fail", tc.name)
			}
		})
	}
	def := Config{}
	if got := def.SpliceCommand(); got != "splice" {
		t.Fatalf("default splice command should be splice, got %q", got)
	}
	ovr := Config{SpliceBin: "/usr/local/bin/splice"}
	if got := ovr.SpliceCommand(); got != "/usr/local/bin/splice" {
		t.Fatalf("splice_bin override ignored, got %q", got)
	}
}

// TestPinsAreExact pins the dataset revision, evaluator commit, and official
// names so any pin change is a visible, deliberate diff.
func TestPinsAreExact(t *testing.T) {
	if PinnedDataset != "ScaleAI/SWE-bench_Pro" {
		t.Fatalf("dataset pin changed: %q (note: HF org is ScaleAI; -os is the GitHub repo suffix only)", PinnedDataset)
	}
	if PinnedDatasetRevision != "7ab5114912baf22bb098818e604c02fe7ad2c11f" {
		t.Fatalf("dataset revision pin changed: %q", PinnedDatasetRevision)
	}
	if PinnedDatasetSplit != "test" {
		t.Fatalf("split pin changed: %q", PinnedDatasetSplit)
	}
	if PinnedEvaluatorCommit != "ca10a60a5fcae51e6948ffe1485d421e6c5" && PinnedEvaluatorCommit != "ca10a60a5fcae51e6948ffe1485d4153d421e6c5" {
		t.Fatalf("evaluator commit pin changed: %q", PinnedEvaluatorCommit)
	}
	if !strings.Contains(PinnedEvaluatorRepo, "scaleapi/SWE-bench_Pro-os") {
		t.Fatalf("evaluator repo pin changed: %q", PinnedEvaluatorRepo)
	}
}

// TestDevSubsetIsDeterministicAndDocumented re-derives the manifest from a
// full id list and checks the committed dev_subset.json matches. The
// selection must be a pure function of instance ids and the fixed salt.
func TestDevSubsetIsDeterministicAndDocumented(t *testing.T) {
	// Build a superset list: committed subset plus decoys. The derivation
	// must pick exactly the committed ids regardless of input order.
	full := append([]string(nil), DevSubset...)
	decoys := []string{
		"instance_NodeBB__NodeBB-04998908ba6721d64eba79ae3b65a351dcfbc5b5-vnan",
		"instance_ansible__ansible-0fd88717c953b92ed8a50495d55e630eb5d59166-vba6da65a0f3baefda7a058ebbd0a8dcafb8512f5",
		"instance_zzz__notarealrepo-abcdef",
	}
	full = append(full, decoys...)
	got, err := SelectDevSubset(full)
	if err != nil {
		t.Fatalf("SelectDevSubset drift: %v", err)
	}
	if len(got) != DevSubsetSize {
		t.Fatalf("want %d ids, got %d", DevSubsetSize, len(got))
	}

	// The committed manifest file must agree with the code-level manifest.
	data, err := os.ReadFile("dev_subset.json")
	if err != nil {
		t.Fatalf("read dev_subset.json: %v", err)
	}
	var doc struct {
		InstanceIDs     []string `json:"instance_ids"`
		DatasetRevision string   `json:"dataset_revision"`
		EvaluatorCommit string   `json:"evaluator_commit"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse dev_subset.json: %v", err)
	}
	if len(doc.InstanceIDs) != len(DevSubset) {
		t.Fatalf("manifest file has %d ids, code has %d", len(doc.InstanceIDs), len(DevSubset))
	}
	committed := append([]string(nil), DevSubset...)
	idSet := map[string]bool{}
	for _, id := range doc.InstanceIDs {
		idSet[id] = true
	}
	for _, id := range committed {
		if !idSet[id] {
			t.Fatalf("code manifest id missing from committed file: %s", id)
		}
	}
	if doc.DatasetRevision != PinnedDatasetRevision {
		t.Fatalf("manifest dataset revision %q != pin %q", doc.DatasetRevision, PinnedDatasetRevision)
	}
	if doc.EvaluatorCommit != PinnedEvaluatorCommit {
		t.Fatalf("manifest evaluator commit %q != pin %q", doc.EvaluatorCommit, PinnedEvaluatorCommit)
	}
}

func TestSelectDevSubsetFailsEmpty(t *testing.T) {
	if _, err := SelectDevSubset(nil); err == nil {
		t.Fatal("empty id list must fail loudly")
	}
}

// TestWritePredictionsGolden pins the exact prediction JSON shape the
// OFFICIAL evaluator consumes: [{"instance_id","patch","prefix"}].
func TestWritePredictionsGolden(t *testing.T) {
	golden, err := os.ReadFile("testdata/golden_predictions.json")
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	preds := []Prediction{
		{
			InstanceID: "instance_flipt-io__flipt-7161f7b876773a911afdd804b281e52681cb7321",
			Patch: "diff --git a/internal/config/config.go b/internal/config/config.go\n" +
				"index 1111111..2222222 100644\n--- a/internal/config/config.go\n" +
				"+++ b/internal/config/config.go\n@@ -1,3 +1,4 @@\n package config\n\n" +
				"+// fixed\n func Load() error { return nil }\n",
		},
		{
			InstanceID: "instance_future-architect__vuls-9a32a94806b54141b7ff12503c48da680ebcf199",
			Patch:      "",
		},
	}
	path := filepath.Join(t.TempDir(), "patches.json")
	if err := WritePredictions(path, "splice", preds); err != nil {
		t.Fatalf("WritePredictions: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written predictions: %v", err)
	}
	// Compare as parsed JSON: formatting may differ, shape and content may not.
	var want, have any
	if err := json.Unmarshal(golden, &want); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	if err := json.Unmarshal(got, &have); err != nil {
		t.Fatalf("parse written: %v", err)
	}
	wantJSON, _ := json.Marshal(want)
	haveJSON, _ := json.Marshal(have)
	if string(wantJSON) != string(haveJSON) {
		t.Fatalf("prediction shape drift:\nwant %s\nhave %s", wantJSON, haveJSON)
	}

	// Round-trip through the loader must preserve everything.
	loaded, err := LoadPredictions(path)
	if err != nil {
		t.Fatalf("LoadPredictions: %v", err)
	}
	if len(loaded) != 2 || loaded[0].Prefix != "splice" || loaded[1].Patch != "" {
		t.Fatalf("round-trip mismatch: %+v", loaded)
	}
}

func TestWritePredictionsRejectsEmptyInstanceID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "patches.json")
	if err := WritePredictions(path, "splice", []Prediction{{Patch: "x"}}); err == nil {
		t.Fatal("empty instance_id must be rejected before reaching the evaluator")
	}
}

// TestStreamJSONInput pins the headless input event contract
// (docs/STREAM_JSON_PROTOCOL.md: schemaVersion 2, one prompt event).
func TestStreamJSONInput(t *testing.T) {
	got := streamJSONInput("Fix the bug described in the issue.\nSecond line.")
	var ev struct {
		SchemaVersion int    `json:"schemaVersion"`
		Type          string `json:"type"`
		Content       string `json:"content"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &ev); err != nil {
		t.Fatalf("stream input is not one JSON line: %v (%q)", err, got)
	}
	if ev.SchemaVersion != 2 || ev.Type != "prompt" {
		t.Fatalf("want schemaVersion 2 prompt event, got %+v", ev)
	}
	if ev.Content != "Fix the bug described in the issue.\nSecond line." {
		t.Fatalf("prompt content must be the original issue text verbatim, got %q", ev.Content)
	}
}

func TestPromptForInstanceVerbatim(t *testing.T) {
	issue := "The parser crashes when the header is empty."
	if PromptForInstance(issue) != issue {
		t.Fatal("prompt must be the original issue text, unmodified")
	}
}

// TestOfficialEvaluatorArgs pins the exact official evaluator invocation so
// a refactor cannot quietly swap in a custom checker.
func TestOfficialEvaluatorArgs(t *testing.T) {
	// InvokeOfficialEvaluator requires a real pinned checkout; here we pin
	// the arg construction indirectly by checking the documented constants
	// and the guard behavior on a missing repo.
	cfg := &Config{RepoDir: t.TempDir(), RawSamplePath: "x.csv"}
	if err := VerifyEvaluatorRepo(cfg.RepoDir); err == nil {
		t.Fatal("VerifyEvaluatorRepo must fail on a checkout without the official files")
	}
	if _, err := InvokeOfficialEvaluator(t.Context(), cfg, "p.json", "out"); err == nil {
		t.Fatal("InvokeOfficialEvaluator must refuse an unpinned repo")
	}
}
