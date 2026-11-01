package splice

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/providers/openrouterdecisions"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestSplitRequirementsFollowsClauses(t *testing.T) {
	got := splitRequirements("Add AdminForceSignOut(store *Store, userID string) error in admin.go: invalidate every session held by userID, reusing the SAME rule. Follow that convention. A user with no sessions is not an error: return nil.")
	want := []string{
		"Add AdminForceSignOut(store *Store, userID string) error in admin.go",
		"invalidate every session held by userID, reusing the SAME rule",
		"Follow that convention",
		"A user with no sessions is not an error",
	}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Fatalf("requirements =\n%q\nwant\n%q", got, want)
	}
}

func TestCompletionQuestionsAreValidAndPolicyNamesUnmet(t *testing.T) {
	reqs := []string{"Add the handler", "Reuse the mapping table"}
	state, _ := completionStateV1("req", reqs, "diff")
	if err := (zeroruntime.DecisionRequest{Model: "m", State: state, Questions: completionQuestionsV1(reqs)}).Validate(); err != nil {
		t.Fatal(err)
	}
	unmet := unmetRequirements(reqs, map[string]float64{"requirement_0": 0.93, "requirement_1": 0.05})
	if len(unmet) != 1 || unmet[0] != "Reuse the mapping table" {
		t.Fatalf("unmet = %v", unmet)
	}
}

// TestCompletionCheckEval scores completion_v1 on the gold and wrong trees
// (tests/evals/completion-check/tree_dataset.json). Live endpoint: runs only
// with COMPLETION_EVAL=1. COMPLETION_EVAL_DATA overrides the dataset and
// COMPLETION_EVAL_OUT names the file for the raw answers.
func TestCompletionCheckEval(t *testing.T) {
	if os.Getenv("COMPLETION_EVAL") != "1" {
		t.Skip("set COMPLETION_EVAL=1 to call the live decision endpoint")
	}
	path := os.Getenv("COMPLETION_EVAL_DATA")
	if path == "" {
		path = filepath.Join("..", "..", "tests", "evals", "completion-check", "tree_dataset.json")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var set struct {
		Rows []struct {
			ID        string `json:"id"`
			Request   string `json:"request"`
			Diff      string `json:"diff"`
			Satisfies *bool  `json:"satisfies"`
			Verified  *bool  `json:"verified"`
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
		ID           string
		Label        bool
		Requirements []string
		Answers      map[string]float64
		Min          float64
	}
	var records []record
	for _, row := range set.Rows {
		label := row.Satisfies
		if label == nil {
			label = row.Verified
		}
		if label == nil {
			continue
		}
		reqs := splitRequirements(row.Request)
		state, _ := completionStateV1(row.Request, reqs, row.Diff)
		res, err := client.Evaluate(context.Background(), zeroruntime.DecisionRequest{Model: "typesafe/jev-1.13", State: state, Questions: completionQuestionsV1(reqs)})
		if err != nil {
			t.Fatalf("%s: %v", row.ID, err)
		}
		rec := record{ID: row.ID, Label: *label, Requirements: reqs, Answers: map[string]float64{}, Min: 1}
		for i := range reqs {
			ans, _ := res.AnswerByID(fmt.Sprintf("requirement_%d", i))
			rec.Answers[fmt.Sprintf("requirement_%d", i)] = ans.TrueProbability
			if ans.TrueProbability < rec.Min {
				rec.Min = ans.TrueProbability
			}
		}
		records = append(records, rec)
	}
	if out := os.Getenv("COMPLETION_EVAL_OUT"); out != "" {
		b, _ := json.MarshalIndent(records, "", " ")
		if err := os.WriteFile(out, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	for _, r := range records {
		t.Logf("%-22s label=%-5t lowest requirement %.2f", r.ID, r.Label, r.Min)
	}
	for _, th := range []float64{0.3, 0.5, 0.7, 0.8} {
		caught, wrong, alarms, right := 0, 0, 0, 0
		for _, r := range records {
			flagged := r.Min < th
			if r.Label {
				right++
				if flagged {
					alarms++
				}
			} else {
				wrong++
				if flagged {
					caught++
				}
			}
		}
		t.Logf("threshold %.1f: caught %d/%d wrong changes, false alarms %d/%d correct changes", th, caught, wrong, alarms, right)
	}
}
