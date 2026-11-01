package schemas

import (
	"encoding/json"
	"strings"
	"testing"
)

func validStageMessage() StageMessage {
	return StageMessage{
		ID:    "msg-1",
		RunID: "run-1",
		From:  "test_runner",
		To:    "code_writer",
		Kind:  MessageKindRevisionRequest,
		Payload: RevisionRequest{
			FailingEvidence: []string{"TestFailures failed"},
			ChangedFiles:    []string{"main.go"},
			Instruction:     "fix the failing test",
		},
	}
}

func TestStageMessageValidateAcceptReject(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*StageMessage)
		wantErr string
	}{
		{"valid", func(*StageMessage) {}, ""},
		{"missing id", func(m *StageMessage) { m.ID = "" }, "message id is required"},
		{"missing run_id", func(m *StageMessage) { m.RunID = "" }, "message run_id is required"},
		{"missing from", func(m *StageMessage) { m.From = "" }, "message from is required"},
		{"missing to", func(m *StageMessage) { m.To = "" }, "message to is required"},
		{"unknown kind", func(m *StageMessage) { m.Kind = MessageKind("bogus") }, "invalid message kind"},
		{"empty evidence", func(m *StageMessage) { m.Payload.FailingEvidence = nil }, "failing evidence"},
		{"empty evidence item", func(m *StageMessage) { m.Payload.FailingEvidence = []string{""} }, "failing_evidence[0] is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message := validStageMessage()
			tc.mutate(&message)
			err := message.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestMessageKindValidate(t *testing.T) {
	if err := MessageKindRevisionRequest.Validate(); err != nil {
		t.Fatalf("valid kind rejected: %v", err)
	}
	if err := MessageKind("other").Validate(); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestStageMessageJSONRoundTrip(t *testing.T) {
	message := validStageMessage()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded StageMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.ID != message.ID || decoded.RunID != message.RunID || decoded.From != message.From || decoded.To != message.To {
		t.Fatalf("round-trip identity mismatch: %#v", decoded)
	}
	if decoded.Kind != MessageKindRevisionRequest {
		t.Fatalf("round-trip kind = %q, want revision_request", decoded.Kind)
	}
	if len(decoded.Payload.FailingEvidence) != 1 || decoded.Payload.FailingEvidence[0] != "TestFailures failed" {
		t.Fatalf("round-trip evidence mismatch: %#v", decoded.Payload)
	}
	if decoded.Payload.Instruction != "fix the failing test" {
		t.Fatalf("round-trip instruction mismatch: %#v", decoded.Payload)
	}
	// omitempty: Subject and Evidence are dropped when empty.
	if strings.Contains(string(data), `"subject"`) || strings.Contains(string(data), `"evidence"`) {
		t.Fatalf("empty subject/evidence should be omitted: %s", data)
	}
}

func TestHarnessStageOutputMessagesCap(t *testing.T) {
	message := validStageMessage()
	makeMessages := func(n int) []StageMessage {
		out := make([]StageMessage, n)
		for i := range out {
			out[i] = message
			out[i].ID = "msg-" + string(rune('a'+i))
		}
		return out
	}

	output := HarnessStageOutput{Summary: "ok", Confidence: 0.9}
	output.Messages = makeMessages(4)
	if err := output.Validate(); err != nil {
		t.Fatalf("4 messages should validate, got %v", err)
	}
	output.Messages = makeMessages(5)
	if err := output.Validate(); err == nil || !strings.Contains(err.Error(), "max 4") {
		t.Fatalf("5 messages should be rejected, got %v", err)
	}
}
