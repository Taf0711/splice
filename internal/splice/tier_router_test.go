package splice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/providers/openrouterdecisions"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestTierPolicyMovesUncertainRoutesUp(t *testing.T) {
	cases := []struct {
		name  string
		probs map[string]float64
		risk  float64
		want  schemas.PipelineTier
	}{
		{"confident trivial stays trivial", map[string]float64{"trivial": 0.97, "light": 0.03}, 0.1, schemas.TierTrivial},
		// 30% says heavier than light: above the 10% risk, so standard.
		{"split light moves up", map[string]float64{"trivial": 0.1, "light": 0.6, "standard": 0.25, "substantial": 0.05}, 0.1, schemas.TierStandard},
		// The same answer at a looser risk stays light.
		{"looser risk keeps light", map[string]float64{"trivial": 0.1, "light": 0.6, "standard": 0.25, "substantial": 0.05}, 0.35, schemas.TierLight},
		// Heavy route: the more probable of the two names the tier.
		{"heavy picks architectural", map[string]float64{"substantial": 0.3, "architectural": 0.6, "standard": 0.1}, 0.1, schemas.TierArchitectural},
		{"heavy mass alone forces heavy", map[string]float64{"light": 0.8, "substantial": 0.2}, 0.1, schemas.TierSubstantial},
	}
	for _, tc := range cases {
		got, ok := tierFromProbabilities(tc.probs, tc.risk)
		if !ok || got != tc.want {
			t.Fatalf("%s: got %q (%t), want %q", tc.name, got, ok, tc.want)
		}
	}
	if _, ok := tierFromProbabilities(map[string]float64{"other": 1}, 0.1); ok {
		t.Fatal("a distribution with no tier probability must report no tier")
	}
}

type tierEval struct {
	answer zeroruntime.DecisionAnswer
	err    error
}

func (e tierEval) Evaluate(ctx context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
	if e.err != nil {
		return zeroruntime.DecisionResult{}, e.err
	}
	return zeroruntime.DecisionResult{Answers: []zeroruntime.DecisionAnswer{e.answer}}, nil
}

func TestClassifyWithDecisionsUsesPolicyAndFallsBack(t *testing.T) {
	eval := tierEval{answer: zeroruntime.DecisionAnswer{QuestionID: tierQuestionID, Choice: "standard", Confidence: 0.7,
		Probabilities: map[string]float64{"standard": 0.85, "light": 0.1, "substantial": 0.05}}}
	out := classifyComplexityWithDecisions(context.Background(), eval, "typesafe/jev-1.13", "Add a paged list accessor to the store.", 0.1)
	if out.Tier != schemas.TierStandard || out.Confidence != 0.7 || !strings.Contains(out.Rationale, "tier_v1") {
		t.Fatalf("out = %+v", out)
	}
	out = classifyComplexityWithDecisions(context.Background(), tierEval{err: errors.New("timeout")}, "m", "Fix a typo in README.md.", 0.1)
	if out.Tier != schemas.TierTrivial || !strings.Contains(out.Rationale, "keyword fallback: timeout") {
		t.Fatalf("fallback out = %+v", out)
	}
}

// TestTierRoutingEval scores tier_v1 against the labeled set in
// tests/evals/tier-routing. It calls the live decision endpoint once per row
// (a few cents in total), so it runs only with TIER_EVAL=1 and a stored
// OpenRouter key. TIER_EVAL_OUT names the file for the raw answers.
func TestTierRoutingEval(t *testing.T) {
	if os.Getenv("TIER_EVAL") != "1" {
		t.Skip("set TIER_EVAL=1 to call the live decision endpoint")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "tests", "evals", "tier-routing", "labeled.json"))
	if err != nil {
		t.Fatal(err)
	}
	var set struct {
		Rows []struct {
			ID      string `json:"id"`
			Request string `json:"request"`
			Tier    string `json:"tier"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	store, err := config.ProviderKeyStore()
	if err != nil {
		t.Fatal(err)
	}
	key, ok, err := store.Get("openrouter")
	if err != nil || !ok {
		t.Fatalf("no stored openrouter key: %v", err)
	}
	client := openrouterdecisions.NewClient(key)
	type record struct {
		ID, Label, Keyword, Choice string
		Confidence                 float64
		Probabilities              map[string]float64
	}
	var records []record
	for _, row := range set.Rows {
		state, _ := tierStateV1(row.Request)
		res, err := client.Evaluate(context.Background(), zeroruntime.DecisionRequest{Model: "typesafe/jev-1.13", State: state, Questions: []zeroruntime.DecisionQuestion{tierQuestionV1()}})
		if err != nil {
			t.Fatalf("%s: %v", row.ID, err)
		}
		ans, _ := res.AnswerByID(tierQuestionID)
		records = append(records, record{row.ID, row.Tier, string(ClassifyRequest(row.Request)), ans.Choice, ans.Confidence, ans.Probabilities})
	}
	if out := os.Getenv("TIER_EVAL_OUT"); out != "" {
		b, _ := json.MarshalIndent(records, "", " ")
		if err := os.WriteFile(out, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	score := func(name string, pick func(record) schemas.PipelineTier) {
		right, under, over := 0, 0, 0
		var misses []string
		for _, r := range records {
			got, want := tierRoute(pick(r)), tierRoute(schemas.PipelineTier(r.Label))
			switch {
			case got == want:
				right++
			case got < want:
				under++
				misses = append(misses, fmt.Sprintf("%s(%s->%s)", r.ID, r.Label, pick(r)))
			default:
				over++
			}
		}
		sort.Strings(misses)
		t.Logf("%-26s route accuracy %2d/%d  under-tier %2d  over-tier %2d  under: %s", name, right, len(records), under, over, strings.Join(misses, " "))
	}
	score("keyword classifier", func(r record) schemas.PipelineTier { return schemas.PipelineTier(r.Keyword) })
	score("jev argmax", func(r record) schemas.PipelineTier { return schemas.PipelineTier(r.Choice) })
	for _, risk := range []float64{0.05, 0.1, 0.2, 0.3, 0.4} {
		risk := risk
		score(fmt.Sprintf("jev policy risk=%.2f", risk), func(r record) schemas.PipelineTier {
			tier, _ := tierFromProbabilities(r.Probabilities, risk)
			return tier
		})
	}
}

func TestStageNeedQuestionsAreValidNouls(t *testing.T) {
	req := zeroruntime.DecisionRequest{Model: "m", State: `{"request":"x"}`, Questions: stageNeedQuestionsV1()}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestStageNeedsEval scores needs_v1 against the per-need labels in
// tests/evals/tier-routing, next to the stage decisions the keyword
// classifier and tier_v1 imply. Live endpoint: runs only with NEEDS_EVAL=1.
// NEEDS_EVAL_OUT names the file for the raw answers.
func TestStageNeedsEval(t *testing.T) {
	if os.Getenv("NEEDS_EVAL") != "1" {
		t.Skip("set NEEDS_EVAL=1 to call the live decision endpoint")
	}
	dir := filepath.Join("..", "..", "tests", "evals", "tier-routing")
	raw, err := os.ReadFile(filepath.Join(dir, "labeled.json"))
	if err != nil {
		t.Fatal(err)
	}
	var set struct {
		Rows []struct {
			ID      string           `json:"id"`
			Request string           `json:"request"`
			Needs   map[string]*bool `json:"needs"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	// tier_v1 answers from the earlier run, so the comparison costs nothing.
	tierRaw, err := os.ReadFile(filepath.Join(dir, "answers-tier_v1-20260925.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tierAnswers []struct {
		ID            string
		Probabilities map[string]float64
	}
	if err := json.Unmarshal(tierRaw, &tierAnswers); err != nil {
		t.Fatal(err)
	}
	tierProbs := map[string]map[string]float64{}
	for _, a := range tierAnswers {
		tierProbs[a.ID] = a.Probabilities
	}
	store, err := config.ProviderKeyStore()
	if err != nil {
		t.Fatal(err)
	}
	key, ok, err := store.Get("openrouter")
	if err != nil || !ok {
		t.Fatalf("no stored openrouter key: %v", err)
	}
	client := openrouterdecisions.NewClient(key)
	type record struct {
		ID    string
		Nouls map[string]float64
	}
	var records []record
	for _, row := range set.Rows {
		state, _ := tierStateV1(row.Request)
		res, err := client.Evaluate(context.Background(), zeroruntime.DecisionRequest{Model: "typesafe/jev-1.13", State: state, Questions: stageNeedQuestionsV1()})
		if err != nil {
			t.Fatalf("%s: %v", row.ID, err)
		}
		rec := record{ID: row.ID, Nouls: map[string]float64{}}
		for _, need := range []string{needCodeChange, needNewTests, needSecurityReview} {
			ans, _ := res.AnswerByID(need)
			rec.Nouls[need] = ans.TrueProbability
		}
		records = append(records, rec)
	}
	if out := os.Getenv("NEEDS_EVAL_OUT"); out != "" {
		b, _ := json.MarshalIndent(records, "", " ")
		if err := os.WriteFile(out, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A tier implies needs: verification from light up, new tests from
	// standard up, security review on the heavy route.
	tierNeeds := func(tier schemas.PipelineTier) map[string]bool {
		r := tierRoute(tier)
		return map[string]bool{needCodeChange: r >= 1, needNewTests: r >= 2, needSecurityReview: r >= 3}
	}
	byID := map[string]record{}
	for _, r := range records {
		byID[r.ID] = r
	}
	for _, need := range []string{needCodeChange, needNewTests, needSecurityReview} {
		score := func(name string, decide func(id, request string) bool) {
			missed, extra, n := 0, 0, 0
			var misses []string
			for _, row := range set.Rows {
				label := row.Needs[need]
				if label == nil {
					continue
				}
				n++
				got := decide(row.ID, row.Request)
				switch {
				case *label && !got:
					missed++
					misses = append(misses, row.ID)
				case !*label && got:
					extra++
				}
			}
			t.Logf("%-16s %-24s wrong %2d/%d  skipped-when-needed %2d  ran-when-not-needed %2d  skipped: %s", need, name, missed+extra, n, missed, extra, strings.Join(misses, " "))
		}
		score("keyword tier", func(id, req string) bool { return tierNeeds(ClassifyRequest(req))[need] })
		score("tier_v1 risk=0.10", func(id, req string) bool {
			tier, _ := tierFromProbabilities(tierProbs[id], 0.1)
			return tierNeeds(tier)[need]
		})
		for _, th := range []float64{0.2, 0.3, 0.5, 0.7} {
			th := th
			score(fmt.Sprintf("needs_v1 noul>%.1f", th), func(id, req string) bool { return byID[id].Nouls[need] > th })
		}
	}
}

func TestStageNeedsPolicyGatesOnCodeChange(t *testing.T) {
	// A docs commit that the new_tests Noul still scores high: no code
	// change, so neither test generation nor security review runs.
	docs := stageNeedsFromNouls(map[string]float64{needCodeChange: 0.10, needNewTests: 0.71, needSecurityReview: 0.6})
	for _, need := range []string{needCodeChange, needNewTests, needSecurityReview} {
		if docs[need].Run {
			t.Fatalf("docs request ran %s: %+v", need, docs[need])
		}
	}
	// The jargon viewport commit scored 0.17: the low bar keeps its
	// verification.
	code := stageNeedsFromNouls(map[string]float64{needCodeChange: 0.17, needNewTests: 0.9, needSecurityReview: 0.2})
	if !code[needCodeChange].Run || !code[needNewTests].Run || code[needSecurityReview].Run {
		t.Fatalf("code request decisions = %+v", code)
	}
	if code[needNewTests].Probability != 0.9 || code[needNewTests].Threshold != 0.5 {
		t.Fatalf("a decision must carry its probability and threshold: %+v", code[needNewTests])
	}
}
