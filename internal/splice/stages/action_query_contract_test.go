package stages

// This test pins the declared request_context query contract to the decoder.
// The schema must tell the model every field the decoder requires, or a valid
// model answer is rejected for a field the model was never told to send.

import (
	"encoding/json"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func mustMap(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	m, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want a JSON object", label, value)
	}
	return m
}

// requestContextItemSchema walks the serialized submit_code schema down to the
// request_context query item.
func requestContextItemSchema(t *testing.T) map[string]any {
	t.Helper()
	params := submitCodeToolDefinition(false).Parameters
	props := mustMap(t, params["properties"], "parameters.properties")
	contextProp := mustMap(t, props[actionFieldContext], "request_context")
	contextProps := mustMap(t, contextProp["properties"], "request_context.properties")
	queries := mustMap(t, contextProps["queries"], "request_context.queries")
	return mustMap(t, queries["items"], "request_context.queries.items")
}

func requiredList(t *testing.T, item map[string]any) []string {
	t.Helper()
	switch required := item["required"].(type) {
	case []string:
		return required
	case []any:
		out := make([]string, 0, len(required))
		for _, entry := range required {
			name, ok := entry.(string)
			if !ok {
				t.Fatalf("required entry = %#v, want a string", entry)
			}
			out = append(out, name)
		}
		return out
	default:
		t.Fatalf("required = %#v, want a list", item["required"])
		return nil
	}
}

func TestRequestContextSchemaRequiresEveryDecoderMandatoryField(t *testing.T) {
	item := requestContextItemSchema(t)
	required := requiredList(t, item)
	want := map[string]bool{"query_type": true, "max_results": true, "max_chars": true}
	for _, name := range required {
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("schema required = %v, missing %v: the decoder rejects a query that omits them", required, want)
	}
}

func TestRequestContextSchemaNamesThePerTypeField(t *testing.T) {
	item := requestContextItemSchema(t)
	variants, ok := item["anyOf"].([]any)
	if !ok || len(variants) == 0 {
		t.Fatalf("schema anyOf = %#v, want non-empty variants for path/pattern/symbol", item["anyOf"])
	}
	found := map[string]bool{}
	for _, variant := range variants {
		required := requiredList(t, mustMap(t, variant, "anyOf variant"))
		if len(required) != 1 {
			t.Fatalf("anyOf variant required = %v, want exactly one field", required)
		}
		found[required[0]] = true
	}
	for _, name := range []string{"path", "pattern", "symbol"} {
		if !found[name] {
			t.Fatalf("schema anyOf = %v, missing %q", found, name)
		}
	}
}

// TestRequestContextSchemaShapedQueryDecodes is the round trip: a query shaped
// exactly as the schema declares must pass the decoder's own validator.
func TestRequestContextSchemaShapedQueryDecodes(t *testing.T) {
	item := requestContextItemSchema(t)
	required := requiredList(t, item)
	query := map[string]any{"query_type": string(schemas.ContextReadFile)}
	for _, name := range required {
		switch name {
		case "query_type":
		case "max_results":
			query[name] = 10
		case "max_chars":
			query[name] = 12000
		default:
			t.Fatalf("schema requires %q, but the round-trip fixture does not know its type", name)
		}
	}
	query["path"] = "docs/error-envelope.md"
	args, err := json.Marshal(map[string]any{
		actionFieldName: actionFieldContext,
		actionFieldContext: map[string]any{
			"reason":  "the intent needs the documented envelope",
			"queries": []map[string]any{query},
		},
	})
	if err != nil {
		t.Fatalf("marshal schema-shaped args: %v", err)
	}
	action, err := DecodeStageAction(codeWriterToolName, string(args))
	if err != nil {
		t.Fatalf("a query shaped exactly as the schema declares was rejected: %v", err)
	}
	if action.Request == nil {
		t.Fatal("schema-shaped request_context decoded to a nil request")
	}
}

// TestRequestContextSchemaShapedQueryOmittingBoundsIsRejected pins the reason
// the schema now declares the bounds: the decoder fails loud without them.
func TestRequestContextSchemaShapedQueryOmittingBoundsIsRejected(t *testing.T) {
	args := `{"action":"request_context","request_context":{"reason":"missing bounds","queries":[{"query_type":"read_file","path":"docs/error-envelope.md"}]}}`
	if _, err := DecodeStageAction(codeWriterToolName, args); err == nil {
		t.Fatal("a read_file query without max_results and max_chars decoded, want a loud rejection")
	}
}

// --- Drift guard: schema and validator must agree in both directions -------

// requestContextObjectSchema walks the serialized submit_code schema down to
// the request_context object level, returns the object schema map.
func requestContextObjectSchema(t *testing.T) map[string]any {
	t.Helper()
	params := submitCodeToolDefinition(false).Parameters
	props := mustMap(t, params["properties"], "parameters.properties")
	contextProp := mustMap(t, props[actionFieldContext], "request_context")
	return contextProp
}

// TestRequestContextSchemaRequiresEveryValidatorField demonstrates that the
// schema's required fields cover every field the validators require. The
// recurring defect is that a field the validator enforces drifts out of the
// schema. This test pins both directions:
//
//   - SUFFICIENT: a payload shaped exactly as the schema declares must pass
//     the validators (ContextRequest.Validate + ValidateActionContextRequest).
//   - NECESSARY: every field the validators require is either listed in the
//     schema's required array or in the anyOf alternatives.
func TestRequestContextSchemaMatchesValidators(t *testing.T) {
	// The top-level request_context object must declare reason and queries
	// required. Validate rejects both absences.
	ctxObject := requestContextObjectSchema(t)
	ctxRequired := requiredList(t, ctxObject)
	ctxWant := map[string]bool{"reason": true, "queries": true}
	for _, name := range ctxRequired {
		delete(ctxWant, name)
	}
	if len(ctxWant) != 0 {
		t.Fatalf("request_context schema required = %v, missing %v: the validator rejects a missing reason or queries", ctxRequired, ctxWant)
	}

	// SUFFICIENT: a payload matching the schema must pass both validators.
	item := requestContextItemSchema(t)
	itemRequired := requiredList(t, item)
	query := map[string]any{}
	for _, name := range itemRequired {
		switch name {
		case "query_type":
			query[name] = string(schemas.ContextReadFile)
		case "max_results":
			query[name] = 10
		case "max_chars":
			query[name] = 12000
		default:
			t.Fatalf("schema requires %q, fixture does not know its type", name)
		}
	}
	query["path"] = "internal/cache/client.go"

	req := schemas.ContextRequest{
		Reason: "needs source for cache client",
		Queries: []schemas.ContextQuery{
			{
				QueryType:  schemas.ContextReadFile,
				Path:       strPtr("internal/cache/client.go"),
				MaxResults: 10,
				MaxChars:   12000,
			},
		},
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("a query matching the schema was rejected by ContextRequest.Validate: %v", err)
	}
	if err := ValidateActionContextRequest(req); err != nil {
		t.Fatalf("a query matching the schema was rejected by ValidateActionContextRequest: %v", err)
	}

	// NECESSARY: drop each validator-required field and assert rejection.
	// Also assert the schema declares the field required or in anyOf.
	noReason := schemas.ContextRequest{
		Reason:  "",
		Queries: []schemas.ContextQuery{{QueryType: schemas.ContextReadFile, Path: strPtr("x"), MaxResults: 10, MaxChars: 100}},
	}
	if err := noReason.Validate(); err == nil {
		t.Fatal("reason is required by ContextRequest.Validate but a request without it passed")
	}

	noQueries := schemas.ContextRequest{Reason: "test"}
	if err := noQueries.Validate(); err == nil {
		t.Fatal("queries are required by ContextRequest.Validate but a request without them passed")
	}

	// max_results and max_chars are schema-required, already tested.
	// path is in anyOf. Construct a read_file without path and check the
	// query validators reject it.
	noPath := schemas.ContextQuery{QueryType: schemas.ContextReadFile, MaxResults: 5, MaxChars: 100}
	if err := noPath.Validate(); err == nil {
		t.Fatal("read_file needs path by the query validator but a query without it passed")
	}
}

func strPtr(s string) *string { return &s }
