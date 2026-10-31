package decisions_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/providers/httpdecisions"
	"github.com/Taf0711/splice/internal/providers/llmdecisions"
	"github.com/Taf0711/splice/internal/providers/openrouterdecisions"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type conformanceProvider struct {
	arguments, model string
	wait             bool
	forced           []string
}

func (p *conformanceProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.forced = append(p.forced, request.ToolChoice)
	if len(request.Tools) != 1 || request.Tools[0].Name != "answer" {
		return nil, errors.New("required answer tool")
	}
	ch := make(chan zeroruntime.StreamEvent, 5)
	if p.wait {
		return ch, nil
	}
	cost := 0.01
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 12, OutputTokens: 3}, ReportedCostUSD: &cost}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolName: "answer", ToolCallID: "a"}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ArgumentsFragment: p.arguments, ToolCallID: "a"}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "a"}
	close(ch)
	return ch, nil
}

func TestEvaluatorConformance(t *testing.T) {
	req := zeroruntime.DecisionRequest{Model: "model", State: "{}", Questions: []zeroruntime.DecisionQuestion{{ID: "q", Kind: zeroruntime.DecisionKindChoice, Instructions: "Choose.", Criteria: []zeroruntime.DecisionCriterion{{Label: "continue"}, {Label: "stop"}}}}}
	for _, backend := range []string{"openrouter_decisions", "http", "provider"} {
		t.Run(backend, func(t *testing.T) {
			for _, scenario := range []string{"valid", "omitted", "sum", "drift", "malformed_usage", "oversized", "timeout", "cancelled"} {
				// Only the timeout scenarios need a short client timeout. The others get a
				// generous one, so a loaded machine cannot fail a valid answer.
				callTimeout := 10 * time.Second
				if scenario == "timeout" || scenario == "cancelled" {
					callTimeout = 20 * time.Millisecond
				}
				t.Run(scenario, func(t *testing.T) {
					p := map[string]float64{"continue": 0.8, "stop": 0.2}
					model := "model-2026"
					if scenario == "omitted" {
						p = map[string]float64{"continue": 1}
					}
					if scenario == "sum" {
						p = map[string]float64{"continue": 0.5}
					}
					if scenario == "drift" {
						model = "other"
					}
					probabilities, _ := json.Marshal(p)
					arguments := `{"answers":[{"question_id":"q","probabilities":` + string(probabilities) + `}]}`
					if scenario == "malformed_usage" {
						arguments = `{"answers":"bad"}`
					}
					if scenario == "oversized" {
						arguments = strings.Repeat("x", 2048)
					}
					responseBound := 4096
					if scenario == "oversized" {
						responseBound = 128
					}
					ctx := context.Background()
					if scenario == "cancelled" {
						var cancel context.CancelFunc
						ctx, cancel = context.WithCancel(ctx)
						cancel()
					}
					var evaluator zeroruntime.DecisionEvaluator
					if backend == "provider" {
						fake := &conformanceProvider{arguments: arguments, wait: scenario == "timeout"}
						evaluator = llmdecisions.NewClient(fake, llmdecisions.Options{Model: model, MaxResponseBytes: responseBound, Timeout: callTimeout})
					} else {
						handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							if scenario == "timeout" {
								// Server-side cancellation can lag when the request body is unread.
								// Bound the handler so server.Close cannot wait indefinitely.
								select {
								case <-r.Context().Done():
								case <-time.After(100 * time.Millisecond):
								}
								return
							}
							if scenario == "oversized" {
								io.WriteString(w, strings.Repeat("x", 2048))
								return
							}
							if backend == "http" {
								if r.Header.Get("Authorization") != "Bearer key" {
									t.Error("missing bearer token")
								}
								var wire httpdecisions.Request
								if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
									t.Error(err)
								}
								if wire.Contract != httpdecisions.Contract || wire.State != "{}" {
									t.Error("HTTP request contract")
								}
								answers := `[{"question_id":"q","kind":"choice","probabilities":` + string(probabilities) + `}]`
								if scenario == "malformed_usage" {
									answers = `"bad"`
								}
								io.WriteString(w, `{"resolved_model":"`+model+`","answers":`+answers+`,"usage":{"input_tokens":12,"output_tokens":3,"cost_usd":0.01}}`)
							} else {
								answers := `{"q":{"type":"choice","choice":"continue","probabilities":` + string(probabilities) + `}}`
								if scenario == "malformed_usage" {
									answers = `"bad"`
								}
								io.WriteString(w, `{"model":"`+model+`","answers":`+answers+`,"usage":{"input_tokens":12,"output_tokens":3,"cost":0.01}}`)
							}
						})
						server := httptest.NewServer(handler)
						t.Cleanup(server.Close)
						endpoint, client := server.URL, server.Client()
						client.Timeout = callTimeout
						if backend == "http" {
							evaluator = httpdecisions.NewClient(httpdecisions.Options{Endpoint: endpoint, APIKey: "key", HTTPClient: client, Timeout: callTimeout, MaxResponseBytes: responseBound})
						} else {
							evaluator = openrouterdecisions.NewClient("key", openrouterdecisions.WithEndpoint(endpoint), openrouterdecisions.WithHTTPClient(client), openrouterdecisions.WithMaxResponseBytes(responseBound))
						}
					}
					result, err := evaluator.Evaluate(ctx, req)
					valid := scenario == "valid" || scenario == "omitted"
					if (err == nil) != valid {
						t.Fatalf("result=%+v error=%v", result, err)
					}
					if valid {
						if result.InputTokens != 12 || result.OutputTokens != 3 || result.ReportedCostUSD == nil {
							t.Fatalf("usage lost: %+v", result)
						}
						if _, ok := result.Answers[0].Probabilities["stop"]; !ok {
							t.Fatal("omitted label must be recorded as zero")
						}
					}
					if scenario == "malformed_usage" {
						var billed zeroruntime.DecisionUsageError
						if !errors.As(err, &billed) || billed.DecisionUsage() == nil || billed.DecisionUsage().InputTokens != 12 {
							t.Fatalf("usage lost: %v", err)
						}
					}
					if scenario == "cancelled" && !errors.Is(err, context.Canceled) {
						t.Fatalf("cancellation: %v", err)
					}
					if scenario == "timeout" {
						var timeout interface{ Timeout() bool }
						if !errors.Is(err, context.DeadlineExceeded) && !(errors.As(err, &timeout) && timeout.Timeout()) {
							t.Fatalf("timeout: %v", err)
						}
					}
				})
			}
		})
	}
}
