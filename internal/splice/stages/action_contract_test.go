package stages

// D1 contract-repair tests: the tool schema, the stage prompt, and the
// action decoder must agree on the two declared actions, request_context
// and submit_changes.

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSubmitCodeSchemaDeclaresBothActions pins the model-facing contract:
// the serialized tool schema names the discriminator and both actions.
func TestSubmitCodeSchemaDeclaresBothActions(t *testing.T) {
	def := submitCodeToolDefinition(false)
	props, ok := def.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties object: %T", def.Parameters["properties"])
	}
	actionProp, ok := props[actionFieldName].(map[string]any)
	if !ok {
		t.Fatalf("schema does not declare the %q discriminator", actionFieldName)
	}
	enum, ok := actionProp["enum"].([]string)
	if !ok || len(enum) != 2 {
		t.Fatalf("action enum = %#v, want two values", actionProp["enum"])
	}
	if enum[0] != actionFieldContext || enum[1] != actionFieldChanges {
		t.Fatalf("action enum = %v, want [%s %s]", enum, actionFieldContext, actionFieldChanges)
	}
	if _, ok := props[actionFieldContext].(map[string]any); !ok {
		t.Fatalf("schema does not declare the %q payload", actionFieldContext)
	}
	required, ok := def.Parameters["required"].([]string)
	if !ok || len(required) != 1 || required[0] != actionFieldName {
		t.Fatalf("required = %#v, want only %s", def.Parameters["required"], actionFieldName)
	}
	// Some adapters re-marshal the opaque Parameters map, so the schema
	// must survive a JSON round trip.
	if _, err := json.Marshal(def.Parameters); err != nil {
		t.Fatalf("schema is not serializable: %v", err)
	}
}

// TestCodeWriterPromptDeclaresTheActionContract pins the prompt side: a
// concrete example of each action reaches the model.
func TestCodeWriterPromptDeclaresTheActionContract(t *testing.T) {
	for _, want := range []string{
		`"action":"request_context"`,
		`"action":"submit_changes"`,
		"max_results",
		"known_limitations",
	} {
		if !strings.Contains(codeWriterSystemPrompt, want) {
			t.Fatalf("prompt is missing %q", want)
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
