package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Stage keys for FakeProvider.Script. A model-backed stage is keyed by its
// forced tool name (tools[0].function.name); a request with no tools is a
// prompt node.
const (
	StageCodeWriter    = "submit_code"
	StageTestGenerator = "submit_tests"
	StageStepBack      = "submit_step_back"
	StagePrompt        = "(prompt)"
)

// FakeModel is the model name of the seeded "fake" profile.
const FakeModel = "fake-model"

// FakeAPIKey is the key the seeded profile reads from FakeAPIKeyEnv.
const (
	FakeAPIKey    = "sk-e2e-fake"
	FakeAPIKeyEnv = "SPLICE_E2E_FAKE_KEY"
)

// Reply is one scripted provider answer.
type Reply struct {
	Text     string    // assistant text content (optional)
	Tool     *ToolCall // one tool call (optional)
	Status   int       // non-zero: answer with this HTTP status and Text as the error message
	Prompt   int       // usage prompt_tokens (default 100)
	Complete int       // usage completion_tokens (default 50)
}

// ToolCall is the tool call a Reply streams.
type ToolCall struct {
	Name string
	Args string // raw JSON arguments
}

// Request is one request the fake provider received.
type Request struct {
	Method string
	Path   string
	Model  string
	Stage  string // stage key; "" for a non-chat request
	Header http.Header
	Body   []byte
}

func (r Request) String() string {
	body := string(r.Body)
	if len(body) > 600 {
		body = body[:600] + "..."
	}
	return fmt.Sprintf("%s %s model=%q stage=%q body=%s", r.Method, r.Path, r.Model, r.Stage, body)
}

// FakeProvider is a scripted OpenAI-compatible streaming chat server.
type FakeProvider struct {
	BaseURL string // "<server>/v1"; use it as the profile baseURL
	URL     string // server root

	t        testing.TB
	mu       sync.Mutex
	queues   map[string][]Reply
	requests []Request
}

// NewFakeProvider starts a fake provider that the test owns.
func NewFakeProvider(t testing.TB) *FakeProvider {
	f := &FakeProvider{t: t, queues: map[string][]Reply{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.URL = srv.URL
	f.BaseURL = srv.URL + "/v1"
	return f
}

// Script appends replies to the queue of one stage key.
func (f *FakeProvider) Script(stage string, replies ...Reply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queues[stage] = append(f.queues[stage], replies...)
}

// Requests returns a copy of every request received so far.
func (f *FakeProvider) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// Unused returns the stage keys that still have queued replies.
func (f *FakeProvider) Unused() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int{}
	for k, q := range f.queues {
		if len(q) > 0 {
			out[k] = len(q)
		}
	}
	return out
}

func (f *FakeProvider) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	req := Request{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		f.record(req)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"object":"list","data":[{"id":%q,"object":"model","owned_by":"e2e"}]}`, FakeModel)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
	default:
		f.record(req)
		f.fail(w, "unexpected request", req)
		return
	}

	var wire struct {
		Model string `json:"model"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		f.record(req)
		f.fail(w, "chat request is not JSON: "+err.Error(), req)
		return
	}
	req.Model = wire.Model
	req.Stage = StagePrompt
	if len(wire.Tools) > 0 {
		req.Stage = wire.Tools[0].Function.Name
	}

	f.mu.Lock()
	f.requests = append(f.requests, req)
	queue := f.queues[req.Stage]
	var reply Reply
	ok := len(queue) > 0
	if ok {
		reply, f.queues[req.Stage] = queue[0], queue[1:]
	}
	n := len(f.requests)
	f.mu.Unlock()

	if !ok {
		f.fail(w, "no scripted reply for stage "+req.Stage, req)
		return
	}
	if reply.Status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.Status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": reply.Text, "type": "e2e"}})
		return
	}
	writeSSE(w, n, reply)
}

func (f *FakeProvider) record(req Request) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
}

// fail marks the test failed and answers 400. The client does not retry a
// 400, so the binary stops at once and the error names the fake.
func (f *FakeProvider) fail(w http.ResponseWriter, why string, req Request) {
	f.t.Errorf("fake provider: %s: %s", why, req)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "e2e fake provider: " + why, "type": "e2e"}})
}

// writeSSE streams one reply the way internal/providers/openai reads it:
// content deltas, a tool call split over two argument fragments, a
// finish_reason chunk, a final usage chunk, then [DONE].
func writeSSE(w http.ResponseWriter, n int, reply Reply) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	id := fmt.Sprintf("chatcmpl-e2e-%d", n)
	chunk := func(v map[string]any) {
		v["id"] = id
		v["object"] = "chat.completion.chunk"
		v["model"] = FakeModel
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	}
	choice := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	}
	chunk(choice(map[string]any{"role": "assistant", "content": ""}, nil))
	if reply.Text != "" {
		chunk(choice(map[string]any{"content": reply.Text}, nil))
	}
	finish := "stop"
	if reply.Tool != nil {
		finish = "tool_calls"
		callID := fmt.Sprintf("call_e2e_%d", n)
		args := reply.Tool.Args
		half := len(args) / 2
		chunk(choice(map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": callID, "type": "function",
			"function": map[string]any{"name": reply.Tool.Name, "arguments": args[:half]},
		}}}, nil))
		chunk(choice(map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "function": map[string]any{"arguments": args[half:]},
		}}}, nil))
	}
	chunk(choice(map[string]any{}, finish))
	prompt, complete := reply.Prompt, reply.Complete
	if prompt == 0 {
		prompt = 100
	}
	if complete == 0 {
		complete = 50
	}
	chunk(map[string]any{"choices": []any{}, "usage": map[string]any{
		"prompt_tokens": prompt, "completion_tokens": complete, "total_tokens": prompt + complete,
	}})
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

// FileChange is one file in a submit_code or submit_tests payload.
type FileChange struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	ChangeType string `json:"change_type"`
}

// File builds a full-content file change. Use "create" for a new path and
// "modify" for a path that exists in the workspace.
func File(path, changeType, content string) FileChange {
	return FileChange{Path: path, Content: content, ChangeType: changeType}
}

// SubmitCode answers the code writer with the flat legacy full/1 payload.
func SubmitCode(intent string, files ...FileChange) Reply {
	return ToolCallReply(StageCodeWriter, map[string]any{
		"files": files, "language": "go", "intent": intent, "confidence": 0.95,
	})
}

// SubmitTests answers the test generator.
func SubmitTests(intent string, files ...FileChange) Reply {
	return ToolCallReply(StageTestGenerator, map[string]any{
		"files": files, "language": "go", "intent": intent, "confidence": 0.9,
	})
}

// ToolCallReply answers with one call to name with args encoded as JSON.
func ToolCallReply(name string, args any) Reply {
	b, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	return Reply{Tool: &ToolCall{Name: name, Args: string(b)}}
}

// Answer is a plain text answer (a prompt node).
func Answer(text string) Reply { return Reply{Text: text} }

// HTTPError answers with an HTTP error status.
func HTTPError(status int, message string) Reply { return Reply{Status: status, Text: message} }

// stagesOf lists the stage keys of the recorded chat requests, in order.
func stagesOf(reqs []Request) []string {
	var out []string
	for _, r := range reqs {
		if r.Stage != "" {
			out = append(out, r.Stage)
		}
	}
	return out
}
