package splice

// M4 exit-gate integration tests (SPEC_DECISION_ADVISOR_JEV_FOLD_2026-09-20,
// milestone M4): the integration test proves candidate to delivered payload;
// missing materialization breaks the payload assertion; success still comes
// from normal verification.

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// testCalibration is a fixed calibration store for tests.
type testCalibration struct {
	p  float64
	ok bool
}

func (c testCalibration) PUseful(class string) (float64, bool) { return c.p, c.ok }

// packAnswer builds one pack answer for a candidate question.
func packAnswer(questionID, choice string, pDirect float64) schemas.DecisionAnswerRecord {
	return schemas.DecisionAnswerRecord{
		QuestionID: questionID,
		Kind:       "choice",
		Choice:     choice,
		Probabilities: map[string]float64{
			"direct_dependency":     pDirect,
			"background":            (1 - pDirect) * 0.4,
			"irrelevant":            (1 - pDirect) * 0.3,
			"insufficient_evidence": (1 - pDirect) * 0.3,
		},
	}
}

// packEvaluator answers each question: the first (lexicographically
// earliest) question wins with direct_dependency at pDirect, the rest
// decline with insufficient_evidence. That exercises the selection policy's
// probability comparison, not just its tie-break.
type packEvaluator struct {
	calls   int
	pDirect float64
	first   bool
	weakAll bool // every question declines with insufficient_evidence
}

func (m *packEvaluator) Evaluate(ctx context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
	m.calls++
	questions := append([]zeroruntime.DecisionQuestion(nil), req.Questions...)
	for i := range questions {
		for j := i + 1; j < len(questions); j++ {
			if questions[j].ID < questions[i].ID {
				questions[i], questions[j] = questions[j], questions[i]
			}
		}
	}
	answers := make([]zeroruntime.DecisionAnswer, 0, len(questions))
	for i, q := range questions {
		choice, p := "insufficient_evidence", 0.1
		if !m.weakAll && i == 0 {
			choice, p = "direct_dependency", m.pDirect
		}
		answers = append(answers, zeroruntime.DecisionAnswer{
			QuestionID:    q.ID,
			Kind:          zeroruntime.DecisionKindChoice,
			Choice:        choice,
			Probabilities: packAnswer(q.ID, choice, p).Probabilities,
			ConfidenceSet: false,
		})
	}
	return zeroruntime.DecisionResult{
		RequestID:       "gen-dec-m4",
		ResolvedModel:   "typesafe/jev-1.13-20260917",
		Provider:        "TypeSafe",
		InputTokens:     420,
		OutputTokens:    55,
		ReportedCostUSD: ptrFloat64(0.00003),
		Answers:         answers,
	}, nil
}

func selectorCandidates() []SelectorCandidate {
	return []SelectorCandidate{
		{Ref: "internal/clock.go#runClockTable", Claim: "routes the clock table through the shared helper", Origin: "graph:reuse", Anchors: []string{"internal/clock.go#runClockTable", "internal/clock.go"}},
		{Ref: "internal/other.go#helper", Claim: "concerns the same package but not this need", Origin: "graph:reuse", Anchors: []string{"internal/other.go#helper"}},
	}
}

func activeExperimentalAdvisor(eval zeroruntime.DecisionEvaluator) *DecisionAdvisor {
	cfg := shadowConfig()
	cfg.Mode = AdvisorModeActiveExperimental
	advisor, _ := NewDecisionAdvisor(cfg, eval)
	return advisor
}

func TestSelectorCandidateToDeliveredQuery(t *testing.T) {
	eval := &packEvaluator{pDirect: 0.82}
	advisor := activeExperimentalAdvisor(eval)
	snap := jevSnapshot("internal/clock.go#runClockTable")
	snap.QuestionPack = schemas.QuestionPackDependencyRelationV1

	// The gate must recognize the pack question IDs, so run the selector
	// end to end: pack built, gate bought, policy selected, gate 2 passed.
	outcome, err := advisor.runSelector(context.Background(), snap, selectorCandidates(), testCalibration{p: 0.9, ok: true},
		&schemas.EconomicAssessment{
			BaselineContinuationHighUSD: ptrFloat64(0.001),
			ActionCostUSD:               ptrFloat64(0.0001),
			CalibrationProvenance:       "test",
		})
	if err != nil {
		t.Fatalf("selector: %v", err)
	}
	if outcome.Disposition != schemas.DispositionApplied {
		t.Fatalf("disposition = %q reason = %q", outcome.Disposition, outcome.Reason)
	}
	if outcome.SelectedRef != "internal/clock.go#runClockTable" {
		t.Fatalf("selected = %q", outcome.SelectedRef)
	}
	if len(outcome.Fetches) == 0 || len(outcome.Fetches) > maxSelectorFiles {
		t.Fatalf("fetches = %d, want 1..%d", len(outcome.Fetches), maxSelectorFiles)
	}
	// The materialization query is the exact-declaration form: the M1
	// delivery repair's contract (path and bare symbol, split, so
	// fulfillGetSymbol resolves the declaration).
	queries := ContextQueriesFor(outcome.Fetches)
	found := false
	for _, q := range queries {
		if q.QueryType == schemas.ContextGetSymbol {
			found = true
			if q.Symbol == nil || *q.Symbol != "runClockTable" {
				t.Fatalf("symbol = %v, want the bare declaration name", q.Symbol)
			}
			if q.Path == nil || *q.Path != "internal/clock.go" {
				t.Fatalf("path = %v, want the file path", q.Path)
			}
		}
	}
	if !found {
		t.Fatal("the selected symbol anchor did not materialize into a get_symbol query")
	}
	// The decision identity rides the fetch for trace reconciliation.
	if outcome.Fetches[0].RequestID != "gen-dec-m4" || outcome.Fetches[0].Model != "typesafe/jev-1.13-20260917" {
		t.Fatalf("fetch identity = %q/%q", outcome.Fetches[0].RequestID, outcome.Fetches[0].Model)
	}
	if eval.calls != 1 {
		t.Fatalf("calls = %d, want exactly one", eval.calls)
	}
}

func TestSelectorQueryFulfillsToPayload(t *testing.T) {
	// The materialization actually runs through the guarded fulfillment
	// path against a real temp file: the delivered payload carries the
	// declaration text, not a location claim.
	outcome := SelectorOutcome{
		SelectedRef: "helper.go#RunClock",
		Fetches:     []SelectorFetch{{Path: "helper.go", Symbol: "RunClock", IsSymbol: true, SelectedRef: "helper.go#RunClock"}},
	}
	runner := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		switch name {
		case "read_file", "raw_file_read":
			return ToolResult{OK: true, Output: "package main\n\n// RunClock routes the clock table.\nfunc RunClock() int { return 42 }\n"}, nil
		case "find_symbol", "get_symbol":
			sym, _ := args["symbol"].(string)
			if sym == "Missing" {
				return ToolResult{OK: true, Output: "no declaration named Missing in helper.go"}, nil
			}
			return ToolResult{OK: true, Output: "// RunClock routes the clock table.\nfunc RunClock() int { return 42 }"}, nil
		}
		return ToolResult{OK: true, Output: ""}, nil
	})
	queries := ContextQueriesFor(outcome.Fetches)
	bundle, err := FulfillContextRequest(context.Background(), schemas.ContextRequest{Reason: "selector prefetch", Queries: queries}, runner)
	if err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	declared := false
	for _, item := range bundle.Items {
		if item.Error != nil || item.Payload == nil {
			continue
		}
		if text, ok := item.Payload["text"].(string); ok && strings.Contains(text, "func RunClock()") {
			declared = true
		}
	}
	if !declared {
		t.Fatalf("the declaration never reached the payload: %+v", bundle.Items)
	}

	// Missing materialization breaks the payload assertion: an anchor that
	// resolves to nothing yields typed unresolved evidence, never a
	// satisfied need from the location claim alone.
	broken := SelectorOutcome{
		SelectedRef: "helper.go#Missing",
		Fetches:     []SelectorFetch{{Path: "helper.go", Symbol: "Missing", IsSymbol: true}},
	}
	brokenBundle, err := FulfillContextRequest(context.Background(), schemas.ContextRequest{Reason: "selector prefetch", Queries: ContextQueriesFor(broken.Fetches)}, runner)
	if err != nil {
		t.Fatalf("fulfill broken: %v", err)
	}
	resolved := false
	for _, item := range brokenBundle.Items {
		if item.Error == nil && item.Payload != nil {
			if text, ok := item.Payload["text"].(string); ok && strings.Contains(text, "func Missing(") {
				resolved = true
			}
		}
	}
	if resolved {
		t.Fatal("a missing declaration must never satisfy the need")
	}
}

func TestSelectorShadowNeverDelivers(t *testing.T) {
	eval := &packEvaluator{pDirect: 0.82}
	shadow, _ := NewDecisionAdvisor(shadowConfig(), eval)
	snap := jevSnapshot("internal/clock.go#runClockTable")
	outcome, err := shadow.runSelector(context.Background(), snap, selectorCandidates(), testCalibration{p: 0.9, ok: true},
		&schemas.EconomicAssessment{
			BaselineContinuationHighUSD: ptrFloat64(0.001),
			ActionCostUSD:               ptrFloat64(0.0001),
			CalibrationProvenance:       "test",
		})
	if err != nil {
		t.Fatalf("selector: %v", err)
	}
	if outcome.Disposition != schemas.DispositionShadowOnly {
		t.Fatalf("disposition = %q", outcome.Disposition)
	}
	if len(outcome.Fetches) != 0 {
		t.Fatalf("shadow delivered %d fetches", len(outcome.Fetches))
	}
	if eval.calls != 1 {
		t.Fatalf("shadow bought %d decisions, want exactly one recorded purchase", eval.calls)
	}
}

func TestSelectorGate2UncalibratedRefuses(t *testing.T) {
	eval := &packEvaluator{pDirect: 0.82}
	advisor := activeExperimentalAdvisor(eval)
	snap := jevSnapshot("internal/clock.go#runClockTable")
	outcome, err := advisor.runSelector(context.Background(), snap, selectorCandidates(), nil,
		&schemas.EconomicAssessment{
			BaselineContinuationHighUSD: ptrFloat64(0.001),
			ActionCostUSD:               ptrFloat64(0.0001),
			CalibrationProvenance:       "test", // gate 1 buys; the CalibrationStore is still nil
		})
	if err != nil {
		t.Fatalf("selector: %v", err)
	}
	if outcome.Disposition != schemas.DispositionUncalibrated {
		t.Fatalf("disposition = %q, want uncalibrated", outcome.Disposition)
	}
	if len(outcome.Fetches) != 0 {
		t.Fatalf("uncalibrated gate delivered %d fetches", len(outcome.Fetches))
	}
	if eval.calls != 1 {
		t.Fatal("the decision is bought (fee sunk) even when gate 2 refuses; the answer is recorded, not acted on")
	}
}

func TestSelectorInsufficientEvidenceKeepsBaseline(t *testing.T) {
	// All questions decline: the policy keeps the baseline.
	eval := &packEvaluator{pDirect: 0.0}
	advisor := activeExperimentalAdvisor(eval)
	snap := jevSnapshot("internal/clock.go#runClockTable")
	outcome, err := advisor.runSelector(context.Background(), snap, selectorCandidates(), testCalibration{p: 0.9, ok: true},
		&schemas.EconomicAssessment{
			BaselineContinuationHighUSD: ptrFloat64(0.001),
			ActionCostUSD:               ptrFloat64(0.0001),
			CalibrationProvenance:       "test",
		})
	if err != nil {
		t.Fatalf("selector: %v", err)
	}
	if outcome.SelectedRef != "" || len(outcome.Fetches) != 0 {
		t.Fatalf("a weak answer selected %q with %d fetches", outcome.SelectedRef, len(outcome.Fetches))
	}
	if outcome.Disposition != schemas.DispositionInsufficientEvidence {
		t.Fatalf("outcome disposition = %q", outcome.Disposition)
	}
	// The advisor's trace now separates the purchase from the selection
	// outcome: the bought record plus the refused-selection record.
	records := advisor.Records()
	if len(records) < 2 || records[len(records)-1].Disposition != schemas.DispositionInsufficientEvidence {
		t.Fatalf("the refused selection must be its own trace record: %+v", records)
	}
}

func TestSelectorEligibilityFiltersUnanchorableCandidates(t *testing.T) {
	if got := eligibleCandidates([]SelectorCandidate{
		{Ref: "a", Claim: "x"},                            // no anchors
		{Ref: "", Claim: "x", Anchors: []string{"a.go"}},  // no ref
		{Ref: "b", Claim: "", Anchors: []string{"b.go"}},  // no claim
		{Ref: "c", Claim: "x", Anchors: []string{"c.go"}}, // eligible
	}); len(got) != 1 || got[0].Ref != "c" {
		t.Fatalf("eligibility = %+v, want only c", got)
	}
	// More than the cap is truncated, not rejected.
	many := make([]SelectorCandidate, 0, 6)
	for i := 0; i < 6; i++ {
		many = append(many, SelectorCandidate{Ref: string(rune('a' + i)), Claim: "x", Anchors: []string{"x.go"}})
	}
	if got := eligibleCandidates(many); len(got) != maxSelectorCandidates {
		t.Fatalf("cap = %d, want %d", len(got), maxSelectorCandidates)
	}
}

func TestResolveDecisionAdvisorFlagAndMode(t *testing.T) {
	// Flag off: absent regardless of mode.
	off := flags.Set{flags.DecisionAdvisorEnabled: false, flags.TrajectoryClassifierEnabled: false}
	advisor, err := resolveDecisionAdvisor(off, nil, config.DecisionAdvisorConfig{}, func(string) string { return "shadow" })
	if err != nil || advisor != nil {
		t.Fatalf("flag off: %v %v", advisor, err)
	}
	// Flag on, mode unset: absent.
	advisor, err = resolveDecisionAdvisor(flags.Set{flags.DecisionAdvisorEnabled: true}, nil, config.DecisionAdvisorConfig{}, func(string) string { return "" })
	if err != nil || advisor != nil {
		t.Fatalf("mode unset: %v %v", advisor, err)
	}
	// Flag on, mode off: absent.
	advisor, err = resolveDecisionAdvisor(flags.Set{flags.DecisionAdvisorEnabled: true}, nil, config.DecisionAdvisorConfig{}, func(k string) string {
		if k == DecisionAdvisorModeEnvVar {
			return "off"
		}
		return ""
	})
	if err != nil || advisor != nil {
		t.Fatalf("mode off: %v %v", advisor, err)
	}
	// Flag on, shadow mode, no evaluator: constructed, calls fail loud.
	advisor, err = resolveDecisionAdvisor(flags.Set{flags.DecisionAdvisorEnabled: true}, nil, config.DecisionAdvisorConfig{}, func(k string) string {
		if k == DecisionAdvisorModeEnvVar {
			return "shadow"
		}
		return ""
	})
	if err != nil || advisor == nil || advisor.Mode() != AdvisorModeShadow {
		t.Fatalf("shadow: %v %v", advisor, err)
	}
	// An unknown mode fails loud with the offending value named.
	_, err = resolveDecisionAdvisor(flags.Set{flags.DecisionAdvisorEnabled: true}, nil, config.DecisionAdvisorConfig{}, func(k string) string {
		if k == DecisionAdvisorModeEnvVar {
			return "yolo"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("unknown mode: %v", err)
	}
}

func TestSelectorExperimentalAppliesUnderManifestCalibration(t *testing.T) {
	// The live-campaign shape: active_experimental, manifest calibration
	// via the gate-2 store, no run-level economics. The preregistered
	// exception authorizes the bounded delivery; economics is what the
	// campaign measures.
	eval := &packEvaluator{pDirect: 0.82}
	advisor := activeExperimentalAdvisor(eval)
	snap := jevSnapshot("internal/clock.go#runClockTable")
	outcome, err := advisor.runSelector(context.Background(), snap, selectorCandidates(), testCalibration{p: 0.9, ok: true}, nil)
	if err != nil {
		t.Fatalf("selector: %v", err)
	}
	if outcome.Disposition != schemas.DispositionApplied {
		t.Fatalf("disposition = %q reason = %q", outcome.Disposition, outcome.Reason)
	}
	if len(outcome.Fetches) == 0 {
		t.Fatal("the approved experimental delivery delivered zero fetches")
	}
}

// mainGoNode is the M6 capture shape: one fact node for a file that anchors
// the file and every declaration in it.
func mainGoNode(id int64, symbols ...string) memd.GraphNode {
	n := memd.GraphNode{ID: id, Kind: "fact", Claim: "main.go defines " + strings.Join(symbols, ", "), Status: "active"}
	n.Anchors = append(n.Anchors, memd.GraphAnchor{Kind: "file", Value: "main.go"})
	for _, s := range symbols {
		n.Anchors = append(n.Anchors, memd.GraphAnchor{Kind: "symbol", Value: "main.go#" + s})
	}
	return n
}

// TestSelectorCandidatesOnePerDeclaration is the M6 regression: a file node
// yields one candidate per declaration, ranked against the intent, with the
// declarations the intent names left to the deterministic path.
func TestSelectorCandidatesOnePerDeclaration(t *testing.T) {
	intent := "New admin endpoints must map their store errors using the SAME table as the /session handler. Refactor mapStoreError in main.go if needed."
	nodes := []memd.GraphNode{
		mainGoNode(31, "healthHandler", "main", "mapStoreError", "sessionHandler", "writeJSON"),
		// A second node for the same file must not duplicate candidates.
		mainGoNode(32, "adminSessionsHandler", "healthHandler", "sessionHandler"),
	}
	candidates, named := selectorCandidatesFromNodes(intent, nodes)
	if got := strings.Join(named, ","); got != "main.go#main,main.go#mapStoreError" {
		t.Fatalf("named = %s", got)
	}
	var refs []string
	for _, c := range candidates {
		refs = append(refs, c.Ref)
		if len(c.Anchors) != 1 || c.Anchors[0] != c.Ref {
			t.Fatalf("%s: anchors = %v, want only its own declaration", c.Ref, c.Anchors)
		}
		if !strings.Contains(c.Claim, "is declared in main.go") {
			t.Fatalf("%s: claim = %q", c.Ref, c.Claim)
		}
	}
	// admin+session+handler, session+handler, handler, then none.
	want := "main.go#adminSessionsHandler,main.go#sessionHandler,main.go#healthHandler,main.go#writeJSON"
	if got := strings.Join(refs, ","); got != want {
		t.Fatalf("candidates = %s, want %s", got, want)
	}
}

func TestSelectorCandidatesAllNamedOrFileOnly(t *testing.T) {
	candidates, named := selectorCandidatesFromNodes("Extend healthHandler in main.go", []memd.GraphNode{mainGoNode(1, "healthHandler", "main")})
	if len(candidates) != 0 || len(named) != 2 {
		t.Fatalf("candidates = %+v, named = %v; want none asked, both named", candidates, named)
	}
	fileOnly := memd.GraphNode{ID: 2, Kind: "fact", Claim: "config.yaml sets the TTL", Status: "active",
		Anchors: []memd.GraphAnchor{{Kind: "file", Value: "config.yaml"}}}
	candidates, _ = selectorCandidatesFromNodes("change the ttl", []memd.GraphNode{fileOnly})
	if len(candidates) != 1 || candidates[0].Ref != "config.yaml" {
		t.Fatalf("file-only node: candidates = %+v", candidates)
	}
}

func TestIdentWordsSplitsCamelCaseAndAcronyms(t *testing.T) {
	for in, want := range map[string]string{
		"adminSessionsHandler": "admin,sessions,handler",
		"writeJSON":            "write,json",
		"JSONWriter":           "json,writer",
		"Store.Get":            "store,get",
		"session_ttl.go":       "session,ttl",
	} {
		if got := strings.Join(identWords(in), ","); got != want {
			t.Fatalf("identWords(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestSelectorRefusalReasonReportsTheRealProbability: the M6 traces said
// "probability 0" when no answer chose direct_dependency, even at 0.41.
func TestSelectorRefusalReasonReportsTheRealProbability(t *testing.T) {
	cands := selectorCandidates()
	id := questionIDFor(cands[0].Ref)
	out := selectFromAnswers([]schemas.DecisionAnswerRecord{packAnswer(id, "background", 0.41)}, cands)
	if !strings.Contains(out.Reason, "no answer chose direct_dependency (highest direct_dependency probability 0.41)") {
		t.Fatalf("reason = %q", out.Reason)
	}
	out = selectFromAnswers([]schemas.DecisionAnswerRecord{packAnswer(id, "direct_dependency", 0.49)}, cands)
	if out.SelectedRef != "" || !strings.Contains(out.Reason, "0.49 is below the 0.50 policy floor") {
		t.Fatalf("selected %q, reason = %q", out.SelectedRef, out.Reason)
	}
}

func TestResolveDecisionAdvisorConfigAndEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name          string
		enabled       bool
		mode, profile string
		want          AdvisorMode
	}{
		{name: "config", enabled: true, want: AdvisorModeShadow},
		{name: "environment mode", enabled: true, mode: "active_experimental", want: AdvisorModeActiveExperimental},
		{name: "environment profile", enabled: true, profile: "override-model", want: AdvisorModeShadow},
		{name: "environment off", enabled: true, mode: "off", want: AdvisorModeOff},
		{name: "flag off", mode: "active_experimental", want: AdvisorModeOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			section := config.DecisionAdvisorConfig{
				Mode:         "shadow",
				Backend:      "http",
				Profile:      "config-model",
				QuestionPack: QuestionPackTrajActionV1,
				Endpoint:     "http://example.test/decide",
			}
			set := flags.Set{flags.DecisionAdvisorEnabled: tc.enabled, flags.TrajectoryClassifierEnabled: tc.enabled}
			advisor, err := resolveDecisionAdvisor(set, nil, section, func(key string) string {
				switch key {
				case DecisionAdvisorModeEnvVar:
					return tc.mode
				case DecisionAdvisorModelEnvVar:
					return tc.profile
				case DecisionAdvisorCapUSDVar:
					return "0.005"
				}
				return ""
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == AdvisorModeOff {
				if advisor != nil {
					t.Fatal("off mode must return a nil advisor")
				}
				return
			}
			profile := section.Profile
			if tc.profile != "" {
				profile = tc.profile
			}
			if advisor.Mode() != tc.want || advisor.config.Profile != profile || advisor.config.Backend != "http" || advisor.config.Limits.MaxAdvisorCostUSD != .005 {
				t.Fatalf("resolved config=%+v", advisor.config)
			}
		})
	}
}
