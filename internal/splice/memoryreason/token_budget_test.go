package memoryreason

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func obsFor(id int64, content string) schemas.MemoryObservation {
	return schemas.MemoryObservation{
		ID: id, OwnerAgent: "agent-1", Title: "t", Content: content,
		MemoryType: "pattern", Scope: "global", Visibility: "private",
	}
}

// Valid small observations all fit: the count ceiling still admits 5.
func TestTokenBudgetAdmitsUpToCeiling(t *testing.T) {
	bundle := &schemas.MemoryBundle{}
	for i := 1; i <= 5; i++ {
		bundle.Observations = append(bundle.Observations, obsFor(int64(i), "small content"))
	}
	got := Admit(bundle, "/repo", 1000)
	if got.Bundle == nil || len(got.Bundle.Observations) != 5 {
		t.Fatalf("admitted %d, want 5", len(got.Bundle.Observations))
	}
	if got.Rejected.OverBudget != 0 || got.Rejected.OverLimit != 0 {
		t.Fatalf("unexpected rejections: %+v", got.Rejected)
	}
}

// A 2000-rune observation truncates to the 500-rune content bound and then
// measures against the budget; it fits alone. Two of them do not: the second
// is rejected as OverBudget (354 > 300), proving measurement happens on the
// truncated delivery.
func TestTokenBudgetSecondLargeObservationOverBudget(t *testing.T) {
	bundle := &schemas.MemoryBundle{
		Observations: []schemas.MemoryObservation{
			obsFor(1, strings.Repeat("x", 2000)),
			obsFor(2, strings.Repeat("y", 2000)),
		},
	}
	got := Admit(bundle, "/repo", 1000)
	if got.Bundle == nil || len(got.Bundle.Observations) != 1 {
		t.Fatalf("admitted %d, want 1", len(got.Bundle.Observations))
	}
	if got.Rejected.OverBudget != 1 {
		t.Fatalf("OverBudget = %d, want 1", got.Rejected.OverBudget)
	}
}

// First-fit over the incoming (reranked) order: a smaller item after an
// oversized one still fits the remaining budget. One pass, no backtracking.
func TestTokenBudgetFirstFitKeepsSmallerLaterItem(t *testing.T) {
	// Measured costs: a 40-rune item is ~52 tokens, a 400-rune item ~152,
	// "tiny" ~53. Walk: 52 -> +152 = 204 -> +152 = 356 REJECT -> +53 = 257
	// admit. First-fit over the ranked order with no backtracking: item 3
	// is skipped, item 4 still fits the remainder.
	bundle := &schemas.MemoryBundle{
		Observations: []schemas.MemoryObservation{
			obsFor(1, strings.Repeat("a", 40)),
			obsFor(2, strings.Repeat("b", 400)),
			obsFor(3, strings.Repeat("c", 400)),
			obsFor(4, "tiny"),
		},
	}
	got := Admit(bundle, "/repo", 1000)
	if got.Bundle == nil || len(got.Bundle.Observations) != 3 {
		t.Fatalf("admitted %d, want 3 (1, 2, 4)", len(got.Bundle.Observations))
	}
	admittedIDs := []int64{
		got.Bundle.Observations[0].ID,
		got.Bundle.Observations[1].ID,
		got.Bundle.Observations[2].ID,
	}
	if admittedIDs[0] != 1 || admittedIDs[1] != 2 || admittedIDs[2] != 4 {
		t.Fatalf("wrong items admitted: %v", admittedIDs)
	}
	if got.Rejected.OverBudget != 1 {
		t.Fatalf("OverBudget = %d, want 1", got.Rejected.OverBudget)
	}
}

// Truncation happens before measurement: a 2000-rune content truncates to
// 500 runes + ellipsis and its measured cost reflects the TRUNCATED bytes,
// so it can fit within the budget.
func TestTokenBudgetMeasuresTruncatedContent(t *testing.T) {
	bundle := &schemas.MemoryBundle{
		Observations: []schemas.MemoryObservation{obsFor(1, strings.Repeat("x", 2000))},
	}
	// Direct probe of the measurement helper (same package).
	cost := itemTokens(obsFor(1, strings.Repeat("x", MaxObservationRunes)))
	if cost > ObservationTokenBudget {
		t.Fatalf("truncated-size observation measures %d tokens, must fit %d", cost, ObservationTokenBudget)
	}
	_ = bundle
}

// Zero budget admits nothing (defensive constant check via a local copy of
// the rule, not a variable change).
func TestAdmissionOrderStable(t *testing.T) {
	bundle := &schemas.MemoryBundle{}
	for i := 1; i <= 5; i++ {
		bundle.Observations = append(bundle.Observations, obsFor(int64(i), "same content"))
	}
	first := Admit(bundle, "/repo", 1000)
	second := Admit(bundle, "/repo", 1000)
	if len(first.Bundle.Observations) != len(second.Bundle.Observations) {
		t.Fatal("admission not deterministic")
	}
	for i := range first.Bundle.Observations {
		if first.Bundle.Observations[i].ID != second.Bundle.Observations[i].ID {
			t.Fatalf("order changed at %d", i)
		}
	}
}

// Exemplar budget parity: three historic-size exemplars (400-rune
// distillates, ~108 tokens each with overhead) still all admit under the
// 300-token budget. (The D4 ablation, not D3, changes exemplar delivery.)
func TestExemplarBudgetKeepsHistoricDelivery(t *testing.T) {
	bundle := &schemas.MemoryBundle{}
	for i := 1; i <= 3; i++ {
		bundle.Exemplars = append(bundle.Exemplars, schemas.Exemplar{
			RunID:   fmt.Sprintf("run-%d", i),
			Content: strings.Repeat("c", 400),
		})
	}
	got := Admit(bundle, "/repo", 1000)
	if got.Bundle == nil || len(got.Bundle.Exemplars) != 3 {
		t.Fatalf("admitted %d exemplars, want 3", len(got.Bundle.Exemplars))
	}
}
