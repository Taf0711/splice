package splice

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/tools"
)

func TestDesignTransitionRequestValidate(t *testing.T) {
	valid := DesignTransitionRequest{Action: DesignTransitionCrystallize, Source: DesignTransitionSourceManual}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	for _, bad := range []DesignTransitionRequest{
		{Action: "bogus", Source: DesignTransitionSourceManual},
		{Action: DesignTransitionCrystallize, Source: "bogus"},
		{Action: DesignTransitionCrystallize, Source: ""},
		{Action: DesignTransitionApprove, Source: DesignTransitionSourceAgent, ApproveIfReady: true},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("expected error for %#v", bad)
		}
	}
}

func TestDesignTransitionRecorderSingleRequest(t *testing.T) {
	r := NewDesignTransitionRecorder()
	first := DesignTransitionRequest{Action: DesignTransitionApprove, Source: DesignTransitionSourceAgent}
	second := DesignTransitionRequest{Action: DesignTransitionCrystallize, Source: DesignTransitionSourceAgent}
	if err := r.Record(first); err != nil {
		t.Fatalf("record first: %v", err)
	}
	if err := r.Record(second); err == nil {
		t.Fatal("expected a second transition in the same turn to be rejected")
	}
	got := r.Take()
	if got == nil || got.Action != DesignTransitionApprove {
		t.Fatalf("Take = %#v, want the first approve request", got)
	}
	if again := r.Take(); again != nil {
		t.Fatalf("second Take = %#v, want nil", again)
	}
	// A fresh request is accepted after the previous one is consumed.
	if err := r.Record(second); err != nil {
		t.Fatalf("record after Take: %v", err)
	}
}

func TestDesignTransitionRecorderRejectsInvalid(t *testing.T) {
	r := NewDesignTransitionRecorder()
	if err := r.Record(DesignTransitionRequest{Action: "bogus", Source: DesignTransitionSourceAgent}); err == nil {
		t.Fatal("expected invalid record to error")
	}
	if got := r.Take(); got != nil {
		t.Fatalf("invalid record must not be stored, got %#v", got)
	}
}

func TestCrystallizeDesignToolRecordsRequest(t *testing.T) {
	r := NewDesignTransitionRecorder()
	tool := NewCrystallizeDesignTool(r)
	if tool.Name() != string(DesignTransitionCrystallize) {
		t.Fatalf("name = %q, want %q", tool.Name(), DesignTransitionCrystallize)
	}
	if safety := tool.Safety(); safety.SideEffect != tools.SideEffectLocalControl || safety.Permission != tools.PermissionAllow {
		t.Fatalf("safety = %#v, want local-control allow", safety)
	}

	res := tool.Run(context.Background(), map[string]any{})
	if res.Status != tools.StatusOK {
		t.Fatalf("run = %#v, want ok", res)
	}
	req := r.Take()
	if req == nil || req.Action != DesignTransitionCrystallize || req.Source != DesignTransitionSourceAgent || req.ApproveIfReady {
		t.Fatalf("recorded request = %#v, want agent crystallize with approveIfReady=false", req)
	}

	if res := tool.Run(context.Background(), map[string]any{"approve_if_ready": true}); res.Status != tools.StatusOK {
		t.Fatalf("approve_if_ready run = %#v, want ok", res)
	}
	if req := r.Take(); req == nil || !req.ApproveIfReady {
		t.Fatalf("approve_if_ready not carried: %#v", req)
	}
}

func TestDesignTransitionToolRejectsNonBoolApproveIfReady(t *testing.T) {
	r := NewDesignTransitionRecorder()
	tool := NewCrystallizeDesignTool(r)
	res := tool.Run(context.Background(), map[string]any{"approve_if_ready": "yes"})
	if res.Status != tools.StatusError {
		t.Fatalf("non-bool approve_if_ready = %#v, want error", res)
	}
	if req := r.Take(); req != nil {
		t.Fatalf("invalid call must not record a request, got %#v", req)
	}
}

func TestApproveDesignToolRecordsRequest(t *testing.T) {
	r := NewDesignTransitionRecorder()
	tool := NewApproveDesignTool(r)
	if tool.Name() != string(DesignTransitionApprove) {
		t.Fatalf("name = %q, want %q", tool.Name(), DesignTransitionApprove)
	}
	if safety := tool.Safety(); safety.SideEffect != tools.SideEffectLocalControl || safety.Permission != tools.PermissionAllow {
		t.Fatalf("safety = %#v, want local-control allow", safety)
	}
	res := tool.Run(context.Background(), map[string]any{})
	if res.Status != tools.StatusOK {
		t.Fatalf("run = %#v, want ok", res)
	}
	req := r.Take()
	if req == nil || req.Action != DesignTransitionApprove || req.Source != DesignTransitionSourceAgent {
		t.Fatalf("recorded request = %#v, want agent approve", req)
	}
}
