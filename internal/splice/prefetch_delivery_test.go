package splice

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// P2 acceptance (handoff section 6): a substituted subject's CURRENT
// source must reach the actual provider request. The recorded retention
// failure delivered a location fact while the model still needed the
// declaration body; the prefetch query closes that gap. The mutation
// check is structural: the payload assertion reads the recorded provider
// request, so removing the delivery call removes the declaration from the
// payload and fails the test.

type payloadCapturingProvider struct {
	mu       sync.Mutex
	requests []zeroruntime.CompletionRequest
	steps    [][]zeroruntime.StreamEvent
}

func (p *payloadCapturingProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.mu.Lock()
	index := len(p.requests)
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	var events []zeroruntime.StreamEvent
	if index < len(p.steps) {
		events = p.steps[index]
	} else {
		events = []zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventDone}}
	}
	ch := make(chan zeroruntime.StreamEvent, len(events)+1)
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func userPayload(request zeroruntime.CompletionRequest) string {
	var b strings.Builder
	for _, m := range request.Messages {
		if m.Role == zeroruntime.MessageRoleUser {
			b.WriteString(m.Content)
		}
	}
	return b.String()
}

// TestSubstitutedSubjectSourceReachesTheFirstRequest proves the delivery
// chain end to end: the admitted record substitutes the discovery, the
// prefetch query fetches the declaration through the guarded reader, and
// the fulfilled view enters the first model request's payload.
func TestSubstitutedSubjectSourceReachesTheFirstRequest(t *testing.T) {
	t.Setenv(EvidenceSubstitutionEnvVar, "on")
	workDir, rev, body := testSymbolRepo(t, nil)
	mem := testSymbolVouchedStore(t, workDir, rev, body)

	submission := schemas.CodeWriterOutput{
		Files:      []schemas.FileChange{{Path: "expiry_test.go", ChangeType: "create", Content: "package main\n\nimport \"testing\"\n\nfunc TestStoreExpiryBoundary(t *testing.T) { runClockTable(t) }\n"}},
		Language:   "go",
		Intent:     "add the expiry boundary test",
		Confidence: 0.9,
	}
	submitArgs, err := json.Marshal(submission)
	if err != nil {
		t.Fatal(err)
	}
	provider := &payloadCapturingProvider{steps: [][]zeroruntime.StreamEvent{
		submitToolCallEvents(string(submitArgs)),
	}}
	options := PipelineConfigFromAgentOptions(agent.Options{})
	runner := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		switch name {
		case "read_file":
			return ToolResult{OK: true, Output: testFileBody}, nil
		case "get_symbol":
			return ToolResult{OK: true, Output: "func runClockTable(t *testing.T) {}"}, nil
		case "list_directory":
			return ToolResult{OK: true, Output: "clock.go\n  clock_test.go\n"}, nil
		default:
			return ToolResult{OK: true, Output: ""}, nil
		}
	})

	tr := newRunTraceAccumulator(nil, "prefetch-delivery", "session", workDir, actionRecoveryPlan(), "active", nil)
	records, _, completed, err := runPass(context.Background(), "prefetch-delivery", 1, actionRecoveryPlan(),
		stageRegistry{"code_writer": stages.CodeWriter{}}, provider, options, workDir, runner, time.Time{}, nil, mem, tr, NewStageExecutionBudget(0))
	if err != nil || !completed {
		t.Fatalf("pass: completed=%v err=%v records=%+v", completed, err, records)
	}

	// The prefetch ran before the first model request, and its fulfilled
	// declaration body is in that request's user payload.
	if len(provider.requests) == 0 {
		t.Fatal("no provider request recorded")
	}
	first := userPayload(provider.requests[0])
	if !strings.Contains(first, "func runClockTable") {
		t.Fatalf("the substituted subject's declaration never reached the first provider request; payload=%q", first[:min(len(first), 600)])
	}
	// The delivered view names its source path and current content version,
	// so the model can cite what it used.
	if !strings.Contains(first, "clock_test.go") {
		t.Fatal("the delivered view lost its source path")
	}

	// The trace records the delivered context items (post-filter delivery
	// evidence).
	stage := tr.stages[stageKeyFor("code_writer", 1, 0)]
	if stage.ContextItems == 0 {
		t.Fatalf("trace recorded no delivered context items: %+v", stage)
	}
}

// TestPrefetchFallsBackWhenTheRecordIsStale is the adversarial pair: the
// record's byte digest no longer matches the workspace, admission rejects
// it, no prefetch query is issued, and the cold plan's own discovery
// remains. A stale pointer must degrade to cold, never deliver stale
// bytes.
func TestPrefetchFallsBackWhenTheRecordIsStale(t *testing.T) {
	t.Setenv(EvidenceSubstitutionEnvVar, "on")
	workDir, rev, body := testSymbolRepo(t, nil)
	rec := testSymbolRecord(t, workDir, rev, body)
	rec.Supporting[0].Digest = strings.Repeat("0", 64)
	rec.ContentVersion = contentVersionDigest(*rec)
	mem := testSymbolStoreWithAnchors(t, workDir, rev, rec, []memd.GraphAnchor{
		{Kind: "file", Value: testSymbolPath},
		{Kind: "symbol", Value: testSymbolPath + "#" + testSymbolName},
	})

	provider := &payloadCapturingProvider{steps: [][]zeroruntime.StreamEvent{
		submitToolCallEvents(`{"files":[{"path":"expiry_test.go","change_type":"create","content":"package main\n"}],"language":"go","intent":"add the test","confidence":0.9}`),
	}}
	options := PipelineConfigFromAgentOptions(agent.Options{})
	runner := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		return ToolResult{OK: true, Output: testFileBody}, nil
	})

	_, _, completed, err := runPass(context.Background(), "prefetch-stale", 1, actionRecoveryPlan(),
		stageRegistry{"code_writer": stages.CodeWriter{}}, provider, options, workDir, runner, time.Time{}, nil, mem, nil, NewStageExecutionBudget(0))
	if err != nil || !completed {
		t.Fatalf("pass: completed=%v err=%v", completed, err)
	}

	// The first request's payload must not carry a delivered declaration
	// from the stale record: the pointer was rejected at admission.
	first := userPayload(provider.requests[0])
	if strings.Contains(first, "Evidence-backed exact substitution") {
		t.Fatalf("a stale record still shaped the context request")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// P3 acceptance (handoff section 7.6): the memory-assisted dependency
// prefetch. A fresh retained record reaches the selector, the host fetches
// its file through the guarded reader, and the CURRENT view appears in the
// INITIAL provider request — without any substitution firing (the need is
// cold-resolved; the mechanism is source acquired earlier, not a removed
// operation). The no-memory variant falls back to the ordinary handshake.

// TestMemoryPrefetchDeliversRetainedSourceBeforeTheFirstRequest runs the
// full chain with prefetch ON and no substitution eligible: the retained
// record's file anchor is fetched, and the declaration body appears in the
// first provider request's payload.
func TestMemoryPrefetchDeliversRetainedSourceBeforeTheFirstRequest(t *testing.T) {
	t.Setenv(MemoryPrefetchEnvVar, "on")
	workDir, rev, body := testSymbolRepo(t, nil)
	mem := testSymbolVouchedStore(t, workDir, rev, body)

	submission := schemas.CodeWriterOutput{
		Files:      []schemas.FileChange{{Path: "expiry_test.go", ChangeType: "create", Content: "package main\n\nimport \"testing\"\n\nfunc TestStoreExpiryBoundary(t *testing.T) { runClockTable(t) }\n"}},
		Language:   "go",
		Intent:     "add the expiry boundary test",
		Confidence: 0.9,
	}
	submitArgs, _ := json.Marshal(submission)
	provider := &payloadCapturingProvider{steps: [][]zeroruntime.StreamEvent{
		submitToolCallEvents(string(submitArgs)),
	}}
	options := PipelineConfigFromAgentOptions(agent.Options{})
	var hostReads []string
	runner := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		switch name {
		case "read_file":
			if p, ok := args["path"].(string); ok {
				hostReads = append(hostReads, p)
			}
			return ToolResult{OK: true, Output: testFileBody}, nil
		case "list_directory":
			return ToolResult{OK: true, Output: "clock.go\n  clock_test.go\n"}, nil
		default:
			return ToolResult{OK: true, Output: ""}, nil
		}
	})

	tr := newRunTraceAccumulator(nil, "memory-prefetch", "session", workDir, actionRecoveryPlan(), "active", nil)
	records, _, completed, err := runPass(context.Background(), "memory-prefetch", 1, actionRecoveryPlan(),
		stageRegistry{"code_writer": stages.CodeWriter{}}, provider, options, workDir, runner, time.Time{}, nil, mem, tr, NewStageExecutionBudget(0))
	if err != nil || !completed {
		t.Fatalf("pass: completed=%v err=%v records=%+v", completed, err, records)
	}

	// The prefetch fetched the record's file through the guarded reader
	// BEFORE the first model request: the handshake is provider-free, so
	// one provider request means the source was already in it.
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1 (the prefetch must not add provider calls)", len(provider.requests))
	}
	payload := userPayload(provider.requests[0])
	if !strings.Contains(payload, "func runClockTable") {
		t.Fatalf("the prefetched declaration never reached the first request; payload=%q", payload[:min(len(payload), 600)])
	}
	foundRead := false
	for _, r := range hostReads {
		if strings.HasSuffix(r, testSymbolPath) {
			foundRead = true
		}
	}
	if !foundRead {
		t.Fatalf("the host never fetched the retained file; reads=%v", hostReads)
	}

	// The trace records the prefetch decision with its record identity and
	// verified revision (the report's per-candidate evidence).
	stage := tr.stages[stageKeyFor("code_writer", 1, 0)]
	if len(stage.MemoryPrefetch) == 0 {
		t.Fatalf("trace recorded no memory prefetch decision: %+v", stage)
	}
	for _, rec := range stage.MemoryPrefetch {
		if rec.Path != testSymbolPath {
			t.Fatalf("prefetch path = %q, want %q", rec.Path, testSymbolPath)
		}
		if rec.ContentVersion != rev {
			t.Fatalf("prefetch content version = %q, want the verified revision %q", rec.ContentVersion, rev)
		}
	}
}

// TestMemoryPrefetchOffKeepsTheColdHandshake proves the fallback: with the
// treatment off, no prefetch decision is recorded. The declaration still
// reaches the handshake because the SHARED resolver resolves the visible
// test helper for cold on its own (the P2a capability), never through a
// memory pointer.
func TestMemoryPrefetchOffKeepsTheColdHandshake(t *testing.T) {
	workDir, rev, body := testSymbolRepo(t, nil)
	mem := testSymbolVouchedStore(t, workDir, rev, body)

	provider := &payloadCapturingProvider{steps: [][]zeroruntime.StreamEvent{
		submitToolCallEvents(`{"files":[{"path":"expiry_test.go","change_type":"create","content":"package main\n"}],"language":"go","intent":"add the test","confidence":0.9}`),
	}}
	options := PipelineConfigFromAgentOptions(agent.Options{})
	runner := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		return ToolResult{OK: true, Output: testFileBody}, nil
	})

	tr := newRunTraceAccumulator(nil, "prefetch-off", "session", workDir, actionRecoveryPlan(), "active", nil)
	_, _, completed, err := runPass(context.Background(), "prefetch-off", 1, actionRecoveryPlan(),
		stageRegistry{"code_writer": stages.CodeWriter{}}, provider, options, workDir, runner, time.Time{}, nil, mem, tr, NewStageExecutionBudget(0))
	if err != nil || !completed {
		t.Fatalf("pass: completed=%v err=%v", completed, err)
	}
	if len(provider.requests) == 0 {
		t.Fatal("no provider request recorded")
	}
	// No prefetch decision recorded for any invocation.
	for key, meta := range tr.stages {
		if len(meta.MemoryPrefetch) != 0 {
			t.Fatalf("prefetch recorded with the treatment off: %v %+v", key, meta.MemoryPrefetch)
		}
	}
	// The shared resolver still delivers the helper through cold's own
	// handshake, which is the P2a capability, not the prefetch treatment.
	if got := userPayload(provider.requests[0]); !strings.Contains(got, "func runClockTable") {
		t.Fatalf("the cold handshake lost the shared-resolver delivery; payload=%q", got[:min(len(got), 400)])
	}
}
