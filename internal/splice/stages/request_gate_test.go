package stages

// B4 regression tests: the shared pure request builder and the final
// request gate. Byte counts are exact; token fields are labeled estimates;
// overflow names the bound without trimming required content; the builder
// is the SAME function production uses (no second approximate builder).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/zeroruntime"
)

func b4Tool() zeroruntime.ToolDefinition {
	return zeroruntime.ToolDefinition{
		Name:        "submit_code",
		Description: "Submit code changes.",
		Parameters:  map[string]any{"type": "object"},
	}
}

// TestBuildFinalRequestMatchesCallToolUse pins the single-builder rule:
// the request callToolUse sends is byte-identical to BuildFinalRequest's
// output for the same inputs. Any drift between the builder and the
// provider path means the gate measured a different request than the
// provider received.
func TestBuildFinalRequestMatchesCallToolUse(t *testing.T) {
	provider := &requestCapturingProvider{events: toolCallEvent("submit_code", `{"confidence":0.9,"intent":"i","language":"go","files":[]}`)}
	const system, user = "system prompt", "user payload with source"
	_, err := callToolUse(t.Context(), provider, "model-x", "medium", system, user, nil, b4Tool(), 777, nil, "cache-key", true)
	if err != nil {
		t.Fatal(err)
	}
	built, _ := BuildFinalRequest("code_writer", "model-x", "medium", system, user, nil, b4Tool(), 777, "cache-key", true, 1)
	if len(provider.request.Messages) != len(built.Messages) {
		t.Fatalf("message count drifted: %d vs %d", len(provider.request.Messages), len(built.Messages))
	}
	for i := range built.Messages {
		if provider.request.Messages[i].Content != built.Messages[i].Content || provider.request.Messages[i].Role != built.Messages[i].Role {
			t.Fatalf("message %d drifted between builder and production path", i)
		}
	}
	if provider.request.ToolChoice != built.ToolChoice || provider.request.MaxOutputTokens != built.MaxOutputTokens {
		t.Fatalf("request controls drifted: %+v vs %+v", provider.request, *built)
	}
}

// TestFinalRequestBreakdownByteCountsAreExact pins the exactness contract:
// byte counts equal len() of the components; the token field carries the
// estimate label, never a claim of provider-exact tokens.
func TestFinalRequestBreakdownByteCountsAreExact(t *testing.T) {
	const system, user = "abc", "hello world"
	schema, _ := json.Marshal(b4Tool())
	built, breakdown := BuildFinalRequest("stage", "m", "", system, user, nil, b4Tool(), 0, "", false, 2)
	if breakdown.SystemBytes != len(system) || breakdown.UserBytes != len(user) {
		t.Fatalf("component bytes drifted: %+v", breakdown)
	}
	if breakdown.SchemaBytes != len(schema) {
		t.Fatalf("schema bytes = %d, want %d", breakdown.SchemaBytes, len(schema))
	}
	if breakdown.TotalBytes != len(system)+len(user)+len(schema) {
		t.Fatal("total bytes must be the sum of exact components")
	}
	if breakdown.EstimatedInputTokens != (breakdown.TotalBytes+3)/4 {
		t.Fatalf("estimate = %d, want bytes/4 of %d", breakdown.EstimatedInputTokens, breakdown.TotalBytes)
	}
	if breakdown.EstimateLabel != EstimateLabelEstimate || !strings.Contains(EstimateLabelEstimate, "estimate") {
		t.Fatal("token estimate must carry the estimate label")
	}
	if breakdown.Attempt != 2 {
		t.Fatalf("attempt attribution = %d, want 2", breakdown.Attempt)
	}
	_ = built
}

// TestGateFinalRequestOverflowNamesBound pins the overflow contract: the
// gate reports the bound, the measured size, and actionable guidance; it
// never trims content itself and never reports overflow when unbounded.
func TestGateFinalRequestOverflowNamesBound(t *testing.T) {
	built, breakdown := BuildFinalRequest("s", "m", "", strings.Repeat("s", 400), strings.Repeat("u", 400), nil, b4Tool(), 0, "", false, 1)
	// Unbounded: no overflow.
	if gate := GateFinalRequest(breakdown, 0); gate.Overflow != nil {
		t.Fatalf("unbounded gate reported overflow: %v", gate.Overflow)
	}
	// Bounded below the estimate: overflow names bound and measurement.
	gate := GateFinalRequest(breakdown, 100)
	if gate.Overflow == nil {
		t.Fatal("overflow must fire under a 100-token bound")
	}
	if gate.Overflow.BoundBytes != 400 {
		t.Fatalf("bound bytes = %d, want 400", gate.Overflow.BoundBytes)
	}
	if gate.Overflow.MeasuredBytes != breakdown.TotalBytes {
		t.Fatal("measured bytes must equal the exact total")
	}
	if !strings.Contains(gate.Overflow.Message, "targeted ranges") || !strings.Contains(gate.Overflow.Message, "explicit incomplete outcome") {
		t.Fatalf("overflow guidance missing: %s", gate.Overflow.Message)
	}
	// The request itself is untouched by the gate: no silent trimming.
	if len(built.Messages[1].Content) != 400 {
		t.Fatal("the gate must never trim the request content")
	}
}

// TestFinalRequestGateFitsAfterFulfillment pins the ordering property the
// handoff requires: the gate runs on the request that includes fulfilled
// source (the user payload), not the pre-fulfillment harness input. A
// large fulfilled view pushes the measured bytes up accordingly.
func TestFinalRequestGateFitsAfterFulfillment(t *testing.T) {
	smallSource := strings.Repeat("a", 200)
	largeSource := strings.Repeat("a", 40000)
	small, sb := BuildFinalRequest("s", "m", "", "sys", smallSource, nil, b4Tool(), 0, "", false, 1)
	large, lb := BuildFinalRequest("s", "m", "", "sys", largeSource, nil, b4Tool(), 0, "", false, 1)
	if sb.UserBytes != 200 || lb.UserBytes != 40000 {
		t.Fatal("user bytes must reflect the FULFILLED source size")
	}
	if GateFinalRequest(sb, 200).Overflow != nil {
		t.Fatal("small request must fit a 200-token bound")
	}
	if GateFinalRequest(lb, 200).Overflow == nil {
		t.Fatal("oversized fulfilled source must trip the gate")
	}
	_ = small
	_ = large
}
