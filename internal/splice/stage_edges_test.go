package splice

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestScopedStageInputs(t *testing.T) {
	summaries := map[string]string{"writer": "wrote files", "tester": "ran tests"}
	changed := map[string][]string{"writer": {"a.go"}, "tester": {"b_test.go"}}
	outputs := map[string]schemas.HarnessStageOutput{
		"writer": {Summary: "wrote files", Data: map[string]interface{}{"k": "v"}},
	}
	cases := []struct {
		name          string
		stage         schemas.ExecutionStage
		wantSummaries map[string]string
		wantChanged   map[string][]string
	}{
		{
			name:          "summary carries the dependency summary and changed files",
			stage:         schemas.ExecutionStage{Name: "x", DependsOn: []string{"writer"}, EdgePayloads: map[string]schemas.EdgePayload{"writer": schemas.EdgePayloadSummary}},
			wantSummaries: map[string]string{"writer": "wrote files"},
			wantChanged:   map[string][]string{"writer": {"a.go"}},
		},
		{
			name:          "none carries nothing",
			stage:         schemas.ExecutionStage{Name: "x", DependsOn: []string{"writer"}, EdgePayloads: map[string]schemas.EdgePayload{"writer": schemas.EdgePayloadNone}},
			wantSummaries: map[string]string{},
			wantChanged:   map[string][]string{},
		},
		{
			name:          "output adds bounded data JSON",
			stage:         schemas.ExecutionStage{Name: "x", DependsOn: []string{"writer"}, EdgePayloads: map[string]schemas.EdgePayload{"writer": schemas.EdgePayloadOutput}},
			wantSummaries: map[string]string{"writer": "wrote files\n[data] {\"k\":\"v\"}"},
			wantChanged:   map[string][]string{"writer": {"a.go"}},
		},
		{
			name:          "missing payload entry defaults to summary",
			stage:         schemas.ExecutionStage{Name: "x", DependsOn: []string{"writer"}},
			wantSummaries: map[string]string{"writer": "wrote files"},
			wantChanged:   map[string][]string{"writer": {"a.go"}},
		},
		{
			name:          "only direct dependencies cross",
			stage:         schemas.ExecutionStage{Name: "x", DependsOn: []string{"tester"}, EdgePayloads: map[string]schemas.EdgePayload{"tester": schemas.EdgePayloadSummary}},
			wantSummaries: map[string]string{"tester": "ran tests"},
			wantChanged:   map[string][]string{"tester": {"b_test.go"}},
		},
		{
			name:          "a node with no dependencies sees nothing",
			stage:         schemas.ExecutionStage{Name: "x"},
			wantSummaries: map[string]string{},
			wantChanged:   map[string][]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotSummaries, gotChanged := scopedStageInputs(tc.stage, summaries, changed, outputs)
			if !reflect.DeepEqual(gotSummaries, tc.wantSummaries) {
				t.Fatalf("scoped summaries = %v, want %v", gotSummaries, tc.wantSummaries)
			}
			if !reflect.DeepEqual(gotChanged, tc.wantChanged) {
				t.Fatalf("scoped changed files = %v, want %v", gotChanged, tc.wantChanged)
			}
		})
	}
}

// TestScopedStageInputsDoesNotMutateSources pins that scoping copies the
// cumulative maps instead of aliasing them.
func TestScopedStageInputsDoesNotMutateSources(t *testing.T) {
	summaries := map[string]string{"writer": "wrote files"}
	changed := map[string][]string{"writer": {"a.go"}}
	stage := schemas.ExecutionStage{Name: "x", DependsOn: []string{"writer"}}
	gotSummaries, gotChanged := scopedStageInputs(stage, summaries, changed, nil)
	gotSummaries["writer"] = "tampered"
	gotChanged["writer"][0] = "tampered.go"
	if summaries["writer"] != "wrote files" {
		t.Fatalf("source summary mutated: %q", summaries["writer"])
	}
	if changed["writer"][0] != "a.go" {
		t.Fatalf("source changed files mutated: %q", changed["writer"][0])
	}
}

// TestScopedStageInputsOutputDataCap bounds the data JSON an output edge can
// inject into a downstream summary.
func TestScopedStageInputsOutputDataCap(t *testing.T) {
	blob := strings.Repeat("x", edgeOutputDataCapChars+500)
	outputs := map[string]schemas.HarnessStageOutput{
		"writer": {Data: map[string]interface{}{"blob": blob}},
	}
	stage := schemas.ExecutionStage{
		Name:         "x",
		DependsOn:    []string{"writer"},
		EdgePayloads: map[string]schemas.EdgePayload{"writer": schemas.EdgePayloadOutput},
	}
	got, _ := scopedStageInputs(stage, map[string]string{"writer": "s"}, nil, outputs)
	const marker = "\n[data] "
	index := strings.Index(got["writer"], marker)
	if index < 0 {
		t.Fatalf("output edge payload %q does not contain the data marker", got["writer"])
	}
	dataPart := got["writer"][index+len(marker):]
	if count := utf8.RuneCountInString(dataPart); count > edgeOutputDataCapChars {
		t.Fatalf("data part has %d characters, want at most %d", count, edgeOutputDataCapChars)
	}
}

// TestCompileTopologyCarriesNonDefaultPayloads pins that the compiler records
// only non-default edge payloads, so an all-summary topology serializes
// without the field.
func TestCompileTopologyCarriesNonDefaultPayloads(t *testing.T) {
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "payloads",
		Nodes: []schemas.PipelineNode{
			{Name: "write", Type: "code_writer"},
			{Name: "lint", Type: "static_analyzer"},
			{Name: "verify", Type: "test_runner"},
		},
		Edges: []schemas.PipelineEdge{
			{From: "write", To: "lint", Payload: schemas.EdgePayloadNone},
			{From: "lint", To: "verify", Payload: schemas.EdgePayloadOutput},
		},
	}
	compiled, err := CompileTopology(topology, schemas.TierLight)
	if err != nil {
		t.Fatalf("CompileTopology = %v", err)
	}
	byName := make(map[string]schemas.ExecutionStage, len(compiled.Stages))
	for _, stage := range compiled.Stages {
		byName[stage.Name] = stage
	}
	if got := byName["lint"].EdgePayloads["write"]; got != schemas.EdgePayloadNone {
		t.Fatalf("lint -> write payload = %q, want %q", got, schemas.EdgePayloadNone)
	}
	if got := byName["verify"].EdgePayloads["lint"]; got != schemas.EdgePayloadOutput {
		t.Fatalf("verify -> lint payload = %q, want %q", got, schemas.EdgePayloadOutput)
	}
	if byName["write"].EdgePayloads != nil {
		t.Fatalf("root stage edge payloads = %v, want nil", byName["write"].EdgePayloads)
	}
}

// TestDefaultTopologyKeepsSummaryPayloadsEmpty pins that the embedded default
// stays all-summary, so its compiled form carries no edge payload overrides
// and today's serialized plan is unchanged.
func TestDefaultTopologyKeepsSummaryPayloadsEmpty(t *testing.T) {
	for _, tier := range allTiers {
		compiled, err := CompileTopology(defaultTopology(), tier)
		if err != nil {
			t.Fatalf("CompileTopology(default, %s) = %v", tier, err)
		}
		for _, stage := range compiled.Stages {
			if stage.EdgePayloads != nil {
				t.Fatalf("tier %s stage %s carries edge payload overrides %v, want none", tier, stage.Name, stage.EdgePayloads)
			}
		}
	}
}

// TestDefaultTopologyKeepsBuiltinReaderCoupling pins the observable contract
// of the default graph: test_generator reads PriorSummaries["code_writer"] by
// name, so the edge that delivers it must exist at every tier where
// test_generator is active.
func TestDefaultTopologyKeepsBuiltinReaderCoupling(t *testing.T) {
	for _, tier := range allTiers {
		compiled, err := CompileTopology(defaultTopology(), tier)
		if err != nil {
			t.Fatalf("CompileTopology(default, %s) = %v", tier, err)
		}
		var testGenerator *schemas.ExecutionStage
		for i := range compiled.Stages {
			if compiled.Stages[i].Name == "test_generator" {
				testGenerator = &compiled.Stages[i]
			}
		}
		if testGenerator == nil {
			continue
		}
		found := false
		for _, dependency := range testGenerator.DependsOn {
			if dependency == "code_writer" {
				found = true
			}
		}
		if !found {
			t.Fatalf("tier %s: test_generator dependencies = %v, want code_writer", tier, testGenerator.DependsOn)
		}
		summaries := map[string]string{"code_writer": "wrote files"}
		scoped, _ := scopedStageInputs(*testGenerator, summaries, nil, nil)
		if scoped["code_writer"] != "wrote files" {
			t.Fatalf("tier %s: test_generator scoped summaries = %v, want code_writer", tier, scoped)
		}
	}
}

// TestStageChangedFilesPrefersAdditiveField pins the additive field wins over
// the legacy Data keys, and that the legacy keys still work without it.
func TestStageChangedFilesPrefersAdditiveField(t *testing.T) {
	additive := schemas.HarnessStageOutput{
		ChangedFiles: []string{"new.go"},
		Data: map[string]interface{}{
			"code_writer_output": schemas.CodeWriterOutput{Files: []schemas.FileChange{{Path: "legacy.go"}}},
		},
	}
	if got := stageChangedFiles(additive); !reflect.DeepEqual(got, []string{"new.go"}) {
		t.Fatalf("additive changed files = %v, want [new.go]", got)
	}
	legacy := schemas.HarnessStageOutput{
		Data: map[string]interface{}{
			"code_writer_output": schemas.CodeWriterOutput{Files: []schemas.FileChange{{Path: "legacy.go"}}},
		},
	}
	if got := stageChangedFiles(legacy); !reflect.DeepEqual(got, []string{"legacy.go"}) {
		t.Fatalf("legacy changed files = %v, want [legacy.go]", got)
	}
	if got := stageChangedFiles(schemas.HarnessStageOutput{Summary: "no files"}); len(got) != 0 {
		t.Fatalf("empty changed files = %v, want none", got)
	}
}
