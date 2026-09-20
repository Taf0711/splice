package stages

// D1 contract-repair tests: the tool schema, the stage prompt, and the
// action decoder must agree on the two declared actions, request_context
// and submit_changes.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/zeroruntime"
)

// TestSubmitAndContextToolsAreStructurallyExclusive pins the model-facing
// contract: two independently named tool schemas, one per action. The
// submission tool carries no request_context property and no action
// discriminator, so a model cannot attach a context payload to a
// submission through a field the schema declares. Each schema states the
// fields its validator requires.
func TestSubmitAndContextToolsAreStructurallyExclusive(t *testing.T) {
	submit := submitCodeToolDefinition()
	submitProps, ok := submit.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("submit schema has no properties object: %T", submit.Parameters["properties"])
	}
	if _, ok := submitProps["request_context"]; ok {
		t.Fatal("submission schema advertises request_context; the branches are no longer structurally exclusive")
	}
	if _, ok := submitProps[actionFieldName]; ok {
		t.Fatal("submission schema advertises an action discriminator; the tool name is the discriminator")
	}
	submitRequired, ok := submit.Parameters["required"].([]string)
	if !ok {
		t.Fatalf("submit schema required = %#v, want a list", submit.Parameters["required"])
	}
	for _, want := range []string{"files", "language", "intent", "confidence"} {
		found := false
		for _, r := range submitRequired {
			if r == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("submit schema required = %v, missing %q: the validator requires it and the model must see that", submitRequired, want)
		}
	}

	context := contextRequestToolDefinition()
	if context.Name != contextRequestToolName {
		t.Fatalf("context tool name = %q, want %q", context.Name, contextRequestToolName)
	}
	contextProps, ok := context.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("context schema has no properties object: %T", context.Parameters["properties"])
	}
	for _, forbidden := range []string{"files", "language", "intent", "confidence", "action"} {
		if _, ok := contextProps[forbidden]; ok {
			t.Fatalf("context schema advertises %q; the branches are no longer structurally exclusive", forbidden)
		}
	}
	contextRequired, ok := context.Parameters["required"].([]string)
	if !ok {
		t.Fatalf("context schema required = %#v, want a list", context.Parameters["required"])
	}
	for _, want := range []string{"reason", "queries"} {
		found := false
		for _, r := range contextRequired {
			if r == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("context schema required = %v, missing %q", contextRequired, want)
		}
	}

	// Some adapters re-marshal the opaque Parameters map, so both schemas
	// must survive a JSON round trip.
	for _, def := range []zeroruntime.ToolDefinition{submit, context} {
		if _, err := json.Marshal(def.Parameters); err != nil {
			t.Fatalf("schema %s is not serializable: %v", def.Name, err)
		}
	}
}

// TestCodeWriterPromptDeclaresTheActionContract pins the prompt side: the
// two named tools, one concrete example per tool, and the required-field
// statement. The prompt examples must match the emitted schemas exactly.
func TestCodeWriterPromptDeclaresTheActionContract(t *testing.T) {
	for _, want := range []string{
		"request_codebase_context",
		`"reason":"`,
		"max_results",
		"known_limitations",
		"files, language, intent, and confidence are required",
	} {
		if !strings.Contains(codeWriterSystemPrompt, want) {
			t.Fatalf("prompt is missing %q", want)
		}
	}
	// The retired single-tool discriminator must be gone: its examples
	// taught the model to mix the payloads, which was the recorded
	// failure.
	for _, gone := range []string{`"action":"request_context"`, `"action":"submit_changes"`} {
		if strings.Contains(codeWriterSystemPrompt, gone) {
			t.Fatalf("prompt still carries the retired discriminator example %q", gone)
		}
	}
}

// TestDeclaredActionRequestContextRoundTrip pins the advertised request
// shape decoding to a validated request.
func TestDeclaredActionRequestContextRoundTrip(t *testing.T) {
	args := dEnvelope(t, map[string]any{
		actionFieldName: actionFieldContext,
		actionFieldContext: map[string]any{
			"reason": "need the cache client",
			"queries": []map[string]any{
				{"query_type": "read_file", "path": "internal/cache/client.go", "max_results": 10, "max_chars": 12000},
			},
		},
	})
	action, err := DecodeStageAction("submit_code", args)
	if err != nil {
		t.Fatal(err)
	}
	if action.Request == nil || len(action.Request.Queries) != 1 {
		t.Fatalf("request not decoded: %+v", action)
	}
	if action.ProposalArgs != "" {
		t.Fatalf("a request action carried a proposal: %q", action.ProposalArgs)
	}
}

// TestDeclaredActionSubmitChangesFlatAndEnvelope pins both advertised
// submit shapes reaching the same stage parser, including the unwrap of
// the submit_changes envelope.
func TestDeclaredActionSubmitChangesFlatAndEnvelope(t *testing.T) {
	flat := dEnvelope(t, map[string]any{
		actionFieldName: actionFieldChanges,
		"files":         []map[string]any{{"path": "a.go", "change_type": "create", "content": "package a\n"}},
		"language":      "go",
		"intent":        "add",
		"confidence":    0.9,
	})
	action, err := DecodeStageAction("submit_code", flat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseCodeWriterArgs(action.ProposalArgs); err != nil {
		t.Fatalf("flat declared submit did not parse: %v", err)
	}

	envelope := dEnvelope(t, map[string]any{
		actionFieldName: actionFieldChanges,
		actionFieldChanges: map[string]any{
			"files":      []map[string]any{{"path": "a.go", "change_type": "create", "content": "package a\n"}},
			"language":   "go",
			"intent":     "add",
			"confidence": 0.9,
		},
	})
	action, err = DecodeStageAction("submit_code", envelope)
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Files json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal([]byte(action.ProposalArgs), &probe); err != nil || probe.Files == nil {
		t.Fatalf("submit envelope was not unwrapped: %q", action.ProposalArgs)
	}
	if _, err := parseCodeWriterArgs(action.ProposalArgs); err != nil {
		t.Fatalf("unwrapped submit did not parse: %v", err)
	}
}

// TestDeclaredActionRejectsBothNeitherAndUnknown pins the fail-loud rules.
func TestDeclaredActionRejectsBothNeitherAndUnknown(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
	}{
		{"context and files together", map[string]any{
			actionFieldName: actionFieldContext,
			actionFieldContext: map[string]any{
				"reason":  "r",
				"queries": []map[string]any{{"query_type": "read_file", "path": "a.go", "max_results": 1, "max_chars": 100}},
			},
			"files": []map[string]any{{"path": "a.go", "change_type": "create", "content": "x"}},
		}},
		{"context without payload", map[string]any{actionFieldName: actionFieldContext}},
		{"submit without payload", map[string]any{actionFieldName: actionFieldChanges}},
		{"submit with empty files", map[string]any{
			actionFieldName: actionFieldChanges,
			"files":         []map[string]any{},
			"language":      "go",
			"intent":        "i",
			"confidence":    0.9,
		}},
		{"unknown action", map[string]any{
			actionFieldName: "delete_everything",
			"files":         []map[string]any{{"path": "a.go", "change_type": "create", "content": "x"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeStageAction("submit_code", dEnvelope(t, tc.args)); err == nil {
				t.Fatalf("expected a loud error for %s", tc.name)
			}
		})
	}
}

// TestSubmitEnvelopeWithoutFilesFailsLoud pins the unwrap guard: an empty
// submit envelope must not decode as an empty proposal.
func TestSubmitEnvelopeWithoutFilesFailsLoud(t *testing.T) {
	args := dEnvelope(t, map[string]any{
		actionFieldName: actionFieldChanges,
		actionFieldChanges: map[string]any{
			"language":   "go",
			"intent":     "i",
			"confidence": 0.9,
		},
	})
	if _, err := DecodeStageAction("submit_code", args); err == nil {
		t.Fatal("a submit envelope without files must fail loudly")
	}
}

// TestLegacyFormsStillDecode pins backward compatibility for callers and
// models that predate the declared action field.
func TestLegacyFormsStillDecode(t *testing.T) {
	legacyEnvelope := dEnvelope(t, map[string]any{
		actionFieldContext: map[string]any{
			"reason":  "r",
			"queries": []map[string]any{{"query_type": "read_file", "path": "a.go", "max_results": 1, "max_chars": 100}},
		},
	})
	if action, err := DecodeStageAction("submit_code", legacyEnvelope); err != nil || action.Request == nil {
		t.Fatalf("legacy envelope must decode: %+v err=%v", action, err)
	}

	bare := dEnvelope(t, map[string]any{
		"files":      []map[string]any{{"path": "a.go", "change_type": "create", "content": "x"}},
		"language":   "go",
		"intent":     "i",
		"confidence": 0.9,
	})
	action, err := TryDecodeStageAction("submit_code", dStream(bare))
	if err != nil || action.Request != nil || action.ProposalArgs == "" {
		t.Fatalf("legacy flat payload must decode as submit: %+v err=%v", action, err)
	}
	if _, err := parseCodeWriterArgs(action.ProposalArgs); err != nil {
		t.Fatalf("legacy flat payload did not parse: %v", err)
	}
}
