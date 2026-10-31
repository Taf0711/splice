package openrouterdecisions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/zeroruntime"
)

func sampleRequest() zeroruntime.DecisionRequest {
	return zeroruntime.DecisionRequest{
		Model: "typesafe/jev-1.13",
		State: `{"need":"clock_test.go#runClockTable","anchor":"clock_test.go"}`,
		Questions: []zeroruntime.DecisionQuestion{{
			ID:           "dep_relation",
			Kind:         zeroruntime.DecisionKindChoice,
			Instructions: "Is the supplied candidate a direct dependency of the named need?",
			Criteria: []zeroruntime.DecisionCriterion{
				{Label: "direct_dependency", Description: "The candidate identifies a concrete dependency required for this need."},
				{Label: "background", Description: "The candidate concerns the area but does not resolve this need."},
				{Label: "irrelevant", Description: "The candidate does not concern this need."},
				{Label: "insufficient_evidence", Description: "The description cannot establish the relation."},
			},
		}},
		SessionID: "proof-session",
	}
}

func okChoiceBody() string {
	return `{"answers":{"dep_relation":{"type":"choice","choice":"direct_dependency","confidence":0.81,"probabilities":{"direct_dependency":0.86,"background":0.09,"irrelevant":0.03,"insufficient_evidence":0.02}}},"model":"typesafe/jev-1.13-20260917","provider":"TypeSafe","id":"gen-dec-1","usage":{"cost":0.000019992,"input_tokens":476,"output_tokens":70}}`
}

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return NewClient("key-test", WithEndpoint(server.URL+"/api/alpha/decisions"), WithHTTPClient(server.Client())), server, &calls
}

func TestEvaluateHappyPathChoice(t *testing.T) {
	client, server, calls := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/alpha/decisions" {
			t.Errorf("request path = %q, want the decisions endpoint", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key-test" {
			t.Errorf("authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["model"] != "typesafe/jev-1.13" {
			t.Errorf("model = %v", body["model"])
		}
		state, ok := body["state"].(map[string]any)
		if !ok || state["need"] != "clock_test.go#runClockTable" {
			t.Errorf("state must pass through as JSON: %v", body["state"])
		}
		questions, ok := body["questions"].(map[string]any)
		if !ok {
			t.Fatalf("questions = %v", body["questions"])
		}
		q, ok := questions["dep_relation"].(map[string]any)
		if !ok || q["type"] != "choice" {
			t.Errorf("question = %v", questions["dep_relation"])
		}
		if body["session_id"] != "proof-session" {
			t.Errorf("session id = %v", body["session_id"])
		}
		w.Write([]byte(okChoiceBody()))
	})
	result, err := client.Evaluate(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("calls = %d, want exactly one (no retries)", *calls)
	}
	if result.RequestID != "gen-dec-1" || result.Provider != "TypeSafe" {
		t.Fatalf("identity = %q/%q", result.RequestID, result.Provider)
	}
	// The resolved model carries a dated suffix beyond the pinned ID: that
	// is the documented behavior, not drift.
	if result.ResolvedModel != "typesafe/jev-1.13-20260917" {
		t.Fatalf("resolved model = %q", result.ResolvedModel)
	}
	answer, ok := result.AnswerByID("dep_relation")
	if !ok || answer.Choice != "direct_dependency" {
		t.Fatalf("answer = %+v", answer)
	}
	// Zero-probability labels are preserved, never dropped.
	if answer.Probabilities["irrelevant"] != 0.03 || answer.Probabilities["insufficient_evidence"] != 0.02 {
		t.Fatalf("probabilities = %+v", answer.Probabilities)
	}
	if !answer.ConfidenceSet || answer.Confidence != 0.81 {
		t.Fatalf("confidence = %v set=%v", answer.Confidence, answer.ConfidenceSet)
	}
	if result.InputTokens != 476 || result.OutputTokens != 70 {
		t.Fatalf("usage = %d/%d", result.InputTokens, result.OutputTokens)
	}
	if result.ReportedCostUSD == nil || *result.ReportedCostUSD != 0.000019992 {
		t.Fatalf("reported cost = %v", result.ReportedCostUSD)
	}
	_ = server
}

func TestEvaluateRejectsVersionDrift(t *testing.T) {
	client, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"answers":{"dep_relation":{"type":"choice","choice":"irrelevant"}},"model":"typesafe/jev-2.0","id":"x","usage":{}}`))
	})
	_, err := client.Evaluate(context.Background(), sampleRequest())
	if err == nil || !strings.Contains(err.Error(), "does not match the pinned model") {
		t.Fatalf("err = %v, want a version-drift error naming both models", err)
	}
}

func TestEvaluateRejectsMissingAndUnknownAnswers(t *testing.T) {
	client, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"answers":{},"model":"typesafe/jev-1.13","id":"x","usage":{}}`))
	})
	if _, err := client.Evaluate(context.Background(), sampleRequest()); err == nil || !strings.Contains(err.Error(), "missing answer") {
		t.Fatalf("err = %v, want a missing-answer error", err)
	}
	client2, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"answers":{"ghost":{"type":"choice","choice":"irrelevant"}},"model":"typesafe/jev-1.13","id":"x","usage":{}}`))
	})
	if _, err := client2.Evaluate(context.Background(), sampleRequest()); err == nil || !strings.Contains(err.Error(), "unknown question") {
		t.Fatalf("err = %v, want an unknown-question error", err)
	}
}

func TestEvaluateRejectsMalformedDistribution(t *testing.T) {
	client, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// The selected label is not one of the criteria.
		w.Write([]byte(`{"answers":{"dep_relation":{"type":"choice","choice":"payments","probabilities":{}}},"model":"typesafe/jev-1.13","id":"x","usage":{}}`))
	})
	if _, err := client.Evaluate(context.Background(), sampleRequest()); err == nil || !strings.Contains(err.Error(), "not one of the criteria") {
		t.Fatalf("err = %v, want an unknown-label error", err)
	}
	client2, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// The probability sum deviates far from one.
		w.Write([]byte(`{"answers":{"dep_relation":{"type":"choice","choice":"irrelevant","probabilities":{"direct_dependency":0.1,"background":0.1,"irrelevant":0.1,"insufficient_evidence":0.1}}},"model":"typesafe/jev-1.13","id":"x","usage":{}}`))
	})
	if _, err := client2.Evaluate(context.Background(), sampleRequest()); err == nil || !strings.Contains(err.Error(), "probability sum") {
		t.Fatalf("err = %v, want a distribution-sum error", err)
	}
	client3, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// A noul answer for a choice question.
		w.Write([]byte(`{"answers":{"dep_relation":{"type":"noul","noul":0.9}},"model":"typesafe/jev-1.13","id":"x","usage":{}}`))
	})
	if _, err := client3.Evaluate(context.Background(), sampleRequest()); err == nil || !strings.Contains(err.Error(), "does not match question kind") {
		t.Fatalf("err = %v, want a kind-mismatch error", err)
	}
}

func TestEvaluatePreservesChargeOnMalformedAnswer(t *testing.T) {
	client, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"answers":{"dep_relation":{"type":"choice","choice":"ghost"}},"model":"typesafe/jev-1.13","id":"gen-dec-9","usage":{"cost":0.000019992,"input_tokens":476,"output_tokens":70}}`))
	})
	_, err := client.Evaluate(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("malformed answers must fail")
	}
	var respErr *ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("err = %T, want a ResponseError", err)
	}
	if respErr.Usage == nil || respErr.Usage.InputTokens != 476 || respErr.Usage.ReportedCostUSD == nil {
		t.Fatalf("the billed usage must survive an unusable answer: %+v", respErr.Usage)
	}
	if respErr.Usage.RequestID != "gen-dec-9" {
		t.Fatalf("request id = %q", respErr.Usage.RequestID)
	}
}

func TestEvaluateClassifiesStatusErrorsWithoutRetry(t *testing.T) {
	cases := map[int]string{
		http.StatusUnauthorized:        "status 401",
		http.StatusPaymentRequired:     "status 402",
		http.StatusTooManyRequests:     "status 429",
		http.StatusInternalServerError: "status 500",
	}
	for status, want := range cases {
		client, _, calls := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte(`{"error":"no"}`))
		})
		_, err := client.Evaluate(context.Background(), sampleRequest())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("status %d: err = %v, want %q", status, err, want)
		}
		if *calls != 1 {
			t.Fatalf("status %d: calls = %d, want exactly one (zero automatic retries)", status, *calls)
		}
		var statusErr *StatusCodeError
		if !errors.As(err, &statusErr) || statusErr.Status != status {
			t.Fatalf("status %d: err = %T, want a StatusCodeError", status, err)
		}
	}
}

func TestEvaluateHonorsCancellation(t *testing.T) {
	client, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Write([]byte(okChoiceBody()))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := client.Evaluate(ctx, sampleRequest()); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestEvaluateBoundsBodies(t *testing.T) {
	client, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"answers":` + strings.Repeat("x", 300*1024) + `}`))
	})
	if _, err := client.Evaluate(context.Background(), sampleRequest()); err == nil || !strings.Contains(err.Error(), "byte bound") {
		t.Fatalf("err = %v, want the response bound error", err)
	}
}

func TestEvaluateFailsLoudWithoutKeyOrWire(t *testing.T) {
	noKey := NewClient("")
	if _, err := noKey.Evaluate(context.Background(), sampleRequest()); err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Fatalf("err = %v, want the missing-key error", err)
	}
	request := sampleRequest()
	request.State = "not json"
	client, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	if _, err := client.Evaluate(context.Background(), request); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("err = %v, want the invalid-state error", err)
	}
}

func TestRequestValidation(t *testing.T) {
	base := sampleRequest()
	if err := base.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	noModel := base
	noModel.Model = " "
	if err := noModel.Validate(); err == nil {
		t.Fatal("empty model must be rejected")
	}
	noState := base
	noState.State = ""
	if err := noState.Validate(); err == nil {
		t.Fatal("empty state must be rejected")
	}
	noQuestions := base
	noQuestions.Questions = nil
	if err := noQuestions.Validate(); err == nil {
		t.Fatal("zero questions must be rejected")
	}
	dup := base
	dup.Questions = append(dup.Questions, base.Questions[0])
	if err := dup.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate question id") {
		t.Fatalf("err = %v, want a duplicate-id error", err)
	}
	badNoul := base
	badNoul.Questions = []zeroruntime.DecisionQuestion{{
		ID: "is_dep", Kind: zeroruntime.DecisionKindNoul, Instructions: "x",
		Criteria: []zeroruntime.DecisionCriterion{{Label: "yes"}, {Label: "no"}},
	}}
	if err := badNoul.Validate(); err == nil || !strings.Contains(err.Error(), "false then true") {
		t.Fatalf("err = %v, want the noul label-order error", err)
	}
}

func TestTranslatePreservesMalformedAnswerErrorOrder(t *testing.T) {
	for _, tc := range []struct {
		name, answer, want string
	}{
		{
			name:   "kind before absent choice",
			answer: `{"type":"noul","noul":0.9}`,
			want:   "does not match question kind",
		},
		{
			name:   "confidence before kind",
			answer: `{"type":"noul","confidence":2,"noul":0.9}`,
			want:   "confidence",
		},
		{
			name:   "choice before distribution",
			answer: `{"type":"choice","probabilities":{"irrelevant":0.1}}`,
			want:   "without a selected label",
		},
		{
			name:   "selected label before distribution",
			answer: `{"type":"choice","choice":"ghost","probabilities":{}}`,
			want:   "not one of the criteria",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response wireResponse
			raw := `{"answers":{"dep_relation":` + tc.answer + `},"model":"typesafe/jev-1.13","usage":{}}`
			if err := json.Unmarshal([]byte(raw), &response); err != nil {
				t.Fatal(err)
			}
			_, err := translate(sampleRequest(), response)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %s", err, tc.want)
			}
		})
	}
}
