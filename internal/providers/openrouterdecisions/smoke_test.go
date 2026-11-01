package openrouterdecisions

// The live alpha-contract smoke. It is guarded behind SPLICE_JEV_SMOKE=1 so
// routine CI never spends or depends on the network, and it makes exactly ONE
// bounded request per run: a two-criterion choice over a three-line state. At
// the documented Jev pricing this costs a fraction of a cent. A failure here
// is recorded verbatim: an unverified alpha contract is reported as
// unverified, never papered over.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestLiveDecisionsSmoke(t *testing.T) {
	if os.Getenv("SPLICE_JEV_SMOKE") != "1" {
		t.Skip("live smoke disabled: set SPLICE_JEV_SMOKE=1 to authorize one bounded Decisions request")
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		if store, err := config.ProviderKeyStore(); err == nil && store != nil {
			if key, ok, gerr := store.Get("openrouter"); gerr == nil && ok {
				apiKey = key
			}
		}
	}
	if apiKey == "" {
		t.Skip("no OpenRouter credential available (env or keyring)")
	}

	client := NewClient(apiKey)
	request := zeroruntime.DecisionRequest{
		Model: "typesafe/jev-1.13",
		State: `{"need":"handle expired sessions","candidate":"internal/session/expiry.go#IsExpired"}`,
		Questions: []zeroruntime.DecisionQuestion{{
			ID:           "is_dependency",
			Kind:         zeroruntime.DecisionKindChoice,
			Instructions: "Does the supplied candidate identify a concrete dependency required for the named need?",
			Criteria: []zeroruntime.DecisionCriterion{
				{Label: "direct_dependency", Description: "The candidate names a concrete dependency required for the need."},
				{Label: "insufficient_evidence", Description: "The description cannot establish the relation."},
			},
		}},
		SessionID: "splice-jev-m2-smoke",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	result, err := client.Evaluate(ctx, request)
	if err != nil {
		t.Fatalf("live Decisions smoke failed (the alpha contract is UNVERIFIED; record this verbatim): %v", err)
	}
	if !strings.HasPrefix(result.ResolvedModel, "typesafe/jev-1.13") {
		t.Fatalf("resolved model = %q, want the pinned prefix", result.ResolvedModel)
	}
	answer, ok := result.AnswerByID("is_dependency")
	if !ok || answer.Choice == "" {
		t.Fatalf("no typed answer: %+v", result.Answers)
	}
	t.Logf("LIVE SMOKE OK: resolved=%s provider=%s id=%s usage=%d in / %d out cost=%v answer=%s (p=%v, confidence set=%v)",
		result.ResolvedModel, result.Provider, result.RequestID, result.InputTokens, result.OutputTokens,
		result.ReportedCostUSD, answer.Choice, answer.Probabilities, answer.ConfidenceSet)
}
