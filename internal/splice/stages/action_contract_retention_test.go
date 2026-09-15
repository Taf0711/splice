package stages

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The retention read attempt failed on the action contract, not on memory:
// six of eight tool calls mixed request_context into submit_changes and
// were rejected by the decoder as mutually exclusive, each rejection
// billed as a format retry. These fixtures preserve the original payloads
// reassembled from the committed raw stream, and this test pins the
// decoder behavior the repair must change.

type actionContractFixture struct {
	Source      string `json:"source"`
	Description string `json:"description"`
	Calls       []struct {
		Index             int    `json:"index"`
		ToolCallID        string `json:"tool_call_id"`
		Tool              string `json:"tool"`
		ArgumentsJSON     string `json:"arguments_json"`
		DeclaredAction    string `json:"declared_action"`
		HasFiles          bool   `json:"has_files"`
		HasRequestContext bool   `json:"has_request_context"`
	} `json:"calls"`
}

func loadActionContractFixture(t *testing.T) actionContractFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/action_contract/retention-read-payloads.json")
	if err != nil {
		t.Fatalf("read action contract fixture: %v", err)
	}
	var fx actionContractFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("decode action contract fixture: %v", err)
	}
	if len(fx.Calls) != 8 {
		t.Fatalf("fixture carries %d calls, want the 8 recorded calls", len(fx.Calls))
	}
	return fx
}

// TestRetentionReadPayloadsReplay pins the recorded decoder outcome for
// every payload the model actually emitted. Calls 1 and 5 are pure context
// requests and decode. The six mixed submissions are rejected as mutually
// exclusive before any field check, which is the format-retry cost the
// action-contract repair removes.
func TestRetentionReadPayloadsReplay(t *testing.T) {
	fx := loadActionContractFixture(t)
	for _, call := range fx.Calls {
		action, err := DecodeStageAction(call.Tool, call.ArgumentsJSON)
		switch call.Index {
		case 1, 5:
			if err != nil {
				t.Fatalf("call %d (%s): expected the pure context request to decode, got %v", call.Index, call.DeclaredAction, err)
			}
			if action.Request == nil {
				t.Fatalf("call %d: decoded without a context request: %+v", call.Index, action)
			}
			if action.Request.Reason == "" || len(action.Request.Queries) == 0 {
				t.Fatalf("call %d: context request lost its reason or queries: %+v", call.Index, *action.Request)
			}
		default:
			if err == nil {
				t.Fatalf("call %d (%s): the mixed submission must stay rejected; decoded as %+v", call.Index, call.DeclaredAction, action)
			}
			if !strings.Contains(err.Error(), "mutually exclusive") {
				t.Fatalf("call %d: rejection does not name the conflict: %v", call.Index, err)
			}
		}
	}
}

// TestRetentionReadPayloadsShape documents the emission pattern the repair
// must change: the schema advertises request_context unconditionally, so
// the model attached it to every submission except none. When the contract
// is fixed, this test must be updated to assert the mixed shape is gone.
func TestRetentionReadPayloadsShape(t *testing.T) {
	fx := loadActionContractFixture(t)
	mixed, contextOnly, submitOnly := 0, 0, 0
	for _, call := range fx.Calls {
		switch {
		case call.DeclaredAction == "request_context":
			contextOnly++
		case call.DeclaredAction == "submit_changes" && call.HasRequestContext:
			mixed++
		case call.DeclaredAction == "submit_changes":
			submitOnly++
		}
	}
	if mixed != 6 || contextOnly != 2 || submitOnly != 0 {
		t.Fatalf("recorded shape changed: mixed=%d contextOnly=%d submitOnly=%d; re-derive from the raw stream before updating this test", mixed, contextOnly, submitOnly)
	}
}
